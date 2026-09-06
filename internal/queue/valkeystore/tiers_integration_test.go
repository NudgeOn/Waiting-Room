//go:build integration && tiers

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"waiting-room/internal/queue/model"
)

// Bounded store-protocol correctness, not HTTP/socket concurrency or qualification.
func TestLocalVisitorTiers(t *testing.T) {
	for _, n := range []int{1000, 2000, 5000, 10000} {
		if !t.Run(strconv.Itoa(n), func(t *testing.T) { runVisitorTier(t, n) }) {
			break
		}
	}
}
func bounded(n int, work func(int)) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 32; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				work(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}
func serverCounters(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	raw, err := s.client.Do(context.Background(), s.client.B().Info().Section("memory", "stats").Build()).ToString()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]int64{}
	for _, line := range strings.Split(raw, "\r\n") {
		key, val, ok := strings.Cut(line, ":")
		if ok {
			if n, e := strconv.ParseInt(val, 10, 64); e == nil {
				m[key] = n
			}
		}
	}
	for _, key := range []string{"used_memory", "maxmemory", "evicted_keys", "total_error_replies"} {
		if _, ok := m[key]; !ok {
			t.Fatal("missing resource field", key)
		}
	}
	return m
}
func runVisitorTier(t *testing.T, n int) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := model.DefaultConfig()
	c.VisitorCap = n
	c.IdempotencyCap = 2 * n
	c.LeaseCap = 3
	c.Rate = 6
	stores, _ := installationStores(t, 4, c, InstallationConfig{"standard", n, 2 * n})
	before := serverCounters(t, stores[0])
	// Conservative envelope is for these fixed, small fixture payloads only.
	if before["maxmemory"] <= 0 || before["used_memory"]+int64(n)*8192 > before["maxmemory"]*7/10 {
		t.Fatal("NO-GO_PREFLIGHT: fixture memory envelope exceeds budget")
	}
	started := time.Now()
	tickets := make([]model.Ticket, n)
	joinMS := make([]float64, n)
	statusMS := make([]float64, n)
	bounded(n, func(i int) {
		start := time.Now()
		key := fmt.Sprintf("visitor-%d", i)
		r, e := stores[i%4].Join(ctx, key, "/shop", Hash(key), "fixed-test-replay")
		joinMS[i] = float64(time.Since(start).Microseconds()) / 1000
		if e != nil || r.Ticket == nil {
			t.Errorf("join[%d]: %v", i, e)
			return
		}
		tickets[i] = *r.Ticket
	})
	if t.Failed() {
		return
	}
	seen := map[string]bool{}
	sequences := make([][]uint64, 4)
	for i, ticket := range tickets {
		if seen[ticket.ID] || ticket.State != model.Waiting {
			t.Fatal("duplicate/non-WAITING ticket")
		}
		seen[ticket.ID] = true
		sequences[i%4] = append(sequences[i%4], ticket.Sequence)
	}
	for _, seq := range sequences {
		sort.Slice(seq, func(i, j int) bool { return seq[i] < seq[j] })
		for i, v := range seq {
			if v != uint64(i+1) {
				t.Fatal("sequence gap/duplicate")
			}
		}
	}
	if got := capacity(t, stores[0]); got.Visitors != n || got.Idempotency != n || !got.Warning {
		t.Fatal(got)
	}
	bounded(n, func(i int) {
		start := time.Now()
		r, e := stores[i%4].Status(ctx, tickets[i].ID)
		statusMS[i] = float64(time.Since(start).Microseconds()) / 1000
		if e != nil || r.Ticket == nil || *r.Ticket != tickets[i] {
			t.Errorf("status[%d]: %v", i, e)
		}
	})
	bounded(n/10, func(sample int) {
		i := sample * 10
		r, e := stores[i%4].Join(ctx, fmt.Sprintf("visitor-%d", i), "/shop", Hash("unused"), "other")
		if e != nil || r.Ticket == nil || *r.Ticket != tickets[i] || r.Replay != "fixed-test-replay" {
			t.Errorf("retry[%d]: %v", i, e)
		}
	})
	bounded(128, func(i int) {
		_, e := stores[i%4].Join(ctx, fmt.Sprintf("overflow-%d", i), "/shop", Hash(fmt.Sprintf("overflow-%d", i)), "replay")
		if !errors.Is(e, model.ErrCapacity) {
			t.Errorf("overflow[%d]: %v", i, e)
		}
	})
	admitted := 0
	for _, s := range stores {
		r, e := s.Promote(ctx, 128)
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Tickets) != 3 {
			t.Fatal("lease count")
		}
		for i, ticket := range r.Tickets {
			if ticket.Sequence != uint64(i+1) {
				t.Fatal("FIFO inversion")
			}
			first, e := s.Claim(ctx, ticket.ID)
			if e != nil {
				t.Fatal(e)
			}
			second, e := s.Claim(ctx, ticket.ID)
			if e != nil || *first.Ticket != *second.Ticket || first.Ticket.State != model.Admitted {
				t.Fatal("claim changed", e)
			}
			admitted++
		}
		r, e = s.Promote(ctx, 128)
		if e != nil || len(r.Tickets) != 0 {
			t.Fatal("lease cap exceeded", e)
		}
	}
	if got := capacity(t, stores[0]); got.Visitors != n || got.Idempotency != n {
		t.Fatal("count changed", got)
	}
	var stored int64
	for _, s := range stores {
		count, e := s.client.Do(ctx, s.client.B().Hlen().Key(s.keys[1]).Build()).ToInt64()
		if e != nil {
			t.Fatal(e)
		}
		stored += count
	}
	if stored != int64(n) {
		t.Fatal("physical count mismatch", stored)
	}
	after := serverCounters(t, stores[0])
	if after["evicted_keys"] != before["evicted_keys"] || after["used_memory"] >= after["maxmemory"]*7/10 {
		t.Fatal("resource threshold", after)
	}
	if after["total_error_replies"]-before["total_error_replies"] != 128 {
		t.Fatal("unexpected server error count", after["total_error_replies"]-before["total_error_replies"])
	}
	sort.Float64s(joinMS)
	sort.Float64s(statusMS)
	summary := map[string]any{"scope": "local-store-logical-visitors", "visitors": n, "rooms": 4, "workers": 32, "joined": n, "statusReads": n, "retries": n / 10, "expectedCapacityRejections": 128, "admitted": admitted, "waiting": n - admitted, "elapsedSeconds": time.Since(started).Seconds(), "joinP95ms": joinMS[(n*95+99)/100-1], "joinP99ms": joinMS[(n*99+99)/100-1], "statusP95ms": statusMS[(n*95+99)/100-1], "statusP99ms": statusMS[(n*99+99)/100-1], "valkeyUsedBytes": after["used_memory"], "valkeyMaxBytes": after["maxmemory"], "evictions": after["evicted_keys"] - before["evicted_keys"], "qualification": "NOT_RUN"}
	raw, _ := json.Marshal(summary)
	t.Log("TIER_RESULT " + string(raw))
}

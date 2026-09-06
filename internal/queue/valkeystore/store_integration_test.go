//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"waiting-room/internal/queue/model"
)

func testStore(t *testing.T, edit func(*model.Config)) (*Store, model.Config, string) {
	t.Helper()
	address := os.Getenv("WR_TEST_VALKEY")
	if address == "" {
		t.Fatal("WR_TEST_VALKEY required; integration tests never silently skip")
	}
	c := model.DefaultConfig()
	c.VisitorCap = 120
	c.IdempotencyCap = 150
	c.LeaseCap = 3
	c.Rate = 6
	if edit != nil {
		edit(&c)
	}
	ns := fmt.Sprintf("wr:lab:test-%d", time.Now().UnixNano())
	s, err := Open(context.Background(), address, ns, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Do(context.Background(), s.client.B().Del().Key(s.keys...).Build()); s.Close() })
	return s, c, ns
}
func must(t *testing.T, r Result, err error) Result {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func add(t *testing.T, s *Store, key string) Result {
	t.Helper()
	r, e := s.Join(context.Background(), key, "/shop", Hash(key), "encrypted-test-replay")
	return must(t, r, e)
}

func TestConcurrentJoinPromotionClaim(t *testing.T) {
	s, c, ns := testStore(t, nil)
	other, err := Open(context.Background(), os.Getenv("WR_TEST_VALKEY"), ns, c)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	results := make(chan Result, 40)
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 0 {
				store = other
			}
			r, e := store.Join(context.Background(), "same-key", "/shop", Hash(fmt.Sprint(i)), "replay")
			results <- r
			errs <- e
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for r := range results {
		if id == "" {
			id = r.Ticket.ID
		}
		if r.Ticket.ID != id || r.Ticket.Sequence != 1 {
			t.Fatal("duplicate ticket", r)
		}
	}
	for i := 0; i < 20; i++ {
		add(t, s, "unique-"+fmt.Sprint(i))
	}
	promotions := make(chan Result, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := other.Promote(context.Background(), 128)
			if e != nil {
				t.Error(e)
			}
			promotions <- r
		}()
	}
	wg.Wait()
	close(promotions)
	seen := map[string]bool{}
	for r := range promotions {
		for _, v := range r.Tickets {
			if seen[v.ID] {
				t.Fatal("duplicate READY")
			}
			seen[v.ID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatal("lease cap", len(seen))
	}
	claims := make(chan model.Ticket, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := other.Claim(context.Background(), id)
			if e != nil {
				t.Error(e)
				return
			}
			claims <- *r.Ticket
		}()
	}
	wg.Wait()
	close(claims)
	var first *model.Ticket
	for v := range claims {
		if first == nil {
			copy := v
			first = &copy
		}
		if !reflect.DeepEqual(*first, v) {
			t.Fatal("claim changed")
		}
	}
}
func TestServerTimeModelTrace(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s, c, _ := testStore(t, func(c *model.Config) { c.LeaseCap = 15; c.Rate = 30 })
			oracle, _ := model.New(c)
			random := rand.New(rand.NewSource(seed))
			ids := map[string]string{}
			for step := 0; step < 100; step++ {
				key := fmt.Sprintf("%d-%d", seed, step)
				r := add(t, s, key)
				if err := oracle.Advance(r.Now); err != nil {
					t.Fatal(err)
				}
				expected, e := oracle.Join(key, "/shop")
				if e != nil {
					t.Fatal(e)
				}
				ids[r.Ticket.ID] = expected.ID
				if r.Ticket.Sequence != expected.Sequence || r.Ticket.JoinedAt != expected.JoinedAt {
					t.Fatal("join trace mismatch")
				}
				if random.Intn(2) == 0 {
					batch := 1 + random.Intn(5)
					r, e = s.Promote(context.Background(), batch)
					r = must(t, r, e)
					_ = oracle.Advance(r.Now)
					expectedTickets := oracle.Promote(batch)
					if len(expectedTickets) != len(r.Tickets) {
						t.Fatal("promotion count")
					}
					// Also probe remaining capacity: a fresh promotion must match the model budget.
					for i, v := range r.Tickets {
						if ids[v.ID] != expectedTickets[i].ID || v.JTI != expectedTickets[i].JTI || v.AdmissionUntil != expectedTickets[i].AdmissionUntil {
							t.Fatal("FIFO/time mismatch")
						}
						claimed, e := s.Claim(context.Background(), v.ID)
						claimed = must(t, claimed, e)
						_ = oracle.Advance(claimed.Now)
						want, e := oracle.Claim(ids[v.ID])
						if e != nil || want.State != claimed.Ticket.State {
							t.Fatal("claim mismatch", e)
						}
					}
				}
			}
			r, e := s.Promote(context.Background(), 128)
			r = must(t, r, e)
			_ = oracle.Advance(r.Now)
			want := oracle.Promote(128)
			if len(r.Tickets) != len(want) {
				t.Fatal("remaining budget mismatch")
			}
		})
	}
}
func TestReadOnlyExpiryAndHeartbeat(t *testing.T) {
	s, _, _ := testStore(t, func(c *model.Config) { c.IdleTTL = 200; c.TicketTTL = 800; c.ReadyTTL = 100 })
	a := add(t, s, "a")
	before, err := s.client.Do(context.Background(), s.client.B().Hgetall().Key(s.keys[1]).Build()).AsStrMap()
	if err != nil || len(before) != 1 {
		t.Fatal("missing pre-read ticket snapshot", err)
	}
	for i := 0; i < 4; i++ {
		r, e := s.Status(context.Background(), a.Ticket.ID)
		must(t, r, e)
	}
	after, err := s.client.Do(context.Background(), s.client.B().Hgetall().Key(s.keys[1]).Build()).AsStrMap()
	if err != nil || len(after) != 1 {
		t.Fatal("missing post-read ticket snapshot", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("status mutated ticket")
	}
	time.Sleep(100 * time.Millisecond)
	r, e := s.Heartbeat(context.Background(), a.Ticket.ID)
	must(t, r, e)
	time.Sleep(130 * time.Millisecond)
	r, e = s.Status(context.Background(), a.Ticket.ID)
	must(t, r, e)
	time.Sleep(150 * time.Millisecond)
	if _, e = s.Heartbeat(context.Background(), a.Ticket.ID); !errors.Is(e, model.ErrExpired) {
		t.Fatal("revived", e)
	}
}
func TestBudgetDrainAndSchema(t *testing.T) {
	s, c, ns := testStore(t, func(c *model.Config) { c.VisitorCap = 3; c.IdempotencyCap = 3 })
	for _, k := range []string{"a", "b", "c"} {
		add(t, s, k)
	}
	if _, e := s.Join(context.Background(), "d", "/shop", Hash("d"), ""); !errors.Is(e, model.ErrCapacity) {
		t.Fatal(e)
	}
	add(t, s, "a")
	if _, e := s.Join(context.Background(), "a", "/other", Hash("a"), ""); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	r, e := s.Mode(context.Background(), "drain")
	must(t, r, e)
	if _, e = s.Join(context.Background(), "d", "/shop", Hash("d"), ""); !errors.Is(e, model.ErrDrain) {
		t.Fatal(e)
	}
	c.Rate++
	bad, e := Open(context.Background(), os.Getenv("WR_TEST_VALKEY"), ns, c)
	if bad != nil {
		bad.Close()
	}
	if !errors.Is(e, ErrSchema) {
		t.Fatal(e)
	}
}
func TestRealTimeRollingWindowAndLeaseGrace(t *testing.T) {
	s, _, _ := testStore(t, func(c *model.Config) { c.LeaseCap = 1; c.Rate = 1; c.AdmissionTTL = 60000; c.ClockSkew = 1000 })
	a := add(t, s, "a")
	add(t, s, "b")
	r, e := s.Promote(context.Background(), 1)
	r = must(t, r, e)
	until := r.Tickets[0].AdmissionUntil
	r, e = s.Claim(context.Background(), a.Ticket.ID)
	must(t, r, e)
	// Actual server TIME; no injected clock in production functions.
	time.Sleep(time.Until(time.UnixMilli(until + 100)))
	r, e = s.Promote(context.Background(), 1)
	r = must(t, r, e)
	if len(r.Tickets) != 0 {
		t.Fatal("lease released before leeway")
	}
	time.Sleep(time.Until(time.UnixMilli(until + 1100)))
	r, e = s.Promote(context.Background(), 1)
	r = must(t, r, e)
	if len(r.Tickets) != 1 {
		t.Fatal("rate/lease not reclaimed")
	}
}
func TestUnknownWriteFailureLatchesClosed(t *testing.T) {
	s, _, _ := testStore(t, nil)
	// Deliberately corrupt only this isolated test namespace, never FLUSHDB.
	s.client.Do(context.Background(), s.client.B().Set().Key(s.keys[3]).Value("wrong-type").Build())
	_, err := s.Join(context.Background(), "a", "/shop", Hash("a"), "")
	if !errors.Is(err, model.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = s.Sweep(context.Background()); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal("failed latch cleared", err)
	}
	if !strings.HasPrefix(s.keys[0], "wr:lab:test-") {
		t.Fatal("unsafe fixture")
	}
}

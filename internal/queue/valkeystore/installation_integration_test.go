//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
	"waiting-room/internal/queue/model"
)

func installationStores(t *testing.T, n int, c model.Config, global InstallationConfig) ([]*Store, string) {
	t.Helper()
	if os.Getenv("WR_TEST_VALKEY") != "127.0.0.1:16379" {
		t.Fatal("dedicated loopback Valkey required")
	}
	ns := fmt.Sprintf("wr:lab:installation-%d", time.Now().UnixNano())
	stores := make([]*Store, 0, n)
	for i := 0; i < n; i++ {
		s, err := OpenRoom(context.Background(), os.Getenv("WR_TEST_VALKEY"), ns, fmt.Sprintf("room%d", i), c, global)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, s)
		t.Cleanup(func() {
			if err := s.client.Do(context.Background(), s.client.B().Del().Key(s.keys...).Build()).Error(); err != nil {
				t.Error(err)
			}
			s.Close()
		})
	}
	return stores, ns
}

func capacity(t *testing.T, s *Store) Capacity {
	t.Helper()
	r, err := s.Capacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Capacity == nil {
		t.Fatal("missing capacity")
	}
	return *r.Capacity
}

func TestInstallationConcurrentCapacity(t *testing.T) {
	c := model.DefaultConfig()
	c.VisitorCap = 40
	c.IdempotencyCap = 80
	c.LeaseCap = 3
	stores, ns := installationStores(t, 4, c, InstallationConfig{"standard", 40, 80})
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := map[int]Result{}
	rejected := 0
	for i := 0; i < 120; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := stores[i%4].Join(context.Background(), fmt.Sprint(i), "fp", Hash(fmt.Sprint(i)), "replay")
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				accepted[i] = r
			} else if errors.Is(e, model.ErrCapacity) {
				rejected++
			} else {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if len(accepted) != 40 || rejected != 80 {
		t.Fatalf("accepted=%d rejected=%d", len(accepted), rejected)
	}
	if got := capacity(t, stores[0]); got.Visitors != 40 || got.Idempotency != 40 || !got.Warning {
		t.Fatal(got)
	}
	for i, first := range accepted {
		r, e := stores[i%4].Join(context.Background(), fmt.Sprint(i), "fp", Hash("different"), "different")
		if e != nil || r.Ticket.ID != first.Ticket.ID || r.Replay != "replay" {
			t.Fatalf("replay at cap: %v", e)
		}
	}
	for _, s := range stores {
		r, e := s.Promote(context.Background(), 128)
		if e != nil {
			t.Fatal(e)
		}
		for _, ticket := range r.Tickets {
			claimed, e := s.Claim(context.Background(), ticket.ID)
			if e != nil || claimed.Ticket.State != model.Admitted {
				t.Fatal("claim at cap", e)
			}
		}
	}
	if got := capacity(t, stores[0]); got.Visitors != 40 {
		t.Fatal(got)
	}
	other, err := OpenRoom(context.Background(), "127.0.0.1:16379", ns, "room0", c, InstallationConfig{"standard", 40, 80})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if got := capacity(t, other); got.Visitors != 40 {
		t.Fatal(got)
	}
	if _, err = OpenRoom(context.Background(), "127.0.0.1:16379", ns, "newroom", c, InstallationConfig{"standard", 41, 80}); !errors.Is(err, ErrSchema) {
		t.Fatal("mutable installation", err)
	}
}

func TestInstallationCanceledCallerDoesNotPoisonStore(t *testing.T) {
	stores, _ := installationStores(t, 1, model.DefaultConfig(), StandardInstallation())
	s := stores[0]
	first := add(t, s, "first")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Status(ctx, first.Ticket.ID); !errors.Is(e, context.Canceled) {
		t.Errorf("canceled read classification: %v", e)
	}
	if _, e := s.Join(ctx, "not-submitted", "fp", Hash("not-submitted"), "replay"); !errors.Is(e, context.Canceled) {
		t.Errorf("canceled write classification: %v", e)
	}
	if s.failed.Load() {
		t.Fatal("canceled caller poisoned all subsequent requests")
	}
	add(t, s, "second")
	if got := capacity(t, s); got.Visitors != 2 {
		t.Fatal("canceled caller mutated state", got)
	}
}

func TestInstallationCanceledPrimaryReadDoesNotPoisonStore(t *testing.T) {
	stores, _ := installationStores(t, 1, model.DefaultConfig(), StandardInstallation())
	s := stores[0]
	first := add(t, s, "first")
	// Dedicated local server only; short, bounded pause, never concurrent suites.
	if e := s.client.Do(context.Background(), s.client.B().Arbitrary("CLIENT", "PAUSE", "150", "ALL").Build()).Error(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := s.Status(ctx, first.Ticket.ID); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("expected caller timeout", e)
	}
	if s.failed.Load() {
		t.Fatal("timed out metadata read poisoned store")
	}
	add(t, s, "second")
	if capacity(t, s).Visitors != 2 {
		t.Fatal("read timeout changed capacity")
	}
}

func TestInstallationIdempotencyAndExpiry(t *testing.T) {
	c := model.DefaultConfig()
	c.VisitorCap = 4
	c.IdempotencyCap = 2
	c.LeaseCap = 1
	c.IdleTTL = 100
	c.TicketTTL = 100
	stores, _ := installationStores(t, 2, c, InstallationConfig{"standard", 4, 2})
	for _, s := range stores {
		add(t, s, "same-key")
	}
	if got := capacity(t, stores[0]); got.Visitors != 2 || got.Idempotency != 2 {
		t.Fatal(got)
	}
	time.Sleep(150 * time.Millisecond)
	if got := capacity(t, stores[0]); got.Visitors != 0 || got.Idempotency != 2 {
		t.Fatal(got)
	}
	if _, err := stores[1].Join(context.Background(), "new", "fp", Hash("new"), "replay"); !errors.Is(err, model.ErrCapacity) {
		t.Fatal(err)
	}
}

func TestInstallationPartialWriteClosesAllRooms(t *testing.T) {
	c := model.DefaultConfig()
	stores, ns := installationStores(t, 2, c, StandardInstallation())
	s := stores[0]
	// Wrong type is test-owned fault injection, after global indexes have been written.
	if err := s.client.Do(context.Background(), s.client.B().Set().Key(s.keys[2]).Value("wrong-type").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(context.Background(), "key", "fp", Hash("key"), "replay"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := stores[1].Join(context.Background(), "other", "fp", Hash("other"), "replay"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal("other room stayed open", err)
	}
	if _, err := OpenRoom(context.Background(), "127.0.0.1:16379", ns, "newroom", c, StandardInstallation()); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal("fresh client bypassed dirty latch", err)
	}
}

func TestInstallationMissingIndexFailsClosed(t *testing.T) {
	stores, _ := installationStores(t, 2, model.DefaultConfig(), StandardInstallation())
	add(t, stores[0], "key")
	s := stores[0]
	if err := s.client.Do(context.Background(), s.client.B().Del().Key(s.keys[9]).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if _, err := stores[1].Join(context.Background(), "new", "fp", Hash("new"), "replay"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatal("missing index accepted", err)
	}
}

func TestInstallationExpiredOwnerRetainsBudget(t *testing.T) {
	c := model.DefaultConfig()
	c.VisitorCap = 4
	c.IdempotencyCap = 4
	c.LeaseCap = 1
	c.IdleTTL = 100
	c.TicketTTL = 100
	c.IdempotencyTTL = 100
	stores, _ := installationStores(t, 2, c, InstallationConfig{"standard", 4, 4})
	for i := 0; i < 4; i++ {
		add(t, stores[0], fmt.Sprint(i))
	}
	time.Sleep(150 * time.Millisecond)
	if _, e := stores[1].Join(context.Background(), "new", "fp", Hash("new"), "replay"); !errors.Is(e, model.ErrCapacity) {
		t.Fatal("expired owner data freed aggregate budget before physical cleanup", e)
	}
	count, e := stores[0].client.Do(context.Background(), stores[0].client.B().Hlen().Key(stores[0].keys[1]).Build()).ToInt64()
	if e != nil || count != 4 {
		t.Fatal("fixture population", count, e)
	}
	if _, e = stores[0].Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	add(t, stores[1], "replacement")
}

func TestInstallationMissingRoomCannotResetSequence(t *testing.T) {
	c := model.DefaultConfig()
	stores, ns := installationStores(t, 2, c, StandardInstallation())
	add(t, stores[0], "before")
	s := stores[0]
	if e := s.client.Do(context.Background(), s.client.B().Del().Key(s.keys[:8]...).Build()).Error(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenRoom(context.Background(), "127.0.0.1:16379", ns, "room0", c, StandardInstallation())
	if reopened != nil {
		reopened.Close()
	}
	if !errors.Is(e, model.ErrUnavailable) {
		t.Fatal("lost Room metadata silently recreated", e)
	}
}

func TestInstallationWarningBoundaryAndTransitions(t *testing.T) {
	c := model.DefaultConfig()
	c.VisitorCap = 10
	c.IdempotencyCap = 20
	c.LeaseCap = 3
	stores, _ := installationStores(t, 2, c, InstallationConfig{"standard", 10, 20})
	for i := 0; i < 7; i++ {
		add(t, stores[i%2], fmt.Sprint(i))
	}
	if capacity(t, stores[0]).Warning {
		t.Fatal("warning below 80 percent")
	}
	first := add(t, stores[1], "eighth")
	if !capacity(t, stores[0]).Warning {
		t.Fatal("missing 80 percent warning")
	}
	updated, err := stores[1].Heartbeat(context.Background(), first.Ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	score, err := stores[1].client.Do(context.Background(), stores[1].client.B().Zscore().Key(stores[1].keys[9]).Member("room1:"+first.Ticket.ID).Build()).ToFloat64()
	if err != nil || score != float64(updated.Ticket.IdleUntil) {
		t.Fatal("heartbeat index mismatch", score, err)
	}
	r, err := stores[0].Promote(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket := r.Tickets[0]
	score, err = stores[0].client.Do(context.Background(), stores[0].client.B().Zscore().Key(stores[0].keys[9]).Member("room0:"+ticket.ID).Build()).ToFloat64()
	if err != nil || score != float64(ticket.ReadyUntil) {
		t.Fatal("READY index mismatch", err)
	}
	if _, err = stores[0].Claim(context.Background(), ticket.ID); err != nil {
		t.Fatal(err)
	}
	score, err = stores[0].client.Do(context.Background(), stores[0].client.B().Zscore().Key(stores[0].keys[9]).Member("room0:"+ticket.ID).Build()).ToFloat64()
	if err != nil || score != float64(ticket.AdmissionUntil+c.ClockSkew) {
		t.Fatal("leeway index mismatch", err)
	}
	if capacity(t, stores[0]).Visitors != 8 {
		t.Fatal("transition changed population")
	}
}

func TestInstallationBoundedCrossRoomSweep(t *testing.T) {
	c := model.DefaultConfig()
	c.VisitorCap = 400
	c.IdempotencyCap = 400
	c.LeaseCap = 1
	c.IdleTTL = 500
	c.TicketTTL = 500
	c.IdempotencyTTL = 500
	stores, _ := installationStores(t, 2, c, InstallationConfig{"standard", 400, 400})
	for i := 0; i < 400; i++ {
		add(t, stores[0], fmt.Sprint(i))
	}
	time.Sleep(550 * time.Millisecond)
	if _, err := stores[1].Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := capacity(t, stores[1]); got.Visitors != 0 || got.Idempotency != 0 || got.RetainedVisitors != 400 || got.RetainedIdempotency != 400 || !got.Warning {
		t.Fatal("inactive owner reservations were released", got)
	}
	if _, err := stores[1].Join(context.Background(), "blocked", "fp", Hash("blocked"), "replay"); !errors.Is(err, model.ErrCapacity) {
		t.Fatal("retained capacity not enforced", err)
	}
	if _, err := stores[0].Sweep(context.Background()); !errors.Is(err, ErrSweep) {
		t.Fatal("unbounded owner cleanup", err)
	}
	for i := 0; i < 4; i++ {
		_, err := stores[0].Sweep(context.Background())
		if err == nil {
			break
		}
		if !errors.Is(err, ErrSweep) {
			t.Fatal(err)
		}
	}
	if got := capacity(t, stores[1]); got.Visitors != 0 || got.Idempotency != 0 || got.RetainedVisitors != 0 || got.RetainedIdempotency != 0 {
		t.Fatal(got)
	}
	add(t, stores[1], "replacement")
	if got := capacity(t, stores[1]); got.Visitors != 1 || got.Idempotency != 1 || got.RetainedVisitors != 1 || got.RetainedIdempotency != 1 {
		t.Fatal(got)
	}
	// Cleaning the original room must not erase the replacement room's indexes.
	for i := 0; i < 4; i++ {
		_, err := stores[0].Sweep(context.Background())
		if err == nil {
			break
		}
		if !errors.Is(err, ErrSweep) {
			t.Fatal(err)
		}
	}
	if capacity(t, stores[1]).Visitors != 1 {
		t.Fatal("cross-room expiry erased live visitor")
	}
}

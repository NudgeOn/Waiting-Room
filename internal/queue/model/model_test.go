// SPDX-License-Identifier: Apache-2.0
package model

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func setup(t *testing.T, edit func(*Config)) *Model {
	t.Helper()
	c := DefaultConfig()
	c.LeaseCap = 2
	c.Rate = 2
	if edit != nil {
		edit(&c)
	}
	m, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func join(t *testing.T, m *Model, key string) Ticket {
	t.Helper()
	v, err := m.Join(key, "same-target")
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func advance(t *testing.T, m *Model, at int64) {
	t.Helper()
	if err := m.Advance(at); err != nil {
		t.Fatal(err)
	}
}

func TestConfigBounds(t *testing.T) {
	for _, edit := range []func(*Config){func(c *Config) { c.LeaseCap = c.VisitorCap + 1 }, func(c *Config) { c.Rate = 0 }, func(c *Config) { c.AdmissionTTL = 1 }, func(c *Config) { c.ClockSkew = 30_001 }, func(c *Config) { c.IdleTTL = 0 }} {
		c := DefaultConfig()
		edit(&c)
		if _, err := New(c); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestFIFOAndCapacity(t *testing.T) {
	m := setup(t, nil)
	a := join(t, m, "a")
	b := join(t, m, "b")
	join(t, m, "c")
	r := m.Promote(10)
	if len(r) != 2 || r[0].ID != a.ID || r[1].ID != b.ID {
		t.Fatal(r)
	}
	if len(m.Promote(10)) != 0 || m.LeaseCount() != 2 {
		t.Fatal("READY reservation not counted")
	}
	if _, err := m.Claim(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.Promote(10)) != 0 {
		t.Fatal("claim freed capacity")
	}
}

func TestJoinReplayAndBudget(t *testing.T) {
	m := setup(t, func(c *Config) { c.VisitorCap = 2; c.IdempotencyCap = 2 })
	a := join(t, m, "a")
	join(t, m, "b")
	if _, err := m.Join("c", "same-target"); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if got := join(t, m, "a"); got.ID != a.ID || m.VisitorCount() != 2 {
		t.Fatal(got)
	}
	if _, err := m.Join("a", "different-target"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestStatusReadOnlyAndHeartbeat(t *testing.T) {
	m := setup(t, nil)
	a := join(t, m, "a")
	before := m.Snapshot()
	for i := 0; i < 5; i++ {
		if _, err := m.Status(a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, m.Snapshot()) {
		t.Fatal("status mutated state")
	}
	advance(t, m, 300_000)
	if err := m.Heartbeat(a.ID); err != nil {
		t.Fatal(err)
	}
	advance(t, m, 600_000)
	got, err := m.Status(a.ID)
	if err != nil || got.IdleUntil != 900_000 {
		t.Fatal(got, err)
	}
	advance(t, m, 900_000)
	if _, err := m.Status(a.ID); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
}

func TestAbsoluteExpiryAndNoRevival(t *testing.T) {
	m := setup(t, func(c *Config) { c.IdleTTL = 10_000; c.TicketTTL = 20_000 })
	a := join(t, m, "a")
	for _, at := range []int64{9_000, 18_000} {
		advance(t, m, at)
		if err := m.Heartbeat(a.ID); err != nil {
			t.Fatal(err)
		}
	}
	advance(t, m, 20_000)
	if err := m.Heartbeat(a.ID); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	if got := join(t, m, "a"); got.ID != a.ID || got.State != Expired {
		t.Fatal("idempotency revived ticket", got)
	}
}

func TestReadyExpiryDoesNotRefundRate(t *testing.T) {
	m := setup(t, func(c *Config) { c.ReadyTTL = 10_000 })
	join(t, m, "a")
	join(t, m, "b")
	join(t, m, "c")
	m.Promote(10)
	advance(t, m, 10_000)
	if m.LeaseCount() != 0 {
		t.Fatal("abandoned READY leaked")
	}
	if len(m.Promote(1)) != 0 {
		t.Fatal("rate was refunded")
	}
	advance(t, m, Minute-1)
	if len(m.Promote(1)) != 0 {
		t.Fatal("early window release")
	}
	advance(t, m, Minute)
	if len(m.Promote(1)) != 1 {
		t.Fatal("window did not release")
	}
}

func TestClaimRetryAndSkewReservation(t *testing.T) {
	m := setup(t, func(c *Config) { c.AdmissionTTL = Minute })
	a := join(t, m, "a")
	m.Promote(1)
	one, err := m.Claim(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	advance(t, m, 500)
	two, err := m.Claim(a.ID)
	if err != nil || one != two {
		t.Fatal("claim changed", one, two, err)
	}
	advance(t, m, Minute)
	if _, err := m.Claim(a.ID); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	if m.LeaseCount() != 1 {
		t.Fatal("lease released before verifier leeway")
	}
	advance(t, m, Minute+30_000)
	if m.LeaseCount() != 0 {
		t.Fatal("lease not released")
	}
}

func TestRecoveryWindowAndNewEpoch(t *testing.T) {
	m := setup(t, nil)
	a := join(t, m, "a")
	m.Promote(1)
	until := m.Uncertain()
	if until != 930_000 {
		t.Fatal(until)
	}
	if len(m.Promote(1)) != 0 {
		t.Fatal("promotion during uncertainty")
	}
	if _, err := m.Claim(a.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := m.SetHold(false); !errors.Is(err, ErrUnavailable) {
		t.Fatal("bypassed recovery", err)
	}
	advance(t, m, until-1)
	if err := m.Resume(true); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	advance(t, m, until)
	if err := m.Resume(false); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := m.Resume(true); err != nil {
		t.Fatal(err)
	}
	m.NewEpoch()
	if _, err := m.Status(a.ID); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	b := join(t, m, "b")
	if b.Epoch != 2 || b.Sequence != 1 {
		t.Fatal(b)
	}
	if len(m.Promote(1)) != 0 {
		t.Fatal("new epoch must stay HOLD")
	}
}

func TestHoldDrainAndClock(t *testing.T) {
	m := setup(t, nil)
	join(t, m, "a")
	if err := m.SetHold(true); err != nil {
		t.Fatal(err)
	}
	if len(m.Promote(1)) != 0 {
		t.Fatal("HOLD promoted")
	}
	if err := m.Drain(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join("new", "same-target"); !errors.Is(err, ErrDrain) {
		t.Fatal(err)
	}
	if len(m.Promote(1)) != 1 {
		t.Fatal("cutoff waiter not promoted")
	}
	advance(t, m, 100)
	if err := m.Advance(99); !errors.Is(err, ErrClock) {
		t.Fatal(err)
	}
}

func TestRandomizedModelInvariants(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			m := setup(t, func(c *Config) {
				c.VisitorCap = 15
				c.LeaseCap = 3
				c.Rate = 5
				c.ReadyTTL = 15_000
				c.AdmissionTTL = Minute
				c.IdempotencyCap = 30
			})
			rng := rand.New(rand.NewSource(seed))
			var now int64
			for step := 0; step < 400; step++ {
				now += int64(rng.Intn(3000))
				advance(t, m, now)
				switch rng.Intn(5) {
				case 0:
					_, _ = m.Join(fmt.Sprint(rng.Intn(80)), "target")
				case 1:
					m.Promote(1 + rng.Intn(6))
				case 2:
					for _, ticket := range m.Snapshot() {
						if ticket.State == Ready {
							_, _ = m.Claim(ticket.ID)
							break
						}
					}
				case 3:
					for _, ticket := range m.Snapshot() {
						if ticket.State == Waiting {
							_ = m.Heartbeat(ticket.ID)
							break
						}
					}
				case 4:
					_ = m.SetHold(rng.Intn(5) == 0)
				}
				if m.LeaseCount() > 3 || m.VisitorCount() > 15 {
					t.Fatal("capacity exceeded", seed, step)
				}
			}
			seen := map[string]bool{}
			var last uint64
			history := m.Promotions()
			for i, p := range history {
				if seen[p.ID] || p.Sequence <= last {
					t.Fatal("duplicate or FIFO inversion", p)
				}
				seen[p.ID] = true
				last = p.Sequence
				count := 0
				for j := 0; j <= i; j++ {
					if history[j].At > p.At-Minute {
						count++
					}
				}
				if count > 5 {
					t.Fatal("rolling window exceeded", p, count)
				}
			}
		})
	}
}

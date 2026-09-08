//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"fmt"
	valkey "github.com/valkey-io/valkey-go"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func recoveryStore(t *testing.T, c model.Config) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	opt := valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}
	owner, err := valkey.NewClient(opt)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = InstallRecoveryLibrary(ctx, owner); err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("wr:lab:v5-trace-%d", time.Now().UnixNano())
	s, err := OpenRecoveryRoom(ctx, opt, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.client.Do(ctx, s.client.B().Del().Key(s.keys...).Build()); s.Close() })
	return s, ns
}
func compareTicket(t *testing.T, actual, want model.Ticket) {
	t.Helper()
	actual.ID = want.ID
	actual.PromotedAt = 0
	want.PromotedAt = 0
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("runtime-v5 model ticket mismatch")
	}
}
func TestRuntimeV5CommandModelTrace(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			ctx := context.Background()
			c := model.DefaultConfig()
			c.LeaseCap = 30
			c.Rate = 40
			c.AdmissionTTL = 60000
			c.ReadyTTL = 60000
			s, _ := recoveryStore(t, c)
			oracle, _ := model.New(c)
			random := rand.New(rand.NewSource(seed))
			rt := control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}
			if _, err := s.Configure(ctx, c, 1, rt); err != nil {
				t.Fatal(err)
			}
			ids := map[string]string{}
			keys := []string{}
			for step := 0; step < 300; step++ {
				key := fmt.Sprintf("seed-%d-step-%d", seed, step)
				out := add(t, s, key)
				if err := oracle.Advance(out.Now); err != nil {
					t.Fatal(err)
				}
				want, err := oracle.Join(key, "/shop")
				if err != nil {
					t.Fatal(err)
				}
				compareTicket(t, *out.Ticket, want)
				ids[out.Ticket.ID] = want.ID
				keys = append(keys, out.Ticket.ID)
				switch random.Intn(5) {
				case 0:
					// Exact idempotency and changed fingerprint exercise failure paths too.
					replay := add(t, s, key)
					oracle.Advance(replay.Now)
					want, _ = oracle.Join(key, "/shop")
					compareTicket(t, *replay.Ticket, want)
					if _, err = s.Join(ctx, key, "different-target", Hash("unused"), "opaque"); err != model.ErrConflict {
						t.Fatal("fingerprint conflict", err)
					}
				case 1:
					id := keys[random.Intn(len(keys))]
					before, err := s.client.Do(ctx, s.client.B().Hgetall().Key(s.keys[1]).Build()).AsStrMap()
					if err != nil {
						t.Fatal(err)
					}
					out, err = s.Status(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					oracle.Advance(out.Now)
					want, err = oracle.Status(ids[id])
					if err != nil {
						t.Fatal(err)
					}
					compareTicket(t, *out.Ticket, want)
					after, _ := s.client.Do(ctx, s.client.B().Hgetall().Key(s.keys[1]).Build()).AsStrMap()
					if !reflect.DeepEqual(before, after) {
						t.Fatal("read-only status wrote records")
					}
				case 2:
					id := keys[random.Intn(len(keys))]
					out, err = s.Heartbeat(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					oracle.Advance(out.Now)
					if err = oracle.Heartbeat(ids[id]); err != nil {
						t.Fatal(err)
					}
					want, _ = oracle.Status(ids[id])
					compareTicket(t, *out.Ticket, want)
				case 3:
					hold := random.Intn(2) == 0
					rt.Revision++
					rt.Mode = "AUTO"
					if hold {
						rt.Mode = "HOLD"
					}
					out, err = s.Configure(ctx, c, 1, rt)
					if err != nil {
						t.Fatal(err)
					}
					oracle.Advance(out.Now)
					oracle.SetHold(hold)
				case 4:
					batch := 1 + random.Intn(8)
					out, err = s.Promote(ctx, batch)
					if err != nil {
						t.Fatal(err)
					}
					oracle.Advance(out.Now)
					expected := oracle.Promote(batch)
					if len(expected) != len(out.Tickets) {
						t.Fatal("promotion budget")
					}
					for i, got := range out.Tickets {
						compareTicket(t, got, expected[i])
						claimed, err := s.Claim(ctx, got.ID)
						if err != nil {
							t.Fatal(err)
						}
						oracle.Advance(claimed.Now)
						wanted, err := oracle.Claim(ids[got.ID])
						if err != nil {
							t.Fatal(err)
						}
						compareTicket(t, *claimed.Ticket, wanted)
					}
				}
			}
			rt.Revision++
			rt.Mode = "DRAINING"
			out, err := s.Configure(ctx, c, 1, rt)
			if err != nil {
				t.Fatal(err)
			}
			oracle.Advance(out.Now)
			oracle.Drain()
			if _, err = s.Join(ctx, Hash("drain-new"), Hash("/shop"), Hash("drain-new"), "opaque"); err != model.ErrDrain {
				t.Fatal("drain accepted new visitor", err)
			}
			out, err = s.Promote(ctx, 128)
			if err != nil {
				t.Fatal(err)
			}
			oracle.Advance(out.Now)
			want := oracle.Promote(128)
			if len(want) != len(out.Tickets) {
				t.Fatal("drain budget")
			}
			cap, err := s.Capacity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if cap.Capacity.Visitors != oracle.VisitorCount() {
				t.Fatal("shared visitor budget")
			}
		})
	}
}

func TestRuntimeV5SharedInstallationTenThousandCap(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	left, ns := recoveryStore(t, c)
	right, err := OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "bbbbbbbbbbbbbbbbbbbb", c, StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	defer right.client.Do(ctx, right.client.B().Del().Key(right.keys[:8]...).Build())
	jobs := make(chan int)
	failures := make(chan error, 10000)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				store := left
				if i%2 == 1 {
					store = right
				}
				key := fmt.Sprintf("shared-v5-cap-%d", i)
				_, err := store.Join(ctx, key, "/shop", Hash(key), "private-test-replay")
				if err != nil {
					failures <- err
				}
			}
		}()
	}
	for i := 0; i < 10000; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal("cap population", err)
	}
	for _, store := range []*Store{left, right} {
		out, err := store.Capacity(ctx)
		if err != nil || out.Capacity.Visitors != 10000 || out.Capacity.RetainedVisitors != 10000 {
			t.Fatal("shared cap mismatch", err)
		}
		if _, err = store.Join(ctx, "over-cap", "/shop", Hash("over-cap"), "private"); err != model.ErrCapacity {
			t.Fatal("installation over-admitted", err)
		}
	}
	replay := add(t, left, "shared-v5-cap-0")
	if replay.Ticket.Sequence < 1 {
		t.Fatal("lost retained ticket")
	}
	out, err := left.Capacity(ctx)
	if err != nil || out.Capacity.Visitors != 10000 {
		t.Fatal("replay reserved again")
	}
}

func TestRuntimeV5SharedFenceAndCorruptRecoveryRemainClosed(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	c.ReadyTTL = 60000
	s, ns := recoveryStore(t, c)
	ticket := add(t, s, "retained")
	before := s.recovery
	s.uncertainty.Add(1)
	held, err := s.MaintainRecovery(ctx)
	if err != nil || held.Mode != "RECOVERY_HOLD" || held.Fence <= before.Fence || held.UnsafeUntil-held.Now != 90000 {
		t.Fatal("shared safety fence", err)
	}
	second, err := OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	observed, err := second.MaintainRecovery(ctx)
	if err != nil || observed.Fence != held.Fence || observed.UnsafeUntil != held.UnsafeUntil {
		t.Fatal("other coordinator diverged")
	}
	if _, err = second.Claim(ctx, ticket.Ticket.ID); err != model.ErrUnavailable {
		t.Fatal("claim during hold", err)
	}
	stale := s.client.Do(ctx, s.client.B().Fcall().Function("wr_r5_command").Numkeys(int64(len(s.keys))).Key(s.keys...).Arg("promote", "1", before.Primary, fmt.Sprint(before.Fence)).Build()).Error()
	if stale == nil || !strings.Contains(stale.Error(), "WR_FENCED") {
		t.Fatal("stale fenced write accepted")
	}
	if err = s.client.Do(ctx, s.client.B().Zrem().Key(s.keys[9]).Member("abcdefghijklmnopqrst:"+ticket.Ticket.ID).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	s.client.Do(ctx, s.client.B().Hset().Key(s.keys[8]).FieldValue().FieldValue("unsafeUntil", fmt.Sprint(time.Now().UnixMilli()-1)).Build())
	for range 8 {
		held, err = s.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if held.Mode != "RECOVERY_HOLD" || held.ValidationError == "" {
		t.Fatal("corrupted owner index recovered", held)
	}
	if count, err := s.client.Do(ctx, s.client.B().Zcard().Key(s.keys[9]).Build()).AsInt64(); err != nil || count != 0 {
		t.Fatal("index silently reconstructed")
	}
	if _, err = s.Join(ctx, "after-corruption", "/shop", Hash("after-corruption"), "private"); err != model.ErrUnavailable {
		t.Fatal("corrupted queue admitted", err)
	}
}

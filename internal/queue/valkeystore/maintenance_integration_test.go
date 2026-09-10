//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func queueValues(t *testing.T, s *Store) []string {
	t.Helper()
	out := make([]string, len(s.keys))
	for i, key := range s.keys {
		kind, err := s.client.Do(context.Background(), s.client.B().Type().Key(key).Build()).ToString()
		if err != nil {
			t.Fatal("fixture type unavailable")
		}
		if kind == "hash" {
			// Compare every field/value, independent of hash serialization order.
			fields, err := s.client.Do(context.Background(), s.client.B().Hgetall().Key(key).Build()).AsStrMap()
			if err != nil {
				t.Fatal("fixture hash unavailable")
			}
			canonical, err := json.Marshal(fields)
			if err != nil {
				t.Fatal("fixture hash encoding unavailable")
			}
			out[i] = "hash:" + string(canonical)
			continue
		}
		raw, err := s.client.Do(context.Background(), s.client.B().Dump().Key(key).Build()).ToString()
		if err != nil && !valkey.IsValkeyNil(err) {
			t.Fatal("fixture dump unavailable")
		}
		out[i] = raw
	}
	return out
}

func TestIdlePromotionDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	s, _ := recoveryStore(t, c)
	original := s.client
	observed := &observedRecoveryClient{Client: original}
	s.client = observed
	t.Cleanup(func() { s.client = original })
	for revision, mode := range []string{"HOLD", "OFF", "AUTO", "DRAINING"} {
		if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: int64(revision + 1), Epoch: 1, Mode: mode}); err != nil {
			t.Fatal(err)
		}
		before := queueValues(t, s)
		writes := observed.writes.Load()
		time.Sleep(5 * time.Millisecond)
		for range 10 {
			out, err := s.Promote(ctx, 128)
			if err != nil || len(out.Tickets) != 0 {
				t.Fatal("idle promotion failed", err)
			}
		}
		if observed.writes.Load() != writes {
			t.Fatal("idle promotion submitted a write RPC in " + mode)
		}
		after := queueValues(t, s)
		if !reflect.DeepEqual(before, after) {
			for i := range before {
				if before[i] != after[i] {
					t.Logf("changed key index=%d bytes=%d -> %d", i, len(before[i]), len(after[i]))
				}
			}
			t.Fatal("idle promotion changed persistent state in " + mode)
		}
	}
}

func TestIdlePromotionReadLossDoesNotFenceWaitingVisitors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := model.DefaultConfig()
	s, _ := recoveryStore(t, c)
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "HOLD"}); err != nil {
		t.Fatal(err)
	}
	first := add(t, s, "before-idle-read-loss")
	before := queueValues(t, s)
	fence := s.recovery.Fence
	fault := loseOneReply(t, s, "wr_qm1_needed")
	if _, err := s.Promote(ctx, 128); err == nil || !fault.used.Load() {
		t.Fatal("read-only promotion probe reply was not lost")
	}
	if s.uncertainty.Load() != 0 || s.recovery.Fence != fence {
		t.Fatal("idle read loss fenced installation")
	}
	for {
		_, err := s.Promote(ctx, 128)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("read client did not reconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reflect.DeepEqual(before, queueValues(t, s)) {
		t.Fatal("idle read loss changed retained queue")
	}
	second := add(t, s, "after-idle-read-loss")
	if second.Ticket.Sequence != first.Ticket.Sequence+1 {
		t.Fatal("visitor order changed")
	}
}

func TestMaintenanceProbeRetainsRealExpiryAndPromotion(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.IdleTTL = 500
	c.IdempotencyTTL = 500
	c.ReadyTTL = 30
	s, _ := recoveryStore(t, c)
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "HOLD"}); err != nil {
		t.Fatal(err)
	}
	first := add(t, s, "idle-expiry")
	before := queueValues(t, s)
	if _, err := s.Promote(ctx, 128); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, queueValues(t, s)) {
		t.Fatal("unexpired held visitor was written")
	}
	time.Sleep(650 * time.Millisecond)
	if _, err := s.Promote(ctx, 128); err != nil {
		t.Fatal(err)
	}
	metrics, err := s.Metrics(ctx)
	if err != nil || metrics.Metrics.Waiting != 0 {
		t.Fatal("HOLD skipped due expiry", err)
	}
	if count, err := s.client.Do(ctx, s.client.B().Hlen().Key(s.keys[6]).Build()).ToInt64(); err != nil || count != 0 {
		t.Fatal("expired replay not cleaned")
	}
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 2, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	second := add(t, s, "actual-promotion")
	if second.Ticket.Sequence != first.Ticket.Sequence+1 {
		t.Fatal("expiry changed FIFO sequence")
	}
	promoted, err := s.Promote(ctx, 1)
	if err != nil || len(promoted.Tickets) != 1 || promoted.Tickets[0].ID != second.Ticket.ID {
		t.Fatal("eligible admission not promoted", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := s.Promote(ctx, 128); err != nil {
		t.Fatal(err)
	}
	metrics, err = s.Metrics(ctx)
	if err != nil || metrics.Metrics.Ready != 0 || metrics.Metrics.Leases != 0 {
		t.Fatal("READY expiry skipped", err)
	}
}

func TestNeededPromotionWriteLossStillFences(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := model.DefaultConfig()
	s, _ := recoveryStore(t, c)
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	add(t, s, "eligible-write-loss")
	fault := loseOneReply(t, s, "wr_r6_command")
	if _, err := s.Promote(ctx, 1); err == nil || !fault.used.Load() || s.uncertainty.Load() == 0 {
		t.Fatal("actual promotion write loss was ignored")
	}
	state, err := s.MaintainRecovery(ctx)
	if err != nil || state.Mode != "RECOVERY_HOLD" || state.UnsafeUntil <= state.Now {
		t.Fatal("actual promotion lost safety hold")
	}
}

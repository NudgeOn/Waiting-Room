//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

func TestRuntimeJoinCompletesBoundedReplayExpiry(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.IdempotencyTTL = 3000
	s, _ := recoveryStore(t, c)
	var last Result
	for i := range 300 {
		last = add(t, s, "expiry-burst-"+time.Duration(i).String())
	}
	fence := s.recovery.Fence
	// Real expiration of this fixture's short-lived replay records. Waiting
	// visitors retain their normal TTL and may not be dropped by cleanup.
	time.Sleep(time.Until(time.UnixMilli(last.Now+c.IdempotencyTTL)) + 20*time.Millisecond)
	joined, err := s.Join(ctx, "after-replay-burst", "/shop", Hash("after-replay-ticket"), "sealed-after-replay")
	if err != nil {
		t.Fatal("a bounded backlog of expired replay records escaped as a join failure", err)
	}
	if joined.Ticket == nil || joined.Ticket.Sequence != last.Ticket.Sequence+1 {
		t.Fatal("cleanup changed the visitor sequence")
	}
	replayed, err := s.Join(ctx, "after-replay-burst", "/shop", Hash("different-retry-ticket"), "different-sealed-retry")
	if err != nil || replayed.Ticket == nil || replayed.Ticket.ID != joined.Ticket.ID || replayed.Replay != joined.Replay {
		t.Fatal("cleanup retry changed the exact join result", err)
	}
	metrics, err := s.Metrics(ctx)
	if err != nil || metrics.Metrics == nil || metrics.Metrics.Waiting != 301 || s.recovery.Fence != fence || s.uncertainty.Load() != 0 {
		t.Fatal("replay expiry affected active visitors or recovery state", err)
	}
}

type cancelAfterSweepClient struct {
	valkey.Client
	cancel context.CancelFunc
	calls  int
}

func (c *cancelAfterSweepClient) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	c.calls++
	result := c.Client.Do(ctx, cmd)
	if errors.Is(classify(result.Error()), ErrSweep) {
		c.cancel()
	}
	return result
}

func TestExpiryRetryKeepsOriginalCancellation(t *testing.T) {
	c := model.DefaultConfig()
	c.IdempotencyTTL = 3000
	s, _ := recoveryStore(t, c)
	var last Result
	for i := range 300 {
		last = add(t, s, "cancel-burst-"+time.Duration(i).String())
	}
	time.Sleep(time.Until(time.UnixMilli(last.Now+c.IdempotencyTTL)) + 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := s.client
	wrapped := &cancelAfterSweepClient{Client: original, cancel: cancel}
	s.client = wrapped
	t.Cleanup(func() { s.client = original })
	_, err := s.Join(ctx, "cancel-after-cleanup", "/shop", Hash("cancel-ticket"), "cancel-sealed")
	if !errors.Is(err, context.Canceled) || wrapped.calls != 1 || s.uncertainty.Load() != 0 {
		t.Fatal("retry reset the canceled caller or submitted another command", wrapped.calls, err)
	}
}

func TestRuntimeExpiryRetryRemainsBounded(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.IdempotencyTTL = 5000
	s, _ := recoveryStore(t, c)
	var last Result
	for i := range 1300 {
		last = add(t, s, "bounded-burst-"+time.Duration(i).String())
	}
	// Earlier replay rows can legitimately expire while the real AOF-backed
	// fixture is being populated. Assert the actual remaining backlog rather
	// than counting visitors as if all replay records shared one creation time.
	seeded, err := s.client.Do(ctx, s.client.B().Hlen().Key(s.keys[6]).Build()).ToInt64()
	if err != nil || seeded <= 8*128 {
		t.Fatal("fixture did not retain enough replay records for the retry bound", seeded, err)
	}
	time.Sleep(time.Until(time.UnixMilli(last.Now+c.IdempotencyTTL)) + 20*time.Millisecond)
	_, err = s.Join(ctx, "bounded-join", "/shop", Hash("bounded-ticket"), "bounded-sealed")
	if !errors.Is(err, ErrSweep) {
		t.Fatal("more than eight cleanup batches were hidden", err)
	}
	remaining, err := s.client.Do(ctx, s.client.B().Hlen().Key(s.keys[6]).Build()).ToInt64()
	if err != nil || remaining != seeded-8*128 || s.uncertainty.Load() != 0 {
		t.Fatal("cleanup attempt limit or acknowledged-write boundary changed", remaining, err)
	}
	joined, err := s.Join(ctx, "bounded-join", "/shop", Hash("bounded-ticket"), "bounded-sealed")
	if err != nil || joined.Ticket == nil || joined.Ticket.Sequence != last.Ticket.Sequence+1 {
		t.Fatal("next explicit attempt did not resume the same operation", err)
	}
}

func TestLostExpiryCleanupReplyStillFences(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.IdempotencyTTL = 3000
	s, _ := recoveryStore(t, c)
	var last Result
	for i := range 300 {
		last = add(t, s, "lost-burst-"+time.Duration(i).String())
	}
	time.Sleep(time.Until(time.UnixMilli(last.Now+c.IdempotencyTTL)) + 20*time.Millisecond)
	fault := loseOneReply(t, s, "wr_r6_command")
	_, err := s.Join(ctx, "lost-cleanup-join", "/shop", Hash("lost-cleanup-ticket"), "lost-cleanup-sealed")
	if err == nil || !fault.used.Load() || s.uncertainty.Load() == 0 || s.recovery.Mode != "RECOVERY_HOLD" {
		t.Fatal("an unacknowledged cleanup write was retried as a known safe response", err)
	}
}

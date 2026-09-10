//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"waiting-room/internal/queue/model"
)

// Pause the first check so cancellation between the initial check and RPC is
// deterministic, independently of the maintenance network lock.
type checkedContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
	release chan struct{}
}

func (c *checkedContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked); <-c.release })
	return err
}

func TestRuntimeCancellationAfterFirstCheckDoesNotWriteOrFence(t *testing.T) {
	s, _ := recoveryStore(t, model.DefaultConfig())
	before := s.recovery
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &checkedContext{Context: ctx, checked: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := s.Join(observed, "cancelled-during-recovery-wait", "fp", Hash("cancelled-wait"), "replay")
		done <- err
	}()
	select {
	case <-observed.checked:
	case <-time.After(time.Second):
		close(observed.release)
		t.Fatal("request never checked its context")
	}
	cancel()
	close(observed.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("local cancellation became queue uncertainty: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled request did not finish")
	}
	if s.uncertainty.Load() != 0 {
		t.Error("unsubmitted write increased uncertainty")
	}
	after, err := s.MaintainRecovery(context.Background())
	if err != nil || after.Mode != "ACTIVE" || after.Fence != before.Fence {
		t.Fatalf("unsubmitted write fenced unrelated visitors: mode=%s fence=%d, err=%v", after.Mode, after.Fence, err)
	}
	result := add(t, s, "next-live-visitor")
	if result.Ticket.Sequence != 1 {
		t.Fatal("cancelled request consumed a ticket sequence")
	}
}

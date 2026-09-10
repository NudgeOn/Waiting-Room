//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

func TestRecoveryNetworkLockDoesNotConsumeJoinBudget(t *testing.T) {
	s, _ := recoveryStore(t, model.DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// MaintainRecovery owns this lock across INFO/FCALL. Its network work must
	// not prevent reading the last acknowledged state for an independent call.
	s.recoveryMu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.Join(ctx, "join-during-recovery-io", "/shop", Hash("recovery-io-ticket"), "sealed")
		done <- err
	}()
	select {
	case err := <-done:
		s.recoveryMu.Unlock()
		if err != nil {
			t.Fatal("join lost its original budget while a recovery observation was busy", err)
		}
	case <-time.After(2500 * time.Millisecond):
		s.recoveryMu.Unlock()
		<-done
		t.Fatal("join was blocked behind recovery network I/O")
	}
	if s.uncertainty.Load() != 0 {
		t.Fatal("recovery observation caused a write uncertainty")
	}
}

type observedRecoveryClient struct {
	valkey.Client
	calls  atomic.Int32
	writes atomic.Int32
}

func (c *observedRecoveryClient) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	c.calls.Add(1)
	if cmd.Commands()[0] == "FCALL" {
		c.writes.Add(1)
	}
	return c.Client.Do(ctx, cmd)
}

func TestRecoverySnapshotCannotBypassSharedHoldOrFence(t *testing.T) {
	s, _ := recoveryStore(t, model.DefaultConfig())
	ctx := context.Background()
	old := s.recoverySnapshot.Load()
	originalValue := *old
	s.uncertainty.Add(1)
	held, err := s.MaintainRecovery(ctx)
	if err != nil || held.Mode != "RECOVERY_HOLD" || held.Fence <= old.Fence || *old != originalValue {
		t.Fatal("published state was not immutable or shared hold was not established", err)
	}
	original := s.client
	wrapped := &observedRecoveryClient{Client: original}
	s.client = wrapped
	t.Cleanup(func() { s.client = original })
	_, err = s.Join(ctx, "held-snapshot", "/shop", Hash("held-snapshot-ticket"), "sealed")
	if !errors.Is(err, model.ErrUnavailable) || wrapped.calls.Load() != 0 {
		t.Fatal("published recovery hold allowed a queue RPC", err)
	}
	// A reader can load a previously acknowledged state just before recovery
	// changes. The real server must reject its stale fence without any write.
	s.recoverySnapshot.Store(old)
	before := queueValues(t, s)
	incident := s.uncertainty.Load()
	_, err = s.Join(ctx, "stale-snapshot", "/shop", Hash("stale-snapshot-ticket"), "sealed")
	if !errors.Is(err, model.ErrUnavailable) || s.uncertainty.Load() != incident || !reflect.DeepEqual(before, queueValues(t, s)) {
		t.Fatal("stale acknowledged snapshot bypassed the current server fence", err)
	}
	s.recoveryMu.Lock()
	s.publishRecovery(held)
	s.recoveryMu.Unlock()
}

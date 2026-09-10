//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func TestRuntimeSharedRecoveryWindowAndFence(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	c.ReadyTTL = 60000
	s, ns := runtimeStore(t, c)
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	a := add(t, s, "first-recovery")
	b := add(t, s, "second-recovery")
	old := s.recovery
	s.uncertainty.Add(1)
	held, err := s.MaintainRecovery(ctx)
	if err != nil || held.Mode != "RECOVERY_HOLD" || held.Fence <= old.Fence || held.UnsafeUntil-held.Now != 90000 {
		t.Fatal("handshake window", held, err)
	}
	other, err := OpenRuntimeRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	again, err := other.MaintainRecovery(ctx)
	if err != nil || again.Fence != held.Fence || again.UnsafeUntil != held.UnsafeUntil {
		t.Fatal("shared hold extended or diverged", again, err)
	}
	for _, store := range []*Store{s, other} {
		if _, err := store.Promote(ctx, 1); err != model.ErrUnavailable {
			t.Fatal("promoted in unsafe window", err)
		}
		if _, err := store.Claim(ctx, a.Ticket.ID); err != model.ErrUnavailable {
			t.Fatal("claimed in unsafe window", err)
		}
		metrics, err := store.Metrics(ctx)
		if err != nil || metrics.Metrics.Mode != "RECOVERY_HOLD" || metrics.Metrics.RecoveryUntil != held.UnsafeUntil {
			t.Fatal("recovery metrics", err)
		}
	}
	err = s.client.Do(ctx, s.client.B().Fcall().Function("wr_r4_command").Numkeys(11).Key(s.keys...).Arg("promote", "1", old.Primary, strconv.FormatUint(old.Fence, 10)).Build()).Error()
	if err == nil || !strings.Contains(err.Error(), "WR_FENCED") {
		t.Fatal("stale fence wrote", err)
	}
	// Simulated elapsed safety window for bounded validation logic only. A
	// separate Docker restart test must exercise the real >=90 second window.
	if err = s.client.Do(ctx, s.client.B().Hset().Key(s.keys[8]).FieldValue().FieldValue("unsafeUntil", strconv.FormatInt(held.Now-1, 10)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		again, err = s.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if again.Mode == "ACTIVE" {
			break
		}
	}
	if again.Mode != "ACTIVE" {
		t.Fatal("consistent queue did not recover", again)
	}
	promoted, err := s.Promote(ctx, 2)
	if err != nil || len(promoted.Tickets) != 2 || promoted.Tickets[0].ID != a.Ticket.ID || promoted.Tickets[1].ID != b.Ticket.ID {
		t.Fatal("recovery lost FIFO", err)
	}
}

func TestRuntimeRealPrimaryRestartSafetyWindow(t *testing.T) {
	name := os.Getenv("WR_TEST_RUNTIME_RESTART")
	if name == "" {
		t.Skip("explicit disposable candidate restart required")
	}
	if runtimeTestAddress(t) != "127.0.0.1:16380" || !regexp.MustCompile(`^waiting-room-runtime-v4-candidate-[0-9]{2}$`).MatchString(name) {
		t.Fatal("only the dedicated candidate may restart")
	}
	ctx := context.Background()
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	c.ReadyTTL = 60000
	s, ns := runtimeStore(t, c)
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	admitted := add(t, s, "before-real-restart")
	_, err := s.Promote(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, admitted.Ticket.ID); err != nil {
		t.Fatal(err)
	}
	waiting := add(t, s, "waiting-real-restart")
	other, err := OpenRuntimeRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	old := s.recovery
	command := exec.Command("docker", "restart", name)
	if err = command.Run(); err != nil {
		t.Fatal("candidate restart failed")
	}
	var held RecoveryState
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		held, err = s.MaintainRecovery(ctx)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || held.Mode != "RECOVERY_HOLD" || held.Primary == old.Primary || held.Fence <= old.Fence || held.UnsafeUntil-held.Now != 90000 {
		t.Fatal("real primary handshake", held, err)
	}
	var shared RecoveryState
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		shared, err = other.MaintainRecovery(ctx)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || shared.UnsafeUntil != held.UnsafeUntil || shared.Fence != held.Fence {
		t.Fatal("restart shared fence", shared, err)
	}
	t.Log("real AOF-preserving Valkey restart: shared 90000ms safety window; no test clock/counter edits")
	lastResponse := time.Now()
	wallDeadline := time.Now().Add(120 * time.Second)
	reconnects := 0
	for {
		if time.Now().After(wallDeadline) {
			t.Fatal("bounded recovery test timed out")
		}
		state, err := s.MaintainRecovery(ctx)
		if err != nil {
			// Independent client pipelines reconnect lazily after the process dies.
			// No successful admission is tolerated during these transport errors.
			if time.Since(lastResponse) > 10*time.Second {
				t.Fatal("persistent handshake failure", err)
			}
			reconnects++
			if _, e := s.Promote(ctx, 1); e != model.ErrUnavailable {
				t.Fatal("admission during reconnect", e)
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		lastResponse = time.Now()
		if state.Now >= held.UnsafeUntil {
			break
		}
		if state.Mode != "RECOVERY_HOLD" {
			t.Fatal("early recovery")
		}
		for _, store := range []*Store{s, other} {
			if _, err = store.Promote(ctx, 1); err != model.ErrUnavailable {
				t.Fatal("early promotion", err)
			}
			if _, err = store.Claim(ctx, admitted.Ticket.ID); err != model.ErrUnavailable {
				t.Fatal("claim in unsafe interval", err)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("post-restart pipeline reconnect responses observed: %d", reconnects)
	for range 8 {
		held, err = s.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if held.Mode == "ACTIVE" {
			break
		}
	}
	if held.Mode != "ACTIVE" {
		t.Fatal("persisted valid queue failed recovery", held)
	}
	if _, err = s.Claim(ctx, admitted.Ticket.ID); err != model.ErrExpired {
		t.Fatal("old admission resurrected", err)
	}
	out, err := s.Promote(ctx, 1)
	if err != nil || len(out.Tickets) != 1 || out.Tickets[0].ID != waiting.Ticket.ID {
		t.Fatal("waiting ticket not preserved", err)
	}
	t.Log("after real safety window + invariant validation: old admission expired; original waiting ticket promoted")
}
func TestRuntimeRecoveryRefusesMissingOwnerIndex(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	s, _ := runtimeStore(t, c)
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	ticket := add(t, s, "corrupt-owner")
	if err := s.client.Do(ctx, s.client.B().Zrem().Key(s.keys[9]).Member("abcdefghijklmnopqrst:"+ticket.Ticket.ID).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	held, err := s.MaintainRecovery(ctx)
	if err != nil || held.Mode != "RECOVERY_HOLD" {
		t.Fatal("corruption not held", held, err)
	}
	if err = s.client.Do(ctx, s.client.B().Hset().Key(s.keys[8]).FieldValue().FieldValue("unsafeUntil", strconv.FormatInt(held.Now-1, 10)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		held, err = s.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if held.Mode != "RECOVERY_HOLD" || held.ValidationError != "expiry_owner_index" {
		t.Fatal("missing owner index reconstructed", held)
	}
	if _, err = s.Promote(ctx, 1); err != model.ErrUnavailable {
		t.Fatal("corrupt queue promoted", err)
	}
}

func TestRuntimeCanceledBeforeSubmissionDoesNotHoldInstallation(t *testing.T) {
	c := model.DefaultConfig()
	s, _ := runtimeStore(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Join(ctx, "cancelled", "fp", Hash("cancelled"), "replay"); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := s.Status(ctx, Hash("missing")); err != context.Canceled {
		t.Fatal(err)
	}
	state, err := s.MaintainRecovery(context.Background())
	if err != nil || state.Mode != "ACTIVE" || state.Fence != 1 {
		t.Fatal("local cancellation poisoned installation", state, err)
	}
	add(t, s, "after-cancellation")
}

func TestRuntimeClockRollbackUsesLastObservedClock(t *testing.T) {
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	c.ReadyTTL = 60000
	s, _ := runtimeStore(t, c)
	ctx := context.Background()
	// Simulates a previously observed future clock only in this disposable namespace.
	future := time.Now().Add(10 * time.Second).UnixMilli()
	if err := s.client.Do(ctx, s.client.B().Hset().Key(s.keys[8]).FieldValue().FieldValue("clock", strconv.FormatInt(future, 10)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	state, err := s.MaintainRecovery(ctx)
	if err != nil || state.Mode != "RECOVERY_HOLD" || state.Reason != "clock_rollback" || state.UnsafeUntil != future+90000 {
		t.Fatal("rollback safety boundary", state, err)
	}
	again, err := s.MaintainRecovery(ctx)
	if err != nil || again.Fence != state.Fence || again.UnsafeUntil != state.UnsafeUntil {
		t.Fatal("same rollback observation extended hold", again, err)
	}
}

func TestRuntimeRecoveryWaitsForEveryRoomAndBoundedValidation(t *testing.T) {
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	c.ReadyTTL = 60000
	s, ns := runtimeStore(t, c)
	ctx := context.Background()
	other, err := OpenRuntimeRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "bbbbbbbbbbbbbbbbbbbb", c, StandardInstallation())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	defer func() {
		if err := other.client.Do(ctx, other.client.B().Del().Key(other.keys[:8]...).Build()).Error(); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 260; i++ {
		add(t, s, "a-"+strconv.Itoa(i))
		add(t, other, "b-"+strconv.Itoa(i))
	}
	s.uncertainty.Add(1)
	state, err := s.MaintainRecovery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.client.Do(ctx, s.client.B().Hset().Key(s.keys[8]).FieldValue().FieldValue("unsafeUntil", strconv.FormatInt(state.Now-1, 10)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		state, err = s.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.Mode != "RECOVERY_HOLD" || state.ValidationError != "" {
		t.Fatal("resumed before second room validation", state)
	}
	for i := 0; i < 6; i++ {
		state, err = other.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.Mode != "RECOVERY_HOLD" {
			t.Fatal("validation did not remain bounded", i, state)
		}
	}
	state, err = other.MaintainRecovery(ctx)
	if err != nil || state.Mode != "ACTIVE" {
		t.Fatal("all-room recovery", state, err)
	}
	for _, store := range []*Store{s, other} {
		if _, err = store.MaintainRecovery(ctx); err != nil {
			t.Fatal(err)
		}
		m, e := store.Metrics(ctx)
		if e != nil || m.Metrics.Waiting != 260 {
			t.Fatal("room queue lost", e)
		}
	}
}

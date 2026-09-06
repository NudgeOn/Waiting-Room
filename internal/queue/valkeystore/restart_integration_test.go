//go:build integration && persistence

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/queue/model"
)

// Opt-in only: restarts the exact isolated Compose container after checking its label.
func TestRestartPersistenceFailClosed(t *testing.T) {
	container := os.Getenv("WR_TEST_RESTART_CONTAINER")
	if container != "waiting-room-m1-valkey-1" {
		t.Fatal("explicit dedicated Compose container required")
	}
	out, e := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "waiting-room.scope"}}`, container).Output()
	if e != nil || strings.TrimSpace(string(out)) != "m1-local" {
		t.Fatal("container scope mismatch", e)
	}
	s, c, namespace := testStore(t, nil)
	a := add(t, s, "persisted")
	r, e := s.Promote(context.Background(), 1)
	must(t, r, e)
	if e = s.client.Do(context.Background(), s.client.B().Save().Build()).Error(); e != nil {
		t.Fatal(e)
	}
	if out, e = exec.Command("docker", "restart", container).CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	// The running coordinator must stop on primary-process identity change.
	if _, e = s.Claim(context.Background(), a.Ticket.ID); !errors.Is(e, model.ErrUnavailable) {
		t.Fatal("restart bypass", e)
	}
	// docker restart returns before the dataset has necessarily finished loading.
	// Admission remains stopped; wait only for the independent persistence inspection.
	ready, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		out, e = exec.CommandContext(ready, "docker", "exec", container, "valkey-cli", "ping").Output()
		if e == nil && strings.TrimSpace(string(out)) == "PONG" {
			break
		}
		select {
		case <-ready.Done():
			t.Fatal("Valkey did not finish loading", ready.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	// A newly started coordinator also sees the persisted old-primary marker and holds.
	reopened, e := Open(context.Background(), os.Getenv("WR_TEST_VALKEY"), namespace, c)
	if reopened != nil {
		reopened.Close()
	}
	if !errors.Is(e, model.ErrUnavailable) {
		t.Fatal("fresh coordinator bypass", e)
	}
	// The old client intentionally disables retries and may retain a dead connection.
	// Inspect persistence with a fresh connection, separate from the failed runtime client.
	probe, e := valkey.NewClient(valkey.ClientOption{InitAddress: []string{os.Getenv("WR_TEST_VALKEY")}, ForceSingleClient: true, DisableCache: true})
	if e != nil {
		t.Fatal(e)
	}
	defer probe.Close()
	raw, e := probe.Do(context.Background(), probe.B().Hget().Key(s.keys[1]).Field(a.Ticket.ID).Build()).ToString()
	if e != nil || !strings.Contains(raw, `"State":"READY"`) {
		t.Fatal("persistent READY missing", e)
	}
	seq, e := probe.Do(context.Background(), probe.B().Hget().Key(s.keys[0]).Field("seq").Build()).ToString()
	if e != nil || seq != "1" {
		t.Fatal("sequence not persisted", seq, e)
	}
	t.Log("persisted READY/sequence and library retained; existing/new coordinator both fail closed; automatic recovery not implemented")
}

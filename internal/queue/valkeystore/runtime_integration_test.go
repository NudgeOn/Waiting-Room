//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func runtimeTestAddress(t *testing.T) string {
	t.Helper()
	if os.Getenv("WR_TEST_RUNTIME_VALKEY") == "127.0.0.1:16380" {
		return "127.0.0.1:16380"
	}
	if os.Getenv("WR_TEST_VALKEY") == "127.0.0.1:16379" {
		return "127.0.0.1:16379"
	}
	t.Fatal("dedicated local Valkey required")
	return ""
}
func runtimeStore(t *testing.T, c model.Config) (*Store, string) {
	t.Helper()
	opts := valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}
	owner, err := valkey.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = InstallRuntimeLibrary(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("wr:lab:runtime-%d", time.Now().UnixNano())
	s, err := OpenRuntimeRoom(context.Background(), opts, ns, "abcdefghijklmnopqrst", c, StandardInstallation())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.client.Do(context.Background(), s.client.B().Del().Key(s.keys...).Build()).Error(); err != nil {
			t.Error(err)
		}
		s.Close()
	})
	return s, ns
}
func TestRuntimeConfigurationMetricsAndReplay(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	s, ns := runtimeStore(t, c)
	r := control.Runtime{Revision: 1, Epoch: 1, Mode: "HOLD"}
	if _, err := s.Configure(ctx, c, 1, r); err != nil {
		t.Fatal(err)
	}
	first := add(t, s, "first")
	if out, err := s.Promote(ctx, 10); err != nil || len(out.Tickets) != 0 {
		t.Fatal("hold", err)
	}
	r.Revision++
	r.Mode = "AUTO"
	if _, err := s.Configure(ctx, c, 1, r); err != nil {
		t.Fatal(err)
	}
	out, err := s.Promote(ctx, 10)
	if err != nil || len(out.Tickets) != 1 || out.Tickets[0].PromotedAt == 0 {
		t.Fatal("promote", err)
	}
	metrics, err := s.Metrics(ctx)
	if err != nil || metrics.Metrics == nil || metrics.Metrics.Ready != 1 || metrics.Metrics.Waiting != 0 {
		t.Fatal("ready metrics", metrics, err)
	}
	for range 2 {
		if _, err = s.Claim(ctx, first.Ticket.ID); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err = s.Metrics(ctx)
	if err != nil || metrics.Metrics.Ready != 0 || metrics.Metrics.Leases != 1 {
		t.Fatal("claim metrics", err)
	}
	if _, err = s.Configure(ctx, c, 1, r); err != nil {
		t.Fatal("same revision replay", err)
	}
	bad := r
	bad.Mode = "HOLD"
	if _, err = s.Configure(ctx, c, 1, bad); err != model.ErrConflict {
		t.Fatal("same revision drift", err)
	}
	other, err := OpenRuntimeRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.Configure(ctx, c, 1, r); err != nil {
		t.Fatal("restart retains runtime", err)
	}
	r.Revision++
	r.Mode = "DRAINING"
	if _, err = s.Configure(ctx, c, 1, r); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Join(ctx, "new", "fp", Hash("new"), "replay"); err != model.ErrDrain {
		t.Fatal("drain accepted join", err)
	}
	replay, err := s.Join(ctx, "first", "/shop", Hash("different"), "replay")
	if err != nil || replay.Ticket.ID != first.Ticket.ID {
		t.Fatal("drain lost replay", err)
	}
}

func TestRuntimeReadyExpiryMetrics(t *testing.T) {
	c := model.DefaultConfig()
	c.ReadyTTL = 30
	s, _ := runtimeStore(t, c)
	ctx := context.Background()
	if _, err := s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	add(t, s, "expire")
	if _, err := s.Promote(ctx, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := s.Metrics(ctx)
	if err != nil || out.Metrics.Ready != 0 || out.Metrics.Leases != 0 {
		t.Fatal("expiry count", err)
	}
}

//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func TestRuntimeOriginalJoinSurvivesExpiryAndConfigChange(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.IdleTTL = 100
	c.TicketTTL = 1000
	s, _ := recoveryStore(t, c)
	first, err := s.Join(ctx, "replay-key", "/shop", Hash("original-token"), "original-encrypted-token")
	if err != nil || first.Join == nil {
		t.Fatal("first join", err)
	}
	// Real Valkey TIME must pass the original ticket deadline; no synthetic clock.
	time.Sleep(130 * time.Millisecond)
	if _, err = s.Status(ctx, first.Ticket.ID); !errors.Is(err, model.ErrExpired) {
		t.Fatal("expired status", err)
	}
	replay, err := s.Join(ctx, "replay-key", "/shop", Hash("replacement-token"), "replacement-encrypted-token")
	if err != nil || !reflect.DeepEqual(replay.Join, first.Join) || replay.Replay != first.Replay {
		t.Fatal("original replay after cleanup", err)
	}
	if n, _ := s.client.Do(ctx, s.client.B().Hlen().Key(s.keys[1]).Build()).AsInt64(); n != 0 {
		t.Fatal("replay resurrected a ticket")
	}
	c.IdleTTL = 200
	c.TicketTTL = 2000
	if _, err = s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "HOLD"}); err != nil {
		t.Fatal(err)
	}
	replay, err = s.Join(ctx, "replay-key", "/shop", Hash("replacement-token"), "replacement-encrypted-token")
	if err != nil || !reflect.DeepEqual(replay.Join, first.Join) || replay.Replay != first.Replay {
		t.Fatal("config changed original replay", err)
	}
	if _, err = s.Join(ctx, "replay-key", "/shop/other", Hash("replacement-token"), "replacement"); !errors.Is(err, model.ErrConflict) {
		t.Fatal("expired fingerprint mismatch", err)
	}
	if _, err = s.Claim(ctx, first.Ticket.ID); !errors.Is(err, model.ErrExpired) {
		t.Fatal("expired replay became claimable", err)
	}
}

func TestRuntimeOriginalJoinSnapshotSurvivesPromotion(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	s, _ := recoveryStore(t, c)
	first, err := s.Join(ctx, "promoted-key", "/shop", Hash("promoted-token"), "encrypted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Promote(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, first.Ticket.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.Join(ctx, "promoted-key", "/shop", Hash("unused"), "unused")
	if err != nil || replay.Ticket.State != model.Admitted || !reflect.DeepEqual(replay.Join, first.Join) {
		t.Fatal("promotion changed original response", err)
	}
}

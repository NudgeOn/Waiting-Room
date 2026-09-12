//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"reflect"
	"testing"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func TestRuntimeQueueProgressReadOnlyAndFIFO(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	s, _ := recoveryStore(t, c)
	first := add(t, s, "progress-first")
	second := add(t, s, "progress-second")
	before, err := s.Status(ctx, second.Ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := s.StatusWithProgress(ctx, second.Ticket.ID)
	if err != nil || progress.Progress == nil || progress.Progress.UsersAhead != 1 || progress.Progress.Estimate != nil || progress.Progress.Mode != "HOLD" {
		t.Fatalf("hold progress: %+v %v", progress.Progress, err)
	}
	add(t, s, "progress-behind")
	progress, err = s.StatusWithProgress(ctx, second.Ticket.ID)
	if err != nil || progress.Progress == nil || progress.Progress.UsersAhead != 1 || !reflect.DeepEqual(progress.Ticket, before.Ticket) {
		t.Fatal("new arrivals or observation changed existing place/ticket")
	}
	if _, err = s.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Promote(ctx, 1); err != nil {
		t.Fatal(err)
	}
	progress, err = s.StatusWithProgress(ctx, second.Ticket.ID)
	if err != nil || progress.Progress == nil || progress.Progress.UsersAhead != 0 || progress.Progress.Estimate == nil {
		t.Fatalf("auto progress: %+v %v", progress.Progress, err)
	}
	ready, err := s.StatusWithProgress(ctx, first.Ticket.ID)
	if err != nil || ready.Ticket.State != model.Ready || ready.Progress != nil {
		t.Fatal("progress must not claim a ready ticket")
	}
	after, err := s.Status(ctx, second.Ticket.ID)
	if err != nil || !reflect.DeepEqual(before.Ticket, after.Ticket) {
		t.Fatal("progress refreshed or mutated waiting ticket")
	}
}

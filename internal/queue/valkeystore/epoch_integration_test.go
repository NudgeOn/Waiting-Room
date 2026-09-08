//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"crypto/sha256"
	"fmt"
	valkey "github.com/valkey-io/valkey-go"
	"testing"
	"time"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func TestRecoveryEpochNamespaceAndOldTicketIsolation(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	c.AdmissionTTL = 60000
	c.ReadyTTL = 60000
	old, ns := runtimeStore(t, c)

	if err := InstallRecoveryLibrary(ctx, old.client); err != nil {
		t.Fatal(err)
	}
	// Use a fresh v5 installation; legacy namespaces must first be migrated.
	owner, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}, DisableCache: true})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	owner.Do(ctx, owner.B().Del().Key(old.keys...).Build())
	old, err = OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	defer owner.Do(ctx, owner.B().Del().Key(old.keys...).Build())
	oldTicket := add(t, old, "epoch-old")
	future := time.Now().Add(90 * time.Second).UnixMilli()
	fresh, err := OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 2, future)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	defer fresh.client.Do(ctx, fresh.client.B().Del().Key(fresh.keys...).Build())
	state, err := fresh.MaintainRecovery(ctx)
	if err != nil || state.Mode != "RECOVERY_HOLD" || state.UnsafeUntil < time.Now().Add(3629*time.Second).UnixMilli() {
		t.Fatal("new epoch must retain safety hold", state, err)
	}
	if _, err = fresh.Join(ctx, fmt.Sprintf("%x", sha256.Sum256([]byte("new-key"))), fmt.Sprintf("%x", sha256.Sum256([]byte("new-fingerprint"))), fmt.Sprintf("%x", sha256.Sum256([]byte("new-ticket"))), "opaque"); err != model.ErrUnavailable {
		t.Fatal("joined during safety hold", err)
	}
	if _, err := old.Status(ctx, oldTicket.Ticket.ID); err != model.ErrUnavailable {
		t.Fatal("old epoch writer not fenced", err)
	}
	if count, err := owner.Do(ctx, owner.B().Hlen().Key(old.keys[1]).Build()).AsInt64(); err != nil || count != 1 {
		t.Fatal("old records not retained")
	}
	if _, err := OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 1, 0); err == nil {
		t.Fatal("old epoch reopened")
	}
	// Advance only the test-owned safety fixture to exercise bounded validation.
	// Integration recovery/restore tests separately retain a real safety window.
	if err = fresh.client.Do(ctx, fresh.client.B().Hset().Key(fresh.keys[8]).FieldValue().FieldValue("unsafeUntil", fmt.Sprint(time.Now().UnixMilli()-1)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		state, err = fresh.MaintainRecovery(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.Mode == "ACTIVE" {
			break
		}
	}
	if state.Mode != "ACTIVE" {
		t.Fatal("bounded empty epoch validation", state)
	}
	if _, err = fresh.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 2, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Status(ctx, oldTicket.Ticket.ID); err != model.ErrExpired {
		t.Fatal("old ticket crossed epochs", err)
	}
	ticket := add(t, fresh, "epoch-new")
	if ticket.Ticket.Epoch != 2 {
		t.Fatal("incorrect ticket epoch")
	}
	promoted, err := fresh.Promote(ctx, 1)
	if err != nil || len(promoted.Tickets) != 1 || promoted.Tickets[0].JTI != "admission:2:1" {
		t.Fatal("new epoch reservation", err)
	}
	if _, err = fresh.Configure(ctx, c, 2, control.Runtime{Revision: 2, Epoch: 1, Mode: "AUTO"}); err != ErrSchema {
		t.Fatal("epoch downgrade accepted", err)
	}
	// A previous fast clock cannot be forgotten when changing namespaces.
	high := time.Now().Add(10 * time.Minute).UnixMilli()
	if err = owner.Do(ctx, owner.B().Hset().Key(fresh.keys[11]).FieldValue().FieldValue("clock", fmt.Sprint(high)).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	defer owner.Do(ctx, owner.B().Del().Key(next.keys...).Build())
	held, err := next.MaintainRecovery(ctx)
	if err != nil || held.UnsafeUntil < high+3630000 {
		t.Fatal("epoch forgot clock high-water", held, err)
	}
	if _, err = fresh.Promote(ctx, 1); err != model.ErrUnavailable {
		t.Fatal("previous epoch promoted after next reset", err)
	}

}

func TestRecoveryEpochMissedDeliveryFencesEveryRoomWithOneSafetyWindow(t *testing.T) {
	ctx := context.Background()
	c := model.DefaultConfig()
	left, ns := recoveryStore(t, c)
	options := valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}
	right, err := OpenRecoveryRoom(ctx, options, ns, "bbbbbbbbbbbbbbbbbbbb", c, StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	defer right.client.Do(ctx, right.client.B().Del().Key(right.keys[:8]...).Build())
	a, b := add(t, left, "left-old"), add(t, right, "right-old")
	// A Coordinator can miss an intermediate signed delivery while offline.
	// Applying epoch 3 must fence both old Rooms even before the second is applied.
	newLeft, err := OpenRecoveryRoom(ctx, options, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 3, 0)
	if err != nil {
		t.Fatal("missed epoch delivery cannot prevent recovery", err)
	}
	defer newLeft.Close()
	defer newLeft.client.Do(ctx, newLeft.client.B().Del().Key(newLeft.keys[:11]...).Build())
	for i, store := range []*Store{left, right} {
		id := a.Ticket.ID
		if i == 1 {
			id = b.Ticket.ID
		}
		if _, err = store.Claim(ctx, id); err != model.ErrUnavailable {
			t.Fatal("old Room remained writable after installation reset", i, err)
		}
	}
	first, err := newLeft.MaintainRecovery(ctx)
	if err != nil || first.UnsafeUntil-first.Now < 3629000 {
		t.Fatal("missing full safety window", err)
	}
	newRight, err := OpenRecoveryRoom(ctx, options, ns, "bbbbbbbbbbbbbbbbbbbb", c, StandardInstallation(), 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer newRight.Close()
	defer newRight.client.Do(ctx, newRight.client.B().Del().Key(newRight.keys[:8]...).Build())
	for _, store := range []*Store{newLeft, newRight} {
		if _, err = store.Configure(ctx, c, 2, control.Runtime{Revision: 2, Epoch: 3, Mode: "HOLD"}); err != nil {
			t.Fatal("held config must acknowledge the delivery", err)
		}
		state, err := store.MaintainRecovery(ctx)
		if err != nil || state.Mode != "RECOVERY_HOLD" || state.UnsafeUntil != first.UnsafeUntil {
			t.Fatal("Room delivery changed the installation safety window", err)
		}
		if _, err = store.Join(ctx, "too-early", "/shop", Hash("too-early"), "private"); err != model.ErrUnavailable {
			t.Fatal("held config admitted a visitor", err)
		}
	}
	if _, err = OpenRecoveryRoom(ctx, options, ns, "bbbbbbbbbbbbbbbbbbbb", c, StandardInstallation(), 2, 0); err == nil {
		t.Fatal("late intermediate delivery rolled the epoch back")
	}
}

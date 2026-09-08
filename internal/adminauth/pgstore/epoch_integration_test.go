//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

func TestEpochRecoveryAllRoomsEventsAndAuthorization(t *testing.T) {
	f, p, s, g := securityFixture(t, false)
	ctx := context.Background()
	var raw []byte
	if err := f.pool.QueryRow(ctx, "SELECT document FROM control_delivery WHERE singleton").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var d control.Delivery
	if json.Unmarshal(raw, &d) != nil {
		t.Fatal("delivery")
	}
	other := d.Config.Rooms[0]
	other.ID = "other"
	other.PublicID = "bbbbbbbbbbbbbbbbbbbb"
	other.Hostname = "other.example.test"
	d.Config.Rooms = append(d.Config.Rooms, other)
	d.Runtimes = append(d.Runtimes, control.RoomRuntime{RoomID: "other", Runtime: control.InitialRuntime(other)})
	for i := range d.Runtimes {
		d.Runtimes[i].Runtime.Mode = "AUTO"
		d.Runtimes[i].Runtime.EventState = "scheduled"
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "UPDATE control_delivery SET document=$1", d.Bytes())
	execSQL(t, f.pool, "INSERT INTO control_events VALUES('pending_epoch','other',clock_timestamp()+interval '1 hour',clock_timestamp()+interval '2 hours',clock_timestamp()+interval '3 hours','scheduled')")
	body := []byte(`{"action":"new-epoch","scope":"installation","generation":1}`)
	r := draftRequest(g, "epoch-all-rooms-test", `"runtime-1"`)
	r.Method = "PATCH"
	if _, err := p.Operate(ctx, g.SessionToken(), "sale", r, body); err != adminauth.ErrForbidden {
		t.Fatal("missing reauth accepted", err)
	}
	proof := mintMethodProof(t, s, g, adminauth.NewEpoch, "PATCH", "sale", r.Header.Get("If-Match"), body, "")
	r.Header.Set("X-Reauth-Token", proof)
	for _, role := range []string{"operator", "viewer"} {
		execSQL(t, f.pool, "UPDATE auth_accounts SET role=$1 WHERE id='admin1'", role)
		if _, err := p.Operate(ctx, g.SessionToken(), "sale", r, body); err != adminauth.ErrForbidden {
			t.Fatal("role accepted", role, err)
		}
	}
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='admin' WHERE id='admin1'")
	out, err := p.Operate(ctx, g.SessionToken(), "sale", r, body)
	if err != nil || out.Status != 200 {
		t.Fatal("reset", out.Status, err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT document FROM control_delivery WHERE singleton").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &d) != nil {
		t.Fatal("decode")
	}
	for _, item := range d.Runtimes {
		if item.Runtime.Epoch != 2 || item.Runtime.Mode != "HOLD" || item.Runtime.EventState != "paused_by_override" || item.Runtime.RecoveryUntil < time.Now().Add(3625*time.Second).UnixMilli() {
			t.Fatal("installation not held", item.RoomID)
		}
	}
	var event string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM control_events WHERE id='pending_epoch'").Scan(&event); err != nil || event != "paused_by_override" {
		t.Fatal("event not paused", err)
	}
}

func TestEpochRecoveryRejectsUnreviewedInstallationGeneration(t *testing.T) {
	f, p, s, g := securityFixture(t, false)
	ctx := context.Background()
	body := []byte(`{"action":"new-epoch","scope":"installation","generation":1}`)
	r := draftRequest(g, "epoch-stale-generation", `"runtime-1"`)
	r.Method = "PATCH"
	r.Header.Set("X-Reauth-Token", mintMethodProof(t, s, g, adminauth.NewEpoch, "PATCH", "sale", r.Header.Get("If-Match"), body, ""))
	// Another installation-wide publication wins after the confirmation snapshot.
	execSQL(t, f.pool, "UPDATE control_delivery SET generation=generation+1")
	out, err := p.Operate(ctx, g.SessionToken(), "sale", r, body)
	if err != nil || out.Status != 412 {
		t.Fatal("stale installation review accepted", out.Status, err)
	}
	current, err := p.Runtime(ctx, g.SessionToken(), "sale")
	var runtime control.Runtime
	if err != nil || json.Unmarshal(current.Body, &runtime) != nil || runtime.Epoch != 1 {
		t.Fatal("conflict reset a room", err)
	}
	var accepted, rejected int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE result='accepted'),count(*) FILTER (WHERE result='rejected') FROM control_audit WHERE action='runtime.new_epoch'").Scan(&accepted, &rejected); err != nil || accepted != 0 || rejected != 1 {
		t.Fatal("conflicting reset must be audited once as rejected", err)
	}
}

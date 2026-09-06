//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

func controlFixture(t *testing.T) (fixture, *ControlService, Grant) {
	t.Helper()
	f, _, g := sessionFixture(t)
	execSQL(t, f.pool, Migration005)
	if e := f.store.InitializeControl(context.Background(), "standard-10k", "local"); e != nil {
		t.Fatal(e)
	}
	s, e := NewControlService(f.store, "https://admin.test")
	if e != nil {
		t.Fatal(e)
	}
	return f, s, g
}
func draftRoom() control.Room {
	return control.Room{ID: "sale", PublicID: strings.Repeat("a", 20), Name: "판매", Hostname: "shop.example.test", Origin: "https://origin.example.test", HealthURL: "https://origin.example.test/health", ProtectPrefixes: []string{"/shop"}, ExcludePrefixes: []string{}, QueuePolicy: control.QueuePolicy{Kind: "fifo", TicketIdleTTLSeconds: 600, TicketMaxTTLSeconds: 86400, ReadyTTLSeconds: 120}, Limits: control.Limits{MaxActiveAdmissionLeases: 1000, AdmissionsPerMinute: 600, AdmissionTTLSeconds: 900}, Theme: control.Theme{TemplateID: "calm", Title: "기다려 주세요", Message: "안내", PrimaryColor: "#315b4a", Locale: "ko"}}
}
func draftRequest(g Grant, key, etag string) *http.Request {
	r := sessionRequest(g)
	r.Method = "PUT"
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("If-Match", etag)
	return r
}
func draftDocument() control.Config {
	return control.Config{SchemaVersion: 1, Profile: "standard-10k", RegionID: "local", Rooms: []control.Room{draftRoom()}}
}
func TestControlDraftAtomicReplayAudit(t *testing.T) {
	f, s, g := controlFixture(t)
	ctx := context.Background()
	initial, e := s.Config(ctx, g.SessionToken())
	if e != nil || initial.ETag != `"config-0"` {
		t.Fatal(e)
	}
	r := draftRequest(g, "create-first-room", initial.ETag)
	first, e := s.ReplaceDraft(ctx, g.SessionToken(), r, draftDocument().Bytes())
	if e != nil || first.Status != 200 || first.ETag != `"config-1"` {
		t.Fatal(first, e)
	}
	other, _ := NewControlService(f.replica, "https://admin.test")
	replay, e := other.ReplaceDraft(ctx, g.SessionToken(), r, draftDocument().Bytes())
	if e != nil || !replay.Replay || string(replay.Body) != string(first.Body) || replay.ETag != first.ETag {
		t.Fatal("replay mismatch", e)
	}
	changed := draftDocument()
	changed.Rooms[0].Name = "changed"
	conflict, e := s.ReplaceDraft(ctx, g.SessionToken(), r, changed.Bytes())
	if e != nil || conflict.Status != 409 {
		t.Fatal("key conflict", e)
	}
	rows, e := other.Audit(ctx, g.SessionToken(), 0)
	if e != nil || len(rows) != 1 || rows[0].Result != "saved_draft" || rows[0].Revision != 1 {
		t.Fatal("audit mismatch", e)
	}
	encoded, _ := json.Marshal(rows)
	for _, secret := range []string{g.SessionToken(), g.CSRFToken(), r.Header.Get("Idempotency-Key"), "origin.example.test", "기다려"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("audit leak")
		}
	}
	if e = f.store.InitializeControl(ctx, "standard-10k", "local"); e == nil {
		t.Fatal("initialization overwrote state")
	}
}
func TestControlDraftRevisionRace(t *testing.T) {
	f, s, g := controlFixture(t)
	other, _ := NewControlService(f.replica, "https://admin.test")
	var success, stale, bad atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			service := s
			if i%2 == 0 {
				service = other
			}
			r := draftRequest(g, fmt.Sprintf("revision-race-%03d", i), `"config-0"`)
			reply, e := service.ReplaceDraft(context.Background(), g.SessionToken(), r, draftDocument().Bytes())
			if e != nil {
				bad.Add(1)
			} else if reply.Status == 200 {
				success.Add(1)
			} else if reply.Status == 412 {
				stale.Add(1)
			} else {
				bad.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 1 || stale.Load() != 15 || bad.Load() != 0 {
		t.Fatalf("success=%d stale=%d unexpected=%d", success.Load(), stale.Load(), bad.Load())
	}
	audit, e := s.Audit(context.Background(), g.SessionToken(), 0)
	if e != nil || len(audit) != 16 {
		t.Fatal("missing command audit", e)
	}
	page, e := s.Audit(context.Background(), g.SessionToken(), audit[7].ID)
	if e != nil || len(page) != 8 || page[0].ID >= audit[7].ID {
		t.Fatal("cursor overlap", e)
	}
}
func TestControlDraftPermissionAndCSRF(t *testing.T) {
	for _, role := range []adminauth.Role{adminauth.Operator, adminauth.Viewer} {
		t.Run(string(role), func(t *testing.T) {
			f, s, g := controlFixture(t)
			execSQL(t, f.pool, "UPDATE auth_accounts SET role=$1", role)
			if _, e := s.Config(context.Background(), g.SessionToken()); e != nil {
				t.Fatal("read denied", e)
			}
			if _, e := s.ReplaceDraft(context.Background(), g.SessionToken(), draftRequest(g, "not-allowed-write", `"config-0"`), draftDocument().Bytes()); e != adminauth.ErrForbidden {
				t.Fatal("unauthorized save", e)
			}
		})
	}
	f, s, g := controlFixture(t)
	r := draftRequest(g, "csrf-not-allowed", `"config-0"`)
	r.Header.Set("Origin", "https://evil.test")
	if _, e := s.ReplaceDraft(context.Background(), g.SessionToken(), r, draftDocument().Bytes()); e != adminauth.ErrForbidden {
		t.Fatal("CSRF accepted", e)
	}
	execSQL(t, f.pool, "UPDATE auth_policy SET version=2")
	if _, e := s.Config(context.Background(), g.SessionToken()); e != adminauth.ErrUnauthenticated {
		t.Fatal("stale policy accepted", e)
	}
	var count int
	if e := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM control_audit").Scan(&count); e != nil || count != 0 {
		t.Fatal("invalid auth mutated audit")
	}
}
func TestControlDraftFailureRollsBack(t *testing.T) {
	f, s, g := controlFixture(t)
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT simulated_audit_failure CHECK (false)")
	if _, e := s.ReplaceDraft(context.Background(), g.SessionToken(), draftRequest(g, "audit-failure-key", `"config-0"`), draftDocument().Bytes()); e != adminauth.ErrAuthUnavailable {
		t.Fatal("audit failure not fail-closed", e)
	}
	reply, e := s.Config(context.Background(), g.SessionToken())
	if e != nil || reply.ETag != `"config-0"` {
		t.Fatal("config partially committed", e)
	}
	var n int
	if e = f.pool.QueryRow(context.Background(), "SELECT count(*) FROM control_commands").Scan(&n); e != nil || n != 0 {
		t.Fatal("command partially committed")
	}
}
func TestControlDraftRejectedCommandIsStable(t *testing.T) {
	_, s, g := controlFixture(t)
	r := draftRequest(g, "invalid-draft-key", `"config-0"`)
	bad := draftDocument()
	bad.Rooms[0].Limits.AdmissionsPerMinute = 6001
	one, e := s.ReplaceDraft(context.Background(), g.SessionToken(), r, bad.Bytes())
	if e != nil || one.Status != 422 {
		t.Fatal(one, e)
	}
	two, e := s.ReplaceDraft(context.Background(), g.SessionToken(), r, bad.Bytes())
	if e != nil || !two.Replay || string(one.Body) != string(two.Body) {
		t.Fatal("rejection replay changed", e)
	}
	r.Header.Del("If-Match")
	missing, e := s.ReplaceDraft(context.Background(), g.SessionToken(), r, bad.Bytes())
	if e != nil || missing.Status != 428 {
		t.Fatal("missing precondition accepted", e)
	}
}

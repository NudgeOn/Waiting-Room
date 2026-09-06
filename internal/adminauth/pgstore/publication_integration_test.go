//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
)

func publicationFixture(t *testing.T) (fixture, *PublicationService, Grant) {
	t.Helper()
	f, c, g := controlFixture(t)
	execSQL(t, f.pool, Migration006)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	s, err := NewPublicationService(f.store, "https://admin.test", "local", key)
	if err != nil {
		t.Fatal(err)
	}
	draft := draftDocument()
	draft.Rooms[0].Active = true
	out, err := c.ReplaceDraft(context.Background(), g.SessionToken(), draftRequest(g, "first-runtime-draft", `"config-0"`), draft.Bytes())
	if err != nil || out.Status != 200 {
		t.Fatal("draft", err)
	}
	r := draftRequest(g, "first-publication", `"config-1"`)
	r.Method = "POST"
	if out, err = s.Publish(context.Background(), g.SessionToken(), r, []byte("{}")); err != nil || out.Status != 202 {
		t.Fatal("publish", out, err)
	}
	return f, s, g
}
func TestPublicationSignedAtomicReplayAndACK(t *testing.T) {
	_, s, g := publicationFixture(t)
	ctx := context.Background()
	r := draftRequest(g, "first-publication", `"config-1"`)
	r.Method = "POST"
	out, err := s.Publish(ctx, g.SessionToken(), r, []byte("{}"))
	if err != nil || out.Status != 202 || !out.Replay {
		t.Fatal("publication replay", err)
	}
	envelope, err := s.Envelope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := configtrust.Open(map[string]ed25519.PublicKey{"config-v1": s.key.Public().(ed25519.PublicKey)}, "local", 1, control.ValidateDelivery, &configtrust.MemoryStore{})
	if err != nil || gate.Apply(envelope) != nil {
		t.Fatal("signature not valid", err)
	}
	current, err := gate.Current()
	if err != nil || current.Generation != 1 {
		t.Fatal("generation", err)
	}
	var d control.Delivery
	if json.Unmarshal(current.Payload, &d) != nil || d.Runtimes[0].Runtime.Mode != "HOLD" {
		t.Fatal("first activation did not hold")
	}
	ack := NodeAck{Generation: 1, Digest: digest(envelope), Rooms: []RoomMetrics{{RoomID: "sale", Revision: 1, Epoch: 1, Mode: "HOLD"}}}
	if s.Acknowledge(ctx, "unregistered", ack) != adminauth.ErrForbidden {
		t.Fatal("unknown peer accepted")
	}
	if err = s.Acknowledge(ctx, "gateway", ack); err != nil {
		t.Fatal(err)
	}
	view, _ := s.View(ctx, g.SessionToken())
	var v DeliveryView
	_ = json.Unmarshal(view.Body, &v)
	if v.State != "pending" {
		t.Fatal("one ACK claimed applied")
	}
	if err = s.Acknowledge(ctx, "coordinator", ack); err != nil {
		t.Fatal(err)
	}
	view, _ = s.View(ctx, g.SessionToken())
	_ = json.Unmarshal(view.Body, &v)
	if v.State != "applied" {
		t.Fatal("two ACKs not applied")
	}
	ack.Digest = "wrong"
	if s.Acknowledge(ctx, "gateway", ack) == nil {
		t.Fatal("wrong envelope ACK accepted")
	}
}
func TestRuntimeRevisionRaceAndOperatorBoundary(t *testing.T) {
	f, s, g := publicationFixture(t)
	ctx := context.Background()
	var pass, stale, bad atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := draftRequest(g, fmt.Sprintf("operate-runtime-%03d", i), `"runtime-1"`)
			r.Method = "PATCH"
			out, err := s.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action":"auto"}`))
			if err != nil {
				bad.Add(1)
			} else if out.Status == 200 {
				pass.Add(1)
			} else if out.Status == 412 {
				stale.Add(1)
			} else {
				bad.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if pass.Load() != 1 || stale.Load() != 11 || bad.Load() != 0 {
		t.Fatalf("pass=%d stale=%d bad=%d", pass.Load(), stale.Load(), bad.Load())
	}
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='operator'")
	r := draftRequest(g, "operator-no-bypass", `"runtime-2"`)
	r.Method = "PATCH"
	if out, err := s.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action":"instant-off"}`)); err != nil || out.Status != 403 {
		t.Fatal("operator bypass", out, err)
	}
	r.Method = "POST"
	r.Header.Set("Idempotency-Key", "operator-no-publish")
	if _, err := s.Publish(ctx, g.SessionToken(), r, []byte("{}")); err != adminauth.ErrForbidden {
		t.Fatal("operator published", err)
	}
}
func TestRuntimeAuditRollbackAndOverride(t *testing.T) {
	f, s, g := publicationFixture(t)
	ctx := context.Background()
	now := time.Now()
	execSQL(t, f.pool, "INSERT INTO control_events VALUES('sale-event','sale',$1,$2,$3,'scheduled')", now.Add(time.Hour), now.Add(2*time.Hour), now.Add(3*time.Hour))
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT simulated_publication_audit CHECK(action<>'runtime.operate')")
	r := draftRequest(g, "runtime-failure-key", `"runtime-1"`)
	r.Method = "PATCH"
	if _, err := s.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action":"auto"}`)); err != adminauth.ErrAuthUnavailable {
		t.Fatal("audit failure", err)
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT simulated_publication_audit")
	out, err := s.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action":"auto"}`))
	if err != nil || out.Status != 200 {
		t.Fatal("rollback/retry", out, err)
	}
	var runtime control.Runtime
	if json.Unmarshal(out.Body, &runtime) != nil || runtime.Revision != 2 || runtime.EventState != "paused_by_override" {
		t.Fatal("override")
	}
	var state string
	if f.pool.QueryRow(ctx, "SELECT state FROM control_events WHERE id='sale-event'").Scan(&state) != nil || state != "paused_by_override" {
		t.Fatal("event not paused")
	}
}

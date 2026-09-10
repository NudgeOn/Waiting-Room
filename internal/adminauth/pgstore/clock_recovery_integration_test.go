//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/configtrust"
)

func clockRequest(t *testing.T, s *PublicationService) (configtrust.ClockRecovery, []byte) {
	t.Helper()
	raw, err := s.Envelope(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Snapshot configtrust.Snapshot `json:"snapshot"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("invalid fixture envelope")
	}
	// Real wall time, no issuer clock/TTL modification. The request boundary is
	// strictly later than the original signature, well before its five-minute renewal.
	boundary := envelope.Snapshot.IssuedAt + 1
	if wait := time.Until(time.Unix(boundary, 0)); wait > 0 {
		time.Sleep(wait + time.Millisecond)
	}
	return configtrust.ClockRecovery{Generation: envelope.Snapshot.Generation, NotBefore: boundary, Digest: digest(raw)}, raw
}

func TestClockRecoveryConcurrentRefreshPreservesApprovedState(t *testing.T) {
	f, s, g := publicationFixture(t)
	ctx := context.Background()
	in, before := clockRequest(t, s)
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := s.Envelope(ctx)
	if !bytes.Equal(before, unchanged) {
		t.Fatal("fixture already renewed; not a prompt recovery test")
	}
	execSQL(t, f.pool, "UPDATE control_config SET document=jsonb_set(document,'{rooms,0,name}','\"unpublished clock draft\"')")
	var wg sync.WaitGroup
	results := make([][]byte, 12)
	errors := make([]error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			node := "gateway"
			if i%2 == 1 {
				node = "coordinator"
			}
			results[i], errors[i] = s.RecoverClock(ctx, node, in)
		}(i)
	}
	wg.Wait()
	for i, err := range errors {
		if err != nil || !bytes.Equal(results[i], results[0]) {
			t.Fatal("concurrent/replayed refresh differs", err)
		}
	}
	view, err := s.View(ctx, g.SessionToken())
	if err != nil {
		t.Fatal(err)
	}
	var delivery DeliveryView
	if json.Unmarshal(view.Body, &delivery) != nil || delivery.Generation != 2 || delivery.Config.Rooms[0].Name == "unpublished clock draft" || delivery.Runtimes[0].Runtime.Mode != "HOLD" || delivery.Runtimes[0].Runtime.Epoch != 1 {
		t.Fatal("refresh changed approved state")
	}
	var audit int
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_audit WHERE action='config.clock_recovery' AND actor_role='system'").Scan(&audit) != nil || audit != 1 {
		t.Fatal("refresh audit was not exactly once")
	}
	var oldEnvelope, newEnvelope struct {
		Snapshot configtrust.Snapshot `json:"snapshot"`
	}
	_ = json.Unmarshal(before, &oldEnvelope)
	_ = json.Unmarshal(results[0], &newEnvelope)
	if !bytes.Equal(oldEnvelope.Snapshot.Payload, newEnvelope.Snapshot.Payload) || newEnvelope.Snapshot.IssuedAt < in.NotBefore {
		t.Fatal("payload or signed time changed incorrectly")
	}
	t.Log("normal refresh unchanged; authenticated concurrent recovery produced one newer signature and one audit, exact retries, original approved payload/epoch/HOLD preserved")
}

func TestClockRecoveryRejectsUntrustedFutureAndConflictingRequests(t *testing.T) {
	_, s, _ := publicationFixture(t)
	ctx := context.Background()
	in, before := clockRequest(t, s)
	if _, err := s.RecoverClock(ctx, "public", in); err != adminauth.ErrForbidden {
		t.Fatal("untrusted identity")
	}
	for _, change := range []func(*configtrust.ClockRecovery){
		func(v *configtrust.ClockRecovery) { v.Generation = 0 },
		func(v *configtrust.ClockRecovery) { v.Generation++ },
		func(v *configtrust.ClockRecovery) { v.NotBefore = time.Now().Add(time.Hour).Unix() },
		func(v *configtrust.ClockRecovery) { v.NotBefore = 1 },
		func(v *configtrust.ClockRecovery) { v.Digest = strings.Repeat("a", 64) },
		func(v *configtrust.ClockRecovery) { v.Digest = "invalid" },
	} {
		bad := in
		change(&bad)
		if _, err := s.RecoverClock(ctx, "gateway", bad); err == nil {
			t.Fatal("invalid refresh accepted")
		}
	}
	after, _ := s.Envelope(ctx)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected request mutated envelope")
	}
}

func TestClockRecoveryAuditFailureRollsBackSignature(t *testing.T) {
	f, s, _ := publicationFixture(t)
	ctx := context.Background()
	in, before := clockRequest(t, s)
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT clock_audit_failure CHECK(action<>'config.clock_recovery')")
	if _, err := s.RecoverClock(ctx, "coordinator", in); err != adminauth.ErrAuthUnavailable {
		t.Fatal("audit failure ignored", err)
	}
	after, _ := s.Envelope(ctx)
	if !bytes.Equal(before, after) {
		t.Fatal("unsigned audit gap left a new generation")
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT clock_audit_failure")
	if _, err := s.RecoverClock(ctx, "coordinator", in); err != nil {
		t.Fatal("same request could not retry", err)
	}
}

func TestClockRecoveryNodeBehindPreBoundaryPublication(t *testing.T) {
	_, s, g := publicationFixture(t)
	ctx := context.Background()
	before, err := s.Envelope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in := configtrust.ClockRecovery{Generation: 1, NotBefore: time.Now().Unix() + 2, Digest: digest(before)}
	r := draftRequest(g, "clock-concurrent-command", `"runtime-1"`)
	r.Method = "PATCH"
	if out, err := s.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action":"auto"}`)); err != nil || out.Status != 200 {
		t.Fatal("concurrent operator command", err)
	}
	if wait := time.Until(time.Unix(in.NotBefore, 0)); wait > 0 {
		time.Sleep(wait + time.Millisecond)
	}
	raw, err := s.RecoverClock(ctx, "gateway", in)
	if err != nil {
		t.Fatal("lagging node could not recover", err)
	}
	var envelope struct {
		Snapshot configtrust.Snapshot `json:"snapshot"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if envelope.Snapshot.Generation != 3 || envelope.Snapshot.IssuedAt < in.NotBefore || !bytes.Contains(envelope.Snapshot.Payload, []byte(`"mode":"AUTO"`)) {
		t.Fatal("fresh signature did not preserve newer operator state")
	}
	replay, err := s.RecoverClock(ctx, "coordinator", in)
	if err != nil || !bytes.Equal(raw, replay) {
		t.Fatal("lagging concurrent replay differs", err)
	}
}

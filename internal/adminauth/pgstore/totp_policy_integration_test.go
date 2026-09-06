//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
)

func policyFixture(t *testing.T) (fixture, *SecurityService, Grant) {
	f, s, g := usersFixture(t)
	execSQL(t, f.pool, Migration009)
	return f, s, g
}
func TestPolicyONRotatesOnlyActorAndRequiresCurrentTOTP(t *testing.T) {
	f, s, g := policyFixture(t)
	ctx := context.Background()
	raw := []byte(`{"enabled":true}`)
	proof := mintMethodProof(t, s, g, adminauth.WriteTOTPPolicy, "PUT", "totp", `"policy-1"`, raw, currentCode(t, f.pool))
	r := draftRequest(g, "enable-global-totp", `"policy-1"`)
	r.Method = "PUT"
	r.Header.Set("X-Reauth-Token", proof)
	out, err := s.ChangePolicy(ctx, g.SessionToken(), r, raw)
	if err != nil || out.Status != 200 || out.RotatedGrant.SessionToken() == "" || out.RotatedGrant.SessionToken() == g.SessionToken() {
		t.Fatal("policy rotate", out.Status, err)
	}
	if _, err = s.Policy(ctx, g.SessionToken()); err != adminauth.ErrUnauthenticated {
		t.Fatal("old session survived", err)
	}
	current, err := s.Policy(ctx, out.RotatedGrant.SessionToken())
	if err != nil || current.ETag != `"policy-2"` {
		t.Fatal("new session failed", err)
	}
	var sessions, proofs int
	if f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_sessions),(SELECT count(*) FROM auth_reauth_proofs)").Scan(&sessions, &proofs) != nil || sessions != 1 || proofs != 0 {
		t.Fatal("sessions/proofs not revoked", sessions, proofs)
	}
	// Authenticated retry with the new session returns the same operation JSON,
	// without issuing another session or incrementing the policy again.
	retry := draftRequest(out.RotatedGrant, "enable-global-totp", `"policy-1"`)
	retry.Method = "PUT"
	replay, err := s.ChangePolicy(ctx, out.RotatedGrant.SessionToken(), retry, raw)
	if err != nil || !replay.Replay || string(out.Body) != string(replay.Body) || replay.RotatedGrant.SessionToken() != "" {
		t.Fatal("policy replay", err)
	}
	var leaked int
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_commands WHERE convert_from(response,'UTF8') LIKE $1", "%"+out.RotatedGrant.SessionToken()+"%").Scan(&leaked) != nil || leaked != 0 {
		t.Fatal("session in command cache")
	}
}
func TestPolicyForcedOnAndAuditFailureAreAtomic(t *testing.T) {
	f, s, g := policyFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=true,mode='forced_on'")
	raw := []byte(`{"enabled":false}`)
	proof := mintMethodProof(t, s, g, adminauth.WriteTOTPPolicy, "PUT", "totp", `"policy-1"`, raw, currentCode(t, f.pool))
	r := draftRequest(g, "forced-policy-remains", `"policy-1"`)
	r.Method = "PUT"
	r.Header.Set("X-Reauth-Token", proof)
	out, err := s.ChangePolicy(ctx, g.SessionToken(), r, raw)
	if err != nil || out.Status != 403 || !strings.Contains(string(out.Body), "TOTP_POLICY_LOCKED") {
		t.Fatal("forced policy disabled", out.Status, err)
	}
	execSQL(t, f.pool, "UPDATE auth_policy SET mode='configurable'")
	// Previous step consumed that proof; a fresh counter is required. The test
	// advances only the fixture counter, not application time or policy rules.
	execSQL(t, f.pool, "UPDATE auth_totp_credentials SET last_counter=-1")
	proof = mintMethodProof(t, s, g, adminauth.WriteTOTPPolicy, "PUT", "totp", `"policy-1"`, raw, currentCode(t, f.pool))
	r.Header.Set("X-Reauth-Token", proof)
	r.Header.Set("Idempotency-Key", "policy-audit-rollback")
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT simulated_policy_audit CHECK(action<>'security.totp.write') NOT VALID")
	out, err = s.ChangePolicy(ctx, g.SessionToken(), r, raw)
	if err != adminauth.ErrAuthUnavailable || out.RotatedGrant.SessionToken() != "" {
		t.Fatal("unconfirmed session exposed", err)
	}
	if _, err = s.Policy(ctx, g.SessionToken()); err != nil {
		t.Fatal("rollback revoked actor", err)
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT simulated_policy_audit")
	out, err = s.ChangePolicy(ctx, g.SessionToken(), r, raw)
	if err != nil || out.Status != 200 || out.RotatedGrant.SessionToken() == "" {
		t.Fatal("disable after rollback", out.Status, err)
	}
}
func TestPolicyOFFEnrollmentRequiresBoundAdminAndEncryptsCache(t *testing.T) {
	f, s, g := policyFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "DELETE FROM auth_totp_credentials WHERE user_id='admin1'")
	raw := []byte("{}")
	proof := mintMethodProof(t, s, g, adminauth.WriteTOTPPolicy, "POST", "totp-enrollment", `"policy-1"`, raw, "")
	r := draftRequest(g, "prepare-policy-enrollment", `"policy-1"`)
	r.Method = "POST"
	r.Header.Set("X-Reauth-Token", proof)
	out, err := s.BeginPolicyEnrollment(ctx, g.SessionToken(), r, raw)
	if err != nil || out.Status != 200 {
		t.Fatal("policy enrollment", out.Status, err)
	}
	var result struct {
		Challenge string `json:"challengeToken"`
	}
	if json.Unmarshal(out.Body, &result) != nil || len(result.Challenge) != 43 {
		t.Fatal("missing enrollment proof")
	}
	replay, err := s.BeginPolicyEnrollment(ctx, g.SessionToken(), r, raw)
	if err != nil || !replay.Replay || string(replay.Body) != string(out.Body) {
		t.Fatal("encrypted retry", err)
	}
	var leaked int
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_commands WHERE convert_from(response,'UTF8') LIKE $1", "%"+result.Challenge+"%").Scan(&leaked) != nil || leaked != 0 {
		t.Fatal("plaintext proof cached")
	}
	ordinary, _ := NewEnrollmentService(f.store, s.login.passwords, "k1")
	if _, err = ordinary.Begin(ctx, result.Challenge); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("ordinary OFF enrollment bypass", err)
	}
	e, _ := NewEnrollmentService(f.store, s.login.passwords, "k1", true)
	setup, err := e.Begin(ctx, result.Challenge)
	if err != nil {
		t.Fatal("authorized OFF enrollment", err)
	}
	startRaw, _ := json.Marshal(map[string]string{"challengeToken": result.Challenge})
	startRequest := sessionRequest(g)
	if started, err := s.PolicyEnrollment(ctx, g.SessionToken(), startRequest, startRaw, false); err != nil || started.Status != 200 {
		t.Fatal("authenticated enrollment adapter", started.Status, err)
	}
	startRequest.Header.Set("Origin", "https://evil.test")
	if _, err := s.PolicyEnrollment(ctx, g.SessionToken(), startRequest, startRaw, false); err != adminauth.ErrForbidden {
		t.Fatal("policy enrollment CSRF accepted", err)
	}
	startRequest.Header.Set("Origin", "https://admin.test")
	granted, err := e.Complete(ctx, result.Challenge, enrollmentCode(t, f, setup))
	if err != nil || granted.SessionGrant().SessionToken() == "" || granted.RecoveryCodes()[0] == "" {
		t.Fatal("OFF enrollment verify", err)
	}
	current, err := s.Policy(ctx, granted.SessionGrant().SessionToken())
	if err != nil || !strings.Contains(string(current.Body), `"enabled":false`) || !strings.Contains(string(current.Body), `"enrolled":true`) {
		t.Fatal("enrollment toggled policy prematurely", err)
	}
	if _, err = s.Policy(ctx, g.SessionToken()); err != adminauth.ErrUnauthenticated {
		t.Fatal("enrollment retained previous session", err)
	}
	// Enrollment completion consumed its own TOTP step; policy reauthentication
	// must reject that step and accept the next valid one.
	var now time.Time
	_ = f.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	raw = []byte(`{"enabled":true}`)
	proof = mintMethodProof(t, s, granted.SessionGrant(), adminauth.WriteTOTPPolicy, "PUT", "totp", `"policy-1"`, raw, enrollmentCodeAt(t, setup, uint64(now.Unix()/30)+1))
	r = draftRequest(granted.SessionGrant(), "enable-enrolled-policy", `"policy-1"`)
	r.Method = "PUT"
	r.Header.Set("X-Reauth-Token", proof)
	out, err = s.ChangePolicy(ctx, granted.SessionGrant().SessionToken(), r, raw)
	if err != nil || out.Status != 200 || out.RotatedGrant.SessionToken() == "" {
		t.Fatal("enable enrolled policy", out.Status, err)
	}
}

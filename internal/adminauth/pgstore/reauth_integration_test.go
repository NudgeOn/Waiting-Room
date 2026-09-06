//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"waiting-room/internal/adminauth"
)

func securityFixture(t *testing.T, totp bool) (fixture, *PublicationService, *SecurityService, Grant) {
	t.Helper()
	f, p, g := publicationFixture(t)
	execSQL(t, f.pool, Migration007)
	if !totp {
		execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=false")
	}
	hasher, err := adminauth.NewPasswordHasher(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hasher.Hash(context.Background(), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "INSERT INTO auth_password_credentials VALUES('admin1',1,$1)", hash.StorageValue())
	key := [32]byte{1}
	s, err := NewSecurityService(f.store, hasher, key, "https://admin.test", "k1")
	if err != nil {
		t.Fatal(err)
	}
	return f, p, s, g
}
func mintProof(t *testing.T, s *SecurityService, g Grant, action adminauth.Action, target, etag string, body []byte, code string) string {
	return mintMethodProof(t, s, g, action, "PATCH", target, etag, body, code)
}
func mintMethodProof(t *testing.T, s *SecurityService, g Grant, action adminauth.Action, method, target, etag string, body []byte, code string) string {
	t.Helper()
	in := map[string]any{"password": testPassword, "action": action, "targetId": target, "requestDigest": ActionRequestDigest(method, target, etag, body)}
	if code != "" {
		in["totp"] = code
	}
	raw, _ := json.Marshal(in)
	r := sessionRequest(g)
	r.RemoteAddr = "127.0.0.1:12345"
	out, err := s.Reauthenticate(context.Background(), g.SessionToken(), r, raw)
	if err != nil || out.Status != 200 {
		t.Fatal("mint proof", out.Status, err)
	}
	var result struct {
		Token string `json:"reauthToken"`
	}
	if json.Unmarshal(out.Body, &result) != nil || len(result.Token) != 43 {
		t.Fatal("invalid proof")
	}
	return result.Token
}
func TestReauthActionBindingOneUseAndCommandReplay(t *testing.T) {
	_, p, s, g := securityFixture(t, false)
	ctx := context.Background()
	raw := []byte(`{"action":"instant-off"}`)
	proof := mintProof(t, s, g, adminauth.InstantOff, "sale", `"runtime-1"`, raw, "")
	r := draftRequest(g, "reauth-wrong-body", `"runtime-1"`)
	r.Method = "PATCH"
	r.Header.Set("X-Reauth-Token", proof)
	if _, err := p.Operate(ctx, g.SessionToken(), "sale", r, []byte(`{"action": "instant-off"}`)); err != adminauth.ErrForbidden {
		t.Fatal("body mismatch accepted", err)
	}
	if _, err := p.Operate(ctx, g.SessionToken(), "other", r, raw); err != adminauth.ErrForbidden {
		t.Fatal("target mismatch accepted", err)
	}
	r.Header.Set("Idempotency-Key", "reauth-right-body")
	out, err := p.Operate(ctx, g.SessionToken(), "sale", r, raw)
	if err != nil || out.Status != 200 {
		t.Fatal("authorized action", err)
	}
	replay, err := p.Operate(ctx, g.SessionToken(), "sale", r, raw)
	if err != nil || !replay.Replay || string(out.Body) != string(replay.Body) {
		t.Fatal("durable retry", err)
	}
	r.Header.Set("Idempotency-Key", "reauth-second-body")
	if _, err = p.Operate(ctx, g.SessionToken(), "sale", r, raw); err != adminauth.ErrForbidden {
		t.Fatal("used proof accepted", err)
	}
}
func TestReauthTOTPReplayAndAuthority(t *testing.T) {
	f, _, s, g := securityFixture(t, true)
	code := currentCode(t, f.pool)
	raw := []byte(`{"action":"instant-off"}`)
	_ = mintProof(t, s, g, adminauth.InstantOff, "sale", `"runtime-1"`, raw, code)
	in := map[string]any{"password": testPassword, "totp": code, "action": adminauth.InstantOff, "targetId": "sale", "requestDigest": ActionRequestDigest("PATCH", "sale", `"runtime-1"`, raw)}
	body, _ := json.Marshal(in)
	r := sessionRequest(g)
	r.RemoteAddr = "127.0.0.1:12345"
	if _, err := s.Reauthenticate(context.Background(), g.SessionToken(), r, body); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("TOTP counter replay", err)
	}
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='operator'")
	if _, err := s.Reauthenticate(context.Background(), g.SessionToken(), r, body); err != adminauth.ErrForbidden {
		t.Fatal("operator gained sensitive proof", err)
	}
}
func TestReauthAuditRollbackAndConcurrentConsumption(t *testing.T) {
	f, p, s, g := securityFixture(t, false)
	ctx := context.Background()
	raw := []byte(`{"action":"instant-off"}`)
	proof := mintProof(t, s, g, adminauth.InstantOff, "sale", `"runtime-1"`, raw, "")
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT simulated_sensitive_audit CHECK(action<>'runtime.instant_off')")
	r := draftRequest(g, "reauth-atomic-fail", `"runtime-1"`)
	r.Method = "PATCH"
	r.Header.Set("X-Reauth-Token", proof)
	if _, err := p.Operate(ctx, g.SessionToken(), "sale", r, raw); err != adminauth.ErrAuthUnavailable {
		t.Fatal("audit unavailable action", err)
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT simulated_sensitive_audit")
	var accepted, denied, bad atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := draftRequest(g, fmt.Sprintf("consume-proof-%03d", i), `"runtime-1"`)
			r.Method = "PATCH"
			r.Header.Set("X-Reauth-Token", proof)
			out, err := p.Operate(ctx, g.SessionToken(), "sale", r, raw)
			if err == nil && out.Status == 200 {
				accepted.Add(1)
			} else if err == adminauth.ErrForbidden {
				denied.Add(1)
			} else {
				bad.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 1 || denied.Load() != 7 || bad.Load() != 0 {
		t.Fatalf("accepted=%d denied=%d bad=%d", accepted.Load(), denied.Load(), bad.Load())
	}
}

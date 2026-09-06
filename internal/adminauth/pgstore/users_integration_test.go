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
)

func usersFixture(t *testing.T) (fixture, *SecurityService, Grant) {
	t.Helper()
	f, _, s, g := securityFixture(t, false)
	execSQL(t, f.pool, Migration008)
	execSQL(t, f.pool, "UPDATE auth_bootstrap SET completed=true")
	return f, s, g
}
func userRequest(t *testing.T, s *SecurityService, g Grant, method, target, etag, key string, raw []byte, reset bool) *http.Request {
	t.Helper()
	action := adminauth.WriteUsers
	if reset {
		action = adminauth.ResetUserTOTP
	}
	proof := mintMethodProof(t, s, g, action, method, target, etag, raw, "")
	r := draftRequest(g, key, etag)
	r.Method = method
	r.Header.Set("X-Reauth-Token", proof)
	return r
}
func TestUsersCreateReplayAndCredentialRedaction(t *testing.T) {
	f, s, g := usersFixture(t)
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"id": "operator2", "role": "operator", "password": testPassword})
	r := userRequest(t, s, g, "POST", "operator2", "", "create-operator-2", raw, false)
	first, err := s.CreateUser(ctx, g.SessionToken(), r, raw)
	if err != nil || first.Status != 201 {
		t.Fatal("create", first.Status, err)
	}
	replay, err := s.CreateUser(ctx, g.SessionToken(), r, raw)
	if err != nil || !replay.Replay || string(replay.Body) != string(first.Body) {
		t.Fatal("create replay", err)
	}
	listed, err := s.Users(ctx, g.SessionToken())
	if err != nil || listed.Status != 200 || !strings.Contains(string(listed.Body), "operator2") {
		t.Fatal("list", err)
	}
	var leaked int
	if f.pool.QueryRow(ctx, "SELECT count(*) FROM control_commands WHERE convert_from(response,'UTF8') LIKE $1", "%"+testPassword+"%").Scan(&leaked) != nil || leaked != 0 {
		t.Fatal("password in response cache")
	}
	var hash string
	if f.pool.QueryRow(ctx, "SELECT password_hash FROM auth_password_credentials WHERE user_id='operator2'").Scan(&hash) != nil {
		t.Fatal("missing credential")
	}
	if ok, err := s.login.passwords.Verify(ctx, testPassword, hash); err != nil || !ok {
		t.Fatal("invalid stored hash")
	}
}
func TestUsersLastAdminAPIAndDatabaseGuard(t *testing.T) {
	for _, tc := range []struct{ method, body string }{{"PATCH", `{"role":"operator","enabled":true}`}, {"PATCH", `{"role":"admin","enabled":false}`}, {"DELETE", ""}} {
		t.Run(tc.method+tc.body, func(t *testing.T) {
			f, s, g := usersFixture(t)
			raw := []byte(tc.body)
			r := userRequest(t, s, g, tc.method, "admin1", `"user-1"`, "last-admin-guard", raw, false)
			out, err := s.ChangeUser(context.Background(), g.SessionToken(), "admin1", r, raw, false)
			if err != nil || out.Status != 409 || !strings.Contains(string(out.Body), "LAST_ADMIN_REQUIRED") {
				t.Fatal("last admin", out.Status, err)
			}
			if _, err = f.pool.Exec(context.Background(), "UPDATE auth_accounts SET enabled=false WHERE id='admin1'"); err == nil {
				t.Fatal("DB invariant bypass")
			}
			if _, err = s.Users(context.Background(), g.SessionToken()); err != nil {
				t.Fatal("rejected action revoked session", err)
			}
		})
	}
}
func TestUsersDemotionDeletionAndResetRevokeAllProofs(t *testing.T) {
	f, s, g := usersFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "INSERT INTO auth_accounts(id,role) VALUES('second','admin')")
	execSQL(t, f.pool, "INSERT INTO auth_password_credentials SELECT 'second',1,password_hash FROM auth_password_credentials WHERE user_id='admin1'")
	// OFF login gives a real second-admin session; changing its role revokes it.
	login, err := s.login.Login(ctx, "second", testPassword, testPeer)
	if err != nil {
		t.Fatal("second login", err)
	}
	second := login.SessionGrant()
	body := []byte(`{"role":"operator","enabled":true}`)
	r := userRequest(t, s, g, "PATCH", "second", `"user-1"`, "demote-second-admin", body, false)
	out, err := s.ChangeUser(ctx, g.SessionToken(), "second", r, body, false)
	if err != nil || out.Status != 200 {
		t.Fatal("demote", out.Status, err)
	}
	if _, err = s.Users(ctx, second.SessionToken()); err != adminauth.ErrUnauthenticated {
		t.Fatal("target session survived demotion", err)
	}
	// Reset destroys credentials/recovery and all proof paths, then deletion
	// destroys password too while retaining a non-loginable ID tombstone.
	r = userRequest(t, s, g, "POST", "second", `"user-2"`, "reset-second-totp", []byte("{}"), true)
	out, err = s.ChangeUser(ctx, g.SessionToken(), "second", r, []byte("{}"), true)
	if err != nil || out.Status != 200 {
		t.Fatal("reset", out.Status, err)
	}
	r = userRequest(t, s, g, "DELETE", "second", `"user-3"`, "delete-second-account", nil, false)
	out, err = s.ChangeUser(ctx, g.SessionToken(), "second", r, nil, false)
	if err != nil || out.Status != 200 {
		t.Fatal("delete", out.Status, err)
	}
	var gone bool
	if f.pool.QueryRow(ctx, "SELECT deleted AND NOT enabled AND NOT EXISTS(SELECT 1 FROM auth_password_credentials WHERE user_id='second') FROM auth_accounts WHERE id='second'").Scan(&gone) != nil || !gone {
		t.Fatal("delete did not clear credentials")
	}
	listed, _ := s.Users(ctx, g.SessionToken())
	if strings.Contains(string(listed.Body), "second") {
		t.Fatal("tombstone listed")
	}
}
func TestUsersConcurrentLastAdminAndAuditRollback(t *testing.T) {
	f, s, g := usersFixture(t)
	ctx := context.Background()
	execSQL(t, f.pool, "INSERT INTO auth_accounts(id,role) VALUES('second','admin')")
	body := []byte(`{"role":"operator","enabled":true}`)
	proof := mintProof(t, s, g, adminauth.WriteUsers, "second", `"user-1"`, body, "")
	execSQL(t, f.pool, "ALTER TABLE control_audit ADD CONSTRAINT simulated_user_audit CHECK(action<>'users.write')")
	r := draftRequest(g, "audit-user-failure", `"user-1"`)
	r.Method = "PATCH"
	r.Header.Set("X-Reauth-Token", proof)
	if _, err := s.ChangeUser(ctx, g.SessionToken(), "second", r, body, false); err != adminauth.ErrAuthUnavailable {
		t.Fatal("audit failure not atomic", err)
	}
	execSQL(t, f.pool, "ALTER TABLE control_audit DROP CONSTRAINT simulated_user_audit")
	var pass, denied, bad atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := draftRequest(g, fmt.Sprintf("user-command-race-%03d", i), `"user-1"`)
			r.Method = "PATCH"
			r.Header.Set("X-Reauth-Token", proof)
			out, err := s.ChangeUser(ctx, g.SessionToken(), "second", r, body, false)
			if err == nil && out.Status == 200 {
				pass.Add(1)
			} else if err == adminauth.ErrForbidden {
				denied.Add(1)
			} else {
				t.Logf("unexpected user command: status=%d error=%v", out.Status, err)
				bad.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if pass.Load() != 1 || denied.Load() != 7 || bad.Load() != 0 {
		t.Fatalf("pass=%d denied=%d bad=%d", pass.Load(), denied.Load(), bad.Load())
	}
	// Two raw independent writers cannot both disable the remaining admins.
	execSQL(t, f.pool, "UPDATE auth_accounts SET role='admin' WHERE id='second'")
	pass.Store(0)
	denied.Store(0)
	for _, id := range []string{"admin1", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := f.pool.Exec(ctx, "UPDATE auth_accounts SET enabled=false WHERE id=$1", id)
			if err == nil {
				pass.Add(1)
			} else {
				denied.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if pass.Load() != 1 || denied.Load() != 1 {
		t.Fatal("concurrent DB last-admin invariant", pass.Load(), denied.Load())
	}
}

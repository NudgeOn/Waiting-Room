//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"strings"
	"testing"
	"waiting-room/internal/adminauth"
)

func auditedFixtureStore(t *testing.T, f fixture) *Store {
	t.Helper()
	execSQL(t, f.pool, Migration005)
	execSQL(t, f.pool, Migration010)
	s, err := NewAudited(f.pool, f.store.vault)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rejectAuthAudit(t *testing.T, f fixture) {
	t.Helper()
	execSQL(t, f.pool, `CREATE OR REPLACE FUNCTION reject_auth_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END $$; CREATE TRIGGER reject_auth_audit BEFORE INSERT ON control_audit FOR EACH ROW EXECUTE FUNCTION reject_auth_audit()`)
}
func allowAuthAudit(t *testing.T, f fixture) {
	t.Helper()
	execSQL(t, f.pool, "DROP TRIGGER reject_auth_audit ON control_audit")
}

func authAuditCount(t *testing.T, f fixture, action string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM control_audit WHERE action=$1", action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAuthAuditPasswordLockoutIsBoundedAndAnonymous(t *testing.T) {
	f, l := loginFixture(t)
	l.store = auditedFixtureStore(t, f)
	ctx := context.Background()
	for range 15 {
		if _, err := l.Login(ctx, "admin1", "incorrect local password", testPeer); err != adminauth.ErrUnauthenticated {
			t.Fatal(err)
		}
	}
	if authAuditCount(t, f, "auth.login") != 5 || authAuditCount(t, f, "auth.lockout") != 1 {
		t.Fatal("blocked retries grew audit or missing transition")
	}
	var encoded string
	if err := f.pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(a))::text FROM control_audit a").Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"admin1", "incorrect local password", testPassword, testPeer.String(), "bucket_key", "token_hash"} {
		if strings.Contains(encoded, secret) {
			t.Fatal("auth audit leaked request or credential")
		}
	}
	if !strings.Contains(encoded, "system:authentication") {
		t.Fatal("unverified account used as audit actor")
	}
}

func TestAuthAuditPasswordGrantAndLogoutRollback(t *testing.T) {
	f, l := loginFixture(t)
	s := auditedFixtureStore(t, f)
	l.store = s
	ctx := context.Background()
	execSQL(t, f.pool, "UPDATE auth_policy SET totp_enabled=false")
	rejectAuthAudit(t, f)
	if g, err := l.Login(ctx, "admin1", testPassword, testPeer); err != adminauth.ErrAuthUnavailable || g.SessionGrant().SessionToken() != "" {
		t.Fatal("unaudited login granted", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_sessions").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed audit retained session", err)
	}
	allowAuthAudit(t, f)
	r, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ := NewSessionService(s, "https://admin.test", false)
	grant := r.SessionGrant()
	rejectAuthAudit(t, f)
	if err = sessions.Logout(ctx, grant.SessionToken(), sessionRequest(grant)); err != adminauth.ErrAuthUnavailable {
		t.Fatal("logout ignored audit failure", err)
	}
	if _, err = sessions.Me(ctx, grant.SessionToken()); err != nil {
		t.Fatal("audit failure partially revoked session", err)
	}
	allowAuthAudit(t, f)
	if err = sessions.Logout(ctx, grant.SessionToken(), sessionRequest(grant)); err != nil {
		t.Fatal(err)
	}
	if authAuditCount(t, f, "auth.login") != 1 || authAuditCount(t, f, "auth.logout") != 1 {
		t.Fatal("missing or duplicate successful audit")
	}
}

func TestAuthAuditTOTPGrantRollbackKeepsCodeReusable(t *testing.T) {
	f, l := loginFixture(t)
	s := auditedFixtureStore(t, f)
	l.store = s
	ctx := context.Background()
	r, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	rejectAuthAudit(t, f)
	code := currentCode(t, f.pool)
	if g, err := s.CompleteTOTP(ctx, r.ChallengeToken(), code); err != adminauth.ErrAuthUnavailable || g.SessionToken() != "" {
		t.Fatal("unaudited TOTP session granted", err)
	}
	allowAuthAudit(t, f)
	if _, err = s.CompleteTOTP(ctx, r.ChallengeToken(), code); err != nil {
		t.Fatal("audit rollback consumed code/challenge", err)
	}
	if authAuditCount(t, f, "auth.login") != 2 {
		t.Fatal("password challenge and MFA success not separately recorded")
	}
}

func TestAuthAuditEnrollmentRecoveryAtomicAndRedacted(t *testing.T) {
	f, l, e, token, setup := enrollmentFixture(t)
	s := auditedFixtureStore(t, f)
	l.store = s
	e.store = s
	ctx := context.Background()
	rejectAuthAudit(t, f)
	if _, err := e.Complete(ctx, token, enrollmentCode(t, f, setup)); err != adminauth.ErrAuthUnavailable {
		t.Fatal("unaudited enrollment committed", err)
	}
	enrollmentCounts(t, f, 0, 0, 0)
	allowAuthAudit(t, f)
	enrolled, err := e.Complete(ctx, token, enrollmentCode(t, f, setup))
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	recovery, _ := NewRecoveryService(s, l.passwords)
	rejectAuthAudit(t, f)
	if _, err = recovery.Complete(ctx, r.ChallengeToken(), enrolled.RecoveryCodes()[0]); err != adminauth.ErrAuthUnavailable {
		t.Fatal("unaudited recovery committed", err)
	}
	allowAuthAudit(t, f)
	grant, err := recovery.Complete(ctx, r.ChallengeToken(), enrolled.RecoveryCodes()[0])
	if err != nil {
		t.Fatal("audit rollback consumed recovery code", err)
	}
	if authAuditCount(t, f, "auth.totp.enroll") != 1 || authAuditCount(t, f, "auth.totp.recover") != 1 {
		t.Fatal("credential transitions missing or duplicated")
	}
	var encoded string
	if err = f.pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(a))::text FROM control_audit a").Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	key, _ := setup.ManualKey()
	for _, secret := range []string{key, token, r.ChallengeToken(), grant.SessionToken(), grant.CSRFToken(), enrolled.RecoveryCodes()[0]} {
		if strings.Contains(encoded, secret) {
			t.Fatal("audit credential leak")
		}
	}
}

func TestAuthAuditBootstrapFailureRetainsOneTimeInstallToken(t *testing.T) {
	f, l, install := bootstrapFixture(t)
	l.store = auditedFixtureStore(t, f)
	rejectAuthAudit(t, f)
	ctx := context.Background()
	if _, err := l.Bootstrap(ctx, install.Token(), "admin1", testPassword, testPeer); err != adminauth.ErrAuthUnavailable {
		t.Fatal("unaudited bootstrap committed", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM auth_accounts").Scan(&count); err != nil || count != 0 {
		t.Fatal("bootstrap audit failure retained account", err)
	}
	allowAuthAudit(t, f)
	if _, err := l.Bootstrap(ctx, install.Token(), "admin1", testPassword, testPeer); err != nil {
		t.Fatal("bootstrap audit failure consumed token", err)
	}
	if authAuditCount(t, f, "auth.bootstrap") != 1 {
		t.Fatal("bootstrap missing audit")
	}
}

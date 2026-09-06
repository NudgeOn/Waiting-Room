//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"waiting-room/internal/adminauth"
)

// Fixed local-only fixture URL, never an arbitrary operator/production DATABASE_URL.
const labURL = "postgres://wr_auth_lab:local-test-only@127.0.0.1:15432/wr_auth_lab?sslmode=disable"

type fixture struct {
	pool, other    *pgxpool.Pool
	store, replica *Store
}

func setup(t *testing.T) fixture {
	t.Helper()
	if os.Getenv("WR_TEST_AUTH_DB") != "local" {
		t.Fatal("requires WR_TEST_AUTH_DB=local and dedicated auth-lab Compose")
	}
	ctx := context.Background()
	root, err := pgx.Connect(ctx, labURL)
	if err != nil {
		t.Fatal("dedicated auth lab unavailable")
	}
	nonce, _, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	schema := "wr_auth_test_" + strings.ToLower(strings.ReplaceAll(nonce[:16], "-", "_"))
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		root.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Exact, newly generated test schema only; no existing schemas/data are removed.
		_, err := root.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE")
		if err != nil {
			t.Error("test schema cleanup failed")
		}
		root.Close(ctx)
	})
	var version string
	if err = root.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "17.11") {
		t.Fatal("requires pinned PostgreSQL 17.11")
	}
	t.Log("PostgreSQL", version, "; two independent pools")
	openPool := func() *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(labURL)
		if err != nil {
			t.Fatal(err)
		}
		cfg.MaxConns = 8
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		cfg.ConnConfig.RuntimeParams["application_name"] = schema
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	p, q := openPool(), openPool()
	if _, err = p.Exec(ctx, Migration001); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, Migration002); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, Migration003); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, Migration004); err != nil {
		t.Fatal(err)
	}
	v := testVault(t)
	a, err := New(p, v)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(q, v)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{p, q, a, b}
}
func execSQL(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func seedUser(t *testing.T, f fixture) {
	t.Helper()
	execSQL(t, f.pool, "INSERT INTO auth_accounts (id,role) VALUES ('admin1','admin')")
	// RFC fixture only. Not an actual user credential or password login.
	secret, err := adminauth.ParseSecret("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.store.vault.Seal(adminauth.CredentialRef{UserID: "admin1", Version: 1}, "k1", secret)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "INSERT INTO auth_totp_credentials (user_id,version,key_id,sealed_secret) VALUES ('admin1',1,'k1',$1)", sealed)
}
func seedChallenge(t *testing.T, f fixture) string {
	t.Helper()
	raw, hash, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "INSERT INTO auth_totp_challenges (token_hash,user_id,credential_version,policy_version,user_version,created_at,expires_at) SELECT $1,'admin1',1,1,1,t,t+interval '5 minutes' FROM (SELECT clock_timestamp() AS t) stamp", hash[:])
	return raw
}
func currentCode(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	var now time.Time
	if err := p.QueryRow(context.Background(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	var input [8]byte
	binary.BigEndian.PutUint64(input[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, []byte("12345678901234567890"))
	mac.Write(input[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}
func assertCounts(t *testing.T, f fixture, sessions, consumed int, counterUsed bool) {
	t.Helper()
	var s, c int
	var counter int64
	if err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM auth_sessions),(SELECT count(*) FROM auth_totp_challenges WHERE consumed),last_counter FROM auth_totp_credentials WHERE user_id='admin1'").Scan(&s, &c, &counter); err != nil {
		t.Fatal(err)
	}
	if s != sessions || c != consumed || (counter >= 0) != counterUsed {
		t.Fatalf("sessions=%d consumed=%d counterUsed=%t", s, c, counter >= 0)
	}
}
func TestPostgresConcurrentChallengeAndCounter(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint("same_challenge=", same), func(t *testing.T) {
			f := setup(t)
			seedUser(t, f)
			first := seedChallenge(t, f)
			challenges := make([]string, 64)
			for i := range challenges {
				challenges[i] = first
				if !same && i > 0 {
					challenges[i] = seedChallenge(t, f)
				}
			}
			code := currentCode(t, f.pool)
			var successes, failures atomic.Int32
			var wg sync.WaitGroup
			for i := range challenges {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					s := f.store
					if i%2 == 1 {
						s = f.replica
					}
					grant, err := s.CompleteTOTP(context.Background(), challenges[i], code)
					if err == nil {
						if len(grant.SessionToken()) != 43 || len(grant.CSRFToken()) != 43 {
							failures.Add(1)
						}
						successes.Add(1)
					} else if err != adminauth.ErrInvalidOrReplayed || grant.SessionToken() != "" || grant.CSRFToken() != "" {
						failures.Add(1)
					}
				}(i)
			}
			wg.Wait()
			if successes.Load() != 1 || failures.Load() != 0 {
				t.Fatalf("successes=%d unexpected=%d", successes.Load(), failures.Load())
			}
			assertCounts(t, f, 1, 1, true)
		})
	}
}
func TestPostgresFiveFailuresAndExpiry(t *testing.T) {
	f := setup(t)
	seedUser(t, f)
	challenge := seedChallenge(t, f)
	for range 5 {
		if _, err := f.store.CompleteTOTP(context.Background(), challenge, "invalid"); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal(err)
		}
	}
	if _, err := f.store.CompleteTOTP(context.Background(), challenge, currentCode(t, f.pool)); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("exhausted challenge accepted")
	}
	var attempts int
	if err := f.pool.QueryRow(context.Background(), "SELECT attempts FROM auth_totp_challenges").Scan(&attempts); err != nil || attempts != 5 {
		t.Fatal("attempt limit")
	}
	assertCounts(t, f, 0, 1, false)
	expired := seedChallenge(t, f)
	hash, _ := tokenHash(expired)
	execSQL(t, f.pool, "UPDATE auth_totp_challenges SET created_at=clock_timestamp()-interval '6 minutes',expires_at=clock_timestamp()-interval '2 minutes' WHERE token_hash=$1", hash[:])
	if _, err := f.store.CompleteTOTP(context.Background(), expired, currentCode(t, f.pool)); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("expired challenge accepted")
	}
	assertCounts(t, f, 0, 1, false)
}
func TestPostgresStaleIdentityAndPolicy(t *testing.T) {
	for _, sql := range []string{
		"UPDATE auth_accounts SET enabled=false",
		"UPDATE auth_accounts SET session_version=2",
		"UPDATE auth_policy SET version=2",
		"UPDATE auth_policy SET totp_enabled=false",
		"UPDATE auth_totp_credentials SET version=2",
	} {
		t.Run(sql, func(t *testing.T) {
			f := setup(t)
			seedUser(t, f)
			challenge := seedChallenge(t, f)
			execSQL(t, f.pool, sql)
			if _, err := f.store.CompleteTOTP(context.Background(), challenge, currentCode(t, f.pool)); err != adminauth.ErrInvalidOrReplayed {
				t.Fatal("stale identity/policy accepted", err)
			}
			assertCounts(t, f, 0, 0, false)
		})
	}
}
func TestPostgresRollbackSessionFailure(t *testing.T) {
	f := setup(t)
	seedUser(t, f)
	challenge := seedChallenge(t, f)
	code := currentCode(t, f.pool)
	execSQL(t, f.pool, "ALTER TABLE auth_sessions ADD CONSTRAINT test_reject_session CHECK (user_id <> 'admin1')")
	grant, err := f.store.CompleteTOTP(context.Background(), challenge, code)
	if err != adminauth.ErrAuthUnavailable || grant.SessionToken() != "" {
		t.Fatal("failed session insert did not fail closed")
	}
	assertCounts(t, f, 0, 0, false)
	execSQL(t, f.pool, "ALTER TABLE auth_sessions DROP CONSTRAINT test_reject_session")
	grant, err = f.replica.CompleteTOTP(context.Background(), challenge, code)
	if err != nil {
		t.Fatal(err)
	}
	sh, _ := tokenHash(grant.SessionToken())
	ch, _ := tokenHash(grant.CSRFToken())
	var csrf []byte
	var session adminauth.Session
	err = f.pool.QueryRow(context.Background(), "SELECT csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at FROM auth_sessions WHERE token_hash=$1", sh[:]).Scan(&csrf, &session.UserID, &session.AuthPolicyVersion, &session.UserSessionVersion, &session.MFAVerified, &session.CreatedAt, &session.LastSeenAt)
	if err != nil || string(csrf) != string(ch[:]) {
		t.Fatal("hashed session storage")
	}
	a := adminauth.Account{ID: "admin1", Role: adminauth.Admin, Enabled: true, SessionVersion: 1, TOTPEnrolled: true}
	if err = adminauth.CheckSession(a, session, adminauth.DefaultPolicy(), session.CreatedAt); err != nil {
		t.Fatal(err)
	}
	assertCounts(t, f, 1, 1, true)
	// A fresh pool connection, no in-memory accepted-counter cache.
	f.other.Reset()
	if _, err = f.replica.CompleteTOTP(context.Background(), seedChallenge(t, f), code); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("replay survived reconnection")
	}
}
func TestPostgresCiphertextAndCancellationFailClosed(t *testing.T) {
	f := setup(t)
	seedUser(t, f)
	challenge := seedChallenge(t, f)
	code := currentCode(t, f.pool)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.store.CompleteTOTP(canceled, challenge, code); err != adminauth.ErrAuthUnavailable {
		t.Fatal("cancellation")
	}
	execSQL(t, f.pool, "UPDATE auth_totp_credentials SET sealed_secret=set_byte(sealed_secret,59,get_byte(sealed_secret,59)#1)")
	if _, err := f.store.CompleteTOTP(context.Background(), challenge, code); err != adminauth.ErrAuthUnavailable {
		t.Fatal("ciphertext tamper")
	}
	assertCounts(t, f, 0, 0, false)
}
func TestPostgresExpiryAfterLockWait(t *testing.T) {
	f := setup(t)
	seedUser(t, f)
	challenge := seedChallenge(t, f)
	code := currentCode(t, f.pool)
	hash, _ := tokenHash(challenge)
	execSQL(t, f.pool, "UPDATE auth_totp_challenges SET expires_at=clock_timestamp()+interval '300 milliseconds' WHERE token_hash=$1", hash[:])
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM auth_accounts WHERE id='admin1' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := f.replica.CompleteTOTP(ctx, challenge, code); result <- err }()
	// Hold the account lock until the DATABASE clock has passed challenge expiry.
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var expired bool
		// Separate autocommit reads avoid PostgreSQL's per-transaction statistics snapshot.
		err = f.pool.QueryRow(ctx, "SELECT clock_timestamp()>expires_at,EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock') FROM auth_totp_challenges WHERE token_hash=$1", hash[:]).Scan(&expired, &blocked)
		if err != nil {
			t.Fatal(err)
		}
		if expired && blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("request did not wait on row lock")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("challenge accepted after lock wait", err)
	}
	assertCounts(t, f, 0, 0, false)
}

//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
)

// Fast storage fixture: two usable codes and eight consumed slots sharing one
// hash. Full production generation/verification is tested by EnrollmentLoginFlow.
func recoveryFixture(t *testing.T) (fixture, *LoginService, *RecoveryService, [2]string) {
	t.Helper()
	f, l := loginFixture(t)
	ctx := context.Background()
	var codes [2]string
	for i := range 3 {
		raw, _, err := adminauth.NewCSRFToken()
		if err != nil {
			t.Fatal(err)
		}
		hash, err := l.passwords.Hash(ctx, "wr-recovery-v1:"+raw)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			codes[i] = raw
			execSQL(t, f.pool, "INSERT INTO auth_recovery_codes (user_id,credential_version,slot,code_hash) VALUES ('admin1',1,$1,$2)", i+1, hash.StorageValue())
		} else {
			for slot := 3; slot <= 10; slot++ {
				execSQL(t, f.pool, "INSERT INTO auth_recovery_codes (user_id,credential_version,slot,code_hash,consumed) VALUES ('admin1',1,$1,$2,true)", slot, hash.StorageValue())
			}
		}
	}
	r, err := NewRecoveryService(f.store, l.passwords)
	if err != nil {
		t.Fatal(err)
	}
	return f, l, r, codes
}
func recoveryCounts(t *testing.T, f fixture, wantCodes, wantSessions int) {
	t.Helper()
	var c, s int
	err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM auth_recovery_codes WHERE slot<=2 AND consumed),(SELECT count(*) FROM auth_sessions)").Scan(&c, &s)
	if err != nil || c != wantCodes || s != wantSessions {
		t.Fatalf("recovery counts %d/%d expected %d/%d: %v", c, s, wantCodes, wantSessions, err)
	}
}

func TestRecoveryEnrollmentLoginFlow(t *testing.T) {
	f, l, e, enrollToken, setup := enrollmentFixture(t)
	ctx := context.Background()
	enrolled, err := e.Complete(ctx, enrollToken, enrollmentCode(t, f, setup))
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRecoveryService(f.replica, l.passwords)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{enrollToken, enrolled.SessionGrant().SessionToken()} {
		if _, err = r.Complete(ctx, token, enrolled.RecoveryCodes()[0]); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal("wrong token domain accepted", err)
		}
	}
	if login, err := l.Login(ctx, "admin1", "incorrect-password", testPeer); err != adminauth.ErrUnauthenticated || login.ChallengeToken() != "" {
		t.Fatal("password bypass", err)
	}
	login, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, err = r.Complete(ctx, login.ChallengeToken(), "malformed"); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal("bad recovery accepted", err)
		}
	}
	var beforeCounter int64
	if err = f.pool.QueryRow(ctx, "SELECT last_counter FROM auth_totp_credentials").Scan(&beforeCounter); err != nil {
		t.Fatal(err)
	}
	grant, err := r.Complete(ctx, login.ChallengeToken(), enrolled.RecoveryCodes()[0])
	if err != nil {
		t.Fatal("correct fifth recovery failed", err)
	}
	me, err := NewSessionService(f.store, "https://admin.example.test", false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := me.Me(ctx, grant.SessionToken())
	if err != nil || !view.MFAVerified || !view.TOTPEnrolled || view.UserID != "admin1" {
		t.Fatal("backup-factor session rejected", err)
	}
	var consumed int
	var afterCounter int64
	if err = f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_recovery_codes WHERE consumed),last_counter FROM auth_totp_credentials").Scan(&consumed, &afterCounter); err != nil || consumed != 1 || afterCounter != beforeCounter {
		t.Fatal("recovery changed other material", err)
	}
	if _, err = f.store.CompleteTOTP(ctx, login.ChallengeToken(), enrollmentCode(t, f, setup)); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("challenge reused via OTP", err)
	}
	fresh, err := l.Login(ctx, "admin1", testPassword, testPeer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Complete(ctx, fresh.ChallengeToken(), enrolled.RecoveryCodes()[0]); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("used code accepted by fresh challenge", err)
	}
	unknown, _, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Complete(ctx, fresh.ChallengeToken(), unknown); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("unknown well-formed code accepted", err)
	}
	// Ordinary sessions and the remaining nine codes are preserved, not a reset.
	if _, err = me.Me(ctx, enrolled.SessionGrant().SessionToken()); err != nil {
		t.Fatal("recovery unexpectedly reset sessions", err)
	}
}

func TestRecoverySharedAttemptLimit(t *testing.T) {
	f, _, r, _ := recoveryFixture(t)
	ctx := context.Background()
	token := seedChallenge(t, f)
	for range 2 {
		if _, err := f.store.CompleteTOTP(ctx, token, "bad"); err != adminauth.ErrInvalidOrReplayed {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var unexpected atomic.Int32
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Complete(ctx, token, "malformed")
			if err != adminauth.ErrInvalidOrReplayed {
				unexpected.Add(1)
			}
		}()
	}
	wg.Wait()
	if unexpected.Load() != 0 {
		t.Fatal("shared budget race failed")
	}
	var attempts int
	hash, _ := tokenHash(token)
	if err := f.pool.QueryRow(ctx, "SELECT attempts FROM auth_totp_challenges WHERE token_hash=$1", hash[:]).Scan(&attempts); err != nil || attempts != 5 {
		t.Fatal("OTP/recovery did not share budget", err)
	}
	if _, err := f.store.CompleteTOTP(ctx, token, currentCode(t, f.pool)); err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("OTP bypassed exhausted recovery budget", err)
	}
	recoveryCounts(t, f, 0, 0)
	// A missing hash row is an unavailable/corrupt store, not permission to skip slots.
	fresh := seedChallenge(t, f)
	execSQL(t, f.pool, "DELETE FROM auth_recovery_codes WHERE slot=10")
	if _, err := r.Complete(ctx, fresh, "malformed"); err != adminauth.ErrAuthUnavailable {
		t.Fatal("incomplete recovery storage accepted", err)
	}
}

func TestRecoveryConcurrentConsumption(t *testing.T) {
	for _, mode := range []string{"same-challenge", "different-challenges", "different-codes", "otp-race"} {
		t.Run(mode, func(t *testing.T) {
			f, l, a, _ := recoveryFixture(t)
			ctx := context.Background()
			b, err := NewRecoveryService(f.replica, l.passwords)
			if err != nil {
				t.Fatal(err)
			}
			first := seedChallenge(t, f)
			second := first
			if mode == "different-challenges" {
				second = seedChallenge(t, f)
			}
			h1, _ := tokenHash(first)
			h2, _ := tokenHash(second)
			proof1, err := a.reserve(ctx, h1)
			if err != nil {
				t.Fatal(err)
			}
			proof2, err := b.reserve(ctx, h2)
			if err != nil {
				t.Fatal(err)
			}
			code := currentCode(t, f.pool)
			var wg sync.WaitGroup
			var success, denied, other atomic.Int32
			// KDF is covered separately; this races the final atomic storage phase.
			for i := range 2 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					var err error
					if i == 0 {
						_, err = a.finish(ctx, proof1, 1)
					} else if mode == "otp-race" {
						_, err = f.replica.CompleteTOTP(ctx, first, code)
					} else {
						slot := 1
						if mode == "different-codes" {
							slot = 2
						}
						_, err = b.finish(ctx, proof2, slot)
					}
					switch err {
					case nil:
						success.Add(1)
					case adminauth.ErrInvalidOrReplayed:
						denied.Add(1)
					default:
						other.Add(1)
					}
				}(i)
			}
			wg.Wait()
			if success.Load() != 1 || denied.Load() != 1 || other.Load() != 0 {
				t.Fatalf("single winner %d/%d/%d", success.Load(), denied.Load(), other.Load())
			}
			if mode != "otp-race" {
				recoveryCounts(t, f, 1, 1)
			} else {
				var c, s int
				if err = f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_recovery_codes WHERE slot<=2 AND consumed),(SELECT count(*) FROM auth_sessions)").Scan(&c, &s); err != nil || s != 1 || c > 1 {
					t.Fatal("OTP race state", err)
				}
			}
		})
	}
}

func TestRecoveryStaleStateAndRollback(t *testing.T) {
	f, _, r, _ := recoveryFixture(t)
	ctx := context.Background()
	token := seedChallenge(t, f)
	hash, _ := tokenHash(token)
	proof, err := r.reserve(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, change, restore string
		want                  error
	}{
		{"disabled", "UPDATE auth_accounts SET enabled=false", "UPDATE auth_accounts SET enabled=true", adminauth.ErrInvalidOrReplayed},
		{"role", "UPDATE auth_accounts SET role='viewer'", "UPDATE auth_accounts SET role='admin'", adminauth.ErrInvalidOrReplayed},
		{"user-version", "UPDATE auth_accounts SET session_version=2", "UPDATE auth_accounts SET session_version=1", adminauth.ErrInvalidOrReplayed},
		{"policy", "UPDATE auth_policy SET version=2", "UPDATE auth_policy SET version=1", adminauth.ErrInvalidOrReplayed},
		{"off", "UPDATE auth_policy SET totp_enabled=false", "UPDATE auth_policy SET totp_enabled=true", adminauth.ErrInvalidOrReplayed},
		{"credential-version", "UPDATE auth_totp_challenges SET credential_version=2", "UPDATE auth_totp_challenges SET credential_version=1", adminauth.ErrInvalidOrReplayed},
		{"changed-hash", "UPDATE auth_recovery_codes SET code_hash='changed' WHERE slot=1", "", adminauth.ErrInvalidOrReplayed},
		{"session-failure", "ALTER TABLE auth_sessions ADD CONSTRAINT fixture_reject CHECK (false)", "ALTER TABLE auth_sessions DROP CONSTRAINT fixture_reject", adminauth.ErrAuthUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execSQL(t, f.pool, tc.change)
			_, err := r.finish(ctx, proof, 1)
			if err != tc.want {
				t.Fatal("stale/failed recovery accepted", err)
			}
			recoveryCounts(t, f, 0, 0)
			if tc.restore != "" {
				execSQL(t, f.pool, tc.restore)
			} else {
				execSQL(t, f.pool, "UPDATE auth_recovery_codes SET code_hash=$1 WHERE slot=1", proof.records[0].encoded)
			}
		})
	}
	var attempts int
	var consumed bool
	if err = f.pool.QueryRow(ctx, "SELECT attempts,consumed FROM auth_totp_challenges WHERE token_hash=$1", hash[:]).Scan(&attempts, &consumed); err != nil || attempts != 1 || consumed {
		t.Fatal("rollback lost reserved attempt or consumed challenge", err)
	}
	if _, err = r.finish(ctx, proof, 1); err != nil {
		t.Fatal("valid prepared retry failed", err)
	}
	recoveryCounts(t, f, 1, 1)
}

func TestRecoveryExpiryAfterLockWait(t *testing.T) {
	f, _, r, _ := recoveryFixture(t)
	ctx := context.Background()
	token := seedChallenge(t, f)
	hash, _ := tokenHash(token)
	proof, err := r.reserve(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.pool, "UPDATE auth_totp_challenges SET created_at=clock_timestamp()-interval '4 minutes',expires_at=clock_timestamp()+interval '1 second'")
	tx, err := f.other.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT token_hash FROM auth_totp_challenges FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := r.finish(ctx, proof, 1); result <- err }()
	blocked := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		err = f.other.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event_type='Lock' AND query LIKE 'SELECT user_id,credential_version,policy_version,%')").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("recovery did not reach challenge lock")
	}
	for {
		var expired bool
		if err = f.other.QueryRow(ctx, "SELECT expires_at<=clock_timestamp() FROM auth_totp_challenges").Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != adminauth.ErrInvalidOrReplayed {
		t.Fatal("expired while waiting accepted", err)
	}
	recoveryCounts(t, f, 0, 0)
}

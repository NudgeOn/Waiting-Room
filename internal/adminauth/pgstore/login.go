// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

//go:embed migrations/002_password_login.sql
var Migration002 string

// Fixed local alpha defaults: attempts (successful or not) per anchored minute.
// Not queue traffic limits, and not a claimed production sizing result.
const LoginAccountLimit = 5
const LoginSourceLimit = 20
const LoginInstallationLimit = 60

type LoginService struct {
	store          *Store
	passwords      *adminauth.PasswordHasher
	fingerprintKey [32]byte
}

func (LoginService) String() string               { return "[REDACTED_LOGIN_SERVICE]" }
func (LoginService) GoString() string             { return "[REDACTED_LOGIN_SERVICE]" }
func (LoginService) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_LOGIN_SERVICE]") }
func NewLoginService(store *Store, passwords *adminauth.PasswordHasher, key [32]byte) (*LoginService, error) {
	if store == nil || passwords == nil || key == [32]byte{} {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &LoginService{store, passwords, key}, nil
}

type LoginResult struct {
	challenge  string
	enrollment string
	grant      Grant
	expiresAt  time.Time
}

func (LoginResult) String() string               { return "[REDACTED_LOGIN_RESULT]" }
func (LoginResult) GoString() string             { return "[REDACTED_LOGIN_RESULT]" }
func (LoginResult) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_LOGIN_RESULT]") }
func (r LoginResult) ChallengeToken() string     { return r.challenge }

// EnrollmentToken grants no dashboard/session access. Enrollment handlers are pending.
func (r LoginResult) EnrollmentToken() string { return r.enrollment }
func (r LoginResult) SessionGrant() Grant     { return r.grant }
func (r LoginResult) ExpiresAt() time.Time    { return r.expiresAt }

func (l *LoginService) fingerprint(domain, value string) string {
	mac := hmac.New(sha256.New, l.fingerprintKey[:])
	_, _ = mac.Write([]byte(domain + ":" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
func sourceValue(peer netip.Addr) (string, bool) {
	if !peer.IsValid() || peer.IsUnspecified() || peer.IsMulticast() || peer.Zone() != "" {
		return "", false
	}
	peer = peer.Unmap()
	if peer.Is6() {
		return netip.PrefixFrom(peer, 64).Masked().String(), true
	}
	return peer.String(), true
}

func authTx(ctx context.Context, s *Store) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='4s'; SET LOCAL synchronous_commit=on"); err != nil {
		rollback(tx)
		return nil, adminauth.ErrAuthUnavailable
	}
	return tx, nil
}

// reserveAttempt commits before hashing. A process failure never refunds the
// admitted guess. No account-existence query precedes these shared DB limits.
func (l *LoginService) reserveAttempt(ctx context.Context, user, source string) error {
	tx, err := authTx(ctx, l.store)
	if err != nil {
		return err
	}
	defer rollback(tx)
	// Installation first serializes reservation/pruning only (never Argon2 work).
	// Row locks precede reading clock_timestamp, including concurrent UPSERT waits.
	keys := []string{"installation", "source", "account"}
	limits := []int{LoginInstallationLimit, LoginSourceLimit, LoginAccountLimit}
	allowed := true
	for i, kind := range keys {
		key := kind
		if i > 0 {
			var now time.Time
			if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
				return adminauth.ErrAuthUnavailable
			}
			if kind == "source" {
				key += ":" + l.fingerprint(fmt.Sprintf("source/%d", now.Unix()/86400), source)
			} else {
				key += ":" + l.fingerprint("account", user)
			}
		}
		tag, e := tx.Exec(ctx, "INSERT INTO auth_login_buckets (bucket_key,started_at,expires_at,used) SELECT $1,t,t+interval '1 minute',1 FROM (SELECT clock_timestamp() AS t) stamp ON CONFLICT DO NOTHING", key)
		if e != nil {
			return adminauth.ErrAuthUnavailable
		}
		if tag.RowsAffected() == 0 {
			var start, expiry, now time.Time
			var used int
			e = tx.QueryRow(ctx, "SELECT started_at,expires_at,used FROM auth_login_buckets WHERE bucket_key=$1 FOR UPDATE", key).Scan(&start, &expiry, &used)
			if e != nil {
				return adminauth.ErrAuthUnavailable
			}
			if e = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); e != nil {
				return adminauth.ErrAuthUnavailable
			}
			if now.Before(start) || now.Before(expiry) && used >= limits[i] {
				allowed = false
				break
			}
			if !now.Before(expiry) {
				_, e = tx.Exec(ctx, "UPDATE auth_login_buckets SET started_at=$2::timestamptz,expires_at=$2::timestamptz+interval '1 minute',used=1 WHERE bucket_key=$1", key, now)
			} else {
				_, e = tx.Exec(ctx, "UPDATE auth_login_buckets SET used=used+1 WHERE bucket_key=$1", key)
			}
			if e != nil {
				return adminauth.ErrAuthUnavailable
			}
		}
	}
	if allowed {
		if _, err = tx.Exec(ctx, "DELETE FROM auth_login_buckets WHERE expires_at<=clock_timestamp() AND bucket_key<>'installation' AND bucket_key NOT LIKE 'http-%'"); err != nil {
			return adminauth.ErrAuthUnavailable
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return adminauth.ErrAuthUnavailable
	}
	if !allowed {
		return adminauth.ErrUnauthenticated
	}
	return nil
}

type passwordSnapshot struct {
	user, role, hash                            string
	userVersion, passwordVersion, policyVersion uint64
	enabled, totp                               bool
	mode                                        adminauth.PolicyMode
}

func (passwordSnapshot) String() string   { return "[REDACTED_PASSWORD_SNAPSHOT]" }
func (passwordSnapshot) GoString() string { return "[REDACTED_PASSWORD_SNAPSHOT]" }
func (passwordSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_PASSWORD_SNAPSHOT]")
}
func (l *LoginService) snapshot(ctx context.Context, user string) (passwordSnapshot, error) {
	var s passwordSnapshot
	err := l.store.pool.QueryRow(ctx, "SELECT a.id,a.role,a.enabled,a.session_version,c.version,c.password_hash,p.version,p.mode,p.totp_enabled FROM auth_accounts a JOIN auth_password_credentials c ON c.user_id=a.id CROSS JOIN auth_policy p WHERE a.id=$1 AND p.singleton", user).Scan(&s.user, &s.role, &s.enabled, &s.userVersion, &s.passwordVersion, &s.hash, &s.policyVersion, &s.mode, &s.totp)
	return s, err
}

// Login requires a trusted socket peer (or separately verified proxy identity).
// Never pass X-Forwarded-For or client JSON as peer. No HTTP adapter exists yet.
func (l *LoginService) Login(ctx context.Context, user, password string, peer netip.Addr) (LoginResult, error) {
	source, ok := sourceValue(peer)
	if !ok || !safeID.MatchString(user) || !adminauth.ValidLoginPassword(password) {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := l.reserveAttempt(ctx, user, source); err != nil {
		return LoginResult{}, err
	}
	snapshot, err := l.snapshot(ctx, user)
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	stored := snapshot.hash
	if missing || !snapshot.enabled {
		stored = ""
	}
	valid, err := l.passwords.Verify(ctx, password, stored)
	if err != nil {
		return LoginResult{}, err
	}
	if !valid || missing || !snapshot.enabled {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	return l.finishPassword(ctx, snapshot)
}
func (l *LoginService) finishPassword(ctx context.Context, before passwordSnapshot) (LoginResult, error) {
	tx, err := authTx(ctx, l.store)
	if err != nil {
		return LoginResult{}, err
	}
	defer rollback(tx)
	var policy adminauth.AuthPolicy
	err = tx.QueryRow(ctx, "SELECT mode,totp_enabled,version FROM auth_policy WHERE singleton FOR SHARE").Scan(&policy.Mode, &policy.TOTPEnabled, &policy.Version)
	if err != nil || policy.Validate() != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	var role string
	var enabled bool
	var uv uint64
	err = tx.QueryRow(ctx, "SELECT role,enabled,session_version FROM auth_accounts WHERE id=$1 FOR UPDATE", before.user).Scan(&role, &enabled, &uv)
	if err != nil {
		return LoginResult{}, loginDBError(err)
	}
	var pv uint64
	var hash string
	err = tx.QueryRow(ctx, "SELECT version,password_hash FROM auth_password_credentials WHERE user_id=$1 FOR UPDATE", before.user).Scan(&pv, &hash)
	if err != nil {
		return LoginResult{}, loginDBError(err)
	}
	if !enabled || role != before.role || uv != before.userVersion || pv != before.passwordVersion || hash != before.hash || policy.Version != before.policyVersion || policy.Mode != before.mode || policy.TOTPEnabled != before.totp {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	result := LoginResult{}
	if policy.TOTPEnabled {
		var cv uint64
		err = tx.QueryRow(ctx, "SELECT version FROM auth_totp_credentials WHERE user_id=$1 FOR UPDATE", before.user).Scan(&cv)
		// Restricted enrollment proof only; never a session for an unenrolled user.
		if errors.Is(err, pgx.ErrNoRows) {
			result.enrollment, result.expiresAt, err = issueEnrollment(ctx, tx, before.user, policy.Version, uv)
			if err != nil {
				return LoginResult{}, err
			}
			if err = tx.Commit(ctx); err != nil {
				return LoginResult{}, adminauth.ErrAuthUnavailable
			}
			return result, nil
		}
		if err != nil {
			return LoginResult{}, loginDBError(err)
		}
		raw, token, err := adminauth.NewCSRFToken()
		if err != nil {
			return LoginResult{}, adminauth.ErrAuthUnavailable
		}
		// Replace this user's earlier pending challenges to bound valid challenge count.
		_, err = tx.Exec(ctx, "UPDATE auth_totp_challenges SET consumed=true WHERE user_id=$1 AND NOT consumed", before.user)
		if err != nil {
			return LoginResult{}, adminauth.ErrAuthUnavailable
		}
		err = tx.QueryRow(ctx, "INSERT INTO auth_totp_challenges (token_hash,user_id,credential_version,policy_version,user_version,created_at,expires_at) SELECT $1,$2,$3,$4,$5,t,t+interval '5 minutes' FROM (SELECT clock_timestamp() AS t) stamp RETURNING expires_at", token[:], before.user, cv, policy.Version, uv).Scan(&result.expiresAt)
		if err != nil {
			return LoginResult{}, adminauth.ErrAuthUnavailable
		}
		result.challenge = raw
	} else {
		result.grant, err = issuePasswordSession(ctx, tx, before.user, policy.Version, uv)
		if err != nil {
			return LoginResult{}, adminauth.ErrAuthUnavailable
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	return result, nil
}
func loginDBError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return adminauth.ErrUnauthenticated
	}
	return adminauth.ErrAuthUnavailable
}

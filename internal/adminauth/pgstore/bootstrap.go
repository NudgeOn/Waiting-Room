// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

//go:embed migrations/003_bootstrap.sql
var Migration003 string

type BootstrapToken struct{ raw string }

func (BootstrapToken) String() string   { return "[REDACTED_BOOTSTRAP_TOKEN]" }
func (BootstrapToken) GoString() string { return "[REDACTED_BOOTSTRAP_TOKEN]" }
func (BootstrapToken) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_BOOTSTRAP_TOKEN]")
}

// Token is sensitive: expose only to an authenticated local operator, never logs.
func (t BootstrapToken) Token() string { return t.raw }

// IssueBootstrapToken is an operator-only service primitive, not an HTTP endpoint.
// Explicit issuance rotates the previous token. Constructors never issue tokens.
func (s *Store) IssueBootstrapToken(ctx context.Context) (BootstrapToken, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := authTx(ctx, s)
	if err != nil {
		return BootstrapToken{}, err
	}
	defer rollback(tx)
	var completed bool
	if err = tx.QueryRow(ctx, "SELECT completed FROM auth_bootstrap WHERE singleton FOR UPDATE").Scan(&completed); err != nil {
		return BootstrapToken{}, adminauth.ErrAuthUnavailable
	}
	if completed {
		return BootstrapToken{}, adminauth.ErrUnauthenticated
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_accounts)").Scan(&exists); err != nil {
		return BootstrapToken{}, adminauth.ErrAuthUnavailable
	}
	if exists {
		return BootstrapToken{}, adminauth.ErrUnauthenticated
	}
	raw, hash, err := adminauth.NewCSRFToken()
	if err != nil {
		return BootstrapToken{}, adminauth.ErrAuthUnavailable
	}
	_, err = tx.Exec(ctx, "UPDATE auth_bootstrap SET generation=generation+1,token_hash=$1,token_expires_at=clock_timestamp()+interval '15 minutes' WHERE singleton", hash[:])
	if err != nil {
		return BootstrapToken{}, adminauth.ErrAuthUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return BootstrapToken{}, adminauth.ErrAuthUnavailable
	}
	return BootstrapToken{raw: raw}, nil
}

type bootstrapSnapshot struct {
	generation int64
	policy     adminauth.AuthPolicy
}

func (s *Store) bootstrapSnapshot(ctx context.Context, hash [32]byte) (bootstrapSnapshot, error) {
	var b bootstrapSnapshot
	err := s.pool.QueryRow(ctx, "SELECT b.generation,p.mode,p.totp_enabled,p.version FROM auth_bootstrap b CROSS JOIN auth_policy p WHERE b.singleton AND p.singleton AND NOT b.completed AND b.token_hash=$1 AND b.token_expires_at>clock_timestamp() AND NOT EXISTS(SELECT 1 FROM auth_accounts)", hash[:]).Scan(&b.generation, &b.policy.Mode, &b.policy.TOTPEnabled, &b.policy.Version)
	if err != nil {
		return bootstrapSnapshot{}, loginDBError(err)
	}
	if b.policy.Validate() != nil {
		return bootstrapSnapshot{}, adminauth.ErrAuthUnavailable
	}
	return b, nil
}

// Bootstrap requires the actual local socket peer, never Forwarded headers or a
// client-provided address. A future adapter must also bind a loopback listener and
// enforce its Origin/CSRF boundary; calling this method alone is not HTTP security.
func (l *LoginService) Bootstrap(ctx context.Context, installToken, user, password string, peer netip.Addr) (LoginResult, error) {
	if !peer.IsValid() || peer.Zone() != "" || !peer.Unmap().IsLoopback() || !safeID.MatchString(user) || !adminauth.ValidLoginPassword(password) {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	hash, ok := tokenHash(installToken)
	if !ok {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	before, err := l.store.bootstrapSnapshot(ctx, hash)
	if err != nil {
		return LoginResult{}, err
	}
	if l.installation {
		if err = l.store.setupApplied(ctx); err != nil {
			return LoginResult{}, err
		}
	}
	// Argon2 never holds a database row lock. Invalid install tokens do no KDF work.
	passwordHash, err := l.passwords.Hash(ctx, password)
	if err != nil {
		return LoginResult{}, err
	}
	return l.finishBootstrap(ctx, hash, user, passwordHash, before)
}

func (l *LoginService) finishBootstrap(ctx context.Context, hash [32]byte, user string, password adminauth.PasswordHash, before bootstrapSnapshot) (LoginResult, error) {
	tx, err := authTx(ctx, l.store)
	if err != nil {
		return LoginResult{}, err
	}
	defer rollback(tx)
	// All provisioning writers must use policy -> bootstrap -> account ordering.
	// General user management is not exposed yet and must require completed=true.
	var policy adminauth.AuthPolicy
	err = tx.QueryRow(ctx, "SELECT mode,totp_enabled,version FROM auth_policy WHERE singleton FOR SHARE").Scan(&policy.Mode, &policy.TOTPEnabled, &policy.Version)
	if err != nil || policy.Validate() != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	if policy != before.policy {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	var completed bool
	if err = tx.QueryRow(ctx, "SELECT completed FROM auth_bootstrap WHERE singleton FOR UPDATE").Scan(&completed); err != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	if completed {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	// Check DB time after the lock wait, not the beginning of the transaction.
	var valid bool
	err = tx.QueryRow(ctx, "SELECT generation=$1 AND token_hash=$2 AND token_expires_at>clock_timestamp() AND NOT EXISTS(SELECT 1 FROM auth_accounts) FROM auth_bootstrap WHERE singleton AND token_hash IS NOT NULL", before.generation, hash[:]).Scan(&valid)
	if err != nil {
		return LoginResult{}, loginDBError(err)
	}
	if !valid {
		return LoginResult{}, adminauth.ErrUnauthenticated
	}
	if _, err = tx.Exec(ctx, "INSERT INTO auth_accounts (id,role) VALUES ($1,'admin')", user); err != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "INSERT INTO auth_password_credentials (user_id,version,password_hash) VALUES ($1,1,$2)", user, password.StorageValue()); err != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	result := LoginResult{}
	if policy.TOTPEnabled {
		result.enrollment, result.expiresAt, err = issueEnrollment(ctx, tx, user, policy.Version, 1)
	} else {
		result.grant, err = issuePasswordSession(ctx, tx, user, policy.Version, 1)
	}
	if err != nil {
		return LoginResult{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_bootstrap SET completed=true,token_hash=NULL,token_expires_at=NULL WHERE singleton"); err != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	if err = l.store.auditAuth(ctx, tx, user, "auth.bootstrap", "admin_created"); err != nil {
		return LoginResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LoginResult{}, adminauth.ErrAuthUnavailable
	}
	return result, nil
}

// Caller must hold the account row lock; at most one pending token per user.
func issueEnrollment(ctx context.Context, tx pgx.Tx, user string, policyVersion, userVersion uint64) (string, time.Time, error) {
	raw, hash, err := adminauth.NewCSRFToken()
	if err != nil {
		return "", time.Time{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "UPDATE auth_enrollment_challenges SET consumed=true,key_id=NULL,sealed_secret=NULL WHERE user_id=$1 AND NOT consumed", user); err != nil {
		return "", time.Time{}, adminauth.ErrAuthUnavailable
	}
	var expires time.Time
	err = tx.QueryRow(ctx, "INSERT INTO auth_enrollment_challenges (token_hash,user_id,policy_version,user_version,created_at,expires_at) SELECT $1,$2,$3,$4,t,t+interval '5 minutes' FROM (SELECT clock_timestamp() AS t) stamp RETURNING expires_at", hash[:], user, policyVersion, userVersion).Scan(&expires)
	if err != nil {
		return "", time.Time{}, adminauth.ErrAuthUnavailable
	}
	return raw, expires, nil
}

func issuePasswordSession(ctx context.Context, tx pgx.Tx, user string, policyVersion, userVersion uint64) (Grant, error) {
	session, sh, err := adminauth.NewCSRFToken()
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	csrf, ch, err := adminauth.NewCSRFToken()
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	_, err = tx.Exec(ctx, "INSERT INTO auth_sessions (token_hash,csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at) SELECT $1,$2,$3,$4,$5,false,t,t FROM (SELECT clock_timestamp() AS t) stamp", sh[:], ch[:], user, policyVersion, userVersion)
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	return Grant{session, csrf}, nil
}

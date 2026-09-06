// SPDX-License-Identifier: Apache-2.0
// Package pgstore implements server-side Backoffice authentication storage.
// It exposes no HTTP listener; bootstrap/login/enrollment wiring is separate.
package pgstore

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"waiting-room/internal/adminauth"
)

// Migration001 is for an explicit migration runner; New never applies DDL.
//
//go:embed migrations/001_auth.sql
var Migration001 string

type Store struct {
	pool  *pgxpool.Pool
	vault *Vault
}

func New(pool *pgxpool.Pool, vault *Vault) (*Store, error) {
	if pool == nil || vault == nil || len(vault.keys) == 0 {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &Store{pool, vault}, nil
}

// Grant exposes credentials only through explicit accessors after confirmed commit.
// A future HTTP adapter must set the secure cookie and same-origin CSRF response.
type Grant struct{ session, csrf string }

func (Grant) String() string               { return "[REDACTED_SESSION_GRANT]" }
func (Grant) GoString() string             { return "[REDACTED_SESSION_GRANT]" }
func (Grant) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_SESSION_GRANT]") }
func (g Grant) SessionToken() string       { return g.session }
func (g Grant) CSRFToken() string          { return g.csrf }

func tokenHash(raw string) ([32]byte, bool) {
	if len(raw) != 43 {
		return [32]byte{}, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(raw)), true
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return adminauth.ErrInvalidOrReplayed
	}
	return adminauth.ErrAuthUnavailable
}

// CompleteTOTP uses current database values, not caller-provided user/secret/time.
// Lock order for ALL future credential/policy writers:
// policy -> account -> credential -> challenge -> session.
// Policy is shared-locked; accounts serialize all challenges for one user.
func (s *Store) CompleteTOTP(ctx context.Context, challenge, code string) (Grant, error) {
	hash, ok := tokenHash(challenge)
	if !ok {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout = '4s'"); err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	if _, err = tx.Exec(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	var p adminauth.AuthPolicy
	err = tx.QueryRow(ctx, "SELECT mode, totp_enabled, version FROM auth_policy WHERE singleton FOR SHARE").Scan(&p.Mode, &p.TOTPEnabled, &p.Version)
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	if p.Validate() != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	// OFF-policy login is a separate password-only flow, not a TOTP bypass here.
	if !p.TOTPEnabled {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	var user string
	err = tx.QueryRow(ctx, "SELECT user_id FROM auth_totp_challenges WHERE token_hash=$1", hash[:]).Scan(&user)
	if err != nil {
		return Grant{}, dbError(err)
	}
	var a adminauth.Account
	err = tx.QueryRow(ctx, "SELECT id, role, enabled, session_version FROM auth_accounts WHERE id=$1 FOR UPDATE", user).Scan(&a.ID, &a.Role, &a.Enabled, &a.SessionVersion)
	if err != nil {
		return Grant{}, dbError(err)
	}
	if !a.Enabled || len(adminauth.Capabilities(a.Role)) == 0 {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	ref := adminauth.CredentialRef{UserID: user}
	var kid string
	var sealed []byte
	err = tx.QueryRow(ctx, "SELECT version, key_id, sealed_secret FROM auth_totp_credentials WHERE user_id=$1 FOR UPDATE", user).Scan(&ref.Version, &kid, &sealed)
	if err != nil {
		return Grant{}, dbError(err)
	}
	var cv, pv, uv uint64
	var attempts int
	var consumed bool
	var created, expires, now time.Time
	var lockedUser string
	err = tx.QueryRow(ctx, "SELECT user_id, credential_version, policy_version, user_version, attempts, consumed, created_at, expires_at FROM auth_totp_challenges WHERE token_hash=$1 FOR UPDATE", hash[:]).Scan(&lockedUser, &cv, &pv, &uv, &attempts, &consumed, &created, &expires)
	if err != nil {
		return Grant{}, dbError(err)
	}
	// Read clock AFTER all lock waits. Transaction-start now() can accept expired challenges.
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	if lockedUser != user || cv != ref.Version || pv != p.Version || uv != a.SessionVersion || consumed || attempts >= 5 || now.Before(created) || !now.Before(expires) {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	secret, err := s.vault.open(ref, kid, sealed)
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	session, sh, err := adminauth.NewCSRFToken()
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	csrf, ch, err := adminauth.NewCSRFToken()
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	consumer := &loginConsumer{tx: tx, ref: ref, challengeHash: hash, sessionHash: sh, csrfHash: ch, policy: p.Version, userVersion: a.SessionVersion}
	err = adminauth.VerifyAndConsume(ctx, ref, secret, code, now, consumer)
	if err == nil {
		return Grant{session, csrf}, nil
	}
	if !errors.Is(err, adminauth.ErrInvalidOrReplayed) {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	// Invalid or already-used codes consume an attempt, never a session.
	_, err = tx.Exec(ctx, "UPDATE auth_totp_challenges SET attempts=attempts+1, consumed=(attempts+1>=5) WHERE token_hash=$1", hash[:])
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	return Grant{}, adminauth.ErrInvalidOrReplayed
}

type loginConsumer struct {
	tx                                   pgx.Tx
	ref                                  adminauth.CredentialRef
	challengeHash, sessionHash, csrfHash [32]byte
	policy, userVersion                  uint64
}

func (c *loginConsumer) ConsumeCounter(ctx context.Context, ref adminauth.CredentialRef, counter uint64) (bool, error) {
	if ref != c.ref || counter > math.MaxInt64 {
		return false, nil
	}
	result, err := c.tx.Exec(ctx, "UPDATE auth_totp_credentials SET last_counter=$1 WHERE user_id=$2 AND version=$3 AND last_counter<$1", int64(counter), ref.UserID, ref.Version)
	if err != nil || result.RowsAffected() != 1 {
		return false, err
	}
	// Recheck expiry at the write, so decryption/verification cannot extend the TTL.
	result, err = c.tx.Exec(ctx, "UPDATE auth_totp_challenges SET consumed=true WHERE token_hash=$1 AND NOT consumed AND attempts<5 AND created_at<=clock_timestamp() AND expires_at>clock_timestamp()", c.challengeHash[:])
	if err != nil {
		return false, err
	}
	// A counter write already occurred. Any failure MUST abort the entire transaction.
	if result.RowsAffected() != 1 {
		return false, adminauth.ErrAuthUnavailable
	}
	_, err = c.tx.Exec(ctx, "INSERT INTO auth_sessions (token_hash,csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at) SELECT $1,$2,$3,$4,$5,true,t,t FROM (SELECT clock_timestamp() AS t) stamp", c.sessionHash[:], c.csrfHash[:], ref.UserID, c.policy, c.userVersion)
	if err != nil {
		return false, err
	}
	if err = c.tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

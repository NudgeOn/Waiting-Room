// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

//go:embed migrations/004_enrollment.sql
var Migration004 string

// EnrollmentService is Control-only; no HTTP endpoint or QR service is started.
type EnrollmentService struct {
	store            *Store
	passwords        *adminauth.PasswordHasher
	keyID            string
	policyEnrollment bool
}

func (EnrollmentService) String() string   { return "[REDACTED_ENROLLMENT_SERVICE]" }
func (EnrollmentService) GoString() string { return "[REDACTED_ENROLLMENT_SERVICE]" }
func (EnrollmentService) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_ENROLLMENT_SERVICE]")
}

func NewEnrollmentService(s *Store, h *adminauth.PasswordHasher, activeKeyID string, policyEnrollment ...bool) (*EnrollmentService, error) {
	if s == nil || h == nil || s.vault == nil || s.vault.keys[activeKeyID] == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	if len(policyEnrollment) > 1 {
		return nil, adminauth.ErrAuthUnavailable
	}
	enabled := len(policyEnrollment) == 1 && policyEnrollment[0]
	return &EnrollmentService{s, h, activeKeyID, enabled}, nil
}

type EnrollmentSetup struct {
	secret  adminauth.Secret
	expires time.Time
}

func (EnrollmentSetup) String() string   { return "[REDACTED_ENROLLMENT_SETUP]" }
func (EnrollmentSetup) GoString() string { return "[REDACTED_ENROLLMENT_SETUP]" }
func (EnrollmentSetup) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_ENROLLMENT_SETUP]")
}

// ManualKey is sensitive. Only render in a private same-origin enrollment UI.
func (s EnrollmentSetup) ManualKey() (string, error) { return s.secret.ProvisioningBase32() }
func (s EnrollmentSetup) ExpiresAt() time.Time       { return s.expires }

type EnrollmentGrant struct {
	grant    Grant
	recovery [10]string
}

func (EnrollmentGrant) String() string   { return "[REDACTED_ENROLLMENT_GRANT]" }
func (EnrollmentGrant) GoString() string { return "[REDACTED_ENROLLMENT_GRANT]" }
func (EnrollmentGrant) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_ENROLLMENT_GRANT]")
}
func (g EnrollmentGrant) SessionGrant() Grant { return g.grant }

// RecoveryCodes returns a copy, only after confirmed enrollment commit. Never log.
func (g EnrollmentGrant) RecoveryCodes() [10]string { return g.recovery }

type enrollmentState struct {
	ref          adminauth.CredentialRef
	policy       uint64
	attempts     int
	kid          string
	sealed       []byte
	expires, now time.Time
}

func (enrollmentState) String() string   { return "[REDACTED_ENROLLMENT_STATE]" }
func (enrollmentState) GoString() string { return "[REDACTED_ENROLLMENT_STATE]" }
func (enrollmentState) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_ENROLLMENT_STATE]")
}

// Lock policy -> account -> active credential -> enrollment. Callers hold these
// through their short mutation. Existing credential means this is NOT a reset flow.
func loadEnrollment(ctx context.Context, tx pgx.Tx, hash [32]byte, policyEnrollment bool) (enrollmentState, error) {
	var out enrollmentState
	var p adminauth.AuthPolicy
	err := tx.QueryRow(ctx, "SELECT mode,totp_enabled,version FROM auth_policy WHERE singleton FOR SHARE").Scan(&p.Mode, &p.TOTPEnabled, &p.Version)
	if err != nil || p.Validate() != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	if !p.TOTPEnabled {
		var permitted bool
		if !policyEnrollment {
			return out, adminauth.ErrInvalidOrReplayed
		}
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_policy_enrollments e JOIN auth_sessions s ON s.token_hash=e.session_hash JOIN auth_accounts a ON a.id=s.user_id JOIN auth_enrollment_challenges c ON c.token_hash=e.token_hash AND c.user_id=a.id WHERE e.token_hash=$1 AND a.role='admin' AND a.enabled AND s.policy_version=$2 AND s.user_version=a.session_version AND s.created_at<=clock_timestamp() AND s.last_seen_at>=s.created_at AND s.last_seen_at<=clock_timestamp() AND s.created_at>clock_timestamp()-make_interval(secs=>$3) AND s.last_seen_at>clock_timestamp()-make_interval(secs=>$4))`, hash[:], p.Version, adminauth.SessionAbsoluteTTL.Seconds(), adminauth.SessionIdleTTL.Seconds()).Scan(&permitted) != nil {
			return out, adminauth.ErrAuthUnavailable
		}
		if !permitted {
			return out, adminauth.ErrInvalidOrReplayed
		}
	}
	var user string
	if err = tx.QueryRow(ctx, "SELECT user_id FROM auth_enrollment_challenges WHERE token_hash=$1", hash[:]).Scan(&user); err != nil {
		return out, dbError(err)
	}
	var account adminauth.Account
	err = tx.QueryRow(ctx, "SELECT id,role,enabled,session_version FROM auth_accounts WHERE id=$1 FOR UPDATE", user).Scan(&account.ID, &account.Role, &account.Enabled, &account.SessionVersion)
	if err != nil {
		return out, dbError(err)
	}
	if !account.Enabled || len(adminauth.Capabilities(account.Role)) == 0 {
		return out, adminauth.ErrInvalidOrReplayed
	}
	var existing uint64
	err = tx.QueryRow(ctx, "SELECT version FROM auth_totp_credentials WHERE user_id=$1 FOR UPDATE", user).Scan(&existing)
	if err == nil {
		return out, adminauth.ErrInvalidOrReplayed
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, adminauth.ErrAuthUnavailable
	}
	var lockedUser string
	var uv, pv uint64
	var created time.Time
	var consumed bool
	err = tx.QueryRow(ctx, "SELECT user_id,user_version,policy_version,attempts,consumed,created_at,expires_at,COALESCE(key_id,''),sealed_secret FROM auth_enrollment_challenges WHERE token_hash=$1 FOR UPDATE", hash[:]).Scan(&lockedUser, &uv, &pv, &out.attempts, &consumed, &created, &out.expires, &out.kid, &out.sealed)
	if err != nil {
		return out, dbError(err)
	}
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&out.now); err != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	if user != lockedUser || uv != account.SessionVersion || pv != p.Version || consumed || out.now.Before(created) || !out.now.Before(out.expires) {
		return out, adminauth.ErrInvalidOrReplayed
	}
	// Initial registration only. Future resets must retain monotonic version history.
	out.ref = adminauth.CredentialRef{UserID: user, Version: uv}
	out.policy = p.Version
	return out, nil
}

// Begin prepares a stable encrypted pending secret, without issuing a session.
// Repeated calls on the same live token neither rotate the secret nor extend TTL.
func (e *EnrollmentService) Begin(ctx context.Context, token string) (EnrollmentSetup, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return EnrollmentSetup{}, adminauth.ErrInvalidOrReplayed
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := authTx(ctx, e.store)
	if err != nil {
		return EnrollmentSetup{}, err
	}
	defer rollback(tx)
	state, err := loadEnrollment(ctx, tx, hash, e.policyEnrollment)
	if err != nil {
		return EnrollmentSetup{}, err
	}
	if state.attempts >= 5 {
		return EnrollmentSetup{}, adminauth.ErrInvalidOrReplayed
	}
	var secret adminauth.Secret
	if state.kid == "" {
		secret, err = adminauth.NewSecret()
		if err != nil {
			return EnrollmentSetup{}, adminauth.ErrAuthUnavailable
		}
		sealed, err := e.store.vault.sealEnrollment(state.ref, e.keyID, hash, secret)
		if err != nil {
			return EnrollmentSetup{}, err
		}
		tag, err := tx.Exec(ctx, "UPDATE auth_enrollment_challenges SET key_id=$2,sealed_secret=$3 WHERE token_hash=$1 AND expires_at>clock_timestamp()", hash[:], e.keyID, sealed)
		if err != nil {
			return EnrollmentSetup{}, adminauth.ErrAuthUnavailable
		}
		if tag.RowsAffected() != 1 {
			return EnrollmentSetup{}, adminauth.ErrInvalidOrReplayed
		}
	} else {
		secret, err = e.store.vault.openEnrollment(state.ref, state.kid, hash, state.sealed)
		if err != nil {
			return EnrollmentSetup{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return EnrollmentSetup{}, adminauth.ErrAuthUnavailable
	}
	return EnrollmentSetup{secret, state.expires}, nil
}

type enrollmentAttempt struct {
	hash   [32]byte
	ref    adminauth.CredentialRef
	policy uint64
	number int
}

func (e *EnrollmentService) reserve(ctx context.Context, hash [32]byte) (enrollmentAttempt, error) {
	tx, err := authTx(ctx, e.store)
	if err != nil {
		return enrollmentAttempt{}, err
	}
	defer rollback(tx)
	state, err := loadEnrollment(ctx, tx, hash, e.policyEnrollment)
	if err != nil {
		return enrollmentAttempt{}, err
	}
	if state.kid == "" || state.attempts >= 5 {
		return enrollmentAttempt{}, adminauth.ErrInvalidOrReplayed
	}
	tag, err := tx.Exec(ctx, "UPDATE auth_enrollment_challenges SET attempts=attempts+1 WHERE token_hash=$1 AND expires_at>clock_timestamp()", hash[:])
	if err != nil {
		return enrollmentAttempt{}, adminauth.ErrAuthUnavailable
	}
	if tag.RowsAffected() != 1 {
		return enrollmentAttempt{}, adminauth.ErrInvalidOrReplayed
	}
	if err = tx.Commit(ctx); err != nil {
		return enrollmentAttempt{}, adminauth.ErrAuthUnavailable
	}
	return enrollmentAttempt{hash, state.ref, state.policy, state.attempts + 1}, nil
}

type recoveryBundle struct {
	codes  [10]string
	hashes [10]adminauth.PasswordHash
}

func (recoveryBundle) String() string   { return "[REDACTED_RECOVERY_BUNDLE]" }
func (recoveryBundle) GoString() string { return "[REDACTED_RECOVERY_BUNDLE]" }
func (recoveryBundle) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_RECOVERY_BUNDLE]")
}
func (e *EnrollmentService) prepareRecovery(ctx context.Context) (recoveryBundle, error) {
	var out recoveryBundle
	for i := range out.codes {
		raw, _, err := adminauth.NewCSRFToken()
		if err != nil {
			return recoveryBundle{}, adminauth.ErrAuthUnavailable
		}
		out.codes[i] = raw
		out.hashes[i], err = e.passwords.Hash(ctx, "wr-recovery-v1:"+raw)
		if err != nil {
			return recoveryBundle{}, err
		}
	}
	return out, nil
}
func sixDigits(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Complete reserves one of five attempts BEFORE expensive work, including failed
// requests. No refunds on failure/cancellation. Hashes run outside all DB locks.
func (e *EnrollmentService) Complete(ctx context.Context, token, code string) (EnrollmentGrant, error) {
	hash, ok := tokenHash(token)
	if !ok {
		return EnrollmentGrant{}, adminauth.ErrInvalidOrReplayed
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	attempt, err := e.reserve(ctx, hash)
	if err != nil {
		return EnrollmentGrant{}, err
	}
	if !sixDigits(code) {
		return EnrollmentGrant{}, adminauth.ErrInvalidOrReplayed
	}
	recovery, err := e.prepareRecovery(ctx)
	if err != nil {
		return EnrollmentGrant{}, err
	}
	return e.finish(ctx, attempt, code, recovery)
}
func (e *EnrollmentService) finish(ctx context.Context, attempt enrollmentAttempt, code string, recovery recoveryBundle) (EnrollmentGrant, error) {
	tx, err := authTx(ctx, e.store)
	if err != nil {
		return EnrollmentGrant{}, err
	}
	defer rollback(tx)
	state, err := loadEnrollment(ctx, tx, attempt.hash, e.policyEnrollment)
	if err != nil {
		return EnrollmentGrant{}, err
	}
	if state.ref != attempt.ref || state.policy != attempt.policy || attempt.number < 1 || attempt.number > state.attempts || state.kid == "" {
		return EnrollmentGrant{}, adminauth.ErrInvalidOrReplayed
	}
	secret, err := e.store.vault.openEnrollment(state.ref, state.kid, attempt.hash, state.sealed)
	if err != nil {
		return EnrollmentGrant{}, err
	}
	// Pending ciphertext is bound to its token; active credential uses a distinct AAD.
	sealed, err := e.store.vault.Seal(state.ref, e.keyID, secret)
	if err != nil {
		return EnrollmentGrant{}, err
	}
	session, sh, err := adminauth.NewCSRFToken()
	if err != nil {
		return EnrollmentGrant{}, adminauth.ErrAuthUnavailable
	}
	csrf, ch, err := adminauth.NewCSRFToken()
	if err != nil {
		return EnrollmentGrant{}, adminauth.ErrAuthUnavailable
	}
	consumer := &enrollmentConsumer{tx: tx, store: e.store, attempt: attempt, keyID: e.keyID, sealed: sealed, recovery: recovery, sessionHash: sh, csrfHash: ch}
	if err = adminauth.VerifyAndConsume(ctx, state.ref, secret, code, state.now, consumer); err != nil {
		return EnrollmentGrant{}, err
	}
	return EnrollmentGrant{Grant{session, csrf}, recovery.codes}, nil
}

// Initial credential insertion is the counter transition (-1 -> accepted counter).
// It commits together with token consumption, recovery hashes and the MFA session.
type enrollmentConsumer struct {
	tx                    pgx.Tx
	store                 *Store
	attempt               enrollmentAttempt
	keyID                 string
	sealed                []byte
	recovery              recoveryBundle
	sessionHash, csrfHash [32]byte
}

func (c *enrollmentConsumer) ConsumeCounter(ctx context.Context, ref adminauth.CredentialRef, counter uint64) (bool, error) {
	if ref != c.attempt.ref || counter > math.MaxInt64 {
		return false, nil
	}
	tag, err := c.tx.Exec(ctx, "UPDATE auth_enrollment_challenges SET consumed=true,key_id=NULL,sealed_secret=NULL WHERE token_hash=$1 AND NOT consumed AND attempts BETWEEN $2 AND 5 AND created_at<=clock_timestamp() AND expires_at>clock_timestamp()", c.attempt.hash[:], c.attempt.number)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, adminauth.ErrAuthUnavailable
	}
	_, err = c.tx.Exec(ctx, "INSERT INTO auth_totp_credentials (user_id,version,key_id,sealed_secret,last_counter) VALUES ($1,$2,$3,$4,$5)", ref.UserID, ref.Version, c.keyID, c.sealed, int64(counter))
	if err != nil {
		return false, err
	}
	for i, hash := range c.recovery.hashes {
		_, err = c.tx.Exec(ctx, "INSERT INTO auth_recovery_codes (user_id,credential_version,slot,code_hash) VALUES ($1,$2,$3,$4)", ref.UserID, ref.Version, i+1, hash.StorageValue())
		if err != nil {
			return false, err
		}
	}
	// Revoke any pre-registration sessions/challenges before minting the new session.
	if _, err = c.tx.Exec(ctx, "DELETE FROM auth_sessions WHERE user_id=$1", ref.UserID); err != nil {
		return false, err
	}
	if _, err = c.tx.Exec(ctx, "UPDATE auth_totp_challenges SET consumed=true WHERE user_id=$1 AND NOT consumed", ref.UserID); err != nil {
		return false, err
	}
	if _, err = c.tx.Exec(ctx, "UPDATE auth_enrollment_challenges SET consumed=true,key_id=NULL,sealed_secret=NULL WHERE user_id=$1", ref.UserID); err != nil {
		return false, err
	}
	_, err = c.tx.Exec(ctx, "INSERT INTO auth_sessions (token_hash,csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at) SELECT $1,$2,$3,$4,$5,true,t,t FROM (SELECT clock_timestamp() AS t) stamp", c.sessionHash[:], c.csrfHash[:], ref.UserID, c.attempt.policy, ref.Version)
	if err != nil {
		return false, err
	}
	if err = c.store.auditAuth(ctx, c.tx, ref.UserID, "auth.totp.enroll", "enrolled"); err != nil {
		return false, err
	}
	if err = c.tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

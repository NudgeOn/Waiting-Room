// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
)

// RecoveryService completes a password-issued TOTP login challenge using one
// backup code. It is not password reset, TOTP reset, reauthentication or HTTP.
type RecoveryService struct {
	store     *Store
	passwords *adminauth.PasswordHasher
}

func (RecoveryService) String() string   { return "[REDACTED_RECOVERY_SERVICE]" }
func (RecoveryService) GoString() string { return "[REDACTED_RECOVERY_SERVICE]" }
func (RecoveryService) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_RECOVERY_SERVICE]")
}
func NewRecoveryService(s *Store, h *adminauth.PasswordHasher) (*RecoveryService, error) {
	if s == nil || h == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &RecoveryService{s, h}, nil
}

type recoveryState struct {
	challenge           [32]byte
	ref                 adminauth.CredentialRef
	role                adminauth.Role
	policy, userVersion uint64
	attempts            int
}
type recoveryRecord struct {
	encoded  string
	consumed bool
}
type recoveryAttempt struct {
	state   recoveryState
	records [10]recoveryRecord
	number  int
}

func (recoveryAttempt) String() string   { return "[REDACTED_RECOVERY_ATTEMPT]" }
func (recoveryAttempt) GoString() string { return "[REDACTED_RECOVERY_ATTEMPT]" }
func (recoveryAttempt) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED_RECOVERY_ATTEMPT]")
}

// Shared order with ordinary TOTP login: policy -> account -> credential ->
// challenge -> recovery rows -> session. Clock is read after all challenge locks.
func loadRecovery(ctx context.Context, tx pgx.Tx, hash [32]byte) (recoveryState, error) {
	out := recoveryState{challenge: hash}
	var policy adminauth.AuthPolicy
	err := tx.QueryRow(ctx, "SELECT mode,totp_enabled,version FROM auth_policy WHERE singleton FOR SHARE").Scan(&policy.Mode, &policy.TOTPEnabled, &policy.Version)
	if err != nil || policy.Validate() != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	if !policy.TOTPEnabled {
		return out, adminauth.ErrInvalidOrReplayed
	}
	var user string
	if err = tx.QueryRow(ctx, "SELECT user_id FROM auth_totp_challenges WHERE token_hash=$1", hash[:]).Scan(&user); err != nil {
		return out, dbError(err)
	}
	var enabled bool
	err = tx.QueryRow(ctx, "SELECT role,enabled,session_version FROM auth_accounts WHERE id=$1 FOR UPDATE", user).Scan(&out.role, &enabled, &out.userVersion)
	if err != nil {
		return out, dbError(err)
	}
	if !enabled || len(adminauth.Capabilities(out.role)) == 0 {
		return out, adminauth.ErrInvalidOrReplayed
	}
	out.ref.UserID = user
	if err = tx.QueryRow(ctx, "SELECT version FROM auth_totp_credentials WHERE user_id=$1 FOR UPDATE", user).Scan(&out.ref.Version); err != nil {
		return out, dbError(err)
	}
	var lockedUser string
	var cv, pv, uv uint64
	var consumed bool
	var created, expires, now time.Time
	err = tx.QueryRow(ctx, "SELECT user_id,credential_version,policy_version,user_version,attempts,consumed,created_at,expires_at FROM auth_totp_challenges WHERE token_hash=$1 FOR UPDATE", hash[:]).Scan(&lockedUser, &cv, &pv, &uv, &out.attempts, &consumed, &created, &expires)
	if err != nil {
		return out, dbError(err)
	}
	if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	if lockedUser != user || cv != out.ref.Version || pv != policy.Version || uv != out.userVersion || consumed || now.Before(created) || !now.Before(expires) {
		return out, adminauth.ErrInvalidOrReplayed
	}
	out.policy = policy.Version
	return out, nil
}

// Commit the shared challenge reservation before Argon2. Missing/consumed codes
// do not bypass the budget. No refunds after failures, cancellation or lost commits.
func (r *RecoveryService) reserve(ctx context.Context, hash [32]byte) (recoveryAttempt, error) {
	var out recoveryAttempt
	tx, err := authTx(ctx, r.store)
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	state, err := loadRecovery(ctx, tx, hash)
	if err != nil {
		return out, err
	}
	if state.attempts >= 5 {
		return out, adminauth.ErrInvalidOrReplayed
	}
	rows, err := tx.Query(ctx, "SELECT slot,code_hash,consumed FROM auth_recovery_codes WHERE user_id=$1 AND credential_version=$2 ORDER BY slot FOR SHARE", state.ref.UserID, state.ref.Version)
	if err != nil {
		return out, adminauth.ErrAuthUnavailable
	}
	count := 0
	for rows.Next() {
		var slot int
		var record recoveryRecord
		if err = rows.Scan(&slot, &record.encoded, &record.consumed); err != nil || count >= 10 || slot != count+1 {
			rows.Close()
			return recoveryAttempt{}, adminauth.ErrAuthUnavailable
		}
		out.records[count] = record
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 10 {
		return recoveryAttempt{}, adminauth.ErrAuthUnavailable
	}
	tag, err := tx.Exec(ctx, "UPDATE auth_totp_challenges SET attempts=attempts+1 WHERE token_hash=$1 AND NOT consumed AND attempts<5 AND expires_at>clock_timestamp()", hash[:])
	if err != nil {
		return recoveryAttempt{}, adminauth.ErrAuthUnavailable
	}
	if tag.RowsAffected() != 1 {
		return recoveryAttempt{}, adminauth.ErrInvalidOrReplayed
	}
	if err = tx.Commit(ctx); err != nil {
		return recoveryAttempt{}, adminauth.ErrAuthUnavailable
	}
	out.state = state
	out.number = state.attempts + 1
	return out, nil
}

// Verify all ten slots (including consumed slots), with no early success exit.
// Fixed work avoids leaking the matching slot/remaining count via loop length;
// this is not a claim of full HTTP timing indistinguishability.
func (r *RecoveryService) match(ctx context.Context, attempt recoveryAttempt, raw string) (int, error) {
	if _, ok := tokenHash(raw); !ok {
		return 0, adminauth.ErrInvalidOrReplayed
	}
	slot := 0
	for i, record := range attempt.records {
		valid, err := r.passwords.Verify(ctx, "wr-recovery-v1:"+raw, record.encoded)
		if err != nil {
			return 0, err
		}
		if valid && !record.consumed {
			if slot != 0 {
				return 0, adminauth.ErrAuthUnavailable
			}
			slot = i + 1
		}
	}
	if slot == 0 {
		return 0, adminauth.ErrInvalidOrReplayed
	}
	return slot, nil
}

func (r *RecoveryService) Complete(ctx context.Context, challenge, code string) (Grant, error) {
	hash, ok := tokenHash(challenge)
	if !ok {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	attempt, err := r.reserve(ctx, hash)
	if err != nil {
		return Grant{}, err
	}
	slot, err := r.match(ctx, attempt, code)
	if err != nil {
		return Grant{}, err
	}
	return r.finish(ctx, attempt, slot)
}
func (r *RecoveryService) finish(ctx context.Context, attempt recoveryAttempt, slot int) (Grant, error) {
	if slot < 1 || slot > 10 || attempt.number < 1 || attempt.number > 5 || attempt.records[slot-1].consumed {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	tx, err := authTx(ctx, r.store)
	if err != nil {
		return Grant{}, err
	}
	defer rollback(tx)
	state, err := loadRecovery(ctx, tx, attempt.state.challenge)
	if err != nil {
		return Grant{}, err
	}
	if state.ref != attempt.state.ref || state.role != attempt.state.role || state.policy != attempt.state.policy || state.userVersion != attempt.state.userVersion || state.attempts < attempt.number {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	var encoded string
	var consumed bool
	err = tx.QueryRow(ctx, "SELECT code_hash,consumed FROM auth_recovery_codes WHERE user_id=$1 AND credential_version=$2 AND slot=$3 FOR UPDATE", state.ref.UserID, state.ref.Version, slot).Scan(&encoded, &consumed)
	if err != nil {
		return Grant{}, dbError(err)
	}
	if consumed || encoded != attempt.records[slot-1].encoded {
		return Grant{}, adminauth.ErrInvalidOrReplayed
	}
	session, sh, err := adminauth.NewCSRFToken()
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	csrf, ch, err := adminauth.NewCSRFToken()
	if err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	if err = commitRecovery(ctx, tx, attempt, slot, sh, ch); err != nil {
		return Grant{}, adminauth.ErrAuthUnavailable
	}
	return Grant{session, csrf}, nil
}

// Caller holds current policy/account/credential/challenge/recovery locks. Any
// partial write or uncertain commit MUST fail closed and be rolled back by caller.
func commitRecovery(ctx context.Context, tx pgx.Tx, attempt recoveryAttempt, slot int, session, csrf [32]byte) error {
	state := attempt.state
	tag, err := tx.Exec(ctx, "UPDATE auth_recovery_codes SET consumed=true WHERE user_id=$1 AND credential_version=$2 AND slot=$3 AND NOT consumed AND code_hash=$4", state.ref.UserID, state.ref.Version, slot, attempt.records[slot-1].encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return adminauth.ErrAuthUnavailable
	}
	tag, err = tx.Exec(ctx, "UPDATE auth_totp_challenges SET consumed=true WHERE token_hash=$1 AND NOT consumed AND attempts BETWEEN $2 AND 5 AND created_at<=clock_timestamp() AND expires_at>clock_timestamp()", state.challenge[:], attempt.number)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return adminauth.ErrAuthUnavailable
	}
	_, err = tx.Exec(ctx, "INSERT INTO auth_sessions (token_hash,csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at) SELECT $1,$2,$3,$4,$5,true,t,t FROM (SELECT clock_timestamp() AS t) stamp", session[:], csrf[:], state.ref.UserID, state.policy, state.userVersion)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

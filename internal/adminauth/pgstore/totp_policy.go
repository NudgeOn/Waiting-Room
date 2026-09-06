// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

//go:embed migrations/009_policy_enrollment.sql
var Migration009 string

type PolicyView struct {
	Mode     adminauth.PolicyMode `json:"mode"`
	Enabled  bool                 `json:"enabled"`
	Version  uint64               `json:"version"`
	Enrolled bool                 `json:"enrolled"`
}

func policyETag(v uint64) string { return fmt.Sprintf(`"policy-%d"`, v) }
func policyView(state sessionSnapshot) PolicyView {
	return PolicyView{state.policy.Mode, state.policy.TOTPEnabled, state.policy.Version, state.account.TOTPEnrolled}
}
func (s *SecurityService) Policy(ctx context.Context, token string) (ControlReply, error) {
	tx, state, err := s.control.begin(ctx, token, adminauth.ReadSecurity, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, policyView(state), policyETag(state.policy.Version)), nil
}

// Cached enrollment proofs are encrypted with a domain-separated Control-only
// key. Neither database response cache nor audit contains plaintext bearer proof.
func (s *SecurityService) proofCipher() (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, s.login.fingerprintKey[:])
	_, _ = mac.Write([]byte("wr/security/command-proof/v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (s *SecurityService) sealProof(body []byte, aad string) ([]byte, error) {
	aead, err := s.proofCipher()
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	sealed := aead.Seal(nonce, nonce, body, []byte(aad))
	return json.Marshal(map[string]string{"sealed": base64.RawURLEncoding.EncodeToString(sealed)})
}
func (s *SecurityService) openProof(body []byte, aad string) ([]byte, error) {
	var encoded struct {
		Sealed string `json:"sealed"`
	}
	if json.Unmarshal(body, &encoded) != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded.Sealed)
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	aead, err := s.proofCipher()
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, adminauth.ErrAuthUnavailable
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(aad))
	if err != nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return plain, nil
}
func (s *SecurityService) BeginPolicyEnrollment(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	if r == nil || r.Method != "POST" || control.DecodeExact(raw, &struct{}{}) != nil {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	// Exact request + session bind the encrypted cache, beyond the generic actor
	// and idempotency-key binding. Another session must begin its own enrollment.
	aad := "policy-enrollment/v1:" + digest([]byte(token)) + ":" + digest([]byte(r.Header.Get("Idempotency-Key")))
	out, err := s.control.command(ctx, token, r, raw, adminauth.WriteTOTPPolicy, "totp-enrollment", true, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, _ control.Config) (commandResult, error) {
		if r.Header.Get("If-Match") != policyETag(state.policy.Version) {
			return commandResult{reply: replyProblem(412, "REVISION_MISMATCH")}, nil
		}
		if state.policy.TOTPEnabled || state.account.TOTPEnrolled {
			return commandResult{reply: replyProblem(409, "TOTP_ALREADY_ENROLLED_OR_ENABLED")}, nil
		}
		if _, err := tx.Exec(ctx, "DELETE FROM auth_enrollment_challenges WHERE user_id=$1", state.account.ID); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		proof, hash, err := adminauth.NewCSRFToken()
		if err != nil {
			return commandResult{}, err
		}
		expires := state.now.Add(5 * time.Minute)
		if _, err = tx.Exec(ctx, "INSERT INTO auth_enrollment_challenges(token_hash,user_id,policy_version,user_version,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6)", hash[:], state.account.ID, state.policy.Version, state.account.SessionVersion, state.now, expires); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		if _, err = tx.Exec(ctx, "INSERT INTO auth_policy_enrollments VALUES($1,$2)", hash[:], mustSessionHash(token)); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		reply := jsonReply(200, map[string]any{"state": "enrollment_required", "challengeToken": proof, "expiresAt": expires, "policyEnrollment": true}, policyETag(state.policy.Version))
		reply.Body, err = s.sealProof(reply.Body, aad)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{reply: reply}, nil
	})
	if err == nil && out.Status == 200 {
		out.Body, err = s.openProof(out.Body, aad)
	}
	return out, err
}

// Policy enrollment uses its own authenticated endpoints. Pre-auth login APIs
// continue rejecting cookies; there is no relaxed mixed-credential auth mode.
func (s *SecurityService) PolicyEnrollment(ctx context.Context, token string, r *http.Request, raw []byte, verify bool) (ControlReply, error) {
	var in struct {
		Challenge string `json:"challengeToken"`
		Code      string `json:"code,omitempty"`
	}
	if r == nil || r.Method != "POST" || control.DecodeExact(raw, &in) != nil || (!verify && in.Code != "") {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	hash, ok := tokenHash(in.Challenge)
	if !ok {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	tx, _, err := s.control.begin(ctx, token, adminauth.ReadSecurity, r)
	if err != nil {
		return ControlReply{}, err
	}
	var bound bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_policy_enrollments WHERE token_hash=$1 AND session_hash=$2)", hash[:], mustSessionHash(token)).Scan(&bound)
	if err != nil || !bound {
		rollback(tx)
		return ControlReply{}, adminauth.ErrForbidden
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	e, err := NewEnrollmentService(s.control.store, s.login.passwords, s.keyID, true)
	if err != nil {
		return ControlReply{}, err
	}
	if !verify {
		setup, err := e.Begin(ctx, in.Challenge)
		if err != nil {
			return ControlReply{}, err
		}
		key, err := setup.ManualKey()
		if err != nil {
			return ControlReply{}, err
		}
		return jsonReply(200, map[string]any{"secret": key, "expiresAt": setup.ExpiresAt()}, ""), nil
	}
	result, err := e.Complete(ctx, in.Challenge, in.Code)
	if err != nil {
		return ControlReply{}, err
	}
	grant := result.SessionGrant()
	tx, err = authTx(ctx, s.control.store)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	sessionHash, _ := tokenHash(grant.SessionToken())
	state, err := loadSession(ctx, tx, sessionHash, false)
	if err != nil {
		return ControlReply{}, err
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	out := jsonReply(200, map[string]any{"state": "authenticated", "session": state.view(), "csrfToken": grant.CSRFToken(), "recoveryCodes": result.RecoveryCodes()}, "")
	out.RotatedGrant = grant
	return out, nil
}

func (s *SecurityService) ChangePolicy(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if r == nil || r.Method != "PUT" || control.DecodeExact(raw, &in) != nil || in.Enabled == nil {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	var fresh Grant
	out, err := s.control.command(ctx, token, r, raw, adminauth.WriteTOTPPolicy, "totp", true, func(ctx context.Context, tx pgx.Tx, state sessionSnapshot, _ control.Config) (commandResult, error) {
		if r.Header.Get("If-Match") != policyETag(state.policy.Version) {
			return commandResult{reply: replyProblem(412, "REVISION_MISMATCH")}, nil
		}
		if state.policy.Mode == adminauth.ForcedOn && !*in.Enabled {
			return commandResult{reply: replyProblem(403, "TOTP_POLICY_LOCKED")}, nil
		}
		if *in.Enabled == state.policy.TOTPEnabled {
			return commandResult{reply: jsonReply(200, policyView(state), policyETag(state.policy.Version))}, nil
		}
		if !state.account.TOTPEnrolled {
			return commandResult{reply: replyProblem(409, "TOTP_ENROLLMENT_REQUIRED")}, nil
		}
		if state.policy.Version >= 9007199254740990 {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		before, _ := json.Marshal(policyView(state))
		state.policy.Version++
		state.policy.TOTPEnabled = *in.Enabled
		if _, err := tx.Exec(ctx, "UPDATE auth_policy SET totp_enabled=$1,version=$2 WHERE singleton", *in.Enabled, state.policy.Version); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		for _, table := range []string{"auth_sessions", "auth_totp_challenges", "auth_enrollment_challenges"} {
			if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil {
				return commandResult{}, adminauth.ErrAuthUnavailable
			}
		}
		var err error
		fresh, err = issuePasswordSession(ctx, tx, state.account.ID, state.policy.Version, state.account.SessionVersion)
		if err != nil {
			return commandResult{}, err
		}
		// The consumed reauth proof included current TOTP even for OFF -> ON.
		if _, err = tx.Exec(ctx, "UPDATE auth_sessions SET mfa_verified=true WHERE token_hash=$1", mustSessionHash(fresh.SessionToken())); err != nil {
			return commandResult{}, adminauth.ErrAuthUnavailable
		}
		after, _ := json.Marshal(policyView(state))
		return commandResult{reply: jsonReply(200, policyView(state), policyETag(state.policy.Version)), before: digest(before), after: digest(after), revision: int64(state.policy.Version)}, nil
	})
	// Tokens never enter durable response JSON. Set-Cookie and the CSRF header
	// are issued only after a confirmed new commit, never on cached replay. If
	// the HTTP response is lost, fresh login + policy GET resolves the outcome.
	if err == nil && !out.Replay && out.Status == 200 {
		out.RotatedGrant = fresh
	}
	return out, err
}

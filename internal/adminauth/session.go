// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"errors"
	"time"
)

var (
	ErrUnauthenticated          = errors.New("UNAUTHENTICATED")
	ErrForbidden                = errors.New("FORBIDDEN")
	ErrReauthenticationRequired = errors.New("REAUTHENTICATION_REQUIRED")
	ErrPolicyLocked             = errors.New("TOTP_POLICY_LOCKED")
	ErrInvalidPolicy            = errors.New("INVALID_AUTH_POLICY")
)

type PolicyMode string

const (
	Configurable       PolicyMode = "configurable"
	ForcedOn           PolicyMode = "forced_on"
	SessionIdleTTL                = 30 * time.Minute
	SessionAbsoluteTTL            = 8 * time.Hour
)

type AuthPolicy struct {
	Mode        PolicyMode
	TOTPEnabled bool
	Version     uint64
}

func DefaultPolicy() AuthPolicy { return AuthPolicy{Mode: Configurable, TOTPEnabled: true, Version: 1} }
func (p AuthPolicy) Validate() error {
	if p.Version == 0 || (p.Mode != Configurable && p.Mode != ForcedOn) {
		return ErrInvalidPolicy
	}
	if p.Mode == ForcedOn && !p.TOTPEnabled {
		return ErrPolicyLocked
	}
	return nil
}

type Account struct {
	ID             string
	Role           Role
	Enabled        bool
	SessionVersion uint64
	TOTPEnrolled   bool
}
type Session struct {
	UserID             string
	AuthPolicyVersion  uint64
	UserSessionVersion uint64
	MFAVerified        bool
	CreatedAt          time.Time
	LastSeenAt         time.Time
}

// CheckSession reads trusted current DB values, never client-supplied claims.
// Revocations and policy changes must commit atomically in the future store.
// Reads do not refresh LastSeenAt. An explicitly configured TOTP=false is honored.
func CheckSession(account Account, session Session, policy AuthPolicy, now time.Time) error {
	if policy.Validate() != nil || account.ID == "" || !account.Enabled || roleBit(account.Role) == 0 ||
		account.SessionVersion == 0 || session.UserID != account.ID ||
		session.AuthPolicyVersion != policy.Version || session.UserSessionVersion != account.SessionVersion ||
		session.CreatedAt.IsZero() || session.LastSeenAt.Before(session.CreatedAt) || now.Before(session.LastSeenAt) ||
		!now.Before(session.CreatedAt.Add(SessionAbsoluteTTL)) || !now.Before(session.LastSeenAt.Add(SessionIdleTTL)) {
		return ErrUnauthenticated
	}
	if policy.TOTPEnabled && (!account.TOTPEnrolled || !session.MFAVerified) {
		return ErrUnauthenticated
	}
	return nil
}

// Dangerous actions remain refused until a separate transaction consumes an
// action-bound one-time reauth proof. Login MFA alone never authorizes these actions.
func CheckOrdinaryAction(account Account, session Session, policy AuthPolicy, action Action, now time.Time) error {
	if err := CheckSession(account, session, policy, now); err != nil {
		return err
	}
	capability, ok := Requirement(account.Role, action)
	if !ok {
		return ErrForbidden
	}
	if capability.RequiresReauthentication {
		return ErrReauthenticationRequired
	}
	return nil
}

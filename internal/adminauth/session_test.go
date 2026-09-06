// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"errors"
	"testing"
	"time"
)

func sessionFixture() (Account, Session, AuthPolicy, time.Time) {
	now := time.Unix(1800000000, 0)
	return Account{ID: "admin-one", Role: Admin, Enabled: true, SessionVersion: 1, TOTPEnrolled: true},
		Session{UserID: "admin-one", AuthPolicyVersion: 1, UserSessionVersion: 1, MFAVerified: true, CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute)},
		DefaultPolicy(), now
}

func TestAuthPolicyDefaultAndForcedOn(t *testing.T) {
	p := DefaultPolicy()
	if p.Validate() != nil || !p.TOTPEnabled || p.Mode != Configurable || p.Version != 1 {
		t.Fatal("unsafe default")
	}
	p.TOTPEnabled = false
	if err := p.Validate(); err != nil {
		t.Fatal("explicit configurable OFF rejected", err)
	}
	p.Mode = ForcedOn
	if !errors.Is(p.Validate(), ErrPolicyLocked) {
		t.Fatal("forced-on disabled")
	}
	p.TOTPEnabled = true
	if p.Validate() != nil {
		t.Fatal("forced-on ON rejected")
	}
	for _, bad := range []AuthPolicy{{}, {Mode: "unknown", TOTPEnabled: true, Version: 1}, {Mode: Configurable, TOTPEnabled: true}} {
		if !errors.Is(bad.Validate(), ErrInvalidPolicy) {
			t.Fatal("invalid policy accepted")
		}
	}
}

func TestSessionHalfOpenExpiryAndReadOnly(t *testing.T) {
	a, s, p, now := sessionFixture()
	before := s
	for range 3 {
		if err := CheckSession(a, s, p, now); err != nil {
			t.Fatal(err)
		}
	}
	if s != before {
		t.Fatal("read refreshed session")
	}
	s.LastSeenAt = now.Add(-SessionIdleTTL + time.Nanosecond)
	if CheckSession(a, s, p, now) != nil {
		t.Fatal("expired before idle boundary")
	}
	s.LastSeenAt = now.Add(-SessionIdleTTL)
	if CheckSession(a, s, p, now) == nil {
		t.Fatal("idle boundary accepted")
	}
	s.CreatedAt = now.Add(-SessionAbsoluteTTL + time.Nanosecond)
	s.LastSeenAt = now
	if CheckSession(a, s, p, now) != nil {
		t.Fatal("expired before absolute boundary")
	}
	s.CreatedAt = now.Add(-SessionAbsoluteTTL)
	if CheckSession(a, s, p, now) == nil {
		t.Fatal("absolute boundary accepted")
	}
}

func TestSessionIdentityRevocationAndInvalidClock(t *testing.T) {
	changes := []struct {
		name   string
		change func(*Account, *Session, *AuthPolicy, time.Time)
	}{
		{"disabled", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { a.Enabled = false }},
		{"wrong-user", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { s.UserID = "other" }},
		{"unknown-role", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { a.Role = "root" }},
		{"revoked-user", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { a.SessionVersion++ }},
		{"zero-version", func(a *Account, s *Session, p *AuthPolicy, n time.Time) {
			a.SessionVersion = 0
			s.UserSessionVersion = 0
		}},
		{"future-seen", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { s.LastSeenAt = n.Add(time.Second) }},
		{"seen-before-created", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { s.LastSeenAt = s.CreatedAt.Add(-time.Second) }},
		{"missing-created", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { s.CreatedAt = time.Time{} }},
		{"invalid-policy", func(a *Account, s *Session, p *AuthPolicy, n time.Time) { p.Mode = "unknown" }},
	}
	for _, item := range changes {
		t.Run(item.name, func(t *testing.T) {
			a, s, p, n := sessionFixture()
			item.change(&a, &s, &p, n)
			if !errors.Is(CheckSession(a, s, p, n), ErrUnauthenticated) {
				t.Fatal("invalid session accepted")
			}
		})
	}
}

func TestAllRoleStalePolicyAndExplicitOff(t *testing.T) {
	for _, role := range []Role{Admin, Operator, Viewer} {
		a, s, p, n := sessionFixture()
		a.Role = role
		a.TOTPEnrolled = false
		s.MFAVerified = false
		if CheckSession(a, s, p, n) == nil {
			t.Fatal("MFA bypass", role)
		}
		p.TOTPEnabled = false
		if CheckSession(a, s, p, n) != nil {
			t.Fatal("explicit OFF ignored", role)
		}
		for _, enabled := range []bool{true, false} {
			p.TOTPEnabled = enabled
			p.Version = 2
			a.TOTPEnrolled = true
			s.MFAVerified = true
			if CheckSession(a, s, p, n) == nil {
				t.Fatal("stale policy accepted", role, enabled)
			}
		}
		s.AuthPolicyVersion = 2
		if CheckSession(a, s, p, n) != nil {
			t.Fatal("current session rejected", role)
		}
	}
}

func TestOrdinaryActionsCannotBypassReauth(t *testing.T) {
	a, s, p, n := sessionFixture()
	if CheckOrdinaryAction(a, s, p, WriteConfig, n) != nil {
		t.Fatal("admin template change denied")
	}
	if !errors.Is(CheckOrdinaryAction(a, s, p, WriteTOTPPolicy, n), ErrReauthenticationRequired) {
		t.Fatal("login MFA became action reauth")
	}
	a.Role = Operator
	if !errors.Is(CheckOrdinaryAction(a, s, p, WriteConfig, n), ErrForbidden) {
		t.Fatal("operator template change allowed")
	}
	if CheckOrdinaryAction(a, s, p, OperateRuntime, n) != nil {
		t.Fatal("operator denied")
	}
	a.Enabled = false
	if !errors.Is(CheckOrdinaryAction(a, s, p, OperateRuntime, n), ErrUnauthenticated) {
		t.Fatal("revoked account accepted")
	}
}

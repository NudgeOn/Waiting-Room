// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestSecurityRequiresExplicitEnabledBoolean(t *testing.T) {
	// Missing/null cannot silently disable a user or a global TOTP policy.
	// Validation must finish before even trying to access the unavailable store.
	s := &SecurityService{}
	for _, raw := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`} {
		out, err := s.ChangePolicy(context.Background(), "", httptest.NewRequest("PUT", "/security/totp", nil), []byte(raw))
		if err != nil || out.Status != 400 {
			t.Fatal("ambiguous policy accepted", out.Status, err)
		}
	}
	for _, raw := range []string{`{"role":"viewer"}`, `{"role":"viewer","enabled":null}`, `{"role":"viewer","enabled":"false"}`} {
		out, err := s.ChangeUser(context.Background(), "", "target", httptest.NewRequest("PATCH", "/users/target", nil), []byte(raw), false)
		if err != nil || out.Status != 400 {
			t.Fatal("ambiguous user update accepted", out.Status, err)
		}
	}
}

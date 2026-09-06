// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"waiting-room/internal/adminauth"
)

func testVault(t *testing.T) *Vault {
	t.Helper()
	v, err := NewVault(map[string][]byte{"k1": bytes.Repeat([]byte{1}, 32), "k2": bytes.Repeat([]byte{2}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestVaultRoundTripAndBinding(t *testing.T) {
	v := testVault(t)
	ref := adminauth.CredentialRef{UserID: "admin1", Version: 1}
	secret, err := adminauth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := secret.ProvisioningBase32()
	a, err := v.Seal(ref, "k1", secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.Seal(ref, "k1", secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 60 || bytes.Equal(a, b) || bytes.Contains(a, []byte(raw)) {
		t.Fatal("invalid nonce or plaintext storage")
	}
	opened, err := v.open(ref, "k1", a)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := opened.ProvisioningBase32()
	if actual != raw {
		t.Fatal("round trip")
	}
	tampered := append([]byte(nil), a...)
	tampered[len(tampered)-1] ^= 1
	for _, tc := range []struct {
		ref  adminauth.CredentialRef
		kid  string
		data []byte
	}{
		{adminauth.CredentialRef{UserID: "admin2", Version: 1}, "k1", a},
		{adminauth.CredentialRef{UserID: "admin1", Version: 2}, "k1", a},
		{ref, "k2", a}, {ref, "missing", a}, {ref, "k1", tampered}, {ref, "k1", a[:10]},
	} {
		if _, err := v.open(tc.ref, tc.kid, tc.data); err != adminauth.ErrAuthUnavailable {
			t.Fatal("accepted unbound ciphertext")
		}
	}
}
func TestVaultInvalidConfigurationAndRedaction(t *testing.T) {
	for _, keys := range []map[string][]byte{nil, {"k": make([]byte, 31)}, {"bad:id": make([]byte, 32)}} {
		if _, err := NewVault(keys); err != adminauth.ErrAuthUnavailable {
			t.Fatal("invalid keys accepted")
		}
	}
	v := testVault(t)
	if _, err := v.Seal(adminauth.CredentialRef{UserID: "invalid:id", Version: 1}, "k1", adminauth.Secret{}); err == nil {
		t.Fatal("invalid ref")
	}
	if _, err := v.Seal(adminauth.CredentialRef{UserID: "u", Version: 1}, "k1", adminauth.Secret{}); err == nil {
		t.Fatal("invalid secret")
	}
	for _, value := range []any{v, Grant{session: "sensitive-session", csrf: "sensitive-csrf"}} {
		encoded, _ := json.Marshal(value)
		for _, s := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%#v", value), string(encoded)} {
			if !strings.Contains(s, "REDACTED") || strings.Contains(s, "sensitive") {
				t.Fatal("sensitive log output")
			}
		}
	}
	if _, err := New(nil, v); err != adminauth.ErrAuthUnavailable {
		t.Fatal("nil pool")
	}
}
func TestOpaqueTokenValidation(t *testing.T) {
	raw, hash, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := tokenHash(raw)
	if !ok || got != hash {
		t.Fatal("valid token")
	}
	for _, s := range []string{"", strings.Repeat("a", 42), raw + "=", strings.Repeat("!", 43), strings.Repeat("A", 42) + "B"} {
		if _, ok := tokenHash(s); ok {
			t.Fatal("malformed token")
		}
	}
}

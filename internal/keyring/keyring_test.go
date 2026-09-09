// SPDX-License-Identifier: Apache-2.0
package keyring

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func testKey(id string) Key {
	c, cp, _ := ed25519.GenerateKey(rand.Reader)
	a, ap, _ := ed25519.GenerateKey(rand.Reader)
	r := make([]byte, 32)
	v := make([]byte, 32)
	rand.Read(r)
	rand.Read(v)
	return Key{ID: id, ConfigPublic: c, ConfigPrivate: cp, AdmissionPublic: a, AdmissionPrivate: ap, Replay: r, Return: v}
}
func TestRoleSeparationAndRetirement(t *testing.T) {
	old := testKey("v1")
	next := testKey("11111111111111111111111111111111")
	next.Since = 2000
	active := Set{Generation: 2, Phase: "active", Current: next, Previous: &old}
	if active.Validate("owner") != nil {
		t.Fatal("owner")
	}
	for _, role := range []string{"control", "gateway", "coordinator"} {
		s := active.ForRole(role)
		if s.Validate(role) != nil || s.Digest() != active.Digest() {
			t.Fatal("role separation", role)
		}
	}
	if active.AdmissionAt(1999).ID != old.ID || active.AdmissionAt(2000).ID != next.ID {
		t.Fatal("promotion cutoff")
	}
	for _, purpose := range []string{"return", "replay", "browser-join"} {
		oldSeal, err := Seal(old, purpose, []byte("same visitor secret"), []byte("room-binding"))
		if err != nil {
			t.Fatal(err)
		}
		newSeal, err := Seal(next, purpose, []byte("new secret"), []byte("room-binding"))
		if err != nil {
			t.Fatal(err)
		}
		if plain, err := active.Open(purpose, oldSeal, []byte("room-binding"), nil); err != nil || !bytes.Equal(plain, []byte("same visitor secret")) {
			t.Fatal("overlap lost old artifact", err)
		}
		if _, err = active.Open(purpose, oldSeal, []byte("other-room"), nil); err == nil {
			t.Fatal("binding")
		}
		if _, err = active.Open(purpose, oldSeal+"x", []byte("room-binding"), nil); err == nil {
			t.Fatal("tamper")
		}
		retired := active
		retired.Generation++
		retired.Phase = "stable"
		retired.Previous = nil
		if _, err = retired.Open(purpose, oldSeal, []byte("room-binding"), nil); err == nil {
			t.Fatal("retired key accepted")
		}
		if _, err = retired.Open(purpose, newSeal, []byte("room-binding"), nil); err != nil {
			t.Fatal("current key rejected")
		}
	}
	gateway := active.ForRole("gateway")
	gateway.Current.AdmissionPrivate = next.AdmissionPrivate
	if gateway.Validate("gateway") == nil {
		t.Fatal("signer key in gateway")
	}
}

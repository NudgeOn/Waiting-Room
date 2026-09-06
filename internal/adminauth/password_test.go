// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPasswordRoundTripAndExactInput(t *testing.T) {
	ctx := context.Background()
	h, err := NewPasswordHasher(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	password := "비밀번호  exact input 🔒"
	a, err := h.Hash(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Hash(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if a.StorageValue() == b.StorageValue() || strings.Contains(a.StorageValue(), password) {
		t.Fatal("salt reuse/plaintext")
	}
	for _, tc := range []struct {
		input string
		want  bool
	}{{password, true}, {password + " ", false}, {"wrong-password", false}} {
		ok, err := h.Verify(ctx, tc.input, a.StorageValue())
		if err != nil || ok != tc.want {
			t.Fatal("password verification mismatch")
		}
	}
	if ok, err := h.Verify(ctx, password, ""); ok || err != nil {
		t.Fatal("missing user dummy")
	}
}
func TestPasswordParameterBounds(t *testing.T) {
	ctx := context.Background()
	for _, n := range []uint32{0, 1, 11, ^uint32(0)} {
		if _, err := NewPasswordHasher(ctx, n); err == nil {
			t.Fatal("unbounded iterations")
		}
	}
	h, err := NewPasswordHasher(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := h.Hash(ctx, "valid long test password")
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{
		strings.Replace(valid.encoded, "m=65536", "m=999999999", 1),
		strings.Replace(valid.encoded, "t=2", "t=999999999", 1),
		strings.Replace(valid.encoded, "p=1", "p=255", 1),
		strings.Replace(valid.encoded, "v=19", "v=16", 1),
		strings.Replace(valid.encoded, "t=2", "t=02", 1), valid.encoded + "=", strings.Repeat("x", 161),
	} {
		if _, _, _, ok := parsePassword(encoded); ok {
			t.Fatal("unsafe encoding accepted")
		}
	}
	if ok, err := h.Verify(ctx, "valid long test password", "corrupt"); ok || err != ErrAuthUnavailable {
		t.Fatal("corrupt record accepted")
	}
	if ok, err := (&PasswordHasher{}).Verify(ctx, "x", ""); ok || err != ErrAuthUnavailable {
		t.Fatal("zero value")
	}
}
func TestPasswordLimitsCancellationAndMemoryGate(t *testing.T) {
	ctx := context.Background()
	h, err := NewPasswordHasher(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "short", strings.Repeat("x", 1025), string([]byte{255})} {
		if _, err := h.Hash(ctx, p); err != ErrUnauthenticated {
			t.Fatal("invalid creation input")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := h.Hash(canceled, "long test password"); err != ErrAuthUnavailable {
		t.Fatal("canceled work")
	}
	passwordSlots <- struct{}{}
	passwordSlots <- struct{}{}
	defer func() { <-passwordSlots; <-passwordSlots }()
	if _, err := h.Hash(ctx, "long test password"); err != ErrAuthUnavailable {
		t.Fatal("unbounded work queue")
	}
}
func TestPasswordHashRedaction(t *testing.T) {
	h := PasswordHash{encoded: "sensitive-hash"}
	for _, value := range []any{h, &PasswordHasher{iterations: 2, dummy: h}} {
		encoded, _ := json.Marshal(value)
		for _, v := range []string{fmt.Sprint(value), fmt.Sprintf("%#v", value), string(encoded)} {
			if !strings.Contains(v, "REDACTED") || strings.Contains(v, h.encoded) {
				t.Fatal("hash logging leak")
			}
		}
	}
}
func FuzzPasswordEncoding(f *testing.F) {
	f.Add("wrp1$argon2id$v=19$m=65536,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Add("")
	f.Add(strings.Repeat("a", 200))
	f.Fuzz(func(t *testing.T, value string) {
		n, salt, key, ok := parsePassword(value)
		if ok && (n < 2 || n > 10 || len(salt) != 16 || len(key) != 32 || passwordEncoding(n, salt, key) != value) {
			t.Fatal("unsafe parse")
		}
	})
}

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestLoginFingerprintNormalizationAndRedaction(t *testing.T) {
	l := &LoginService{fingerprintKey: [32]byte{1}}
	a, _ := sourceValue(netip.MustParseAddr("127.0.0.1"))
	b, _ := sourceValue(netip.MustParseAddr("::ffff:127.0.0.1"))
	if a != b {
		t.Fatal("IPv4 mapping bypass")
	}
	a, _ = sourceValue(netip.MustParseAddr("2001:db8::1"))
	b, _ = sourceValue(netip.MustParseAddr("2001:db8::2"))
	if a != b {
		t.Fatal("IPv6 /64 bypass")
	}
	if l.fingerprint("source/1", a) == l.fingerprint("source/2", a) || l.fingerprint("account", a) == l.fingerprint("source/1", a) {
		t.Fatal("domain/rotation separation")
	}
	for _, s := range []string{"0.0.0.0", "::", "ff02::1", "fe80::1%en0"} {
		if _, ok := sourceValue(netip.MustParseAddr(s)); ok {
			t.Fatal("invalid source")
		}
	}
	for _, v := range []any{l, LoginResult{challenge: "sensitive"}, passwordSnapshot{hash: "sensitive"}} {
		jsonValue, _ := json.Marshal(v)
		for _, s := range []string{fmt.Sprint(v), fmt.Sprintf("%#v", v), string(jsonValue)} {
			if strings.Contains(s, "sensitive") || !strings.Contains(s, "REDACTED") {
				t.Fatal("secret output")
			}
		}
	}
}

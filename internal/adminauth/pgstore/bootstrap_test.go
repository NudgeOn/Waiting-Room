// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"waiting-room/internal/adminauth"
)

func TestBootstrapRedactionAndEarlyReject(t *testing.T) {
	secret := "sensitive-fixture-value"
	for _, value := range []any{BootstrapToken{raw: secret}, LoginResult{enrollment: secret}} {
		b, err := json.Marshal(value)
		if err != nil || strings.Contains(string(b)+fmt.Sprintf("%v %+v %#v", value, value, value), secret) {
			t.Fatal("credential leaked")
		}
	}
	if (BootstrapToken{raw: secret}).Token() != secret || (LoginResult{enrollment: secret}).EnrollmentToken() != secret {
		t.Fatal("explicit accessor broken")
	}
	l := &LoginService{} // Any DB/KDF call would panic: invalid boundary must reject first.
	for _, peer := range []netip.Addr{netip.Addr{}, netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("::1%lo0")} {
		if _, err := l.Bootstrap(context.Background(), secret, "admin", "valid password fixture", peer); err != adminauth.ErrUnauthenticated {
			t.Fatal("non-loopback accepted", err)
		}
	}
	if _, err := l.Bootstrap(context.Background(), secret, "admin", "valid password fixture", netip.MustParseAddr("127.0.0.1")); err != adminauth.ErrUnauthenticated {
		t.Fatal("malformed install token accepted", err)
	}
}

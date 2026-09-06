// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

type fixedResolver struct {
	ips   []netip.Addr
	err   error
	calls int
}

func (r *fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return r.ips, r.err
}
func TestOriginAddressClassification(t *testing.T) {
	for _, s := range []string{"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.168.1.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "2002:7f00:1::1", "2001::1", "ff02::1", "2606:4700:4700::1111%lo0"} {
		if publicAddress(netip.MustParseAddr(s)) {
			t.Errorf("unsafe address accepted: %s", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(s)) {
			t.Errorf("public address rejected: %s", s)
		}
	}
}
func TestOriginRejectsAllMixedDNSBeforeDial(t *testing.T) {
	for _, ips := range [][]netip.Addr{{}, {netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")}} {
		resolver := &fixedResolver{ips: ips}
		dial, err := originDial("https://origin.example.test", resolver)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = dial(context.Background(), "tcp", "origin.example.test:443"); err == nil {
			t.Fatal("unsafe/missing DNS accepted")
		}
		if resolver.calls != 1 {
			t.Fatal("unexpected DNS calls")
		}
	}
	resolver := &fixedResolver{err: errors.New("unavailable")}
	dial, _ := originDial("https://origin.example.test", resolver)
	if _, err := dial(context.Background(), "tcp", "other.example.test:443"); err == nil || resolver.calls != 0 {
		t.Fatal("different authority reached DNS")
	}
	if _, err := dial(context.Background(), "udp", "origin.example.test:443"); err == nil || resolver.calls != 0 {
		t.Fatal("different network accepted")
	}
}
func TestOriginConstructorRejectsAmbiguity(t *testing.T) {
	for _, raw := range []string{"http://example.test", "https://user:pass@example.test", "https://example.test/path", "https://example.test?x=1", "https://example.test?", "https://example.test#fragment", "//example.test"} {
		if _, err := originDial(raw, &fixedResolver{}); err == nil {
			t.Errorf("ambiguous origin accepted: %s", raw)
		}
	}
	if _, err := originDial("https://example.test", nil); err == nil {
		t.Fatal("nil resolver accepted")
	}
}

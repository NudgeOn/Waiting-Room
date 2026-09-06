// SPDX-License-Identifier: Apache-2.0
package adminlab

import (
	"crypto/x509"
	"net"
	"testing"
)

func TestLocalCertificate(t *testing.T) {
	cert, err := Certificate()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf.VerifyHostname("127.0.0.1") != nil || leaf.VerifyHostname("admin.example.com") == nil || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Fatal("certificate escaped loopback scope")
	}
	if leaf.NotAfter.Sub(leaf.NotBefore).Hours() > 25 {
		t.Fatal("lab certificate duration")
	}
}

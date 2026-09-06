// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// NodeIdentity is role-scoped. Never serialize it into logs or an API response.
type NodeIdentity struct {
	Version                                               int    `json:"version"`
	Installation                                          string `json:"installation"`
	Binding                                               string `json:"binding"`
	Node                                                  string `json:"node"`
	CA, Certificate, PrivateKey                           []byte
	ConfigPublic, AdmissionPublic                         []byte
	ConfigPrivate, AdmissionPrivate, ReplayKey, ReturnKey []byte `json:",omitempty"`
	Service, ValkeyPassword                               string `json:",omitempty"`
}

func (NodeIdentity) String() string               { return "[REDACTED_NODE_IDENTITY]" }
func (NodeIdentity) GoString() string             { return "[REDACTED_NODE_IDENTITY]" }
func (NodeIdentity) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_NODE_IDENTITY]") }

type identityDisk NodeIdentity

func derive(s State, label string) []byte {
	h := hmac.New(sha256.New, s.Vault[:])
	h.Write([]byte("waiting-room/local-identity/v1/" + label))
	return h.Sum(nil)
}
func nodeName(name string) bool {
	return name == "control" || name == "gateway" || name == "coordinator" || name == "demo-origin"
}

// ProvisionIdentities is an explicit owner operation. Existing role files are
// validated, never replaced. Keys are domain-separated from the auth vault.
func ProvisionIdentities(s State, root string) error {
	caKey := ed25519.NewKeyFromSeed(derive(s, "ca"))
	configKey := ed25519.NewKeyFromSeed(derive(s, "config"))
	admissionKey := ed25519.NewKeyFromSeed(derive(s, "admission"))
	now := time.Now()
	serial := new(big.Int).SetBytes(derive(s, "ca-serial")[:16])
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Waiting Room local installation CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	// Reuse the persisted CA certificate across interrupted initialization.
	caPath := filepath.Join(root, "ca.pem")
	caPEM, err := ReadPrivate(caPath, 8192)
	if errors.Is(err, os.ErrNotExist) {
		der, e := x509.CreateCertificate(rand.Reader, ca, ca, caKey.Public(), caKey)
		if e != nil {
			return e
		}
		caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		if err = os.MkdirAll(root, 0700); err != nil {
			return err
		}
		if err = writePrivate(root, "ca.pem", caPEM, false); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return errors.New("invalid installation CA")
	}
	ca, err = x509.ParseCertificate(block.Bytes)
	if err != nil || !bytes.Equal(ca.RawSubjectPublicKeyInfo, mustPKIX(caKey.Public())) || !time.Now().Before(ca.NotAfter) {
		return errors.New("mismatched or expired installation CA")
	}
	for _, name := range []string{"control", "gateway", "coordinator", "demo-origin"} {
		dir := filepath.Join(root, name)
		existing, err := LoadIdentity(dir, name)
		if err == nil {
			if existing.Binding != s.Binding() || !bytes.Equal(existing.CA, caPEM) {
				return errors.New("node binding mismatch")
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		key := ed25519.NewKeyFromSeed(derive(s, "tls/"+name))
		leaf := &x509.Certificate{SerialNumber: new(big.Int).SetBytes(derive(s, "serial/"+name)[:16]), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		if name == "gateway" {
			leaf.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		der, err := x509.CreateCertificate(rand.Reader, leaf, ca, key.Public(), caKey)
		if err != nil {
			return err
		}
		priv, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		n := NodeIdentity{Version: 1, Installation: "local", Binding: s.Binding(), Node: name, CA: caPEM, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}), ConfigPublic: configKey.Public().(ed25519.PublicKey), AdmissionPublic: admissionKey.Public().(ed25519.PublicKey)}
		switch name {
		case "control":
			n.ConfigPrivate = configKey
		case "coordinator":
			n.AdmissionPrivate = admissionKey
			n.ReplayKey = derive(s, "replay")
			n.Service = hex.EncodeToString(derive(s, "service"))
			n.ValkeyPassword = hex.EncodeToString(derive(s, "valkey"))
		case "gateway":
			n.ReturnKey = derive(s, "return")
			n.Service = hex.EncodeToString(derive(s, "service"))
		}
		raw, err := json.Marshal(identityDisk(n))
		if err != nil {
			return err
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err = writePrivate(dir, "identity.json", raw, false); err != nil {
			return err
		}
	}
	return nil
}
func mustPKIX(key any) []byte { b, _ := x509.MarshalPKIXPublicKey(key); return b }
func LoadIdentity(dir, name string) (NodeIdentity, error) {
	var n NodeIdentity
	if !nodeName(name) {
		return n, errors.New("unknown node")
	}
	raw, err := ReadPrivate(filepath.Join(dir, "identity.json"), 32768)
	if err != nil {
		return n, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&n) != nil || d.Decode(new(any)) != io.EOF || n.Version != 1 || n.Node != name || n.Installation != "local" || len(n.Binding) != 64 || len(n.ConfigPublic) != 32 || len(n.AdmissionPublic) != 32 {
		return NodeIdentity{}, errors.New("invalid identity")
	}
	if _, err = n.TLS(false); err != nil {
		return NodeIdentity{}, err
	}
	return n, nil
}
func (n NodeIdentity) TLS(client bool) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(n.Certificate, n.PrivateKey)
	if err != nil {
		return nil, errors.New("invalid node certificate")
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(n.CA) {
		return nil, errors.New("invalid CA")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: ca, DNSName: n.Node}); err != nil {
		return nil, errors.New("untrusted or expired node certificate")
	}
	c := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: ca, ClientCAs: ca}
	if !client {
		c.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return c, nil
}

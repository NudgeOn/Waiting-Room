// SPDX-License-Identifier: Apache-2.0
// Package localcontrol provides explicit, persistent local-Docker initialization.
// It is not a public/cloud deployment profile or a queue runtime publisher.
package localcontrol

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Version     int      `json:"version"`
	Vault       [32]byte `json:"vault"`
	Fingerprint [32]byte `json:"fingerprint"`
	Certificate []byte   `json:"certificate"`
	PrivateKey  []byte   `json:"privateKey"`
}

func (State) String() string               { return "[REDACTED_LOCAL_STATE]" }
func (State) GoString() string             { return "[REDACTED_LOCAL_STATE]" }
func (State) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED_LOCAL_STATE]") }

// Only the private-file writer may serialize secret fields.
type stateDisk State

func (s State) Binding() string {
	// Bind the database to the encryption/login keys, not an expiring TLS cert.
	h := sha256.New()
	h.Write(s.Vault[:])
	h.Write(s.Fingerprint[:])
	return hex.EncodeToString(h.Sum(nil))
}

func (s State) TLS() (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(s.Certificate, s.PrivateKey)
	if err != nil {
		return tls.Certificate{}, errors.New("invalid local TLS state")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || leaf.VerifyHostname("127.0.0.1") != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return tls.Certificate{}, errors.New("invalid or expired local certificate")
	}
	return cert, nil
}

func LoadState(dir string) (State, error) {
	var s State
	info, err := os.Lstat(dir)
	if err != nil {
		return s, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return s, errors.New("private state directory required")
	}
	raw, err := ReadPrivate(filepath.Join(dir, "installation.json"), 16384)
	if err != nil {
		return s, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF || s.Version != 1 || s.Vault == [32]byte{} || s.Fingerprint == [32]byte{} {
		return State{}, errors.New("invalid local state")
	}
	if _, err := s.TLS(); err != nil {
		return State{}, err
	}
	return s, nil
}

// ReadPrivate rejects symlinks and over-permissive/oversized secret files.
// State/secret directories are exclusively controlled by the local operator.
func ReadPrivate(name string, max int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > max {
		return nil, errors.New("private regular file required")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("secret file changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, errors.New("secret read failed")
	}
	return data, nil
}

func newState() (State, error) {
	s := State{Version: 1}
	if _, err := rand.Read(s.Vault[:]); err != nil {
		return State{}, err
	}
	if _, err := rand.Read(s.Fingerprint[:]); err != nil {
		return State{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return State{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return State{}, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Waiting Room local Docker only"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(365 * 24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return State{}, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return State{}, err
	}
	s.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	s.PrivateKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	return s, nil
}

// createState publishes a fully synced file without replacing an existing key.
// Only the initializer calls this, after checking for an existing DB install.
func createState(dir string) (State, error) {
	s, err := newState()
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return State{}, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return State{}, errors.New("private state directory required")
	}
	raw, err := json.Marshal(stateDisk(s))
	if err != nil {
		return State{}, err
	}
	if err := writePrivate(dir, "installation.json", raw, false); err != nil {
		return State{}, err
	}
	return LoadState(dir)
}

func writePrivate(dir, name string, data []byte, replace bool) error {
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if replace {
		err = os.Rename(tmp, target)
	} else {
		err = os.Link(tmp, target)
	}
	if err != nil {
		return fmt.Errorf("publish private file: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := createState(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadState(dir)
	if err != nil || s.Binding() != again.Binding() || !bytes.Equal(s.Certificate, again.Certificate) {
		t.Fatal("state changed", err)
	}
	if _, err = createState(dir); err == nil {
		t.Fatal("overwrote existing keys")
	}
	if _, err = s.TLS(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v %#v", s, s) != "[REDACTED_LOCAL_STATE] [REDACTED_LOCAL_STATE]" {
		t.Fatal("state logging exposed keys")
	}
	encoded, err := json.Marshal(s)
	if err != nil || string(encoded) != `"[REDACTED_LOCAL_STATE]"` {
		t.Fatal("state JSON exposed keys")
	}
	other, err := newState()
	if err != nil || s.Binding() == other.Binding() {
		t.Fatal("key reuse")
	}
}

func TestRejectUnsafeState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	_, err := createState(dir)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "installation.json")
	if err = os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadState(dir); err == nil {
		t.Fatal("public secret accepted")
	}
	if err = os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadPrivate(link, 16384); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err = ReadPrivate(file, 1); err == nil {
		t.Fatal("oversized secret accepted")
	}
	if err = os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadState(dir); err == nil {
		t.Fatal("public state directory accepted")
	}
}

func TestPrivatePublication(t *testing.T) {
	dir := t.TempDir()
	if err := writePrivate(dir, "token", []byte("first"), false); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(dir, "token", []byte("second"), false); err == nil {
		t.Fatal("overwrote token without authority")
	}
	if err := writePrivate(dir, "token", []byte("rotated"), true); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPrivate(filepath.Join(dir, "token"), 7)
	if err != nil || string(got) != "rotated" {
		t.Fatal("rotation failed", err)
	}
}

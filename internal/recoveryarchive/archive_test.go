// SPDX-License-Identifier: Apache-2.0
package recoveryarchive

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func roots(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range Volumes {
		if err := os.Mkdir(filepath.Join(root, v), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestColdArchiveRoundTripAndTampering(t *testing.T) {
	ctx := context.Background()
	source := roots(t)
	dest := t.TempDir()
	os.Chmod(dest, 0700)
	for _, v := range Volumes {
		if err := os.WriteFile(filepath.Join(source, v, "persisted"), []byte("private-state-"+v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Create(ctx, source, dest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Entries != 14 {
		t.Fatal(m)
	}
	if got, err := Verify(ctx, dest); err != nil || got != m {
		t.Fatal("verification", err)
	}
	target := roots(t)
	if err = Restore(ctx, dest, target); err != nil {
		t.Fatal(err)
	}
	for _, v := range Volumes {
		b, err := os.ReadFile(filepath.Join(target, v, "persisted"))
		if err != nil || string(b) != "private-state-"+v {
			t.Fatal("data changed", err)
		}
	}
	if err = Restore(ctx, dest, target); err == nil {
		t.Fatal("overwrote populated target")
	}
	f, err := os.OpenFile(filepath.Join(dest, "volumes.tar"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("changed"), 1024)
	f.Close()
	empty := roots(t)
	if err = Restore(ctx, dest, empty); err == nil {
		t.Fatal("tampered archive restored")
	}
	for _, v := range Volumes {
		entries, _ := os.ReadDir(filepath.Join(empty, v))
		if len(entries) != 0 {
			t.Fatal("modified target before verification")
		}
	}
}
func TestArchiveRejectsLinksAndTraversal(t *testing.T) {
	source := roots(t)
	dest := t.TempDir()
	os.Chmod(dest, 0700)
	os.Symlink("/etc/passwd", filepath.Join(source, "state", "link"))
	if _, err := Create(context.Background(), source, dest); err == nil {
		t.Fatal("archived symlink")
	}
	for _, name := range []string{"../escape", "database/../../escape", "/state/escape", "state/./x", "state\\x"} {
		if validHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600}) {
			t.Fatal("unsafe path", name)
		}
	}
	if validHeader(&tar.Header{Name: "state/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0600}) {
		t.Fatal("accepted link")
	}
}
func TestArchiveRejectsMalformedButCorrectlyHashedTarBeforeRestore(t *testing.T) {
	dest := t.TempDir()
	os.Chmod(dest, 0700)
	file := filepath.Join(dest, "volumes.tar")
	f, _ := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	w := tar.NewWriter(f)
	for _, v := range Volumes {
		w.WriteHeader(&tar.Header{Name: v, Typeflag: tar.TypeDir, Mode: 0700})
	}
	w.WriteHeader(&tar.Header{Name: "state/../../escape", Typeflag: tar.TypeReg, Mode: 0600})
	w.Close()
	f.Close()
	b, _ := os.ReadFile(file)
	h := sha256.Sum256(b)
	m := Manifest{1, hex.EncodeToString(h[:]), int64(len(b)), 8}
	raw, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(dest, "volumes.json"), raw, 0600)
	if _, err := Verify(context.Background(), dest); err == nil {
		t.Fatal("hash alone accepted unsafe tar")
	}
	if strings.Contains(ErrArchive.Error(), dest) {
		t.Fatal("path disclosed")
	}
}

func TestArchiveRejectsInvalidDirectoryHierarchyBeforeAnyRestoreWrite(t *testing.T) {
	for _, invalid := range []string{"regular-volume-root", "missing-parent", "file-parent"} {
		t.Run(invalid, func(t *testing.T) {
			dest := t.TempDir()
			os.Chmod(dest, 0700)
			file := filepath.Join(dest, "volumes.tar")
			f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			w := tar.NewWriter(f)
			entries := 0
			put := func(name string, kind byte) {
				t.Helper()
				if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: kind, Mode: 0700}); err != nil {
					t.Fatal(err)
				}
				entries++
			}
			for _, v := range Volumes {
				kind := byte(tar.TypeDir)
				if invalid == "regular-volume-root" && v == "queue-data" {
					kind = tar.TypeReg
				}
				put(v, kind)
			}
			put("state/first-valid-file", tar.TypeReg)
			if invalid == "file-parent" {
				put("state/parent", tar.TypeReg)
			}
			if invalid != "regular-volume-root" {
				put("state/parent/child", tar.TypeReg)
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			b, _ := os.ReadFile(file)
			h := sha256.Sum256(b)
			raw, _ := json.Marshal(Manifest{1, hex.EncodeToString(h[:]), int64(len(b)), entries})
			os.WriteFile(filepath.Join(dest, "volumes.json"), raw, 0600)
			target := roots(t)
			if err = Restore(context.Background(), dest, target); err == nil {
				t.Fatal("malformed hierarchy accepted")
			}
			for _, v := range Volumes {
				items, _ := os.ReadDir(filepath.Join(target, v))
				if len(items) != 0 {
					t.Fatal("files created before hierarchy verification")
				}
			}
		})
	}
}

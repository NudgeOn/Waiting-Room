// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRestoreLifecycleRequiresPrivateVerifiedEmptyTarget(t *testing.T) {
	e, f, _, dir, before := installFixture(t)
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "backup")
	if err := e.execute(ctx, options{command: "backup", directory: dir, backupDirectory: dest}); err != nil {
		t.Fatal(err)
	}
	record, err := verifyBackup(ctx, dest)
	if err != nil || record.Installation != before {
		t.Fatal("snapshot record", err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	f.calls = nil
	if err = e.execute(ctx, options{command: "restore", directory: target, backupDirectory: dest}); err != nil {
		t.Fatal(err)
	}
	after, err := loadInstallation(target)
	if err != nil || after.Image != before.Image || after.Project == before.Project {
		t.Fatal("not isolated", err)
	}
	for _, call := range f.calls {
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, " up ") || strings.Contains(joined, "initialize") || strings.Contains(joined, "volume rm") {
			t.Fatal("restore started or replaced services")
		}
	}
	for _, name := range []string{"secrets/owner-password", "secrets/runtime-password", "compose.yaml"} {
		a, _ := os.ReadFile(filepath.Join(dir, name))
		b, _ := os.ReadFile(filepath.Join(target, name))
		if string(a) != string(b) {
			t.Fatal("matching files changed")
		}
	}
	f.calls = nil
	if err = e.execute(ctx, options{command: "restore", directory: dir, backupDirectory: dest}); err == nil {
		t.Fatal("overwrote source")
	}
	if len(f.calls) != 0 {
		t.Fatal("Docker touched before empty-target validation")
	}
	f.calls = nil
	os.WriteFile(filepath.Join(dest, "secrets/runtime-password"), []byte(strings.Repeat("f", 64)), 0600)
	if err = e.execute(ctx, options{command: "restore", directory: filepath.Join(t.TempDir(), "tampered"), backupDirectory: dest}); err == nil {
		t.Fatal("tampered private backup accepted")
	}
	if len(f.calls) != 0 {
		t.Fatal("Docker touched before complete checksum validation")
	}
}

func TestPhysicalRestoreRejectsDifferentArchitectureBeforeCreatingVolumes(t *testing.T) {
	e, f, _, dir, _ := installFixture(t)
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "backup")
	if err := e.execute(ctx, options{command: "backup", directory: dir, backupDirectory: dest}); err != nil {
		t.Fatal(err)
	}
	record, err := verifyBackup(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	record.Architecture = "arm64" // The fixture Docker host reports amd64.
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dest, "backup.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	err = e.execute(ctx, options{command: "restore", directory: filepath.Join(t.TempDir(), "target"), backupDirectory: dest})
	if err == nil || !strings.Contains(err.Error(), "original CPU architecture") {
		t.Fatal("cross-architecture physical restore accepted", err)
	}
	for _, call := range f.calls {
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, "volume create") || strings.Contains(joined, "archive-restore") || strings.HasPrefix(joined, "pull ") {
			t.Fatal("restore mutation before architecture check")
		}
	}
}
func TestUpgradeBackupFailureCannotRunMigrations(t *testing.T) {
	e, f, _, dir, before := installFixture(t)
	f.calls = nil
	f.fail = "archive-create"
	if err := e.execute(context.Background(), options{command: "upgrade", directory: dir, image: nextImage}); err == nil {
		t.Fatal("archive failure ignored")
	}
	after, err := loadInstallation(dir)
	if err != nil || after != before {
		t.Fatal("upgrade recorded before verified backup", err)
	}
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call.Args, " "), "initialize upgrade") {
			t.Fatal("migration before backup verified")
		}
	}
}

func TestUpgradeBackupRestoresOldRuntimeUsingNewArchiveHelper(t *testing.T) {
	e, f, _, dir, before := installFixture(t)
	ctx := context.Background()
	if err := e.execute(ctx, options{command: "upgrade", directory: dir, image: nextImage}); err != nil {
		t.Fatal(err)
	}
	backups, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(backups) != 1 {
		t.Fatal("expected one pre-upgrade backup", err)
	}
	dest := filepath.Join(dir, "backups", backups[0].Name())
	record, err := verifyBackup(ctx, dest)
	if err != nil || record.Installation.Image != before.Image || record.HelperImage != nextImage {
		t.Fatal("runtime and archive helper versions were conflated", err)
	}
	f.calls = nil
	target := filepath.Join(t.TempDir(), "rollback")
	if err = e.execute(ctx, options{command: "restore", directory: target, backupDirectory: dest}); err != nil {
		t.Fatal(err)
	}
	after, err := loadInstallation(target)
	if err != nil || after.Image != before.Image {
		t.Fatal("restore selected upgraded runtime for old data", err)
	}
	restored := false
	for _, call := range f.calls {
		joined := strings.Join(call.Args, " ")
		if strings.HasSuffix(joined, " archive-restore") {
			restored = true
			if !strings.HasSuffix(joined, nextImage+" archive-restore") {
				t.Fatal("restore requires the helper from the new image")
			}
		}
	}
	if !restored {
		t.Fatal("archive was not restored")
	}
}

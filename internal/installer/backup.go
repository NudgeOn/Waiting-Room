// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"waiting-room/internal/recoveryarchive"
)

type backupRecord struct {
	Architecture string                   `json:"architecture"`
	HelperImage  string                   `json:"helperImage"`
	Schema       int                      `json:"schema"`
	CreatedAt    time.Time                `json:"createdAt"`
	Installation installation             `json:"installation"`
	Files        map[string]string        `json:"files"`
	Volumes      recoveryarchive.Manifest `json:"volumes"`
}

var backupFiles = []string{"compose.yaml", "installation.json", "secrets/owner-password", "secrets/runtime-password"}

func (e engine) archive(ctx context.Context, dir string, s installation, image, dest, operation string, restore bool) error {
	args := []string{"run", "--rm", "--network", "none", "--read-only", "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges", "--mount", "type=bind,src=" + dest + ",dst=/backup"}
	if strings.ContainsAny(dest, ",\r\n") {
		return errors.New("backup directory cannot contain mount separators")
	}
	for _, v := range recoveryarchive.Volumes {
		mount := "type=volume,src=" + s.Project + "_" + v + ",dst=/volumes/" + v
		if !restore {
			mount += ",readonly"
		}
		args = append(args, "--mount", mount)
	}
	args = append(args, "--entrypoint", "/wr-control", image, operation)
	if _, err := e.docker(ctx, dir, image, args...); err != nil {
		return errors.New("cold volume archive operation failed; services remain stopped and original volumes retained")
	}
	return nil
}
func (e engine) stopped(ctx context.Context, dir string, s installation) error {
	b, err := e.docker(ctx, dir, s.Image, "ps", "--filter", "label=com.docker.compose.project="+s.Project, "--format", "{{.ID}}")
	if err != nil || len(bytes.TrimSpace(b)) != 0 {
		return errors.New("all source installation containers must be stopped before backup or restore")
	}
	return nil
}
func (e engine) volumeSet(ctx context.Context, dir string, s installation) error {
	args := []string{"volume", "inspect"}
	for _, v := range recoveryarchive.Volumes {
		args = append(args, s.Project+"_"+v)
	}
	b, err := e.docker(ctx, dir, s.Image, args...)
	if err != nil {
		return errors.New("cannot verify all seven source volumes")
	}
	var found []struct {
		Name   string
		Labels map[string]string
	}
	if json.Unmarshal(b, &found) != nil || len(found) != len(recoveryarchive.Volumes) {
		return errors.New("cannot verify all seven source volumes")
	}
	seen := map[string]bool{}
	for _, v := range found {
		if v.Labels["com.docker.compose.project"] != s.Project || v.Name != s.Project+"_"+v.Labels["com.docker.compose.volume"] || seen[v.Name] {
			return errors.New("source volume ownership mismatch")
		}
		seen[v.Name] = true
	}
	for _, v := range recoveryarchive.Volumes {
		if !seen[s.Project+"_"+v] {
			return errors.New("source volume missing")
		}
	}
	return nil
}
func (e engine) backup(ctx context.Context, dir string, s installation, dest, helper, arch string) error {
	if dest == "" || strings.ContainsAny(dest, ",\r\n") {
		return errors.New("use --backup-directory with a new private directory")
	}
	if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		return errors.New("backup destination must not already exist")
	}
	if err := e.volumeSet(ctx, dir, s); err != nil {
		return err
	}
	// Stop all writers and both stores before any snapshot is opened.
	if err := e.step(ctx, dir, s, s.Image, "Stopping this installation for a consistent cold backup", "stop"); err != nil {
		return err
	}
	if err := e.stopped(ctx, dir, s); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		return errors.New("cannot create private backup directory")
	}
	if err := privateDirectory(dest, false); err != nil {
		return err
	}
	record := backupRecord{Architecture: arch, HelperImage: helper, Schema: 1, CreatedAt: time.Now().UTC(), Installation: s, Files: map[string]string{}}
	if err := privateDirectory(filepath.Join(dest, "secrets"), true); err != nil {
		return err
	}
	for _, name := range backupFiles {
		b, err := readPrivate(filepath.Join(dir, name), 65536)
		if err != nil {
			return err
		}
		if err = writeExclusive(filepath.Join(dest, name), b); err != nil {
			return err
		}
		record.Files[name] = digest(b)
	}
	if err := e.archive(ctx, dir, s, helper, dest, "archive-create", false); err != nil {
		return err
	}
	report, err := recoveryarchive.Verify(ctx, dest)
	if err != nil {
		return err
	}
	record.Volumes = report
	raw, _ := json.MarshalIndent(record, "", "  ")
	if err = writeExclusive(filepath.Join(dest, "backup.json"), raw); err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.out, "Cold backup verified. Source services remain stopped. Backup: "+dest+"\nThe backup contains private keys and credentials; keep its directory private. Use wrctl up to resume the source, or restore into a new empty directory.")
	return err
}
func verifyBackup(ctx context.Context, dir string) (backupRecord, error) {
	var r backupRecord
	if privateDirectory(dir, false) != nil {
		return r, errors.New("private backup directory required")
	}
	b, err := readPrivate(filepath.Join(dir, "backup.json"), 16384)
	if err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Schema != 1 || !imagePattern.MatchString(r.HelperImage) || (r.Architecture != "amd64" && r.Architecture != "arm64") || len(r.Files) != len(backupFiles) {
		return r, errors.New("invalid backup manifest")
	}
	s, err := loadInstallation(dir)
	if err != nil || s != r.Installation || verifyFiles(dir, s) != nil {
		return r, errors.New("backup installation record mismatch")
	}
	for _, name := range backupFiles {
		b, err := readPrivate(filepath.Join(dir, name), 65536)
		if err != nil || digest(b) != r.Files[name] {
			return r, errors.New("backup file checksum mismatch")
		}
	}
	report, err := recoveryarchive.Verify(ctx, dir)
	if err != nil || report != r.Volumes {
		return r, errors.New("backup volume checksum or structure mismatch")
	}
	return r, nil
}
func (e engine) restore(ctx context.Context, o options) error {
	record, err := verifyBackup(ctx, o.backupDirectory)
	if err != nil {
		return err
	}
	if record.Installation.Phase != "ready" {
		return errors.New("backup was not taken before a completed installation; restore requires a ready snapshot")
	}
	if err = privateDirectory(o.directory, true); err != nil {
		return err
	}
	unlock, err := lockDirectory(o.directory)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := os.ReadDir(o.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != ".wrctl.lock" {
			return errors.New("restore requires an empty new installation directory")
		}
	}
	arch, err := e.checkDocker(ctx)
	if err != nil {
		return err
	}
	s := record.Installation
	if arch != record.Architecture {
		return errors.New("physical backup restore requires the original CPU architecture")
	}
	if err = e.verifyImage(ctx, s.Image, arch, true); err != nil {
		return err
	}
	// A pre-upgrade runtime may not contain any archive helper. Restore with the
	// exact helper that created the archive, then retain the old runtime image.
	if record.HelperImage != s.Image {
		if err = e.verifyImage(ctx, record.HelperImage, arch, true); err != nil {
			return err
		}
	}
	if err = e.stopped(ctx, o.directory, s); err != nil {
		return err
	}
	b, err := e.docker(ctx, o.directory, s.Image, "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	s.Project, err = newProject(strings.Fields(string(b)))
	if err != nil {
		return err
	}
	// Prepare only this new project's empty volumes. No source volume is mounted writable.
	for _, v := range recoveryarchive.Volumes {
		if _, err = e.docker(ctx, o.directory, s.Image, "volume", "create", "--label", "com.docker.compose.project="+s.Project, "--label", "com.docker.compose.volume="+v, s.Project+"_"+v); err != nil {
			return errors.New("cannot create isolated restore volumes")
		}
	}
	if err = e.archive(ctx, o.directory, s, record.HelperImage, o.backupDirectory, "archive-restore", true); err != nil {
		return err
	}
	if err = privateDirectory(filepath.Join(o.directory, "secrets"), true); err != nil {
		return err
	}
	for _, name := range backupFiles {
		if name == "installation.json" {
			continue
		}
		b, err := readPrivate(filepath.Join(o.backupDirectory, name), 65536)
		if err != nil {
			return err
		}
		if err = writeExclusive(filepath.Join(o.directory, name), b); err != nil {
			return err
		}
	}
	if err = saveInstallation(o.directory, s, true); err != nil {
		return err
	}
	if err = verifyFiles(o.directory, s); err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.out, "Backup restored into a new isolated installation. No services were started. Keep the source stopped, then run wrctl up"+directoryHint(o.directory)+". Queue recovery enforces its safety hold and validates retained state before admission.")
	return err
}

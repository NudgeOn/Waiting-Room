// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type installation struct {
	SchemaVersion int    `json:"schemaVersion"`
	Project       string `json:"project"`
	Image         string `json:"image"`
	PreviousImage string `json:"previousImage,omitempty"`
	PendingImage  string `json:"pendingImage,omitempty"`
	InitialTOTP   string `json:"initialTOTP"`
	Phase         string `json:"phase"`
	ComposeSHA256 string `json:"composeSHA256"`
}

var secretPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func privateDirectory(dir string, create bool) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return errors.New("cannot create private installation directory")
		}
		info, err = os.Lstat(dir)
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("installation and secret directories must be private real directories (mode 0700)")
	}
	return nil
}

func readPrivate(file string, max int64) ([]byte, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > max {
		return nil, errors.New("private regular installation file required")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, errors.New("installation file unavailable")
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.New("installation file changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, errors.New("installation file unavailable")
	}
	return b, nil
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func loadInstallation(dir string) (installation, error) {
	b, err := readPrivate(filepath.Join(dir, "installation.json"), 16384)
	if err != nil {
		return installation{}, err
	}
	var s installation
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF || s.SchemaVersion != 1 ||
		!projectPattern.MatchString(s.Project) || !imagePattern.MatchString(s.Image) ||
		(s.PreviousImage != "" && !imagePattern.MatchString(s.PreviousImage)) ||
		(s.PendingImage != "" && !imagePattern.MatchString(s.PendingImage)) ||
		(s.InitialTOTP != "on" && s.InitialTOTP != "off") || !secretPattern.MatchString(s.ComposeSHA256) ||
		(s.Phase != "prepared" && s.Phase != "ready" && s.Phase != "upgrading") ||
		(s.Phase == "upgrading") != (s.PendingImage != "") {
		return installation{}, errors.New("invalid installation record; no state was replaced")
	}
	return s, nil
}

func writeExclusive(file string, b []byte) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("refusing to replace an existing installation file")
	}
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return errors.New("cannot write installation file")
	}
	if err = f.Sync(); err != nil {
		return errors.New("cannot sync installation file")
	}
	return syncDirectory(filepath.Dir(file))
}

// The pending-upgrade record must survive a crash before migrations run.
// Syncing only the temporary file does not make its renamed directory entry
// durable; a failure here must stop the lifecycle before any migration.
func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return errors.New("cannot open installation directory for durable commit")
	}
	defer f.Close()
	if err = f.Sync(); err != nil {
		return errors.New("cannot durably commit installation directory")
	}
	return nil
}

func replacePrivate(file string, b []byte) error {
	if _, err := readPrivate(file, 65536); err != nil {
		return errors.New("refusing to replace an unverified installation file")
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".wrctl-write-")
	if err != nil {
		return errors.New("cannot prepare installation update")
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot write installation update")
	}
	if os.Rename(f.Name(), file) != nil {
		return errors.New("cannot commit installation update")
	}
	return syncDirectory(filepath.Dir(file))
}

func saveInstallation(dir string, s installation, fresh bool) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errors.New("cannot encode installation record")
	}
	b = append(b, '\n')
	file := filepath.Join(dir, "installation.json")
	if fresh {
		return writeExclusive(file, b)
	}
	return replacePrivate(file, b)
}

func verifyFiles(dir string, s installation) error {
	b, err := readPrivate(filepath.Join(dir, "compose.yaml"), 65536)
	if err != nil || digest(b) != s.ComposeSHA256 {
		return errors.New("generated Compose file changed; restore the recorded file before continuing")
	}
	if privateDirectory(filepath.Join(dir, "secrets"), false) != nil {
		return errors.New("matching private secret directory is required; credentials were not regenerated")
	}
	for _, name := range []string{"owner-password", "runtime-password"} {
		b, err := readPrivate(filepath.Join(dir, "secrets", name), 64)
		if err != nil || !secretPattern.Match(b) {
			return errors.New("matching private secret files are required; credentials were not regenerated")
		}
	}
	return nil
}

func newProject(existingVolumes []string) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var id [8]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", errors.New("secure random source unavailable")
		}
		project := "waiting-room-preview-" + hex.EncodeToString(id[:])
		collision := false
		for _, volume := range existingVolumes {
			if strings.HasPrefix(volume, project+"_") {
				collision = true
				break
			}
		}
		if !collision {
			return project, nil
		}
	}
	return "", errors.New("cannot allocate an isolated Docker project")
}

func prepareFiles(dir, image, totp, project string, compose []byte) (installation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return installation{}, errors.New("installation directory unavailable")
	}
	for _, entry := range entries {
		if entry.Name() != ".wrctl.lock" {
			return installation{}, errors.New("refusing to install into a nonempty unmanaged directory")
		}
	}
	if !projectPattern.MatchString(project) {
		return installation{}, errors.New("invalid isolated Docker project")
	}
	s := installation{SchemaVersion: 1, Project: project, Image: image, InitialTOTP: totp, Phase: "prepared", ComposeSHA256: digest(compose)}
	if err = privateDirectory(filepath.Join(dir, "secrets"), true); err != nil {
		return installation{}, err
	}
	for _, name := range []string{"owner-password", "runtime-password"} {
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return installation{}, errors.New("secure random source unavailable")
		}
		if err = writeExclusive(filepath.Join(dir, "secrets", name), []byte(hex.EncodeToString(secret[:]))); err != nil {
			return installation{}, err
		}
	}
	if err = writeExclusive(filepath.Join(dir, "compose.yaml"), compose); err != nil {
		return installation{}, err
	}
	if err = saveInstallation(dir, s, true); err != nil {
		return installation{}, err
	}
	return s, nil
}

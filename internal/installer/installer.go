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

	"waiting-room/deploy"
)

// Run performs only an explicitly selected lifecycle command. Output never
// contains Docker stderr, credentials, generated keys or bootstrap tokens except
// for the separately requested token/setup commands.
func Run(ctx context.Context, args []string, out, stderr io.Writer) int {
	base, _ := os.UserConfigDir()
	dir := ""
	if base != "" {
		dir = filepath.Join(base, "waiting-room")
	}
	o, err := parse(args, dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	e := engine{run: execDocker, out: out, environ: os.Environ()}
	if err = e.execute(ctx, o); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func selectedImage(requested string) (string, error) {
	if requested == "" {
		requested = DefaultImage
	}
	if !imagePattern.MatchString(requested) {
		return "", errors.New("this binary has no published runtime digest; use an official release or an explicit --image digest")
	}
	return requested, nil
}

func (e engine) execute(ctx context.Context, o options) error {
	if o.command == "restore" {
		return e.restore(ctx, o)
	}
	if _, err := os.Lstat(o.directory); errors.Is(err, os.ErrNotExist) && o.command == "install" {
		if _, err = selectedImage(o.image); err != nil {
			return err
		}
	}
	if err := privateDirectory(o.directory, o.command == "install"); err != nil {
		return err
	}
	unlock, err := lockDirectory(o.directory)
	if err != nil {
		return err
	}
	defer unlock()
	s, loadErr := loadInstallation(o.directory)
	fresh := errors.Is(loadErr, os.ErrNotExist)
	if loadErr != nil && !fresh {
		return errors.New("cannot verify the private installation record; no state was replaced")
	}
	if fresh && o.command != "install" {
		return errors.New("no managed installation exists; run wrctl install first")
	}
	if !fresh {
		if err := verifyFiles(o.directory, s); err != nil {
			return err
		}
	}
	if fresh {
		entries, err := os.ReadDir(o.directory)
		if err != nil {
			return errors.New("installation directory unavailable")
		}
		for _, entry := range entries {
			if entry.Name() != ".wrctl.lock" {
				return errors.New("refusing to install into a nonempty unmanaged directory")
			}
		}
	}
	arch, err := e.checkDocker(ctx)
	if err != nil {
		return err
	}
	if o.command == "install" {
		if !fresh && s.Phase != "prepared" {
			return errors.New("installation already exists; use wrctl up or wrctl upgrade")
		}
		if !fresh && ((o.image != "" && o.image != s.Image) || (o.totpExplicit && o.totp != s.InitialTOTP)) {
			return errors.New("an interrupted install must resume with its recorded image and TOTP policy")
		}
		image := s.Image
		if fresh {
			image, err = selectedImage(o.image)
			if err != nil {
				return err
			}
		}
		if err = e.verifyImage(ctx, image, arch, true); err != nil {
			return err
		}
		if fresh {
			b, err := e.docker(ctx, o.directory, image, "volume", "ls", "--format", "{{.Name}}")
			if err != nil {
				return errors.New("cannot check isolated Docker volume names")
			}
			project, err := newProject(strings.Fields(string(b)))
			if err != nil {
				return err
			}
			b, err = e.docker(ctx, o.directory, image, "ps", "--all", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.ID}}")
			if err != nil || len(bytes.TrimSpace(b)) != 0 {
				return errors.New("cannot confirm an unused isolated Docker project; no installation files were created")
			}
			s, err = prepareFiles(o.directory, image, o.totp, project, deploy.PreviewCompose)
			if err != nil {
				return err
			}
		}
		if err = e.initialize(ctx, o.directory, s, image, false); err != nil {
			return err
		}
		s.Phase = "ready"
		if err = saveInstallation(o.directory, s, false); err != nil {
			return err
		}
		_, err = fmt.Fprintln(e.out, "Local preview is running. Admin: https://127.0.0.1:19443\nNext: wrctl setup"+directoryHint(o.directory)+"\nNo administrator or bootstrap token was created automatically. This local preview is not production or 10K/100K qualification.")
		return err
	}
	if o.command == "status" {
		return e.status(ctx, o, s)
	}
	if o.command == "stop" {
		return e.step(ctx, o.directory, s, s.Image, "Stopping this installation", "stop")
	}
	if o.command != "upgrade" && s.Phase != "ready" {
		return errors.New("installation has an incomplete install or upgrade; retry that command before starting services")
	}
	switch o.command {
	case "keys-stage", "keys-activate", "keys-retire", "keys-revoke", "keys-status":
		return e.rotateKeys(ctx, o.directory, s, o.command)
	case "backup":
		if err = e.verifyImage(ctx, s.Image, arch, false); err != nil {
			return err
		}
		return e.backup(ctx, o.directory, s, o.backupDirectory, s.Image, arch)
	case "up":
		if err = e.verifyImage(ctx, s.Image, arch, false); err != nil {
			return err
		}
		if err = e.step(ctx, o.directory, s, s.Image, "Checking pinned dependency images", "pull", "--policy", "missing", "postgres", "valkey"); err != nil {
			return err
		}
		return e.start(ctx, o.directory, s, s.Image)
	case "upgrade":
		if s.Phase == "prepared" {
			return errors.New("finish the initial install before upgrading")
		}
		image := o.image
		if image == "" && s.PendingImage != "" {
			image = s.PendingImage
		}
		image, err = selectedImage(image)
		if err != nil {
			return err
		}
		if s.PendingImage != "" && image != s.PendingImage {
			return errors.New("retry the recorded pending image; automatic rollback after migrations is not supported")
		}
		if err = e.verifyImage(ctx, image, arch, true); err != nil {
			return err
		}
		// Pull first: network/metadata failure must not stop the running install.
		if err = e.step(ctx, o.directory, s, image, "Pulling pinned dependency images", "pull", "postgres", "valkey"); err != nil {
			return err
		}
		if s.Phase == "ready" {
			dest := filepath.Join(o.directory, "backups", time.Now().UTC().Format("20060102T150405.000000000Z"))
			if err = e.backup(ctx, o.directory, s, dest, image, arch); err != nil {
				return err
			}
		}
		s.PendingImage, s.Phase = image, "upgrading"
		if err = saveInstallation(o.directory, s, false); err != nil {
			return err
		}
		if err = e.initialize(ctx, o.directory, s, image, true); err != nil {
			return err
		}
		s.PreviousImage, s.Image, s.PendingImage, s.Phase = s.Image, image, "", "ready"
		if err = saveInstallation(o.directory, s, false); err != nil {
			return err
		}
		_, err = fmt.Fprintln(e.out, "Upgrade completed and services are healthy. Existing volumes, secrets, accounts, TOTP policy and queue data were retained.")
		return err
	case "bootstrap":
		return e.step(ctx, o.directory, s, s.Image, "Issuing a 15-minute first-admin token", "run", "--rm", "--no-deps", "--pull", "never", "bootstrap")
	case "token":
		return e.printToken(ctx, o.directory, s)
	case "setup":
		return e.setup(ctx, o.directory, s)
	}
	return errors.New("unsupported runtime command")
}

func directoryHint(dir string) string {
	// A shell-quoted local path only; no user input is interpolated into commands.
	return " --directory '" + strings.ReplaceAll(dir, "'", "'\\''") + "'"
}

func (e engine) initialize(ctx context.Context, dir string, s installation, image string, upgrade bool) error {
	if !upgrade {
		if err := e.step(ctx, dir, s, image, "Pulling pinned dependency images", "pull", "postgres", "valkey"); err != nil {
			return err
		}
	} else {
		if err := e.step(ctx, dir, s, s.Image, "Stopping application roles for the upgrade", "stop", "control", "gateway", "coordinator", "demo-origin", "valkey"); err != nil {
			return err
		}
	}
	if err := e.step(ctx, dir, s, image, "Starting PostgreSQL", "up", "-d", "--wait", "--wait-timeout", "180", "--no-build", "--pull", "never", "postgres"); err != nil {
		return err
	}
	args := []string{"run", "--rm", "--no-deps", "--pull", "never", "initialize"}
	if upgrade {
		args = append(args, "upgrade")
	} else {
		args = append(args, "init", s.InitialTOTP)
	}
	if err := e.step(ctx, dir, s, image, "Verifying installation identity and database migrations", args...); err != nil {
		return err
	}
	if err := e.step(ctx, dir, s, image, "Starting Valkey", "up", "-d", "--wait", "--wait-timeout", "180", "--no-build", "--pull", "never", "valkey"); err != nil {
		return err
	}
	queueArgs := []string{"run", "--rm", "--no-deps", "--pull", "never", "queue-initialize"}
	if upgrade {
		queueArgs = append(queueArgs, "queue-upgrade")
	}
	if err := e.step(ctx, dir, s, image, "Verifying the persisted queue schema", queueArgs...); err != nil {
		return err
	}
	return e.start(ctx, dir, s, image)
}

func (e engine) start(ctx context.Context, dir string, s installation, image string) error {
	err := e.step(ctx, dir, s, image, "Starting application roles", "up", "-d", "--no-build", "--pull", "never", "control", "coordinator", "gateway", "demo-origin")
	if err == nil {
		_, err = fmt.Fprintln(e.out, "Waiting for all six services to be healthy. Coordinator recovery after a queue restart can take the longest configured ticket lifetime plus 30 seconds (up to about 62 minutes). Ctrl-C cancels safely.")
		if err == nil {
			err = e.waitReady(ctx, dir, s, image, 3*time.Second)
		}
	}
	if err == nil {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, stopErr := e.compose(cleanup, dir, s, image, "stop", "control", "gateway", "coordinator", "demo-origin"); stopErr != nil {
		return errors.New("runtime health checks and cleanup failed; inspect wrctl status before retrying; data and secrets were retained")
	}
	return errors.New("runtime health checks failed; application roles were stopped and data retained; retry the same install, up or upgrade command")
}

func (e engine) printToken(ctx context.Context, dir string, s installation) error {
	b, err := e.compose(ctx, dir, s, s.Image, "exec", "-T", "control", "/wr-control", "token")
	if err != nil || len(b) != 43 || strings.ContainsAny(string(b), "\r\n") {
		return errors.New("first-admin token unavailable; no token was printed")
	}
	for _, c := range b {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return errors.New("first-admin token unavailable; no token was printed")
		}
	}
	_, err = fmt.Fprintln(e.out, string(b))
	return err
}

type serviceStatus struct {
	Service string `json:"service"`
	State   string `json:"state"`
	Health  string `json:"health"`
}

func (e engine) status(ctx context.Context, o options, s installation) error {
	b, err := e.compose(ctx, o.directory, s, s.Image, "ps", "--all", "--format", "json")
	if err != nil {
		return errors.New("runtime status unavailable")
	}
	services, err := decodeServices(b)
	if err != nil {
		return err
	}
	if services == nil {
		services = []serviceStatus{}
	}
	result := struct {
		SchemaVersion int             `json:"schemaVersion"`
		Version       string          `json:"version"`
		Project       string          `json:"project"`
		Image         string          `json:"image"`
		PendingImage  string          `json:"pendingImage,omitempty"`
		Phase         string          `json:"phase"`
		Services      []serviceStatus `json:"services"`
	}{1, Version, s.Project, s.Image, s.PendingImage, s.Phase, services}
	if o.json {
		return json.NewEncoder(e.out).Encode(result)
	}
	if _, err = fmt.Fprintf(e.out, "Local preview: %s\nImage: %s\nPhase: %s\n", s.Project, s.Image, s.Phase); err != nil {
		return err
	}
	for _, service := range services {
		if _, err = fmt.Fprintf(e.out, "%s: %s %s\n", service.Service, service.State, service.Health); err != nil {
			return err
		}
	}
	return nil
}

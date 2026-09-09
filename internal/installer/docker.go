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
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type dockerRunner func(context.Context, string, []string, ...string) ([]byte, error)

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("command output limit reached")
	}
	return b.Buffer.Write(p)
}

func execDocker(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = time.Second
	out := &boundedOutput{limit: 1024 * 1024}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	if cmd.Run() != nil {
		return nil, errors.New("Docker command failed")
	}
	return out.Bytes(), nil
}

func dockerEnvironment(base []string, image string) []string {
	env := make([]string, 0, len(base)+2)
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "COMPOSE_") || key == "WR_RUNTIME_IMAGE" {
			continue
		}
		env = append(env, item)
	}
	return append(env, "COMPOSE_DISABLE_ENV_FILE=1", "WR_RUNTIME_IMAGE="+image)
}

type engine struct {
	run     dockerRunner
	out     io.Writer
	environ []string
}

func (e engine) docker(ctx context.Context, dir, image string, args ...string) ([]byte, error) {
	return e.run(ctx, dir, dockerEnvironment(e.environ, image), args...)
}

func (e engine) compose(ctx context.Context, dir string, s installation, image string, args ...string) ([]byte, error) {
	// Existing installations retain their checksummed Compose file, which may
	// predate the runtime drain budget. Give those containers the same grace.
	if len(args) > 0 && args[0] == "stop" {
		args = append([]string{"stop", "--timeout", "20"}, args[1:]...)
	}
	prefix := []string{"compose", "--project-name", s.Project, "--project-directory", dir, "--env-file", os.DevNull, "--file", filepath.Join(dir, "compose.yaml")}
	return e.docker(ctx, dir, image, append(prefix, args...)...)
}

func (e engine) step(ctx context.Context, dir string, s installation, image, label string, args ...string) error {
	if _, err := fmt.Fprintln(e.out, label); err != nil {
		return errors.New("cannot write runtime progress")
	}
	if _, err := e.compose(ctx, dir, s, image, args...); err != nil {
		return fmt.Errorf("%s failed; existing data and secrets were retained", label)
	}
	return nil
}

func (e engine) checkDocker(ctx context.Context) (string, error) {
	for _, item := range e.environ {
		if strings.HasPrefix(item, "DOCKER_HOST=") && !strings.HasPrefix(item, "DOCKER_HOST=unix://") && item != "DOCKER_HOST=" {
			return "", errors.New("only a local Unix-socket Docker daemon is supported")
		}
	}
	b, err := e.docker(ctx, "", "", "context", "inspect", "--format", `{{(index .Endpoints "docker").Host}}`)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(b)), "unix://") {
		return "", errors.New("a local Docker context is required")
	}
	b, err = e.docker(ctx, "", "", "info", "--format", "{{.OSType}}/{{.Architecture}}")
	if err != nil {
		return "", errors.New("Docker is unavailable; start a local Linux Docker daemon")
	}
	arch := strings.TrimSpace(string(b))
	switch arch {
	case "linux/amd64", "linux/x86_64":
		arch = "amd64"
	case "linux/arm64", "linux/aarch64":
		arch = "arm64"
	default:
		return "", errors.New("the runtime supports Linux amd64 or arm64 Docker engines")
	}
	b, err = e.docker(ctx, "", "", "compose", "version", "--short")
	if err != nil {
		return "", errors.New("Docker Compose 2.35 or newer is required")
	}
	m := regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.`).FindStringSubmatch(strings.TrimSpace(string(b)))
	if m == nil {
		return "", errors.New("cannot verify Docker Compose version")
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 2 || (major == 2 && minor < 35) {
		return "", errors.New("Docker Compose 2.35 or newer is required for private identity volume subpaths")
	}
	return arch, nil
}

type imageInfo struct {
	RepoDigests  []string `json:"RepoDigests"`
	OS           string   `json:"Os"`
	Architecture string   `json:"Architecture"`
	Config       struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (e engine) verifyImage(ctx context.Context, image, arch string, pull bool) error {
	if !imagePattern.MatchString(image) {
		return errors.New("a published official image digest is required; use a release wrctl or --image")
	}
	if _, err := fmt.Fprintln(e.out, "Verifying the pinned preview runtime image…"); err != nil {
		return errors.New("cannot write runtime progress")
	}
	var b []byte
	var err error
	if !pull {
		b, err = e.docker(ctx, "", image, "image", "inspect", image)
		pull = err != nil
	}
	if pull {
		if _, err = e.docker(ctx, "", image, "pull", image); err != nil {
			return errors.New("cannot pull the pinned runtime image; verify GHCR access and the published digest")
		}
		b, err = e.docker(ctx, "", image, "image", "inspect", image)
	}
	var images []imageInfo
	if err != nil || json.Unmarshal(b, &images) != nil || len(images) != 1 {
		return errors.New("cannot verify the runtime image metadata")
	}
	i := images[0]
	matched := false
	for _, candidate := range i.RepoDigests {
		if candidate == image {
			matched = true
		}
	}
	if !matched || i.OS != "linux" || i.Architecture != arch || i.Config.Labels["org.opencontainers.image.source"] != ImageSource ||
		i.Config.Labels["io.nudgeon.waiting-room.runtime-contract"] != "1" || !revisionPattern.MatchString(i.Config.Labels["org.opencontainers.image.revision"]) {
		return errors.New("runtime image digest, architecture, source or compatibility labels do not match; installation was not changed")
	}
	return nil
}

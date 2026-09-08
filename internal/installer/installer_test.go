// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"waiting-room/deploy"
	"waiting-room/internal/recoveryarchive"
)

var firstImage = ImageRepository + "@sha256:" + strings.Repeat("a", 64)
var nextImage = ImageRepository + "@sha256:" + strings.Repeat("b", 64)

type invocation struct {
	Dir       string
	Env, Args []string
}
type fakeDocker struct {
	calls       []invocation
	fail        string
	mutateImage func(*imageInfo)
	statuses    [][]byte
}

func (f *fakeDocker) run(_ context.Context, dir string, env []string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, invocation{dir, append([]string(nil), env...), append([]string(nil), args...)})
	joined := strings.Join(args, " ")
	if f.fail != "" && strings.Contains(joined, f.fail) {
		return nil, errors.New("DO-NOT-ECHO secret from Docker")
	}
	switch {
	case strings.HasPrefix(joined, "volume inspect "):
		var found []map[string]any
		for _, name := range args[2:] {
			part := strings.LastIndex(name, "_")
			found = append(found, map[string]any{"Name": name, "Labels": map[string]string{"com.docker.compose.project": name[:part], "com.docker.compose.volume": name[part+1:]}})
		}
		return json.Marshal(found)
	case strings.HasSuffix(joined, " archive-create"):
		dest := ""
		for _, a := range args {
			if strings.HasPrefix(a, "type=bind,src=") {
				dest = strings.TrimSuffix(strings.TrimPrefix(a, "type=bind,src="), ",dst=/backup")
			}
		}
		source, err := os.MkdirTemp(filepath.Dir(dest), "fixture-volumes-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(source)
		for _, v := range recoveryarchive.Volumes {
			os.Mkdir(filepath.Join(source, v), 0700)
		}
		_, err = recoveryarchive.Create(context.Background(), source, dest)
		return nil, err
	case strings.HasPrefix(joined, "context inspect"):
		return []byte("unix:///tmp/docker.sock\n"), nil
	case strings.HasPrefix(joined, "info "):
		return []byte("linux/amd64\n"), nil
	case joined == "compose version --short":
		return []byte("2.40.3-desktop.1\n"), nil
	case strings.HasPrefix(joined, "image inspect "):
		i := imageInfo{RepoDigests: []string{args[2]}, OS: "linux", Architecture: "amd64"}
		i.Config.Labels = map[string]string{"org.opencontainers.image.source": ImageSource, "org.opencontainers.image.revision": strings.Repeat("c", 40), "io.nudgeon.waiting-room.runtime-contract": "1"}
		if f.mutateImage != nil {
			f.mutateImage(&i)
		}
		return json.Marshal([]imageInfo{i})
	case strings.Contains(joined, "ps --all --format json"):
		if len(f.statuses) > 0 {
			result := f.statuses[0]
			if len(f.statuses) > 1 {
				f.statuses = f.statuses[1:]
			}
			return result, nil
		}
		return json.Marshal(healthyServices())
	default:
		return nil, nil
	}
}

func fixture(t *testing.T) (engine, *fakeDocker, *bytes.Buffer, string) {
	t.Helper()
	f := &fakeDocker{}
	out := &bytes.Buffer{}
	return engine{run: f.run, out: out, environ: []string{"PATH=/usr/bin", "COMPOSE_FILE=DO-NOT-ECHO", "COMPOSE_PROJECT_NAME=unrelated", "WR_RUNTIME_IMAGE=bad"}}, f, out, filepath.Join(t.TempDir(), "install")
}

func installFixture(t *testing.T) (engine, *fakeDocker, *bytes.Buffer, string, installation) {
	t.Helper()
	e, f, out, dir := fixture(t)
	if err := e.execute(context.Background(), options{command: "install", directory: dir, image: firstImage, totp: "off", totpExplicit: true}); err != nil {
		t.Fatal(err)
	}
	s, err := loadInstallation(dir)
	if err != nil {
		t.Fatal(err)
	}
	return e, f, out, dir, s
}

func TestOptionsRejectUnpinnedOrForeignImagesWithoutValues(t *testing.T) {
	for _, image := range []string{"latest", ImageRepository + ":preview", "ghcr.io/other/waiting-room@sha256:" + strings.Repeat("a", 64), firstImage + "\n", "DO-NOT-ECHO"} {
		_, err := parse([]string{"install", "--image", image}, "/tmp/wr")
		if err == nil || strings.Contains(err.Error(), "DO-NOT-ECHO") {
			t.Fatal("untrusted image accepted or echoed")
		}
	}
	for _, args := range [][]string{{"install", "--password", "DO-NOT-ECHO"}, {"up", "--image", firstImage}, {"install", "--image", firstImage, "--image", firstImage}, {"upgrade", "--totp", "off"}, {"install", "--totp", "maybe"}, {"up", "--json"}, {"install", "--directory"}} {
		if _, err := parse(args, "/tmp/wr"); err == nil || strings.Contains(err.Error(), "DO-NOT-ECHO") {
			t.Fatal(args, err)
		}
	}
	o, err := parse([]string{"status", "--json", "--directory", "/tmp/with spaces"}, "")
	if err != nil || !o.json || o.directory != "/tmp/with spaces" {
		t.Fatal(o, err)
	}
	if o, err = parse([]string{"install", "--image", firstImage}, "/tmp/wr"); err != nil || o.totp != "on" {
		t.Fatal(o, err)
	}
}

func TestFreshInstallUsesPrebuiltImagePrivateStateAndNoBootstrap(t *testing.T) {
	_, f, out, dir, s := installFixture(t)
	if s.Phase != "ready" || s.Image != firstImage || s.InitialTOTP != "off" || s.PendingImage != "" {
		t.Fatal(s)
	}
	if err := verifyFiles(dir, s); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"installation.json", "compose.yaml", "secrets/owner-password", "secrets/runtime-password"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(name, err)
		}
	}
	joined := ""
	for _, call := range f.calls {
		args := strings.Join(call.Args, " ")
		joined += args + "\n"
		if strings.Contains(args, "bootstrap") || strings.Contains(args, " token") || strings.Contains(args, " build") {
			t.Fatal("implicit bootstrap or build", args)
		}
		for _, item := range call.Env {
			if strings.Contains(item, "DO-NOT-ECHO") || item == "COMPOSE_PROJECT_NAME=unrelated" || item == "WR_RUNTIME_IMAGE=bad" {
				t.Fatal("environment injection", item)
			}
		}
		if strings.Contains(args, " up ") && (!strings.Contains(args, "--no-build") || !strings.Contains(args, "--pull never")) {
			t.Fatal("unbounded compose up", args)
		}
	}
	if !strings.Contains(joined, "initialize init off") || !strings.Contains(joined, "queue-initialize") || !strings.Contains(out.String(), "wrctl setup") {
		t.Fatal("incomplete install")
	}
	secret, _ := os.ReadFile(filepath.Join(dir, "secrets/owner-password"))
	if strings.Contains(joined+out.String(), string(secret)) {
		t.Fatal("secret exposed")
	}
}

func TestImageVerificationHappensBeforeInstallationFilesOrContainerChanges(t *testing.T) {
	for _, mutate := range []func(*imageInfo){
		func(i *imageInfo) { i.RepoDigests = []string{nextImage} },
		func(i *imageInfo) { i.OS = "windows" },
		func(i *imageInfo) { i.Architecture = "arm64" },
		func(i *imageInfo) { i.Config.Labels["org.opencontainers.image.source"] = "https://example.com" },
		func(i *imageInfo) { delete(i.Config.Labels, "org.opencontainers.image.revision") },
		func(i *imageInfo) { i.Config.Labels["io.nudgeon.waiting-room.runtime-contract"] = "2" },
	} {
		e, f, _, dir := fixture(t)
		f.mutateImage = mutate
		if err := e.execute(context.Background(), options{command: "install", directory: dir, image: firstImage, totp: "on"}); err == nil {
			t.Fatal("unverified image accepted")
		}
		if _, err := os.Stat(filepath.Join(dir, "secrets")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("secrets created before validation")
		}
		for _, call := range f.calls {
			if strings.Contains(strings.Join(call.Args, " "), "--project-name") {
				t.Fatal("containers touched before image verification")
			}
		}
	}
}

func TestInstallRefusesUnrelatedOrInsecureDirectories(t *testing.T) {
	e, f, _, dir := fixture(t)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("user data")
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.execute(context.Background(), options{command: "install", directory: dir, image: firstImage, totp: "on"}); err == nil {
		t.Fatal("unmanaged directory accepted")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if !bytes.Equal(b, original) || len(f.calls) != 0 {
		t.Fatal("unrelated state changed")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := e.execute(context.Background(), options{command: "install", directory: dir, image: firstImage, totp: "on"}); err == nil {
		t.Fatal("public directory accepted")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := e.execute(context.Background(), options{command: "install", directory: link, image: firstImage, totp: "on"}); err == nil {
		t.Fatal("symlink directory accepted")
	}
}

func TestUpgradeKeepsProjectSecretsAndCommitsOnlyAfterHealth(t *testing.T) {
	e, f, _, dir, before := installFixture(t)
	owner, _ := os.ReadFile(filepath.Join(dir, "secrets/owner-password"))
	runtime, _ := os.ReadFile(filepath.Join(dir, "secrets/runtime-password"))
	f.calls = nil
	if err := e.execute(context.Background(), options{command: "upgrade", directory: dir, image: nextImage}); err != nil {
		t.Fatal(err)
	}
	after, err := loadInstallation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after.Project != before.Project || after.Image != nextImage || after.PreviousImage != firstImage || after.InitialTOTP != "off" || after.Phase != "ready" || after.PendingImage != "" {
		t.Fatal(after)
	}
	actualOwner, _ := os.ReadFile(filepath.Join(dir, "secrets/owner-password"))
	actualRuntime, _ := os.ReadFile(filepath.Join(dir, "secrets/runtime-password"))
	if !bytes.Equal(owner, actualOwner) || !bytes.Equal(runtime, actualRuntime) {
		t.Fatal("credentials changed")
	}
	joined := ""
	for _, call := range f.calls {
		joined += strings.Join(call.Args, " ") + "\n"
	}
	if strings.Contains(joined, " init ") || strings.Contains(joined, " down") || strings.Contains(joined, "volume rm") || !strings.Contains(joined, "initialize upgrade") {
		t.Fatal("unsafe upgrade sequence", joined)
	}
	if strings.Index(joined, "pull "+nextImage) > strings.Index(joined, " stop ") {
		t.Fatal("stopped before image verification")
	}
}

func TestUpgradePullFailureLeavesRunningInstallRecordUntouched(t *testing.T) {
	e, f, out, dir, _ := installFixture(t)
	f.calls = nil
	out.Reset()
	f.fail = "pull " + nextImage
	before, _ := os.ReadFile(filepath.Join(dir, "installation.json"))
	err := e.execute(context.Background(), options{command: "upgrade", directory: dir, image: nextImage})
	if err == nil || strings.Contains(err.Error()+out.String(), "DO-NOT-ECHO") {
		t.Fatal("missing or unsanitized error", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "installation.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("record changed on pull failure")
	}
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call.Args, " "), " stop ") {
			t.Fatal("running services stopped on pull failure")
		}
	}
}

func TestInterruptedUpgradeBlocksUpAndRetriesRecordedImage(t *testing.T) {
	e, f, _, dir, before := installFixture(t)
	f.fail = "initialize upgrade"
	err := e.execute(context.Background(), options{command: "upgrade", directory: dir, image: nextImage})
	if err == nil {
		t.Fatal("migration failure ignored")
	}
	pending, err := loadInstallation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Phase != "upgrading" || pending.PendingImage != nextImage || pending.Image != before.Image {
		t.Fatal(pending)
	}
	f.fail = ""
	f.calls = nil
	if err = e.execute(context.Background(), options{command: "up", directory: dir}); err == nil {
		t.Fatal("old image restarted after uncertain migration")
	}
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call.Args, " "), " up ") {
			t.Fatal("blocked up reached compose")
		}
	}
	if err = e.execute(context.Background(), options{command: "upgrade", directory: dir, image: firstImage}); err == nil {
		t.Fatal("implicit rollback accepted")
	}
	if err = e.execute(context.Background(), options{command: "upgrade", directory: dir}); err != nil {
		t.Fatal(err)
	}
	after, _ := loadInstallation(dir)
	if after.Phase != "ready" || after.Image != nextImage || after.Project != before.Project {
		t.Fatal(after)
	}
}

func TestFailedHealthStopsApplicationsAndRetainsPendingUpgrade(t *testing.T) {
	e, f, _, dir, _ := installFixture(t)
	f.calls = nil
	f.fail = "--pull never control coordinator gateway demo-origin"
	err := e.execute(context.Background(), options{command: "upgrade", directory: dir, image: nextImage})
	if err == nil {
		t.Fatal("health failure ignored")
	}
	s, _ := loadInstallation(dir)
	if s.Phase != "upgrading" {
		t.Fatal(s)
	}
	last := strings.Join(f.calls[len(f.calls)-1].Args, " ")
	if !strings.HasSuffix(last, "stop control gateway coordinator demo-origin") {
		t.Fatal("partial new roles not stopped", last)
	}
}

func TestInterruptedInstallResumesWithoutChangingPolicyOrSecrets(t *testing.T) {
	e, f, _, dir := fixture(t)
	f.fail = "queue-initialize"
	err := e.execute(context.Background(), options{command: "install", directory: dir, image: firstImage, totp: "off", totpExplicit: true})
	if err == nil {
		t.Fatal("failure ignored")
	}
	s, _ := loadInstallation(dir)
	if s.Phase != "prepared" {
		t.Fatal(s)
	}
	secret, _ := os.ReadFile(filepath.Join(dir, "secrets/runtime-password"))
	f.fail = ""
	if err = e.execute(context.Background(), options{command: "install", directory: dir, image: nextImage, totp: "off"}); err == nil {
		t.Fatal("changed interrupted image accepted")
	}
	if err = e.execute(context.Background(), options{command: "install", directory: dir, totp: "on", totpExplicit: true}); err == nil {
		t.Fatal("changed interrupted policy accepted")
	}
	if err = e.execute(context.Background(), options{command: "install", directory: dir, totp: "on"}); err != nil {
		t.Fatal(err)
	}
	after, _ := loadInstallation(dir)
	actual, _ := os.ReadFile(filepath.Join(dir, "secrets/runtime-password"))
	if after.InitialTOTP != "off" || !bytes.Equal(secret, actual) {
		t.Fatal("retry regenerated state")
	}
}

func TestMissingSecretsAndChangedComposeNeverRegenerateOrRunDocker(t *testing.T) {
	for _, mode := range []string{"missing", "symlink", "compose"} {
		t.Run(mode, func(t *testing.T) {
			e, f, _, dir, _ := installFixture(t)
			f.calls = nil
			file := filepath.Join(dir, "secrets/runtime-password")
			if mode == "compose" {
				if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					if err := os.Symlink(filepath.Join(dir, "secrets/owner-password"), file); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := e.execute(context.Background(), options{command: "upgrade", directory: dir, image: nextImage}); err == nil {
				t.Fatal("damaged state accepted")
			}
			if len(f.calls) != 0 {
				t.Fatal("Docker called with damaged state")
			}
		})
	}
}

func TestStatusJSONOnlyExposesSelectedRuntimeFields(t *testing.T) {
	e, f, out, dir, s := installFixture(t)
	f.statuses = [][]byte{[]byte("{\"Service\":\"control\",\"State\":\"running\",\"Health\":\"healthy\",\"Command\":\"DO-NOT-ECHO\"}\n{\"Service\":\"valkey\",\"State\":\"running\",\"Health\":\"healthy\"}\n")}
	out.Reset()
	if err := e.execute(context.Background(), options{command: "status", directory: dir, json: true}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Project, Image, Phase string
		Services              []serviceStatus
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Project != s.Project || result.Image != firstImage || result.Phase != "ready" || len(result.Services) != 2 || strings.Contains(out.String(), "DO-NOT-ECHO") {
		t.Fatal(out.String())
	}
}

func TestRemoteDockerAndConcurrentOperationsAreRejected(t *testing.T) {
	e, f, _, dir := fixture(t)
	e.environ = []string{"DOCKER_HOST=ssh://DO-NOT-ECHO"}
	if err := e.execute(context.Background(), options{command: "install", directory: dir, image: firstImage, totp: "on"}); err == nil || strings.Contains(err.Error(), "DO-NOT-ECHO") {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal("remote Docker reached")
	}
	unlock, err := lockDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err = lockDirectory(dir); err == nil {
		t.Fatal("simultaneous mutation allowed")
	}
}

func TestTokenCommandDoesNotReflectUnexpectedOutput(t *testing.T) {
	s := installation{Project: "waiting-room-preview-0123456789abcdef", Image: firstImage}
	for _, value := range []string{"DO-NOT-ECHO", strings.Repeat("x", 43) + "\n", strings.Repeat("!", 43)} {
		out := &bytes.Buffer{}
		e := engine{out: out, run: func(context.Context, string, []string, ...string) ([]byte, error) { return []byte(value), nil }}
		if err := e.printToken(context.Background(), t.TempDir(), s); err == nil || out.Len() != 0 {
			t.Fatal("unexpected token reflected")
		}
	}
}

func TestExecDockerSubprocessErrorsAreBoundedAndNotReflected(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "docker")
	t.Setenv("PATH", dir)
	if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf 'DO-NOT-ECHO' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := execDocker(context.Background(), dir, []string{"PATH=" + dir}, "info")
	if err == nil || len(b) != 0 || strings.Contains(err.Error(), "DO-NOT-ECHO") {
		t.Fatal("subprocess details leaked", err)
	}
	if err := os.WriteFile(file, []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err = execDocker(ctx, dir, []string{"PATH=" + dir}, "info"); err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("subprocess cancellation unbounded", err)
	}
}

func TestEmbeddedComposeMatchesExistingIsolationWithoutBuildOrHostSecrets(t *testing.T) {
	b, err := os.ReadFile("../../deploy/compose/local-beta.yaml")
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.ReplaceAll(string(b), "# Persistent local control developer preview. This is not a Beta GO release.", "# Prebuilt local runtime preview. This is not a production or capacity qualification.")
	expected = strings.ReplaceAll(expected, "name: waiting-room-local-beta\n", "")
	expected = strings.ReplaceAll(expected, "waiting-room-local-control:dev", "${WR_RUNTIME_IMAGE:?A pinned runtime image is required}")
	expected = strings.ReplaceAll(expected, "    build:\n      context: ../..\n      dockerfile: deploy/docker/control.Dockerfile\n", "")
	expected = strings.ReplaceAll(expected, "${WR_LOCAL_BETA_SECRET_DIR:-../../.cache/local-beta/secrets}", "./secrets")
	expected = strings.ReplaceAll(expected, "waiting-room.scope: local-beta", "waiting-room.scope: prebuilt-local-preview")
	if !reflect.DeepEqual([]byte(expected), deploy.PreviewCompose) {
		t.Fatal("prebuilt compose isolation drifted from the verified local runtime")
	}
}

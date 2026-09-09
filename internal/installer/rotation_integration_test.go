//go:build integration

// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This opt-in regression operates only on a prepared copy of a failed local
// fixture. Source volumes must be mounted read-only during preparation; this
// test refuses original volumes and never removes the preserved copy.
func TestKeyRotationFromPreservedRecoveryFixture(t *testing.T) {
	dir, project := os.Getenv("WR_TEST_KEY_RECOVERY_DIRECTORY"), os.Getenv("WR_TEST_KEY_RECOVERY_PROJECT")
	if dir == "" && project == "" {
		t.Skip("explicit isolated copy of a failed key-rotation fixture required")
	}
	if !regexp.MustCompile(`^waiting-room-key-recovery-test-[a-f0-9]{8}$`).MatchString(project) || !strings.HasPrefix(filepath.Base(dir), project+"-") {
		t.Fatal("only a dedicated key recovery fixture is allowed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e := engine{run: execDocker, out: os.Stdout, environ: os.Environ()}
	s := installation{Project: project, Image: "waiting-room-beta4-patched:local"}
	raw, err := e.compose(ctx, dir, s, s.Image, "config", "--format", "json")
	var config struct {
		Volumes map[string]struct{ Name string }
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || len(config.Volumes) != 7 {
		t.Fatal("cannot verify copied fixture volumes")
	}
	for name, volume := range config.Volumes {
		if volume.Name != project+"_"+name {
			t.Fatal("fixture references a volume outside its project")
		}
		raw, err = e.docker(ctx, dir, s.Image, "volume", "inspect", volume.Name, "--format", "{{json .Labels}}")
		var labels map[string]string
		if err != nil || json.Unmarshal(raw, &labels) != nil || labels["com.docker.compose.project"] != project || !strings.HasPrefix(labels["waiting-room.forensic-source"], "waiting-room-traffic-test-") {
			t.Fatal("fixture copy ownership is not verified")
		}
	}
	before, err := e.keyReport(ctx, dir, s)
	if err != nil || before.Phase != "staged" || before.Acknowledged >= 2 {
		t.Fatal("fixture must retain a staged key operation with incomplete ACKs")
	}
	started := time.Now()
	if err = e.rotateKeys(ctx, dir, s, "keys-stage"); err != nil {
		t.Fatal(err)
	}
	after, err := e.keyReport(ctx, dir, s)
	if err != nil || after.Acknowledged != 2 || after.Generation != before.Generation || after.Digest != before.Digest || after.Phase != before.Phase {
		t.Fatal("retry changed the staged keys or did not receive both actual role ACKs")
	}
	if time.Since(started) < 30*time.Second {
		t.Fatal("fixture did not exercise the previous premature ACK timeout")
	}
	t.Logf("same staged generation %d acknowledged by both roles after %.1f seconds; original and copied volumes retained", after.Generation, time.Since(started).Seconds())
}

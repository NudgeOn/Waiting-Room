// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"waiting-room/internal/localcontrol"
)

func (e engine) keyReport(ctx context.Context, dir string, s installation) (localcontrol.RotationReport, error) {
	raw, err := e.compose(ctx, dir, s, s.Image, "run", "--rm", "--no-deps", "--pull", "never", "initialize", "keys-status")
	var report localcontrol.RotationReport
	if err != nil {
		return report, errors.New("cannot read key rotation status; upgrade this installation to the matching source first")
	}
	// Compose may prepend progress on stderr; only a complete JSON report line is accepted.
	for _, line := range strings.Split(string(raw), "\n") {
		if json.Unmarshal([]byte(line), &report) == nil && report.Phase != "" {
			return report, nil
		}
	}
	return report, errors.New("key rotation status unavailable")
}
func (e engine) rotateKeys(ctx context.Context, dir string, s installation, command string) error {
	report, err := e.keyReport(ctx, dir, s)
	if err != nil {
		return err
	}
	if command == "keys-status" {
		return json.NewEncoder(e.out).Encode(report)
	}
	if command == "keys-activate" && report.Phase != "active" && (report.Phase != "staged" || report.Acknowledged != 2) {
		return errors.New("run keys-stage and wait for both role ACKs before activation")
	}
	if command == "keys-revoke" && report.Phase != "stable" && !report.EmergencyReady {
		return errors.New("keys-revoke requires a fresh Admin reauthenticated installation-wide new epoch, followed by both role ACKs")
	}
	if command == "keys-retire" && report.Phase != "stable" && (report.Phase != "active" || report.Acknowledged != 2 || time.Now().UnixMilli() < report.RetireAfter) {
		return errors.New("previous keys must remain until both role ACKs and retireAfter; keys-status shows the deadline")
	}
	if command == "keys-stage" && report.Phase == "active" {
		return errors.New("retire the previous generation before starting another rotation")
	}
	// The managed profile supports exactly one signer and one Gateway. Refuse
	// unexpected replicas; recording a cutoff while an old signer lives is unsafe.
	raw, err := e.compose(ctx, dir, s, s.Image, "ps", "--all", "--format", "json")
	services, decodeErr := decodeServices(raw)
	if err != nil || decodeErr != nil {
		return errors.New("cannot verify single-replica deployment")
	}
	counts := map[string]int{}
	for _, service := range services {
		counts[service.Service]++
	}
	for _, role := range []string{"control", "gateway", "coordinator"} {
		if counts[role] != 1 {
			return errors.New("key rotation requires the managed single-replica local profile")
		}
	}
	if err = e.step(ctx, dir, s, s.Image, "Stopping application roles for a stable signing cutoff", "stop", "control", "gateway", "coordinator"); err != nil {
		return err
	}
	raw, err = e.compose(ctx, dir, s, s.Image, "ps", "--status", "running", "--services")
	if err != nil {
		return errors.New("cannot confirm stopped signers")
	}
	for _, service := range strings.Fields(string(raw)) {
		if service == "control" || service == "gateway" || service == "coordinator" {
			return errors.New("old signing roles are still running")
		}
	}
	if err = e.step(ctx, dir, s, s.Image, "Applying the durable key operation", "run", "--rm", "--no-deps", "--pull", "never", "initialize", command); err != nil {
		return errors.New("key operation incomplete; application roles remain stopped; retry the same command")
	}
	if err = e.step(ctx, dir, s, s.Image, "Starting roles with the recorded key generation", "up", "-d", "--no-build", "--pull", "never", "control", "coordinator", "gateway"); err != nil {
		return err
	}
	// Stopping a signer can leave an uncertain in-flight queue write. The
	// persisted safety hold blocks Configure (and thus the new key ACK) until
	// the longest issued lease plus its margin has elapsed and validation passes.
	// Use the same bounded readiness gate as startup before timing ACK delivery.
	if _, err = fmt.Fprintln(e.out, "Waiting for runtime recovery before key ACKs. Coordinator safety checks can take up to about 62 minutes; Ctrl-C cancels safely and preserves the recorded key operation."); err != nil {
		return err
	}
	if err = e.waitReady(ctx, dir, s, s.Image, time.Second); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("roles started but runtime recovery is not ready; data and recorded key operation retained; inspect status and keys-status before retrying the same command")
	}
	ackContext, cancelACK := context.WithTimeout(ctx, 30*time.Second)
	defer cancelACK()
	for {
		report, err = e.keyReport(ackContext, dir, s)
		if err == nil && report.Acknowledged == 2 {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ackContext.Err() != nil {
			return errors.New("roles started but key ACKs remain pending; inspect keys-status before the next phase")
		}
		select {
		case <-ackContext.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("roles started but key ACKs remain pending; inspect keys-status before the next phase")
		case <-time.After(time.Second):
		}
	}
	if err = json.NewEncoder(e.out).Encode(report); err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.out, "Key operation complete. Both roles acknowledged the recorded generation; any existing queue recovery hold remains in effect.")
	return err
}

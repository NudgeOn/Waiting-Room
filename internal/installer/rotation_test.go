// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/localcontrol"
)

func TestKeyRotationRequiresACKStoppedSignersAndTTL(t *testing.T) {
	for _, tc := range []struct {
		name, command, phase   string
		acks                   int
		deadline               int64
		failRunning, wantError bool
	}{
		{name: "stage", command: "keys-stage", phase: "legacy"},
		{name: "missing-ack", command: "keys-activate", phase: "staged", acks: 1, wantError: true},
		{name: "activate", command: "keys-activate", phase: "staged", acks: 2},
		{name: "running-signer", command: "keys-activate", phase: "staged", acks: 2, failRunning: true, wantError: true},
		{name: "live-artifacts", command: "keys-retire", phase: "active", acks: 2, deadline: time.Now().Add(time.Hour).UnixMilli(), wantError: true},
		{name: "retire", command: "keys-retire", phase: "active", acks: 2, deadline: time.Now().Add(-time.Minute).UnixMilli()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f, _, dir, s := installFixture(t)
			f.calls = nil
			original := e.run
			stopped, mutated := false, false
			report := localcontrol.RotationReport{Phase: tc.phase, Acknowledged: tc.acks, RetireAfter: tc.deadline}
			e.run = func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.HasSuffix(joined, " keys-status") {
					return json.Marshal(report)
				}
				if strings.HasSuffix(joined, " stop control gateway coordinator") {
					stopped = true
					return nil, nil
				}
				if strings.HasSuffix(joined, " ps --status running --services") {
					if tc.failRunning {
						return []byte("coordinator\n"), nil
					}
					return []byte("postgres\nvalkey\ndemo-origin\n"), nil
				}
				if strings.HasSuffix(joined, " "+tc.command) {
					if !stopped {
						return nil, errors.New("signers were not stopped")
					}
					mutated = true
					report.Acknowledged = 2
					return nil, nil
				}
				return original(ctx, dir, env, args...)
			}
			err := e.rotateKeys(context.Background(), dir, s, tc.command)
			if (err != nil) != tc.wantError {
				t.Fatal("result", err)
			}
			if tc.wantError && mutated {
				t.Fatal("mutation passed a rejected precondition")
			}
			if !tc.wantError && (!mutated || !stopped) {
				t.Fatal("operation skipped")
			}
		})
	}
}

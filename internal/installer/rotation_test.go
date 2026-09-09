// SPDX-License-Identifier: Apache-2.0
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
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
				if strings.HasSuffix(joined, " stop --timeout 20 control gateway coordinator") {
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

func TestKeyRotationWaitsForRecoveryBeforeTimingACKs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cancel  bool
		dead    bool
		missing bool
		blocked bool
	}{
		{name: "150-second-safety-hold"},
		{name: "cancel-during-hold", cancel: true},
		{name: "exited-coordinator", dead: true},
		{name: "healthy-without-key-ack", missing: true},
		{name: "blocked-key-status", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, _, out, dir, s := installFixture(t)
				original := e.run
				var started time.Time
				mutations, healthReads := 0, 0
				e.run = func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
					joined := strings.Join(args, " ")
					switch {
					case strings.HasSuffix(joined, " keys-status"):
						if tc.blocked && !started.IsZero() && time.Since(started) >= 150*time.Second {
							<-ctx.Done()
							return nil, ctx.Err()
						}
						report := localcontrol.RotationReport{Phase: "legacy"}
						if mutations > 0 {
							report.Phase, report.Generation = "staged", 1
							if !started.IsZero() && time.Since(started) >= 150*time.Second && !tc.missing {
								report.Acknowledged = 2
							}
						}
						return json.Marshal(report)
					case strings.HasSuffix(joined, " keys-stage"):
						mutations++
						return nil, nil
					case strings.HasSuffix(joined, " ps --status running --services"):
						return []byte("postgres\nvalkey\ndemo-origin\n"), nil
					case strings.HasSuffix(joined, " up -d --no-build --pull never control coordinator gateway"):
						started = time.Now()
						return nil, nil
					case strings.HasSuffix(joined, " ps --all --format json") && !started.IsZero():
						healthReads++
						services := healthyServices()
						if time.Since(started) < 150*time.Second {
							services[5].Health = "unhealthy"
						}
						if tc.dead {
							services[5].State = "exited"
						}
						return json.Marshal(services)
					case strings.Contains(joined, " down") || strings.Contains(joined, " rm "):
						t.Fatal("key retry removed preserved deployment data")
					}
					return original(ctx, dir, env, args...)
				}
				ctx := context.Background()
				if tc.cancel {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
					defer cancel()
				}
				err := e.rotateKeys(ctx, dir, s, "keys-stage")
				if mutations != 1 || healthReads == 0 {
					t.Fatal("operation or readiness gate skipped/repeated", mutations, healthReads, err)
				}
				if tc.cancel {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("hold cancellation not preserved", err)
					}
				} else if tc.dead || tc.missing || tc.blocked {
					if err == nil {
						t.Fatal("incomplete recovery/key ACK reported success")
					}
					if !tc.dead && time.Since(started) > 180*time.Second {
						t.Fatal("key status query exceeded its 30-second budget after readiness")
					}
				} else if err != nil || time.Since(started) < 150*time.Second {
					t.Fatal("safety hold incorrectly timed as failed ACK delivery", err)
				}
				if strings.Contains(out.String(), "Key operation complete") != (err == nil) {
					t.Fatal("completion output disagrees with readiness and ACKs")
				}
			})
		})
	}
}

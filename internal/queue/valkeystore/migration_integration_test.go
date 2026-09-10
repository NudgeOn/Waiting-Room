//go:build integration

// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"fmt"
	valkey "github.com/valkey-io/valkey-go"
	"reflect"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

func migrationFixture(t *testing.T, version int) (*Store, string, model.Config) {
	t.Helper()
	ctx := context.Background()
	config := model.DefaultConfig()
	config.AdmissionTTL = 60000
	config.ReadyTTL = 60000
	s, ns := runtimeStore(t, config)
	if version == 3 {
		if err := s.client.Do(ctx, s.client.B().FunctionLoad().FunctionCode(runtimeLibrary).Build()).Error(); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatal(err)
		}
		if err := s.client.Do(ctx, s.client.B().Del().Key(s.keys...).Build()).Error(); err != nil {
			t.Fatal(err)
		}
		c, _ := json.Marshal(config)
		g, _ := json.Marshal(StandardInstallation())
		if err := s.client.Do(ctx, s.client.B().Fcall().Function("wr_r3_init").Numkeys(11).Key(s.keys...).Arg(string(c), s.primary, string(g), "abcdefghijklmnopqrst").Build()).Error(); err != nil {
			t.Fatal(err)
		}
	}
	if err := InstallRecoveryLibrary(ctx, s.client); err != nil {
		t.Fatal(err)
	}
	if err := InstallMigrationLibrary(ctx, s.client); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}})
		if err != nil {
			t.Error(err)
			return
		}
		defer owner.Close()
		_ = owner.Do(ctx, owner.B().Del().Key("wr:maintenance:"+strings.TrimPrefix(ns, "wr:"), ns+"{epoch:1}:meta").Build()).Error()
	})
	return s, ns, config
}
func legacyJoin(t *testing.T, s *Store, version int, name string) Result {
	t.Helper()
	var raw string
	var err error
	if version == 3 {
		raw, err = s.client.Do(context.Background(), s.client.B().Fcall().Function("wr_r3_command").Numkeys(11).Key(s.keys...).Arg("join", Hash(name), Hash("/shop"), Hash(name), "opaque-original-replay").Build()).ToString()
	} else {
		return add(t, s, name)
	}
	var out Result
	if err != nil || json.Unmarshal([]byte(raw), &out) != nil {
		t.Fatal("source join", err)
	}
	return out
}
func TestRuntimeMigrationV3V4PreservesRecordsAndFencesOldWriters(t *testing.T) {
	for _, version := range []int{3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ctx := context.Background()
			s, ns, c := migrationFixture(t, version)
			first := legacyJoin(t, s, version, "first")
			second := legacyJoin(t, s, version, "second")
			plan, err := InspectRuntimeMigration(ctx, s.client, ns)
			if err != nil || plan.From != version || plan.State != "prepared" || plan.RetainedVisitors != 2 {
				t.Fatal("inspect", plan.State, err)
			}
			before := map[string]string{}
			for i, key := range s.keys {
				if i == 0 || i == 8 {
					continue
				}
				raw, err := s.client.Do(ctx, s.client.B().Dump().Key(key).Build()).ToString()
				if err == nil {
					before[key] = raw
				}
			}
			out, err := ApplyRuntimeMigration(ctx, s.client, ns, plan.Digest)
			if err != nil || out.State != "recovery_hold" || out.UnsafeUntil < time.Now().Add(89*time.Second).UnixMilli() {
				t.Fatal("staged migration", out.State, err)
			}
			for key, original := range before {
				actual, err := s.client.Do(ctx, s.client.B().Dump().Key(key).Build()).ToString()
				if err != nil || actual != original {
					t.Fatal("ticket/index/replay changed", err)
				}
			}
			again, err := ApplyRuntimeMigration(ctx, s.client, ns, plan.Digest)
			if err != nil || !reflect.DeepEqual(again, out) {
				t.Fatal("migration receipt changed", err)
			}
			if version == 3 {
				if err = s.client.Do(ctx, s.client.B().Fcall().Function("wr_r3_command").Numkeys(11).Key(s.keys...).Arg("join", Hash("late"), Hash("/shop"), Hash("late"), "opaque").Build()).Error(); err == nil {
					t.Fatal("old writer survived")
				}
			} else if _, err = s.Promote(ctx, 1); err == nil {
				t.Fatal("old v4 writer survived")
			}
			upgraded, err := OpenRecoveryRoom(ctx, valkey.ClientOption{InitAddress: []string{runtimeTestAddress(t)}}, ns, "abcdefghijklmnopqrst", c, StandardInstallation(), 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			if _, err = upgraded.Promote(ctx, 1); err != model.ErrUnavailable {
				t.Fatal("migration bypassed safety window", err)
			}
			// Only the empty-admission test fixture's clock gate is advanced. No real
			// admission is shortened. Full invariants still run over both retained rows.
			if err = s.client.Do(ctx, s.client.B().Hset().Key(s.keys[8]).FieldValue().FieldValue("unsafeUntil", fmt.Sprint(upgraded.recoverySnapshot.Load().Now-1)).Build()).Error(); err != nil {
				t.Fatal(err)
			}
			var recovered RecoveryState
			for range 8 {
				recovered, err = upgraded.MaintainRecovery(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if recovered.Mode == "ACTIVE" {
					break
				}
			}
			if recovered.Mode != "ACTIVE" {
				t.Fatal("migration validation", recovered)
			}
			if _, err = upgraded.Configure(ctx, c, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
				t.Fatal(err)
			}
			promoted, err := upgraded.Promote(ctx, 2)
			if err != nil || len(promoted.Tickets) != 2 || promoted.Tickets[0].ID != first.Ticket.ID || promoted.Tickets[1].ID != second.Ticket.ID {
				t.Fatal("FIFO after migration", err)
			}
		})
	}
}
func TestRuntimeMigrationStalePlanAndMissingIndexAreReadOnly(t *testing.T) {
	ctx := context.Background()
	s, ns, _ := migrationFixture(t, 3)
	first := legacyJoin(t, s, 3, "first")
	plan, err := InspectRuntimeMigration(ctx, s.client, ns)
	if err != nil {
		t.Fatal(err)
	}
	legacyJoin(t, s, 3, "concurrent")
	if _, err = ApplyRuntimeMigration(ctx, s.client, ns, plan.Digest); err == nil {
		t.Fatal("stale plan accepted")
	}
	if err = s.client.Do(ctx, s.client.B().Zrem().Key(s.keys[3]).Member(first.Ticket.ID).Build()).Error(); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectRuntimeMigration(ctx, s.client, ns); err == nil {
		t.Fatal("missing index accepted")
	}
	schema, err := s.client.Do(ctx, s.client.B().Hget().Key(s.keys[8]).Field("schema").Build()).ToString()
	if err != nil || schema != "3" {
		t.Fatal("failed preflight changed source", err)
	}
}

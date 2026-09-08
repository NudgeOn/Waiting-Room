// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	valkey "github.com/valkey-io/valkey-go"
	"regexp"
	"sort"
	"strings"
	"waiting-room/internal/queue/model"
)

//go:embed migration_v1.lua
var migrationLibrary string

type MigrationPlan struct {
	State               string   `json:"state"`
	From                int      `json:"from"`
	To                  int      `json:"to"`
	Rooms               []string `json:"rooms"`
	RetainedVisitors    int      `json:"retainedVisitors"`
	RetainedIdempotency int      `json:"retainedIdempotency"`
	MinimumHoldMillis   int64    `json:"minimumHoldMillis"`
	UnsafeUntil         int64    `json:"unsafeUntil,omitempty"`
	Digest              string   `json:"digest,omitempty"`
	snapshot            string
	retainedUntil       int64
	keys                []string
}

func InstallMigrationLibrary(ctx context.Context, c valkey.Client) error {
	err := c.Do(ctx, c.B().FunctionLoad().FunctionCode(migrationLibrary).Build()).Error()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	entries, err := c.Do(ctx, c.B().FunctionList().Libraryname("wr_queue_migration_v1").Withcode().Build()).ToArray()
	if err != nil || len(entries) != 1 {
		return ErrSchema
	}
	fields, err := entries[0].AsMap()
	if err != nil {
		return ErrSchema
	}
	code := fields["library_code"]
	actual, err := code.ToString()
	if err != nil || actual != migrationLibrary {
		return ErrSchema
	}
	return nil
}
func InspectRuntimeMigration(ctx context.Context, c valkey.Client, namespace string) (MigrationPlan, error) {
	if !regexp.MustCompile(`^wr:(runtime|lab):[a-zA-Z0-9_-]{1,80}$`).MatchString(namespace) {
		return MigrationPlan{}, ErrSchema
	}
	meta := namespace + "{installation:1}:meta"
	fields, err := c.Do(ctx, c.B().Hgetall().Key(meta).Build()).AsStrMap()
	if err != nil {
		return MigrationPlan{}, ErrSchema
	}
	rooms := []string{}
	for field := range fields {
		if strings.HasPrefix(field, "room:") {
			room := strings.TrimPrefix(field, "room:")
			if !regexp.MustCompile(`^[a-z2-7]{20}$`).MatchString(room) {
				return MigrationPlan{}, ErrSchema
			}
			rooms = append(rooms, room)
		}
	}
	if len(rooms) > 100 {
		return MigrationPlan{}, ErrSchema
	}
	sort.Strings(rooms)
	keys := []string{meta, namespace + "{installation:1}:visitors", namespace + "{installation:1}:idempotency"}
	for _, room := range rooms {
		for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
			keys = append(keys, namespace+"{"+room+":1}:"+suffix)
		}
	}
	keys = append(keys, "wr:maintenance:"+strings.TrimPrefix(namespace, "wr:"))
	read := func() (MigrationPlan, error) {
		raw, err := c.Do(ctx, c.B().FcallRo().Function("wr_qm_plan").Numkeys(int64(len(keys))).Key(keys...).Build()).ToString()
		if err != nil {
			return MigrationPlan{}, ErrSchema
		}
		// Lua empty arrays encode as objects only for empty/current states.
		var wire struct {
			State    string          `json:"state"`
			Rooms    json.RawMessage `json:"rooms"`
			Snapshot string          `json:"snapshot"`
		}
		if json.Unmarshal([]byte(raw), &wire) != nil {
			return MigrationPlan{}, ErrSchema
		}
		if string(wire.Rooms) == "{}" {
			raw = strings.Replace(raw, `"rooms":{}`, `"rooms":[]`, 1)
		}
		var plan MigrationPlan
		if json.Unmarshal([]byte(raw), &plan) != nil {
			return plan, ErrSchema
		}
		plan.snapshot = wire.Snapshot
		plan.keys = keys
		plan.Digest = fmt.Sprintf("%x", sha256.Sum256([]byte(wire.Snapshot)))
		return plan, nil
	}
	plan, err := read()
	if err != nil || plan.State != "prepared" {
		return plan, err
	}
	retained := int64(0)
	for first := 3; first < len(keys)-1; first += 8 {
		cursor := uint64(0)
		count := 0
		for {
			page, err := c.Do(ctx, c.B().Hscan().Key(keys[first+1]).Cursor(cursor).Count(128).Build()).AsScanEntry()
			if err != nil || len(page.Elements)%2 != 0 {
				return MigrationPlan{}, ErrSchema
			}
			for i := 0; i < len(page.Elements); i += 2 {
				count++
				if count > 100000 || len(page.Elements[i+1]) > 4096 {
					return MigrationPlan{}, ErrSchema
				}
				var ticket model.Ticket
				if json.Unmarshal([]byte(page.Elements[i+1]), &ticket) != nil || ticket.ID != page.Elements[i] || ticket.Epoch != 1 || ticket.AdmissionUntil < 0 || ticket.AdmissionUntil >= 9007199254740990 {
					return MigrationPlan{}, ErrSchema
				}
				if ticket.AdmissionUntil > retained {
					retained = ticket.AdmissionUntil
				}
			}
			cursor = page.Cursor
			if cursor == 0 {
				break
			}
		}
	}
	after, err := read()
	if err != nil || after.snapshot != plan.snapshot {
		return MigrationPlan{}, ErrSchema
	}
	plan.retainedUntil = retained
	return plan, nil
}
func ApplyRuntimeMigration(ctx context.Context, c valkey.Client, namespace, expectedDigest string) (MigrationPlan, error) {
	plan, err := InspectRuntimeMigration(ctx, c, namespace)
	if err != nil {
		return plan, err
	}
	if plan.State == "prepared" && plan.Digest != expectedDigest {
		return MigrationPlan{}, ErrSchema
	}
	if len(expectedDigest) != 64 {
		return MigrationPlan{}, ErrSchema
	}
	raw, err := c.Do(ctx, c.B().Fcall().Function("wr_qm_apply").Numkeys(int64(len(plan.keys))).Key(plan.keys...).Arg(plan.snapshot, expectedDigest, fmt.Sprint(plan.retainedUntil)).Build()).ToString()
	if err != nil {
		return MigrationPlan{}, ErrSchema
	}
	var result MigrationPlan
	if json.Unmarshal([]byte(raw), &result) != nil {
		return result, ErrSchema
	}
	return result, nil
}

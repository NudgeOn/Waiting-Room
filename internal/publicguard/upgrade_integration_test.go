//go:build integration

// SPDX-License-Identifier: Apache-2.0
package publicguard

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	v "github.com/valkey-io/valkey-go"
)

//go:embed guard_v1.lua
var legacyLibrary string

//go:embed guard_v2.lua
var previousLibrary string

//go:embed guard_v3.lua
var previousV3Library string

func TestLegacyLibraryAndGuardRecordsSurviveUpgrade(t *testing.T) {
	testGuardUpgrade(t, "wr_public_guard_v1", "wr_pg1_check", legacyLibrary)
}
func TestPreviousV2LibraryAndGuardRecordsSurviveUpgrade(t *testing.T) {
	testGuardUpgrade(t, "wr_public_guard_v2", "wr_pg2_check", previousLibrary)
}
func TestPreviousV3LibraryAndGuardRecordsSurviveUpgrade(t *testing.T) {
	testGuardUpgrade(t, "wr_public_guard_v3", "wr_pg3_check", previousV3Library)
}
func testGuardUpgrade(t *testing.T, oldName, oldFunction, oldLibrary string) {
	t.Helper()
	address := os.Getenv("WR_TEST_RUNTIME_VALKEY")
	if address != "127.0.0.1:16389" {
		address = os.Getenv("WR_TEST_VALKEY")
	}
	if address != "127.0.0.1:16389" && address != "127.0.0.1:16379" {
		t.Fatal("dedicated Valkey required")
	}
	ctx := context.Background()
	opt := v.ClientOption{InitAddress: []string{address}}
	owner, err := v.NewClient(opt)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	legacy := func() string {
		rows, err := owner.Do(ctx, owner.B().FunctionList().Libraryname(oldName).Withcode().Build()).ToArray()
		if err != nil || len(rows) != 1 {
			t.Fatal("legacy library missing", err)
		}
		row, err := rows[0].AsMap()
		if err != nil {
			t.Fatal(err)
		}
		code := row["library_code"]
		s, err := code.ToString()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	rows, err := owner.Do(ctx, owner.B().FunctionList().Libraryname(oldName).Build()).ToArray()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		if err := owner.Do(ctx, owner.B().FunctionLoad().FunctionCode(oldLibrary).Build()).Error(); err != nil {
			t.Fatal(err)
		}
	}
	beforeLibrary := legacy()
	ns := fmt.Sprintf("wr:lab:guard-upgrade-%d", time.Now().UnixNano())
	keys := []string{ns + "{public-guard:1}:records", ns + "{public-guard:1}:expiry", ns + "{public-guard:1}:meta"}
	defer func() {
		if err := owner.Do(ctx, owner.B().Del().Key(keys...).Build()).Error(); err != nil {
			t.Error(err)
		}
	}()
	source, ticket := hashed("upgrade-source"), hashed("retained-ticket")
	for _, op := range []string{"join", "register"} {
		if err := owner.Do(ctx, owner.B().Fcall().Function(oldFunction).Numkeys(3).Key(keys...).Arg("abcdefghijklmnopqrst", "1", source, op, ticket, "10000").Build()).Error(); err != nil {
			t.Fatal("legacy request", err)
		}
	}
	dump := func() []string {
		out := []string{}
		for _, key := range keys {
			s, err := owner.Do(ctx, owner.B().Dump().Key(key).Build()).ToString()
			if err != nil {
				t.Fatal("retained metadata", err)
			}
			out = append(out, s)
		}
		return out
	}
	beforeRecords := dump()
	for range 2 {
		if err := Install(ctx, owner); err != nil {
			t.Fatal("versioned guard upgrade", err)
		}
	}
	g, err := Open(ctx, opt, ns, "abcdefghijklmnopqrst", 1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if legacy() != beforeLibrary || !reflect.DeepEqual(dump(), beforeRecords) {
		t.Fatal("upgrade replaced legacy function or reset existing counters/schedules")
	}
	d, err := g.Check(ctx, source, "status", ticket)
	if err != nil || d.Allowed || d.RetryAfterMs <= 0 {
		t.Fatal("v4 discarded the existing poll deadline", d, err)
	}
	// New serving clients use exactly v4 while the old library remains present for inspection.
	raw, err := owner.Do(ctx, owner.B().Fcall().Function("wr_pg4_check").Numkeys(3).Key(keys...).Arg("abcdefghijklmnopqrst", "1", source, "join", ticket, "10000").Build()).ToString()
	if err != nil || json.Unmarshal([]byte(raw), &d) != nil || !d.Allowed {
		t.Fatal("v4 could not reuse the retained join record", err)
	}
	t.Log("prior library bytes and all three schema-1 keys preserved; v4 retains existing poll deadline and join record")
}

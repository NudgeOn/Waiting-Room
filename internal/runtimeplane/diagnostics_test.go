// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"waiting-room/internal/configtrust"
)

func TestSyncDiagnosticsRedactAndBoundFailures(t *testing.T) {
	secret := "password=private-token-url"
	if syncErrorCode(errors.New(secret)) != "unavailable" || syncHTTPCode(503, errors.New(secret)) != "unavailable" {
		t.Fatal("unclassified error message exposed")
	}
	code := syncErrorCode(fmt.Errorf("%s: %w", secret, configtrust.ErrPersistence))
	if code != "snapshot_persistence" || syncHTTPCode(409, nil) != "http_409" {
		t.Fatal("safe classification lost")
	}
	d := syncDiagnostic{}
	now := time.Unix(1000, 0)
	first := d.observe("coordinator", "verify", code, 54, now)
	if strings.Contains(first, secret) || !strings.Contains(first, "generation=54") || first == "" {
		t.Fatal("unsafe or missing diagnostic")
	}
	if d.observe("coordinator", "verify", code, 54, now.Add(time.Second)) != "" {
		t.Fatal("repeated failure flooded log")
	}
	if d.observe("coordinator", "verify", code, 54, now.Add(time.Minute)) == "" {
		t.Fatal("persistent failure reminder missing")
	}
	if !strings.Contains(d.observe("coordinator", "", "", 55, now.Add(61*time.Second)), "state=recovered") {
		t.Fatal("recovery missing")
	}
	if d.observe("coordinator", "", "", 55, now.Add(62*time.Second)) != "" {
		t.Fatal("healthy heartbeat flooded log")
	}
}

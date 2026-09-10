// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCallDiagnosticsBoundedAndRedacted(t *testing.T) {
	now := time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "deadline"}, {context.Canceled, "cancelled"}, {errors.New("WR_SCHEMA secret-token"), "server_schema"}, {errors.New("WR_UNAVAILABLE secret-token"), "server_unavailable"}, {errors.New("secret-token"), "server_or_transport"}, {nil, "response_invalid"}} {
		d := callDiagnostic{}
		line := d.observe("secret-token", false, test.err, 1001*time.Millisecond, 3*time.Millisecond, 999*time.Millisecond, now)
		if strings.Contains(line, "secret-token") || !strings.Contains(line, "operation=unknown read=false code="+test.code) || !strings.Contains(line, "elapsed_ms=1001 lock_wait_ms=3 budget_ms=999") {
			t.Fatal(line)
		}
		if d.observe("join", false, test.err, time.Second, 0, time.Second, now.Add(time.Second)) != "" {
			t.Fatal("diagnostic flood")
		}
		if d.observe("join", false, test.err, time.Second, 0, time.Second, now.Add(time.Minute)) == "" {
			t.Fatal("missing bounded reminder")
		}
	}
}

// SPDX-License-Identifier: Apache-2.0
package publicguard

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

func TestGuardDiagnosticsAreBoundedAndRedacted(t *testing.T) {
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	g := &Guard{}
	g.report(errors.New("private-host private-token"))
	for range 50 {
		g.report(errors.New("WR_GUARD_CLOCK private-token"))
	}
	if strings.Count(out.String(), "public_guard") != 1 || strings.Contains(out.String(), "private") || !strings.Contains(out.String(), "code=unavailable") {
		t.Fatal("diagnostics not bounded/redacted")
	}
	g.lastDiagnostic.Store(time.Now().Add(-time.Minute).Unix())
	g.report(errors.New("WR_GUARD_CLOCK private-token"))
	if strings.Count(out.String(), "public_guard") != 2 || strings.Contains(out.String(), "private") || !strings.Contains(out.String(), "code=clock_rollback") {
		t.Fatal("fixed clock diagnostic missing")
	}
}

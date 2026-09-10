// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalClockRecoveryRejectsBeforeStoreAccess(t *testing.T) {
	for _, tc := range []struct {
		peer, host, method, path, content, body string
		status                                  int
	}{
		{"", "control:19445", "POST", "/internal/v1/config/clock-recovery", "application/json", "{}", 403},
		{"demo-origin", "control:19445", "POST", "/internal/v1/config/clock-recovery", "application/json", "{}", 403},
		{"gateway", "evil:19445", "POST", "/internal/v1/config/clock-recovery", "application/json", "{}", 400},
		{"gateway", "control:19445", "POST", "/internal/v1/config/clock-recovery?mode=OFF", "application/json", "{}", 400},
		{"coordinator", "control:19445", "POST", "/internal/v1/config/clock-recovery", "text/plain", "{}", 400},
		{"gateway", "control:19445", "POST", "/internal/v1/config/clock-recovery", "application/json", `{"generation":1,"notBefore":1,"digest":"x","mode":"OFF"}`, 400},
		{"gateway", "control:19445", "POST", "/internal/v1/config/clock-recovery", "application/json", strings.Repeat("x", 513), 400},
		{"coordinator", "control:19445", "GET", "/internal/v1/config/clock-recovery", "application/json", "{}", 404},
	} {
		r := httptest.NewRequest(tc.method, "https://"+tc.host+tc.path, strings.NewReader(tc.body))
		trafficPeer(r, tc.peer)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		InternalControl(nil).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s %s: %d", tc.peer, tc.method, tc.path, w.Code)
		}
	}
}

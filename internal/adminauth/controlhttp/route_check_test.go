// SPDX-License-Identifier: Apache-2.0
package controlhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"waiting-room/internal/adminauth/pgstore"
)

type routeCheckSpy struct {
	PublicationBackend
	calls int
}

func (s *routeCheckSpy) RouteCheck(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error) {
	s.calls++
	return pgstore.ControlReply{Status: 200, Body: []byte(`{"source":"draft","scope":"configuration-only","revision":0,"generation":0,"match":{"decision":"unprotected","reason":"no_active_rule"}}`)}, nil
}
func TestRouteCheckHTTPBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, contentType string
		cookie                                bool
		want                                  int
	}{
		{"valid", "POST", "/api/admin/v1/config/route-check", `{"source":"draft","url":"https://shop.test/shop"}`, "application/json", true, 200},
		{"method", "GET", "/api/admin/v1/config/route-check", "", "", true, 405},
		{"cookie", "POST", "/api/admin/v1/config/route-check", `{}`, "application/json", false, 401},
		{"query", "POST", "/api/admin/v1/config/route-check?url=secret", `{}`, "application/json", true, 400},
		{"encoded", "POST", "/api/admin/v1/config/%72oute-check", `{}`, "application/json", true, 400},
		{"media", "POST", "/api/admin/v1/config/route-check", `{}`, "text/plain", true, 400},
		{"bound", "POST", "/api/admin/v1/config/route-check", strings.Repeat("x", pgstore.MaxRouteCheckBytes+1), "application/json", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &routeCheckSpy{}
			h, _ := NewRuntime(s)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: "__Host-wrs", Value: strings.Repeat("a", 43)})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code)
			}
			if (s.calls == 1) != (tc.want == 200) {
				t.Fatal("invalid request reached backend")
			}
		})
	}
}

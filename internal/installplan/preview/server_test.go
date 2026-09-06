// SPDX-License-Identifier: Apache-2.0
package preview

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPreviewHTTPBoundary(t *testing.T) {
	h, err := Handler(fstest.MapFS{"index.html": {Data: []byte("preview fixture")}, "assets/app.js": {Data: []byte("fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"plan", "estimate", "report"} {
		name := "standard"
		if kind == "estimate" {
			name = "cost-example"
		}
		if kind == "report" {
			name = "report-example"
		}
		b, err := os.ReadFile("../../../test/installplan/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			modify func(*http.Request)
			status int
		}{
			{"valid", func(r *http.Request) {}, 200},
			{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://outside.example") }, 403},
			{"duplicate-origin", func(r *http.Request) { r.Header.Add("Origin", "http://"+Address) }, 403},
			{"host", func(r *http.Request) { r.Host = "outside.example" }, 403},
			{"remote", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" }, 403},
			{"content-type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 403},
			{"marker", func(r *http.Request) { r.Header.Del("X-WR-Preview") }, 403},
			{"cookie", func(r *http.Request) { r.Header.Set("Cookie", "secret=DO-NOT-ECHO") }, 403},
			{"auth", func(r *http.Request) { r.Header.Set("Authorization", "DO-NOT-ECHO") }, 403},
			{"query", func(r *http.Request) { r.URL.RawQuery = "DO-NOT-ECHO" }, 400},
			{"method", func(r *http.Request) { r.Method = "GET" }, 405},
			{"invalid", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`{"password":"DO-NOT-ECHO"}`)) }, 422},
			{"large", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", 17000))) }, 422},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				r := httptest.NewRequest("POST", "http://"+Address+"/preview/"+kind, strings.NewReader(string(b)))
				r.RemoteAddr = "127.0.0.1:12345"
				r.Header.Set("Origin", "http://"+Address)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-WR-Preview", "1")
				tc.modify(r)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tc.status || strings.Contains(w.Body.String(), "DO-NOT-ECHO") {
					t.Fatal(w.Code, w.Body.String())
				}
				if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
					t.Fatal("missing security headers")
				}
			})
		}
	}
	for route, status := range map[string]int{"/install-preview": 200, "/livez": 204, "/assets/app.js": 200, "/assets/": 404, "/setup": 404, "/api/admin/v1/bootstrap": 404, "/preview/apply": 404} {
		r := httptest.NewRequest("GET", "http://"+Address+route, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatal(route, w.Code)
		}
	}
}

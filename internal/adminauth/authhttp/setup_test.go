// SPDX-License-Identifier: Apache-2.0
package authhttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/installplan"
)

type fakeSetup struct{ calls int }

func (f *fakeSetup) Inspect(context.Context, string) (pgstore.SetupInspection, error) {
	f.calls++
	return pgstore.SetupInspection{}, nil
}
func (f *fakeSetup) Calibrate(context.Context, string) (map[string]any, error) {
	f.calls++
	return map[string]any{}, nil
}
func (f *fakeSetup) Plan(context.Context, string, installplan.Input) (pgstore.SetupReview, error) {
	f.calls++
	return pgstore.SetupReview{}, nil
}
func (f *fakeSetup) Apply(context.Context, string, installplan.SetupApply) (pgstore.SetupReport, error) {
	f.calls++
	return pgstore.SetupReport{}, nil
}
func TestSetupHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"allowed", func(*http.Request) {}, 200},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, 403},
		{"peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1000"; r.Header.Set("X-Forwarded-For", "127.0.0.1") }, 403},
		{"local", func(r *http.Request) { *r = *r.WithContext(context.Background()) }, 403},
		{"cookie", func(r *http.Request) { r.Header.Set("Cookie", "x=y") }, 401},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }, 401},
		{"duplicate-token", func(r *http.Request) { r.Header.Add("X-Bootstrap-Token", strings.Repeat("A", 43)) }, 401},
		{"query", func(r *http.Request) { r.URL.RawQuery = "x=y" }, 400},
		{"method", func(r *http.Request) { r.Method = "GET" }, 405},
		{"unexpected-field", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`{"password":"sensitive-fixture"}`)) }, 400},
		{"oversized", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", 17000))) }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeSetup{}
			h := NewSetup(service, &fakeBackend{}, "https://127.0.0.1:9443")
			r := request(`{}`)
			r.URL.Path = "/api/admin/v1/setup/calibrate"
			r.Host = "127.0.0.1:9443"
			r.Header.Set("Origin", "https://127.0.0.1:9443")
			r.Header.Set("X-Bootstrap-Token", strings.Repeat("A", 43))
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9443}))
			tc.change(r)
			w := &recorder{ResponseRecorder: httptest.NewRecorder()}
			h.ServeHTTP(w, r)
			if w.Code != tc.status || (tc.status != 200 && service.calls != 0) || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "sensitive-fixture") {
				t.Fatalf("status %d calls %d", w.Code, service.calls)
			}
		})
	}
}

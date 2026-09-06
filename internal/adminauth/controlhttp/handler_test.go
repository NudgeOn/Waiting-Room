// SPDX-License-Identifier: Apache-2.0
package controlhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

type fake struct {
	calls int
	err   error
}

func (f *fake) Config(context.Context, string) (pgstore.ControlReply, error) {
	f.calls++
	return pgstore.ControlReply{Status: 200, Body: []byte(`{}`), ETag: `"config-0"`}, f.err
}
func (f *fake) ReplaceDraft(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error) {
	f.calls++
	return pgstore.ControlReply{Status: 200, Body: []byte(`{}`), ETag: `"config-1"`, Replay: true}, f.err
}
func (f *fake) AuditPage(context.Context, string, int64, int) ([]pgstore.AuditEvent, error) {
	f.calls++
	return []pgstore.AuditEvent{}, f.err
}
func request(method, address, body string) *http.Request {
	r := httptest.NewRequest(method, address, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: "__Host-wrs", Value: strings.Repeat("a", 43)})
	if method == "PUT" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}
func TestBoundary(t *testing.T) {
	for _, tc := range []struct {
		method, url, body string
		change            func(*http.Request)
		want              int
	}{
		{"GET", "/api/admin/v1/config/draft", "", nil, 200}, {"PUT", "/api/admin/v1/config/draft", "{}", nil, 200}, {"GET", "/api/admin/v1/audit-events?cursor=MTA&limit=20", "", nil, 200},
		{"GET", "/api/admin/v1/audit-events?before=10&before=11", "", nil, 400}, {"GET", "/api/admin/v1/config/draft?x=1", "", nil, 400}, {"GET", "/api/admin/v1/config/draft", "x", nil, 400},
		{"PUT", "/api/admin/v1/config/draft", strings.Repeat("x", 65537), nil, 400}, {"POST", "/api/admin/v1/config/draft", "{}", nil, 405},
		{"PUT", "/api/admin/v1/config/draft", "{}", func(r *http.Request) { r.Header.Set("Content-Type", "application/json-invalid") }, 400},
		{"GET", "/api/admin/v1/config/draft", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer bad") }, 401},
		{"GET", "/api/admin/v1/config/draft", "", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-wrs", Value: strings.Repeat("b", 43)}) }, 401},
	} {
		f := &fake{}
		h, _ := New(f)
		r := request(tc.method, tc.url, tc.body)
		if tc.change != nil {
			tc.change(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s => %d", tc.method, tc.url, w.Code)
		}
		if tc.want != 200 && f.calls != 0 {
			t.Fatal("invalid input reached backend")
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-WR-Config-State") != "draft-only" {
			t.Fatal("missing boundary headers")
		}
	}
}
func TestErrorRedaction(t *testing.T) {
	for _, e := range []error{adminauth.ErrUnauthenticated, adminauth.ErrForbidden, adminauth.ErrAuthUnavailable} {
		f := &fake{err: e}
		h, _ := New(f)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", "/api/admin/v1/config/draft", ""))
		if w.Code < 400 || strings.Contains(w.Body.String(), "password") {
			t.Fatal("bad error")
		}
	}
}

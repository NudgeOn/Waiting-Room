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

type fakeTraffic struct{ calls int }

func (f *fakeTraffic) List(context.Context, string, string) (pgstore.ControlReply, error) {
	f.calls++
	return pgstore.ControlReply{Status: 200, Body: []byte(`{}`)}, nil
}
func (f *fakeTraffic) Start(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error) {
	f.calls++
	return pgstore.ControlReply{Status: 202, Body: []byte(`{}`), Replay: true}, nil
}
func (f *fakeTraffic) Cancel(context.Context, string, string, *http.Request, []byte) (pgstore.ControlReply, error) {
	f.calls++
	return pgstore.ControlReply{Status: 202, Body: []byte(`{}`)}, nil
}
func TestTrafficHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		change             func(*http.Request)
		want               int
	}{
		{"GET", "/lab/runs", "", nil, 200}, {"POST", "/lab/runs", `{"preset":"quick-20"}`, nil, 202},
		{"POST", "/lab/runs/" + strings.Repeat("a", 43) + "/cancel", "{}", nil, 202},
		{"GET", "/lab/runs/" + strings.Repeat("a", 43) + "/cancel", "", nil, 405},
		{"GET", "/lab/runs?url=evil", "", nil, 400}, {"GET", "/lab/runs/invalid", "", nil, 404},
		{"GET", "/lab/runs", "x", nil, 400}, {"POST", "/lab/runs", strings.Repeat("x", 1025), nil, 400},
		{"GET", "/lab/runs", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }, 401},
		{"GET", "/lab/runs", "", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-wrs", Value: strings.Repeat("b", 43)}) }, 401},
		{"POST", "/lab/runs", "{}", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
	} {
		f := &fakeTraffic{}
		r := request(tc.method, "/api/admin/v1"+tc.path, tc.body)
		if tc.method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		if tc.change != nil {
			tc.change(r)
		}
		w := httptest.NewRecorder()
		NewTraffic(f).ServeHTTP(w, r)
		if w.Code != tc.want || (tc.want >= 400 && f.calls != 0) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc.path, w.Code, f.calls)
		}
	}
}

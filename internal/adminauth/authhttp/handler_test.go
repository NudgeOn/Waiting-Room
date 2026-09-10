// SPDX-License-Identifier: Apache-2.0
package authhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

type fakeBackend struct {
	calls            atomic.Int32
	limit            error
	loginErr         error
	entered, release chan struct{}
	peer             netip.Addr
}

func (b *fakeBackend) ReserveAuthHTTP(context.Context, netip.Addr) error {
	if b.entered != nil {
		b.entered <- struct{}{}
		<-b.release
	}
	return b.limit
}
func (b *fakeBackend) Login(_ context.Context, _, _ string, p netip.Addr) (pgstore.LoginResult, error) {
	b.calls.Add(1)
	b.peer = p
	if b.loginErr != nil {
		return pgstore.LoginResult{}, b.loginErr
	}
	return pgstore.LoginResult{}, adminauth.ErrUnauthenticated
}
func (*fakeBackend) Verify(context.Context, string, string) (pgstore.Grant, error) {
	return pgstore.Grant{}, adminauth.ErrInvalidOrReplayed
}
func (*fakeBackend) Recover(context.Context, string, string) (pgstore.Grant, error) {
	return pgstore.Grant{}, adminauth.ErrInvalidOrReplayed
}
func (*fakeBackend) Me(context.Context, string) (pgstore.SessionView, error) {
	return pgstore.SessionView{}, adminauth.ErrUnauthenticated
}

type recorder struct {
	*httptest.ResponseRecorder
	deadlineErr error
}

func (r *recorder) SetReadDeadline(time.Time) error { return r.deadlineErr }
func request(body string) *http.Request {
	r := httptest.NewRequest("POST", "https://admin.test/api/admin/v1/auth/login", strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Origin", "https://admin.test")
	r.Header.Set("X-WR-Auth", "1")
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestAuthHTTPGuardsAndStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		change func(*http.Request)
		status int
		calls  int
	}{
		{"password-failure", `{"username":"admin","password":"incorrect"}`, nil, 401, 1},
		{"wrong-origin", `{}`, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, 403, 0},
		{"duplicate-origin", `{}`, func(r *http.Request) { r.Header.Add("Origin", "https://admin.test") }, 403, 0},
		{"http", `{}`, func(r *http.Request) { r.TLS = nil; r.Header.Set("X-Forwarded-Proto", "https") }, 403, 0},
		{"host", `{}`, func(r *http.Request) { r.Host = "evil.test" }, 403, 0},
		{"marker", `{}`, func(r *http.Request) { r.Header.Del("X-WR-Auth") }, 403, 0},
		{"fetch-site", `{}`, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403, 0},
		{"cookie", `{}`, func(r *http.Request) { r.Header.Set("Cookie", "__Host-wrs=invalid") }, 401, 0},
		{"visitor-cookies", `{"username":"admin","password":"incorrect"}`, func(r *http.Request) {
			r.Header.Set("Cookie", "__Host-wrq_fixture=ticket; __Host-wra_fixture=admission")
		}, 401, 1},
		{"visitor-and-session-cookies", `{}`, func(r *http.Request) {
			r.Header.Set("Cookie", "__Host-wrq_fixture=ticket; __Host-wrs=invalid")
		}, 401, 0},
		{"malformed-session-cookie", `{}`, func(r *http.Request) {
			r.Header.Set("Cookie", `__Host-wrq_fixture=ticket; __Host-wrs="unterminated`)
		}, 401, 0},
		{"second-session-cookie-header", `{}`, func(r *http.Request) {
			r.Header.Set("Cookie", "__Host-wrq_fixture=ticket")
			r.Header.Add("Cookie", "__Host-wrs=invalid")
		}, 401, 0},
		{"bearer-login", `{}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer invalid") }, 401, 0},
		{"method", `{}`, func(r *http.Request) { r.Method = "GET" }, 405, 0},
		{"query", `{}`, func(r *http.Request) { r.URL.RawQuery = "token=bad" }, 400, 0},
		{"trailing-question", `{}`, func(r *http.Request) { r.URL.ForceQuery = true }, 400, 0},
		{"content-type", `{}`, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415, 0},
		{"duplicate-key", `{"username":"a","username":"b","password":"x"}`, nil, 400, 0},
		{"wrong-case", `{"Username":"a","password":"x"}`, nil, 400, 0},
		{"unknown-key", `{"username":"a","password":"x","role":"admin"}`, nil, 400, 0},
		{"null", `{"username":null,"password":"x"}`, nil, 400, 0},
		{"nested", `{"username":{},"password":"x"}`, nil, 400, 0},
		{"trailing", `{"username":"a","password":"x"}{}`, nil, 400, 0},
		{"oversized", strings.Repeat("x", 4097), nil, 413, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{}
			h, err := New(b, "https://admin.test")
			if err != nil {
				t.Fatal(err)
			}
			r := request(tc.body)
			if tc.change != nil {
				tc.change(r)
			}
			w := &recorder{ResponseRecorder: httptest.NewRecorder()}
			h.ServeHTTP(w, r)
			if w.Code != tc.status || int(b.calls.Load()) != tc.calls || len(w.Result().Cookies()) != 0 {
				t.Fatalf("status=%d calls=%d", w.Code, b.calls.Load())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("unsafe response headers")
			}
		})
	}
}
func TestAuthHTTPUnavailableLimitsAndPeer(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{pgstore.ErrAuthHTTPRateLimit, 429}, {errors.New("private DB failure"), 503}} {
		h, _ := New(&fakeBackend{limit: tc.err}, "https://admin.test")
		w := &recorder{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(w, request(`{"username":"a","password":"x"}`))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private DB") {
			t.Fatal("bad error projection")
		}
		if tc.status == 429 && w.Header().Get("Retry-After") != "60" {
			t.Fatal("missing retry bound")
		}
	}
	b := &fakeBackend{}
	h, _ := New(b, "https://admin.test")
	r := request(`{"username":"a","password":"x"}`)
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	w := &recorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, r)
	if b.peer.String() != "127.0.0.1" {
		t.Fatal("forwarded address trusted")
	}
	w = &recorder{ResponseRecorder: httptest.NewRecorder(), deadlineErr: http.ErrNotSupported}
	h.ServeHTTP(w, request(`{}`))
	if w.Code != 503 {
		t.Fatal("missing body deadline accepted")
	}
	for _, origin := range []string{"http://127.0.0.1", "https://admin.test/path"} {
		if _, err := New(b, origin); err == nil {
			t.Fatal("unsafe origin config")
		}
	}
}
func TestAuthHTTPProcessConcurrencyBound(t *testing.T) {
	b := &fakeBackend{entered: make(chan struct{}, 2), release: make(chan struct{}), limit: adminauth.ErrAuthUnavailable}
	h, _ := New(b, "https://admin.test")
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			defer func() { done <- struct{}{} }()
			h.ServeHTTP(&recorder{ResponseRecorder: httptest.NewRecorder()}, request(`{}`))
		}()
	}
	for range 2 {
		select {
		case <-b.entered:
		case <-time.After(3 * time.Second):
			close(b.release)
			t.Fatal("gate did not admit two requests")
		}
	}
	w := &recorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, request(`{}`))
	close(b.release)
	<-done
	<-done
	if w.Code != 503 {
		t.Fatal("unbounded auth work")
	}
}

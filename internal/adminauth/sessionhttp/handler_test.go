// SPDX-License-Identifier: Apache-2.0
package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

type backendDouble struct {
	err   error
	calls int
}

func (b *backendDouble) Me(context.Context, string) (pgstore.SessionView, error) {
	b.calls++
	return pgstore.SessionView{UserID: "u", Role: adminauth.Viewer}, b.err
}
func (b *backendDouble) Logout(context.Context, string, *http.Request) error { b.calls++; return b.err }
func request(method, path string) *http.Request {
	r := httptest.NewRequest(method, "https://admin.test"+path, nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: strings.Repeat("A", 43)})
	return r
}
func TestHTTPRoutesAndCredentialAmbiguity(t *testing.T) {
	for _, kind := range []string{"method", "unknown", "query", "empty-query", "encoding", "encoded", "body", "duplicate", "missing", "bearer"} {
		t.Run(kind, func(t *testing.T) {
			backend := &backendDouble{}
			h, _ := New(backend)
			r := request("GET", "/api/admin/v1/auth/me")
			want := 401
			switch kind {
			case "method":
				r.Method = "POST"
				want = 405
			case "unknown":
				r.URL.Path = "/api/admin/v1/auth/login"
				want = 404
			case "query":
				r.URL.RawQuery = "token=x"
				want = 400
			case "empty-query":
				r.URL.ForceQuery = true
				want = 400
			case "encoding":
				r.Header.Set("Content-Encoding", "identity")
				want = 400
			case "encoded":
				r.URL.RawPath = "/api/admin/v1/auth/%6de"
				want = 400
			case "body":
				r.Body = http.NoBody
				r = httptest.NewRequest("GET", "https://admin.test/api/admin/v1/auth/me", strings.NewReader("{}"))
				want = 400
			case "duplicate":
				r.AddCookie(&http.Cookie{Name: CookieName, Value: strings.Repeat("B", 43)})
			case "missing":
				r.Header.Del("Cookie")
			case "bearer":
				r.Header.Set("Authorization", "Bearer secret")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want || backend.calls != 0 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("routing/credential guard", w.Code)
			}
		})
	}
}
func TestHTTPErrorRedactionAndCookieClear(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		clear  bool
	}{
		{nil, 200, true}, {adminauth.ErrUnauthenticated, 401, true}, {adminauth.ErrForbidden, 403, false}, {errors.New("password secret database detail"), 503, false},
	} {
		b := &backendDouble{err: tc.err}
		h, _ := New(b)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("POST", "/api/admin/v1/auth/logout"))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "database") {
			t.Fatal("error leakage/status")
		}
		cookies := w.Result().Cookies()
		if (len(cookies) > 0) != tc.clear {
			t.Fatal("cookie clear on wrong outcome")
		}
		if tc.clear {
			c := cookies[0]
			if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" || c.MaxAge >= 0 {
				t.Fatal("unsafe cookie flags")
			}
		}
	}
}
func TestHTTPMeDoesNotSetCookie(t *testing.T) {
	h, _ := New(&backendDouble{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/admin/v1/auth/me"))
	if w.Code != 200 || w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), "csrf") {
		t.Fatal("read refreshed/leaked credential")
	}
	if _, err := New(nil); err == nil {
		t.Fatal("nil backend")
	}
}

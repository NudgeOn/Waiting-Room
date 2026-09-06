// SPDX-License-Identifier: Apache-2.0
package authhttp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

type fakeProvisioning struct{ fakeBackend }

func (b *fakeProvisioning) Bootstrap(_ context.Context, _, _, _ string, p netip.Addr) (pgstore.LoginResult, error) {
	b.calls.Add(1)
	b.peer = p
	return pgstore.LoginResult{}, adminauth.ErrUnauthenticated
}
func (b *fakeProvisioning) BeginEnrollment(context.Context, string) (pgstore.EnrollmentSetup, error) {
	b.calls.Add(1)
	return pgstore.EnrollmentSetup{}, adminauth.ErrInvalidOrReplayed
}
func (b *fakeProvisioning) CompleteEnrollment(context.Context, string, string) (pgstore.EnrollmentGrant, error) {
	b.calls.Add(1)
	return pgstore.EnrollmentGrant{}, adminauth.ErrInvalidOrReplayed
}
func TestProvisioningHTTPIsolationAndLoopback(t *testing.T) {
	b := &fakeProvisioning{}
	for _, origin := range []string{"https://admin.test", "https://localhost", "https://0.0.0.0", "https://192.0.2.1", "http://127.0.0.1"} {
		if _, err := NewBootstrap(b, origin); err == nil {
			t.Fatal("nonliteral/nonloopback/HTTP origin accepted")
		}
	}
	h, err := NewBootstrap(b, "https://127.0.0.1:9443")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		change        func(*http.Request)
		status, calls int
	}{
		{"allowed-boundary", nil, 401, 1},
		{"policy-injection", func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"username":"admin","password":"local test password","totpEnabled":false}`))
			r.ContentLength = -1
		}, 400, 0},
		{"missing-local", func(r *http.Request) { *r = *r.WithContext(context.Background()) }, 403, 0},
		{"remote-peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:443"; r.Header.Set("X-Forwarded-For", "127.0.0.1") }, 403, 0},
		{"remote-local", func(r *http.Request) {
			*r = *r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.2"), Port: 9443}))
		}, 403, 0},
		{"duplicate-token", func(r *http.Request) { r.Header.Add("X-Bootstrap-Token", strings.Repeat("A", 43)) }, 401, 0},
		{"bearer-mixed", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43)) }, 401, 0},
		{"cookie-mixed", func(r *http.Request) { r.Header.Set("Cookie", "x=y") }, 401, 0},
		{"wrong-origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, 403, 0},
		{"marker-missing", func(r *http.Request) { r.Header.Del("X-WR-Auth") }, 403, 0},
		{"login-not-mounted", func(r *http.Request) { r.URL.Path = "/api/admin/v1/auth/login" }, 404, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b.calls.Store(0)
			r := request(`{"username":"admin","password":"local test password"}`)
			r.URL.Path = "/api/admin/v1/bootstrap"
			r.Host = "127.0.0.1:9443"
			r.Header.Set("Origin", "https://127.0.0.1:9443")
			r.Header.Set("X-Bootstrap-Token", strings.Repeat("A", 43))
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9443}))
			if tc.change != nil {
				tc.change(r)
			}
			w := &recorder{ResponseRecorder: httptest.NewRecorder()}
			h.ServeHTTP(w, r)
			if w.Code != tc.status || int(b.calls.Load()) != tc.calls || len(w.Result().Cookies()) != 0 {
				t.Fatalf("status=%d calls=%d", w.Code, b.calls.Load())
			}
		})
	}
	for _, constructor := range []func(ProvisioningBackend, string) (*Handler, error){NewEnrollment, func(b ProvisioningBackend, o string) (*Handler, error) { return New(b, o) }} {
		h, _ := constructor(b, "https://admin.test")
		r := request(`{}`)
		r.URL.Path = "/api/admin/v1/bootstrap"
		w := &recorder{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("bootstrap mounted on ordinary auth")
		}
	}
}
func TestProvisioningHTTPStrictProofAndBody(t *testing.T) {
	base, _ := New(&fakeProvisioning{}, "https://admin.test")
	baseRequest := request(`{}`)
	baseRequest.URL.Path = "/api/admin/v1/auth/totp/enroll"
	baseResponse := &recorder{ResponseRecorder: httptest.NewRecorder()}
	base.ServeHTTP(baseResponse, baseRequest)
	if baseResponse.Code != 404 {
		t.Fatal("enrollment not opt-in")
	}
	for _, tc := range []struct {
		path, body      string
		bootstrapHeader bool
		status, calls   int
	}{
		{"/auth/totp/enroll", `{}`, false, 401, 1},
		{"/auth/totp/enroll", `{"secret":"chosen"}`, false, 400, 0},
		{"/auth/totp/enroll", `null`, false, 400, 0},
		{"/auth/totp/enroll/verify", `{"code":"123456"}`, false, 401, 1},
		{"/auth/totp/enroll/verify", `{"code":"123456","code":"654321"}`, false, 400, 0},
		{"/auth/totp/enroll", `{}`, true, 401, 0},
	} {
		b := &fakeProvisioning{}
		h, _ := NewEnrollment(b, "https://admin.test")
		r := request(tc.body)
		r.URL.Path = "/api/admin/v1" + tc.path
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43))
		if tc.bootstrapHeader {
			r.Header.Set("X-Bootstrap-Token", strings.Repeat("A", 43))
		}
		w := &recorder{ResponseRecorder: httptest.NewRecorder()}
		h.ServeHTTP(w, r)
		if w.Code != tc.status || int(b.calls.Load()) != tc.calls || len(w.Result().Cookies()) != 0 {
			t.Fatalf("status=%d calls=%d", w.Code, b.calls.Load())
		}
	}
	if strings.Contains(fmt.Sprintf("%v %#v", enrollmentReply{Secret: "sensitive-key"}, reply{Recovery: []string{"sensitive-code"}}), "sensitive-") {
		t.Fatal("format secret leakage")
	}
}

// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPinnedOriginAndLoopbackException(t *testing.T) {
	for _, origin := range []string{"https://admin.test", "https://admin.test:9443"} {
		if _, err := NewOriginPolicy(origin, false); err != nil {
			t.Fatal("valid origin rejected")
		}
	}
	for _, origin := range []string{"", "https://admin.test/", "https://admin.test?", "https://admin.test?x=1", "https://admin.test/#fragment", "https://user@admin.test", "http://admin.test", "http://localhost:18090", "file:///tmp/admin"} {
		if _, err := NewOriginPolicy(origin, true); err == nil {
			t.Fatal("invalid origin allowed", origin)
		}
	}
	if _, err := NewOriginPolicy("http://127.0.0.1:18090", false); err == nil {
		t.Fatal("implicit HTTP exception")
	}
	if _, err := NewOriginPolicy("http://127.0.0.1:18090", true); err != nil {
		t.Fatal("explicit loopback denied")
	}
}

func TestCSRFSameOriginSessionBindingAndAmbiguity(t *testing.T) {
	p, _ := NewOriginPolicy("https://admin.test", false)
	token, hash, err := NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	newRequest := func() *http.Request {
		r := httptest.NewRequest("PUT", "https://admin.test/api/admin/v1/config", nil)
		r.Header.Set("Origin", "https://admin.test")
		r.Header.Set("X-CSRF-Token", token)
		return r
	}
	if p.CheckMutation(newRequest(), hash) != nil {
		t.Fatal("valid CSRF denied")
	}
	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"missing-origin", func(r *http.Request) { r.Header.Del("Origin") }},
		{"cross-origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }},
		{"null-origin", func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"wrong-port", func(r *http.Request) { r.Header.Set("Origin", "https://admin.test:9443") }},
		{"missing-token", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }},
		{"other-session", func(r *http.Request) { r.Header.Set("X-CSRF-Token", other) }},
		{"duplicate-origin", func(r *http.Request) { r.Header.Add("Origin", "https://admin.test") }},
		{"duplicate-token", func(r *http.Request) { r.Header.Add("X-CSRF-Token", token) }},
		{"short-token", func(r *http.Request) { r.Header.Set("X-CSRF-Token", "short") }},
		{"not-mutation", func(r *http.Request) { r.Method = "GET" }},
		{"spoofed-forwarded", func(r *http.Request) {
			r.Header.Set("Origin", "https://evil.test")
			r.Header.Set("Forwarded", "host=admin.test;proto=https")
			r.Host = "admin.test"
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			r := newRequest()
			item.mutate(r)
			if p.CheckMutation(r, hash) == nil {
				t.Fatal("invalid mutation accepted")
			}
		})
	}
	if p.CheckMutation(newRequest(), [32]byte{}) == nil {
		t.Fatal("missing session hash accepted")
	}
	if (OriginPolicy{}).CheckMutation(newRequest(), hash) == nil {
		t.Fatal("unconfigured origin accepted")
	}
}

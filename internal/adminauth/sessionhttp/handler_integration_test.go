//go:build integration

// SPDX-License-Identifier: Apache-2.0
package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

func TestSessionHTTPPostgresTLS(t *testing.T) {
	if os.Getenv("WR_TEST_AUTH_DB") != "local" {
		t.Fatal("requires dedicated local auth DB")
	}
	const url = "postgres://wr_auth_lab:local-test-only@127.0.0.1:15432/wr_auth_lab?sslmode=disable"
	ctx := context.Background()
	root, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("local auth DB unavailable")
	}
	nonce, _, _ := adminauth.NewCSRFToken()
	schema := "wr_auth_test_http_" + strings.ToLower(strings.ReplaceAll(nonce[:16], "-", "_"))
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		root.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("test schema cleanup")
		}
		root.Close(ctx)
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version string
	if err = pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil || !strings.HasPrefix(version, "17.11") {
		t.Fatal("wrong test database version")
	}
	if _, err = pool.Exec(ctx, pgstore.Migration001); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO auth_accounts (id,role) VALUES ('http_user','viewer'); UPDATE auth_policy SET totp_enabled=false"); err != nil {
		t.Fatal(err)
	}
	token, sh, _ := adminauth.NewCSRFToken()
	csrf, ch, _ := adminauth.NewCSRFToken()
	if _, err = pool.Exec(ctx, "INSERT INTO auth_sessions (token_hash,csrf_hash,user_id,policy_version,user_version,mfa_verified,created_at,last_seen_at) SELECT $1,$2,'http_user',1,1,false,t,t FROM (SELECT clock_timestamp()-interval '1 minute' AS t) stamp", sh[:], ch[:]); err != nil {
		t.Fatal(err)
	}
	vault, _ := pgstore.NewVault(map[string][]byte{"test": bytes.Repeat([]byte{1}, 32)})
	store, _ := pgstore.New(pool, vault)
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	service, err := pgstore.NewSessionService(store, "https://"+server.Listener.Addr().String(), false)
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := New(service)
	server.Config.Handler = handler
	server.StartTLS()
	client := server.Client()
	client.Timeout = 5 * time.Second
	send := func(method, path, origin, csrfValue string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrfValue != "" {
			r.Header.Set("X-CSRF-Token", csrfValue)
		}
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	res := send("GET", "/api/admin/v1/auth/me", "", "")
	if res.StatusCode != 200 || res.TLS == nil || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("TLS session read")
	}
	var before pgstore.SessionView
	if err = json.NewDecoder(res.Body).Decode(&before); err != nil {
		t.Fatal(err)
	}
	if before.UserID != "http_user" || before.Role != adminauth.Viewer || before.MFAVerified {
		t.Fatal("session projection")
	}
	res = send("POST", "/api/admin/v1/auth/logout", "https://evil.test", csrf)
	if res.StatusCode != 403 || len(res.Cookies()) != 0 {
		t.Fatal("cross-origin logout changed cookie")
	}
	res = send("GET", "/api/admin/v1/auth/me", "", "")
	var after pgstore.SessionView
	if err = json.NewDecoder(res.Body).Decode(&after); err != nil || after.LastSeenAt != before.LastSeenAt {
		t.Fatal("read/CSRF changed idle TTL")
	}
	res = send("POST", "/api/admin/v1/auth/logout", server.URL, csrf)
	if res.StatusCode != 200 || len(res.Cookies()) != 1 {
		t.Fatal("logout failed")
	}
	cookie := res.Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.MaxAge >= 0 || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe clearing cookie")
	}
	res = send("GET", "/api/admin/v1/auth/me", "", "")
	if res.StatusCode != 401 {
		t.Fatal("deleted session accepted")
	}
	res = send("POST", "/api/admin/v1/auth/logout", server.URL, csrf)
	if res.StatusCode != 401 {
		t.Fatal("unexpected logout retry")
	}
	// Close only this test's pool: no server stop/network fault claim.
	pool.Close()
	res = send("GET", "/api/admin/v1/auth/me", "", "")
	if res.StatusCode != 503 {
		t.Fatal("unavailable DB failed open")
	}
	t.Log("TLS HTTP -> real PostgreSQL: read, CSRF rejection, logout, replay denial, pool unavailable PASS")
}

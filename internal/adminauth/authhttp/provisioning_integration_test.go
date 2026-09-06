//go:build integration

// SPDX-License-Identifier: Apache-2.0
package authhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminauth/sessionhttp"
)

func TestProvisioningHTTPPostgresTLS(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("http2=%t/totp=%t", http2, enabled), func(t *testing.T) { testProvisioningHTTP(t, http2, enabled) })
		}
	}
}
func testProvisioningHTTP(t *testing.T, http2, enabled bool) {
	if os.Getenv("WR_TEST_AUTH_DB") != "local" {
		t.Fatal("requires dedicated local auth DB")
	}
	ctx := context.Background()
	const db = "postgres://wr_auth_lab:local-test-only@127.0.0.1:15432/wr_auth_lab?sslmode=disable"
	root, err := pgx.Connect(ctx, db)
	if err != nil {
		t.Fatal("dedicated auth lab unavailable")
	}
	nonce, _, err := adminauth.NewCSRFToken()
	if err != nil {
		t.Fatal(err)
	}
	schema := "wr_auth_http_" + strings.ToLower(strings.ReplaceAll(nonce[:16], "-", "_"))
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		root.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("test schema cleanup failed")
		}
		root.Close(ctx)
	})
	cfg, err := pgxpool.ParseConfig(db)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sql := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var version string
	if err = pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil || !strings.HasPrefix(version, "17.11") {
		t.Fatal("wrong fixture DB version")
	}
	for _, migration := range []string{pgstore.Migration001, pgstore.Migration002, pgstore.Migration003, pgstore.Migration004} {
		sql(migration)
	}
	vault, _ := pgstore.NewVault(map[string][]byte{"test": bytes.Repeat([]byte{1}, 32)})
	store, _ := pgstore.New(pool, vault)
	passwords, err := adminauth.NewPasswordHasher(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	sql("UPDATE auth_policy SET totp_enabled=$1", enabled)
	login, err := pgstore.NewLoginService(store, passwords, [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := pgstore.NewEnrollmentService(store, passwords, "test")
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := pgstore.NewRecoveryService(store, passwords)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.IssueBootstrapToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The bootstrap handler is tested on its own loopback TLS server. A second
	// loopback TLS server mounts normal auth/enrollment/me/logout, never bootstrap.
	setupServer := httptest.NewUnstartedServer(nil)
	adminServer := httptest.NewUnstartedServer(nil)
	for _, server := range []*httptest.Server{setupServer, adminServer} {
		server.EnableHTTP2 = http2
		server.Config.ReadHeaderTimeout = 5 * time.Second
		server.Config.WriteTimeout = 30 * time.Second
		server.Config.MaxHeaderBytes = 16 * 1024
		t.Cleanup(server.Close)
	}
	adminOrigin := "https://" + adminServer.Listener.Addr().String()
	setupOrigin := "https://" + setupServer.Listener.Addr().String()
	sessions, err := pgstore.NewSessionService(store, adminOrigin, false)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewProvisioningBackend(login, store, recovery, sessions, enrollment)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := NewBootstrap(backend, setupOrigin)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewEnrollment(backend, adminOrigin)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sessionhttp.New(sessions)
	if err != nil {
		t.Fatal(err)
	}
	setupServer.Config.Handler = setup
	adminServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/v1/auth/me" || r.URL.Path == "/api/admin/v1/auth/logout" {
			session.ServeHTTP(w, r)
		} else {
			auth.ServeHTTP(w, r)
		}
	})
	setupServer.StartTLS()
	adminServer.StartTLS()
	jar, _ := cookiejar.New(nil)
	setupClient, adminClient := setupServer.Client(), adminServer.Client()
	for _, client := range []*http.Client{setupClient, adminClient} {
		client.Jar = jar
		client.Timeout = 30 * time.Second
	}
	type response struct {
		status     int
		auth       reply
		enrollment enrollmentReply
		cookies    []*http.Cookie
		raw        []byte
	}
	send := func(path string, body any, proof, csrf string) response {
		t.Helper()
		client, origin := adminClient, adminOrigin
		if path == "/bootstrap" {
			client, origin = setupClient, setupOrigin
		}
		data, _ := json.Marshal(body)
		method := "POST"
		if path == "/auth/me" {
			method = "GET"
			data = nil
		}
		if path == "/auth/logout" {
			data = nil
		}
		r, _ := http.NewRequest(method, origin+"/api/admin/v1"+path, bytes.NewReader(data))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-WR-Auth", "1")
		r.Header.Set("Content-Type", "application/json")
		if proof != "" {
			if path == "/bootstrap" {
				r.Header.Set("X-Bootstrap-Token", proof)
			} else {
				r.Header.Set("Authorization", "Bearer "+proof)
			}
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if res.TLS == nil || (res.ProtoMajor == 2) != http2 || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("transport/cache boundary")
		}
		var out response
		out.status = res.StatusCode
		out.raw = raw
		out.cookies = res.Cookies()
		_ = json.Unmarshal(raw, &out.auth)
		_ = json.Unmarshal(raw, &out.enrollment)
		return out
	}
	assertSession := func(out response, mfa bool) {
		t.Helper()
		if out.status != 200 || out.auth.State != "authenticated" || out.auth.Session == nil || out.auth.Session.Role != "admin" || out.auth.Session.MFAVerified != mfa || !opaque(out.auth.CSRF) || len(out.cookies) != 1 {
			t.Fatal("session response mismatch")
		}
		c := out.cookies[0]
		if c.Name != sessionhttp.CookieName || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" || strings.Contains(string(out.raw), c.Value) {
			t.Fatal("unsafe cookie or body session leakage")
		}
		if send("/auth/me", nil, "", "").status != 200 {
			t.Fatal("issued cookie rejected")
		}
	}
	logout := func(out response) {
		t.Helper()
		if send("/auth/logout", nil, "", out.auth.CSRF).status != 200 {
			t.Fatal("logout rejected")
		}
	}
	credentials := map[string]string{"username": "initial_admin", "password": "local bootstrap password fixture 2026"}
	invalid := send("/bootstrap", credentials, strings.Repeat("A", 43), "")
	if invalid.status != 401 || len(invalid.cookies) != 0 {
		t.Fatal("invalid install token accepted")
	}
	pending := send("/bootstrap", credentials, token.Token(), "")
	if !enabled {
		assertSession(pending, false)
		logout(pending)
		if retry := send("/bootstrap", credentials, token.Token(), ""); retry.status != 401 || len(retry.cookies) != 0 {
			t.Fatal("bootstrap reopened")
		}
		logged := send("/auth/login", credentials, "", "")
		assertSession(logged, false)
		logout(logged)
		return
	}
	if pending.status != 200 || pending.auth.State != "enrollment_required" || !opaque(pending.auth.Challenge) || pending.auth.Expires == nil || pending.auth.CSRF != "" || len(pending.cookies) != 0 {
		t.Fatal("bootstrap must restrict ON proof")
	}
	if bad := send("/auth/totp/enroll", map[string]string{}, token.Token(), ""); bad.status != 401 || len(bad.cookies) != 0 {
		t.Fatal("installation token used as enrollment proof")
	}
	first := send("/auth/totp/enroll", map[string]string{}, pending.auth.Challenge, "")
	again := send("/auth/totp/enroll", map[string]string{}, pending.auth.Challenge, "")
	if first.status != 200 || again.status != 200 || first.enrollment.Secret == "" || first.enrollment != again.enrollment || !first.enrollment.Expires.Equal(*pending.auth.Expires) || len(first.cookies) != 0 || first.auth.CSRF != "" {
		t.Fatal("unstable or privileged enrollment begin")
	}
	bad := send("/auth/totp/enroll/verify", map[string]string{"code": "bad"}, pending.auth.Challenge, "")
	if bad.status != 401 || len(bad.cookies) != 0 || len(bad.auth.Recovery) != 0 {
		t.Fatal("invalid OTP issued material")
	}
	var now time.Time
	if err = pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(first.enrollment.Secret)
	if err != nil {
		t.Fatal("invalid manual key")
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	code := fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
	completed := send("/auth/totp/enroll/verify", map[string]string{"code": code}, pending.auth.Challenge, "")
	assertSession(completed, true)
	seen := map[string]bool{}
	for _, code := range completed.auth.Recovery {
		if !opaque(code) || seen[code] {
			t.Fatal("invalid recovery bundle")
		}
		seen[code] = true
	}
	if len(seen) != 10 || strings.Contains(string(completed.raw), first.enrollment.Secret) {
		t.Fatal("missing codes or repeated secret")
	}
	logout(completed)
	for _, path := range []string{"/auth/totp/enroll", "/auth/totp/enroll/verify"} {
		body := map[string]string{}
		if strings.HasSuffix(path, "/verify") {
			body["code"] = code
		}
		out := send(path, body, pending.auth.Challenge, "")
		if out.status != 401 || len(out.cookies) != 0 || len(out.auth.Recovery) != 0 || out.enrollment.Secret != "" {
			t.Fatal("completed proof replay exposed material")
		}
	}
	if retry := send("/bootstrap", credentials, token.Token(), ""); retry.status != 401 {
		t.Fatal("bootstrap reopened")
	}
	challenge := send("/auth/login", credentials, "", "")
	if challenge.status != 200 || challenge.auth.State != "totp_required" {
		t.Fatal("enrollment not active on login")
	}
	recovered := send("/auth/totp/recover", map[string]string{"recoveryCode": completed.auth.Recovery[0]}, challenge.auth.Challenge, "")
	assertSession(recovered, true)
	logout(recovered)
	challenge = send("/auth/login", credentials, "", "")
	reused := send("/auth/totp/recover", map[string]string{"recoveryCode": completed.auth.Recovery[0]}, challenge.auth.Challenge, "")
	if reused.status != 401 || len(reused.cookies) != 0 {
		t.Fatal("used recovery code accepted")
	}
	var remaining int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM auth_recovery_codes WHERE NOT consumed").Scan(&remaining); err != nil || remaining != 9 {
		t.Fatal("wrong recovery consumption")
	}
}

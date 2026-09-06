//go:build integration

// SPDX-License-Identifier: Apache-2.0
package authhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminauth/sessionhttp"
)

func TestAuthHTTPPostgresTLS(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", http2), func(t *testing.T) { testAuthHTTPPostgresTLS(t, http2) })
	}
}
func testAuthHTTPPostgresTLS(t *testing.T, http2 bool) {
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
	const password = "local HTTP password fixture 2026"
	hash, err := passwords.Hash(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	sql("INSERT INTO auth_accounts (id,role) VALUES ('http_user','viewer')")
	sql("INSERT INTO auth_password_credentials VALUES ('http_user',1,$1)", hash.StorageValue())
	secret, _ := adminauth.ParseSecret("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	sealed, _ := vault.Seal(adminauth.CredentialRef{UserID: "http_user", Version: 1}, "test", secret)
	sql("INSERT INTO auth_totp_credentials (user_id,version,key_id,sealed_secret) VALUES ('http_user',1,'test',$1)", sealed)
	recoveryCode, _, _ := adminauth.NewCSRFToken()
	recoveryHash, err := passwords.Hash(ctx, "wr-recovery-v1:"+recoveryCode)
	if err != nil {
		t.Fatal(err)
	}
	for slot := 1; slot <= 10; slot++ {
		sql("INSERT INTO auth_recovery_codes (user_id,credential_version,slot,code_hash,consumed) VALUES ('http_user',1,$1,$2,$3)", slot, recoveryHash.StorageValue(), slot != 1)
	}
	login, _ := pgstore.NewLoginService(store, passwords, [32]byte{1})
	recovery, _ := pgstore.NewRecoveryService(store, passwords)
	server := httptest.NewUnstartedServer(nil)
	server.EnableHTTP2 = http2
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	sessions, _ := pgstore.NewSessionService(store, origin, false)
	backend, _ := NewPostgresBackend(login, store, recovery, sessions)
	auth, _ := New(backend, origin)
	session, _ := sessionhttp.New(sessions)
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Config.MaxHeaderBytes = 16 * 1024
	server.Config.WriteTimeout = 30 * time.Second
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/v1/auth/me" || r.URL.Path == "/api/admin/v1/auth/logout" {
			session.ServeHTTP(w, r)
		} else {
			auth.ServeHTTP(w, r)
		}
	})
	server.StartTLS()
	client := server.Client()
	client.Timeout = 30 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	send := func(path string, body any, bearer, csrf string) (*http.Response, reply, []byte) {
		t.Helper()
		data, _ := json.Marshal(body)
		method := "POST"
		if path == "/auth/me" {
			method = "GET"
			data = nil
		}
		if path == "/auth/logout" {
			data = nil
		}
		req, _ := http.NewRequest(method, server.URL+"/api/admin/v1"+path, bytes.NewReader(data))
		req.Header.Set("Origin", origin)
		req.Header.Set("X-WR-Auth", "1")
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var out reply
		_ = json.Unmarshal(raw, &out)
		if res.TLS == nil || res.Header.Get("Cache-Control") != "no-store" || (res.ProtoMajor == 2) != http2 {
			t.Fatal("unsafe TLS response")
		}
		return res, out, raw
	}
	credentials := map[string]string{"username": "http_user", "password": password}
	res, pending, _ := send("/auth/login", credentials, "", "")
	if res.StatusCode != 200 || pending.State != "totp_required" || len(res.Cookies()) != 0 || pending.Expires == nil {
		t.Fatal("password challenge over TLS failed")
	}
	var expiry time.Time
	if err = pool.QueryRow(ctx, "SELECT expires_at FROM auth_totp_challenges WHERE NOT consumed").Scan(&expiry); err != nil || !expiry.Equal(*pending.Expires) {
		t.Fatal("fabricated challenge expiry")
	}
	res, _, _ = send("/auth/totp/verify", map[string]string{"code": "bad"}, pending.Challenge, "")
	if res.StatusCode != 401 || len(res.Cookies()) != 0 {
		t.Fatal("OTP failure cookie")
	}
	var now time.Time
	if err = pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, []byte("12345678901234567890"))
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	code := fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1000000)
	res, authenticated, raw := send("/auth/totp/verify", map[string]string{"code": code}, pending.Challenge, "")
	if res.StatusCode != 200 || authenticated.Session == nil || !authenticated.Session.MFAVerified || authenticated.CSRF == "" || len(res.Cookies()) != 1 {
		t.Fatal("OTP cookie/session issuance")
	}
	cookie := res.Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" || strings.Contains(string(raw), cookie.Value) {
		t.Fatal("session token leaked or unsafe cookie")
	}
	res, _, _ = send("/auth/me", nil, "", "")
	if res.StatusCode != 200 {
		t.Fatal("issued cookie not accepted by me")
	}
	res, _, _ = send("/auth/login", credentials, "", "")
	if res.StatusCode != 401 {
		t.Fatal("cookie/preauth mixed")
	}
	res, _, _ = send("/auth/logout", nil, "", authenticated.CSRF)
	if res.StatusCode != 200 {
		t.Fatal("issued CSRF logout failed")
	}
	res, pending, _ = send("/auth/login", credentials, "", "")
	if res.StatusCode != 200 {
		t.Fatal("second login")
	}
	res, authenticated, _ = send("/auth/totp/recover", map[string]string{"recoveryCode": recoveryCode}, pending.Challenge, "")
	if res.StatusCode != 200 || authenticated.Session == nil || !authenticated.Session.MFAVerified {
		t.Fatal("recovery TLS login")
	}
	res, _, _ = send("/auth/logout", nil, "", authenticated.CSRF)
	if res.StatusCode != 200 {
		t.Fatal("recovery logout")
	}
	sql("UPDATE auth_policy SET totp_enabled=false,version=2")
	res, authenticated, _ = send("/auth/login", credentials, "", "")
	if res.StatusCode != 200 || authenticated.Session == nil || authenticated.Session.MFAVerified {
		t.Fatal("OFF TLS login")
	}
	res, _, _ = send("/auth/logout", nil, "", authenticated.CSRF)
	if res.StatusCode != 200 {
		t.Fatal("OFF logout")
	}
	sql("UPDATE auth_policy SET totp_enabled=true,version=3; DELETE FROM auth_recovery_codes; DELETE FROM auth_totp_credentials")
	res, pending, _ = send("/auth/login", credentials, "", "")
	if res.StatusCode != 200 || pending.State != "enrollment_required" || pending.Expires == nil || len(res.Cookies()) != 0 {
		t.Fatal("unenrolled TLS restriction")
	}
	res, _, _ = send("/auth/totp/verify", map[string]string{"code": code}, pending.Challenge, "")
	if res.StatusCode != 401 {
		t.Fatal("enrollment proof bypass")
	}
	sql("UPDATE auth_login_buckets SET used=20 WHERE bucket_key LIKE 'http-source:%'")
	res, _, _ = send("/auth/login", credentials, "", "")
	if res.StatusCode != 429 || len(res.Cookies()) != 0 {
		t.Fatal("shared HTTP limit missing")
	}
	pool.Close()
	res, _, raw = send("/auth/login", credentials, "", "")
	if res.StatusCode != 503 || strings.Contains(string(raw), "pool closed") {
		t.Fatal("DB failure exposed")
	}
	t.Log("TLS password/OTP/recovery/OFF -> cookie -> me/logout; enrollment restriction, DB rate limit and unavailable PASS; no browser or production listener")
}

// SPDX-License-Identifier: Apache-2.0
// Package authhttp implements opt-in private Control authentication endpoints.
// It starts no listener. Mount only on a direct TLS server with header/write timeouts.
package authhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminauth/sessionhttp"
)

type Backend interface {
	Login(context.Context, string, string, netip.Addr) (pgstore.LoginResult, error)
	Verify(context.Context, string, string) (pgstore.Grant, error)
	Recover(context.Context, string, string) (pgstore.Grant, error)
	Me(context.Context, string) (pgstore.SessionView, error)
	ReserveAuthHTTP(context.Context, netip.Addr) error
}
type ProvisioningBackend interface {
	Backend
	Bootstrap(context.Context, string, string, string, netip.Addr) (pgstore.LoginResult, error)
	BeginEnrollment(context.Context, string) (pgstore.EnrollmentSetup, error)
	CompleteEnrollment(context.Context, string, string) (pgstore.EnrollmentGrant, error)
}
type PostgresBackend struct {
	login      *pgstore.LoginService
	store      *pgstore.Store
	recovery   *pgstore.RecoveryService
	sessions   *pgstore.SessionService
	enrollment *pgstore.EnrollmentService
}

func NewPostgresBackend(l *pgstore.LoginService, s *pgstore.Store, r *pgstore.RecoveryService, session *pgstore.SessionService) (*PostgresBackend, error) {
	if l == nil || s == nil || r == nil || session == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &PostgresBackend{login: l, store: s, recovery: r, sessions: session}, nil
}
func NewProvisioningBackend(l *pgstore.LoginService, s *pgstore.Store, r *pgstore.RecoveryService, session *pgstore.SessionService, enrollment *pgstore.EnrollmentService) (*PostgresBackend, error) {
	b, err := NewPostgresBackend(l, s, r, session)
	if err != nil || enrollment == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	b.enrollment = enrollment
	return b, nil
}
func (b *PostgresBackend) Bootstrap(ctx context.Context, token, user, password string, peer netip.Addr) (pgstore.LoginResult, error) {
	return b.login.Bootstrap(ctx, token, user, password, peer)
}
func (b *PostgresBackend) BeginEnrollment(ctx context.Context, token string) (pgstore.EnrollmentSetup, error) {
	if b.enrollment == nil {
		return pgstore.EnrollmentSetup{}, adminauth.ErrAuthUnavailable
	}
	return b.enrollment.Begin(ctx, token)
}
func (b *PostgresBackend) CompleteEnrollment(ctx context.Context, token, code string) (pgstore.EnrollmentGrant, error) {
	if b.enrollment == nil {
		return pgstore.EnrollmentGrant{}, adminauth.ErrAuthUnavailable
	}
	return b.enrollment.Complete(ctx, token, code)
}
func (b *PostgresBackend) Login(ctx context.Context, u, p string, peer netip.Addr) (pgstore.LoginResult, error) {
	return b.login.Login(ctx, u, p, peer)
}
func (b *PostgresBackend) Verify(ctx context.Context, t, c string) (pgstore.Grant, error) {
	return b.store.CompleteTOTP(ctx, t, c)
}
func (b *PostgresBackend) Recover(ctx context.Context, t, c string) (pgstore.Grant, error) {
	return b.recovery.Complete(ctx, t, c)
}
func (b *PostgresBackend) Me(ctx context.Context, t string) (pgstore.SessionView, error) {
	return b.sessions.Me(ctx, t)
}
func (b *PostgresBackend) ReserveAuthHTTP(ctx context.Context, p netip.Addr) error {
	return b.login.ReserveAuthHTTP(ctx, p)
}

type Handler struct {
	backend       Backend
	origin, host  string
	provision     ProvisioningBackend
	bootstrapOnly bool
}

// Shared across Handler instances in one process. This is not a global cluster cap.
var activeRequests = make(chan struct{}, 2)

func New(backend Backend, origin string) (*Handler, error) {
	if backend == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	if _, err := adminauth.NewOriginPolicy(origin, false); err != nil {
		return nil, err
	}
	u, _ := url.Parse(origin)
	return &Handler{backend: backend, origin: origin, host: u.Host}, nil
}

// NewEnrollment opts into initial enrollment, but never exposes bootstrap.
func NewEnrollment(backend ProvisioningBackend, origin string) (*Handler, error) {
	h, err := New(backend, origin)
	if err != nil {
		return nil, err
	}
	h.provision = backend
	return h, nil
}

// NewBootstrap serves only bootstrap. Mount on a separate loopback TLS listener.
// Per-request socket checks are additional guards, not proof of listener binding.
func NewBootstrap(backend ProvisioningBackend, origin string) (*Handler, error) {
	h, err := NewEnrollment(backend, origin)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(origin)
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || ip.Zone() != "" || !ip.Unmap().IsLoopback() {
		return nil, adminauth.ErrForbidden
	}
	h.bootstrapOnly = true
	return h, nil
}
func problem(w http.ResponseWriter, status int, code string) {
	id, _, err := adminauth.NewCSRFToken()
	if err != nil {
		id = "unavailable"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "requestId": id})
}
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, adminauth.ErrInvalidOrReplayed), errors.Is(err, adminauth.ErrUnauthenticated):
		problem(w, 401, "UNAUTHENTICATED")
	case errors.Is(err, adminauth.ErrForbidden):
		problem(w, 403, "FORBIDDEN")
	case errors.Is(err, pgstore.ErrSetupReview):
		problem(w, 412, "SETUP_REVIEW_REQUIRED")
	case errors.Is(err, pgstore.ErrAuthHTTPRateLimit):
		w.Header().Set("Retry-After", "60")
		problem(w, 429, "AUTH_RATE_LIMITED")
	default:
		problem(w, 503, "AUTH_UNAVAILABLE")
	}
}
func opaque(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == 32
}

// Exact field names/string values, no duplicates/unknown fields/null/trailing JSON.
func object(data []byte, keys ...string) (map[string]string, error) {
	if !utf8.Valid(data) {
		return nil, adminauth.ErrUnauthenticated
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, adminauth.ErrUnauthenticated
	}
	out := map[string]string{}
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, adminauth.ErrUnauthenticated
		}
		allowed := false
		for _, want := range keys {
			if want == key {
				allowed = true
			}
		}
		if _, duplicate := out[key]; !allowed || duplicate {
			return nil, adminauth.ErrUnauthenticated
		}
		v, err := d.Token()
		if err != nil {
			return nil, err
		}
		s, ok := v.(string)
		if !ok {
			return nil, adminauth.ErrUnauthenticated
		}
		out[key] = s
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(out) != len(keys) {
		return nil, adminauth.ErrUnauthenticated
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, adminauth.ErrUnauthenticated
	}
	return out, nil
}

type reply struct {
	State     string               `json:"state"`
	Session   *pgstore.SessionView `json:"session,omitempty"`
	CSRF      string               `json:"csrfToken,omitempty"`
	Challenge string               `json:"challengeToken,omitempty"`
	Expires   *time.Time           `json:"expiresAt,omitempty"`
	Recovery  []string             `json:"recoveryCodes,omitempty"`
}

func (reply) String() string   { return "[REDACTED_AUTH_REPLY]" }
func (reply) GoString() string { return "[REDACTED_AUTH_REPLY]" }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	path := r.URL.Path
	if r.URL.EscapedPath() != path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	known := path == "/api/admin/v1/auth/login" || path == "/api/admin/v1/auth/totp/verify" || path == "/api/admin/v1/auth/totp/recover"
	if h.provision != nil {
		known = known || path == "/api/admin/v1/auth/totp/enroll" || path == "/api/admin/v1/auth/totp/enroll/verify"
	}
	if h.bootstrapOnly {
		known = path == "/api/admin/v1/bootstrap"
	}
	if !known {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	if r.TLS == nil || r.Host != h.host || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != h.origin || len(r.Header.Values("X-WR-Auth")) != 1 || r.Header.Get("X-WR-Auth") != "1" {
		problem(w, 403, "FORBIDDEN")
		return
	}
	if len(r.Header.Values("Sec-Fetch-Site")) > 1 || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
		problem(w, 403, "FORBIDDEN")
		return
	}
	// Cookie credentials cannot accompany pre-auth/challenge endpoints. Logout first.
	if len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("X-CSRF-Token")) != 0 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	challenge := ""
	if !h.bootstrapOnly && len(r.Header.Values("X-Bootstrap-Token")) != 0 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	if h.bootstrapOnly {
		if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("X-Bootstrap-Token")) != 1 || !opaque(r.Header.Get("X-Bootstrap-Token")) {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
		challenge = r.Header.Get("X-Bootstrap-Token")
	} else if path == "/api/admin/v1/auth/login" {
		if len(r.Header.Values("Authorization")) != 0 {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
	} else {
		if len(r.Header.Values("Authorization")) != 1 {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || !opaque(strings.TrimPrefix(header, "Bearer ")) {
			problem(w, 401, "UNAUTHENTICATED")
			return
		}
		challenge = header[7:]
	}
	if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) != 0 {
		problem(w, 415, "INVALID_REQUEST")
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		problem(w, 415, "INVALID_REQUEST")
		return
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || peer.Addr().Zone() != "" {
		problem(w, 403, "FORBIDDEN")
		return
	}
	if h.bootstrapOnly {
		local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		if !peer.Addr().Unmap().IsLoopback() || !ok || local == nil || local.Zone != "" || !local.IP.IsLoopback() {
			problem(w, 403, "FORBIDDEN")
			return
		}
	}
	select {
	case activeRequests <- struct{}{}:
		defer func() { <-activeRequests }()
	default:
		problem(w, 503, "AUTH_UNAVAILABLE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.backend.ReserveAuthHTTP(ctx, peer.Addr().Unmap()); err != nil {
		fail(w, err)
		return
	}
	rc := http.NewResponseController(w)
	if err = rc.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		problem(w, 503, "AUTH_UNAVAILABLE")
		return
	}
	defer rc.SetReadDeadline(time.Time{})
	if r.ContentLength > 4096 {
		problem(w, 413, "INVALID_REQUEST")
		return
	}
	if r.Body == nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if len(data) > 4096 {
		problem(w, 413, "INVALID_REQUEST")
		return
	}
	// Do not turn the body deadline into a five-second KDF/HTTP2 stream deadline.
	if err = rc.SetReadDeadline(time.Time{}); err != nil {
		problem(w, 503, "AUTH_UNAVAILABLE")
		return
	}
	keys := []string{"username", "password"}
	if path == "/api/admin/v1/auth/totp/verify" {
		keys = []string{"code"}
	}
	if path == "/api/admin/v1/auth/totp/recover" {
		keys = []string{"recoveryCode"}
	}
	if path == "/api/admin/v1/auth/totp/enroll" {
		keys = nil
	}
	if path == "/api/admin/v1/auth/totp/enroll/verify" {
		keys = []string{"code"}
	}
	fields, err := object(data, keys...)
	if err != nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	var grant pgstore.Grant
	var out reply
	var result pgstore.LoginResult
	switch path {
	case "/api/admin/v1/auth/login", "/api/admin/v1/bootstrap":
		if h.bootstrapOnly {
			result, err = h.provision.Bootstrap(ctx, challenge, fields["username"], fields["password"], peer.Addr().Unmap())
		} else {
			result, err = h.backend.Login(ctx, fields["username"], fields["password"], peer.Addr().Unmap())
		}
		if err != nil {
			fail(w, err)
			return
		}
		grant = result.SessionGrant()
		if result.ChallengeToken() != "" || result.EnrollmentToken() != "" {
			token := result.ChallengeToken()
			state := "totp_required"
			if result.EnrollmentToken() != "" {
				token = result.EnrollmentToken()
				state = "enrollment_required"
			}
			expiry := result.ExpiresAt()
			if !opaque(token) || expiry.IsZero() || grant.SessionToken() != "" {
				fail(w, adminauth.ErrAuthUnavailable)
				return
			}
			out = reply{State: state, Challenge: token, Expires: &expiry}
		}
	case "/api/admin/v1/auth/totp/verify":
		grant, err = h.backend.Verify(ctx, challenge, fields["code"])
	case "/api/admin/v1/auth/totp/recover":
		grant, err = h.backend.Recover(ctx, challenge, fields["recoveryCode"])
	case "/api/admin/v1/auth/totp/enroll":
		setup, e := h.provision.BeginEnrollment(ctx, challenge)
		if e != nil {
			fail(w, e)
			return
		}
		key, e := setup.ManualKey()
		if e != nil || setup.ExpiresAt().IsZero() {
			fail(w, adminauth.ErrAuthUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(enrollmentReply{key, setup.ExpiresAt()})
		return
	case "/api/admin/v1/auth/totp/enroll/verify":
		completed, e := h.provision.CompleteEnrollment(ctx, challenge, fields["code"])
		if e != nil {
			fail(w, e)
			return
		}
		codes := completed.RecoveryCodes()
		seen := map[string]bool{}
		for _, code := range codes {
			if !opaque(code) || seen[code] {
				fail(w, adminauth.ErrAuthUnavailable)
				return
			}
			seen[code] = true
		}
		grant = completed.SessionGrant()
		out.Recovery = codes[:]
	}
	if err != nil {
		fail(w, err)
		return
	}
	if out.State == "" {
		if !opaque(grant.SessionToken()) || !opaque(grant.CSRFToken()) {
			fail(w, adminauth.ErrAuthUnavailable)
			return
		}
		view, e := h.backend.Me(ctx, grant.SessionToken())
		if e != nil {
			fail(w, e)
			return
		}
		out = reply{State: "authenticated", Session: &view, CSRF: grant.CSRFToken(), Recovery: out.Recovery}
		http.SetCookie(w, &http.Cookie{Name: sessionhttp.CookieName, Value: grant.SessionToken(), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: int(adminauth.SessionAbsoluteTTL.Seconds()), Expires: view.AbsoluteExpiresAt})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

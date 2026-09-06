// SPDX-License-Identifier: Apache-2.0
// Package sessionhttp is a private Control adapter. It starts no listener and must
// never be mounted on the public Gateway. Login/bootstrap are not implemented here.
package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
)

const CookieName = "__Host-wrs"

type Backend interface {
	Me(context.Context, string) (pgstore.SessionView, error)
	Logout(context.Context, string, *http.Request) error
}
type Handler struct{ backend Backend }

func New(backend Backend) (*Handler, error) {
	if backend == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	return &Handler{backend}, nil
}
func sessionCookie(r *http.Request) (string, bool) {
	var value string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == CookieName {
			value = c.Value
			count++
		}
	}
	return value, count == 1 && len(value) == 43
}
func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
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
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	path := r.URL.Path
	if r.URL.EscapedPath() != path || r.URL.RawQuery != "" {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	method := ""
	switch path {
	case "/api/admin/v1/auth/me":
		method = http.MethodGet
	case "/api/admin/v1/auth/logout":
		method = http.MethodPost
	default:
		problem(w, 404, "NOT_FOUND")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	// Endpoints accept no payload; bound reads even for an unknown/chunked length.
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(b) != 0 {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
	}
	// Do not mix bearer/challenge and browser-cookie credentials.
	if len(r.Header.Values("Authorization")) != 0 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	token, ok := sessionCookie(r)
	if !ok {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	var result any
	var err error
	if method == http.MethodGet {
		result, err = h.backend.Me(r.Context(), token)
	} else {
		err = h.backend.Logout(r.Context(), token, r)
		result = map[string]bool{"ok": true}
	}
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrUnauthenticated):
			if method == http.MethodPost {
				clearCookie(w)
			}
			problem(w, 401, "UNAUTHENTICATED")
		case errors.Is(err, adminauth.ErrForbidden):
			problem(w, 403, "FORBIDDEN")
		default:
			problem(w, 503, "AUTH_UNAVAILABLE")
		}
		return
	}
	if method == http.MethodPost {
		clearCookie(w)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

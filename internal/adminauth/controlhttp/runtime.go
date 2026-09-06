// SPDX-License-Identifier: Apache-2.0
package controlhttp

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/control"
)

type PublicationBackend interface {
	RouteCheck(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error)
	Publish(context.Context, string, *http.Request, []byte) (pgstore.ControlReply, error)
	View(context.Context, string) (pgstore.ControlReply, error)
	Runtime(context.Context, string, string) (pgstore.ControlReply, error)
	Operate(context.Context, string, string, *http.Request, []byte) (pgstore.ControlReply, error)
	Events(context.Context, string, string) (pgstore.ControlReply, error)
	ChangeEvent(context.Context, string, string, string, *http.Request, []byte) (pgstore.ControlReply, error)
}
type RuntimeHandler struct {
	backend  PublicationBackend
	security *pgstore.SecurityService
}

func NewRuntime(b PublicationBackend, security ...*pgstore.SecurityService) (*RuntimeHandler, error) {
	if b == nil {
		return nil, adminauth.ErrAuthUnavailable
	}
	h := &RuntimeHandler{backend: b}
	if len(security) > 1 {
		return nil, adminauth.ErrAuthUnavailable
	}
	if len(security) == 1 {
		h.security = security[0]
	}
	return h, nil
}

var roomRoute = regexp.MustCompile(`^/api/admin/v1/rooms/([a-z][a-z0-9_-]{0,63})/(runtime|events)$`)
var eventRoute = regexp.MustCompile(`^/api/admin/v1/events/([A-Za-z0-9_-]{1,80})(/resume)?$`)
var userRoute = regexp.MustCompile(`^/api/admin/v1/users/([A-Za-z0-9_-]{1,128})(/totp-reset)?$`)

func RuntimePath(p string) bool {
	if p == "/api/admin/v1/config/route-check" {
		return true
	}
	return p == "/api/admin/v1/security/totp/enrollment/start" || p == "/api/admin/v1/security/totp/enrollment/verify" || p == "/api/admin/v1/security/totp" || p == "/api/admin/v1/security/totp/enrollment" || p == "/api/admin/v1/auth/reauth" || p == "/api/admin/v1/users" || userRoute.MatchString(p) || p == "/api/admin/v1/config/publish" || p == "/api/admin/v1/config/delivery" || roomRoute.MatchString(p) || eventRoute.MatchString(p)
}
func (h *RuntimeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.EscapedPath() != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	p := r.URL.Path
	room, event, user := "", "", ""
	kind := ""
	allowed := ""
	switch p {
	case "/api/admin/v1/config/route-check":
		kind = "route-check"
		allowed = "POST"
	case "/api/admin/v1/security/totp/enrollment/start", "/api/admin/v1/security/totp/enrollment/verify":
		if h.security != nil {
			kind = "policy-enrollment-step"
			allowed = "POST"
		}
	case "/api/admin/v1/security/totp":
		if h.security != nil {
			kind = "policy"
			allowed = "GET, PUT"
		}
	case "/api/admin/v1/security/totp/enrollment":
		if h.security != nil {
			kind = "policy-enrollment"
			allowed = "POST"
		}
	case "/api/admin/v1/users":
		if h.security != nil {
			kind = "users"
			allowed = "GET, POST"
		}
	case "/api/admin/v1/auth/reauth":
		if h.security != nil {
			kind = "reauth"
			allowed = "POST"
		}
	case "/api/admin/v1/config/publish":
		kind = "publish"
		allowed = "POST"
	case "/api/admin/v1/config/delivery":
		kind = "delivery"
		allowed = "GET"
	default:
		if m := userRoute.FindStringSubmatch(p); m != nil && h.security != nil {
			user = m[1]
			kind = "user"
			allowed = "PATCH, DELETE"
			if m[2] != "" {
				kind = "reset"
				allowed = "POST"
			}
		}
		if m := roomRoute.FindStringSubmatch(p); m != nil {
			room = m[1]
			kind = m[2]
			if kind == "runtime" {
				allowed = "GET, PATCH"
			} else {
				allowed = "GET, POST"
			}
		}
		if m := eventRoute.FindStringSubmatch(p); m != nil {
			event = m[1]
			kind = "event"
			allowed = "PUT, DELETE"
			if m[2] != "" {
				allowed = "POST"
			}
		}
	}
	if kind == "" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	valid := false
	for _, m := range strings.Split(allowed, ", ") {
		if r.Method == m {
			valid = true
		}
	}
	if !valid {
		w.Header().Set("Allow", allowed)
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	token := ""
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == "__Host-wrs" {
			token = c.Value
			count++
		}
	}
	if count != 1 || len(token) != 43 {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	var raw []byte
	var err error
	limit := control.MaxConfigBytes
	if kind == "route-check" {
		limit = pgstore.MaxRouteCheckBytes
	}
	if r.Body != nil {
		raw, err = io.ReadAll(io.LimitReader(r.Body, int64(limit+1)))
	}
	if err != nil || len(raw) > limit || (r.Method == "GET" && len(raw) > 0) {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if r.Method != "GET" && (r.Method != "DELETE" || len(raw) > 0) {
		media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if len(r.Header.Values("Content-Type")) != 1 || e != nil || media != "application/json" {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		for k, v := range params {
			if k != "charset" || !strings.EqualFold(v, "utf-8") {
				problem(w, 400, "INVALID_REQUEST")
				return
			}
		}
	}
	var out pgstore.ControlReply
	switch kind {
	case "route-check":
		out, err = h.backend.RouteCheck(r.Context(), token, r, raw)
	case "policy-enrollment-step":
		out, err = h.security.PolicyEnrollment(r.Context(), token, r, raw, strings.HasSuffix(p, "/verify"))
	case "policy":
		if r.Method == "GET" {
			out, err = h.security.Policy(r.Context(), token)
		} else {
			out, err = h.security.ChangePolicy(r.Context(), token, r, raw)
		}
	case "policy-enrollment":
		out, err = h.security.BeginPolicyEnrollment(r.Context(), token, r, raw)
	case "users":
		if r.Method == "GET" {
			out, err = h.security.Users(r.Context(), token)
		} else {
			out, err = h.security.CreateUser(r.Context(), token, r, raw)
		}
	case "user", "reset":
		out, err = h.security.ChangeUser(r.Context(), token, user, r, raw, kind == "reset")
	case "reauth":
		out, err = h.security.Reauthenticate(r.Context(), token, r, raw)
	case "publish":
		out, err = h.backend.Publish(r.Context(), token, r, raw)
	case "delivery":
		out, err = h.backend.View(r.Context(), token)
	case "runtime":
		if r.Method == "GET" {
			out, err = h.backend.Runtime(r.Context(), token, room)
		} else {
			out, err = h.backend.Operate(r.Context(), token, room, r, raw)
		}
	case "events":
		if r.Method == "GET" {
			out, err = h.backend.Events(r.Context(), token, room)
		} else {
			out, err = h.backend.ChangeEvent(r.Context(), token, room, "", r, raw)
		}
	case "event":
		out, err = h.backend.ChangeEvent(r.Context(), token, "", event, r, raw)
	}
	if err != nil {
		switch {
		case errors.Is(err, adminauth.ErrUnauthenticated):
			problem(w, 401, "UNAUTHENTICATED")
		case errors.Is(err, adminauth.ErrInvalidOrReplayed):
			problem(w, 401, "TOTP_INVALID_OR_REPLAYED")
		case errors.Is(err, adminauth.ErrForbidden):
			problem(w, 403, "FORBIDDEN")
		default:
			problem(w, 503, "CONTROL_UNAVAILABLE")
		}
		return
	}
	if out.Status < 200 || out.Status > 599 || len(out.Body) == 0 {
		problem(w, 503, "CONTROL_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if out.Status >= 400 {
		w.Header().Set("Content-Type", "application/problem+json")
	}
	if out.ETag != "" {
		w.Header().Set("ETag", out.ETag)
	}
	if out.Replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	if out.RotatedGrant.SessionToken() != "" {
		http.SetCookie(w, &http.Cookie{Name: "__Host-wrs", Value: out.RotatedGrant.SessionToken(), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(adminauth.SessionAbsoluteTTL.Seconds()), Expires: time.Now().Add(adminauth.SessionAbsoluteTTL)})
		w.Header().Set("X-CSRF-Token", out.RotatedGrant.CSRFToken())
	}
	w.WriteHeader(out.Status)
	_, _ = w.Write(out.Body)
}

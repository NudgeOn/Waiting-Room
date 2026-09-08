// SPDX-License-Identifier: Apache-2.0
package authhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/installplan"
)

type SetupBackend interface {
	Inspect(context.Context, string) (pgstore.SetupInspection, error)
	Calibrate(context.Context, string) (map[string]any, error)
	Plan(context.Context, string, installplan.Input) (pgstore.SetupReview, error)
	Apply(context.Context, string, installplan.SetupApply) (pgstore.SetupReport, error)
}
type SetupHandler struct {
	service      SetupBackend
	backend      Backend
	origin, host string
}

func NewSetup(service SetupBackend, backend Backend, origin string) *SetupHandler {
	return &SetupHandler{service, backend, origin, strings.TrimPrefix(origin, "https://")}
}
func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := r.URL.Path
	if path != "/api/admin/v1/setup/inspect" && path != "/api/admin/v1/setup/calibrate" && path != "/api/admin/v1/setup/plan" && path != "/api/admin/v1/setup/apply" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if err != nil || peer.Addr().Zone() != "" || !peer.Addr().Unmap().IsLoopback() || !ok || local == nil || local.Zone != "" || !local.IP.IsLoopback() || r.TLS == nil || r.Host != h.host {
		problem(w, 403, "FORBIDDEN")
		return
	}
	if r.URL.EscapedPath() != path || r.URL.RawQuery != "" || r.URL.ForceQuery {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != h.origin || len(r.Header.Values("X-WR-Auth")) != 1 || r.Header.Get("X-WR-Auth") != "1" || len(r.Header.Values("Sec-Fetch-Site")) > 1 || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
		problem(w, 403, "FORBIDDEN")
		return
	}
	if len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("X-CSRF-Token")) != 0 || len(r.Header.Values("X-Bootstrap-Token")) != 1 || !opaque(r.Header.Get("X-Bootstrap-Token")) {
		problem(w, 401, "UNAUTHENTICATED")
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) != 0 || media != "application/json" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		problem(w, 415, "INVALID_REQUEST")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err = h.backend.ReserveAuthHTTP(ctx, peer.Addr().Unmap()); err != nil {
		fail(w, err)
		return
	}
	rc := http.NewResponseController(w)
	if rc.SetReadDeadline(time.Now().Add(5*time.Second)) != nil {
		problem(w, 503, "AUTH_UNAVAILABLE")
		return
	}
	defer rc.SetReadDeadline(time.Time{})
	if r.Body == nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, installplan.MaxInputBytes+1))
	if err != nil || len(raw) > installplan.MaxInputBytes {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if rc.SetReadDeadline(time.Time{}) != nil {
		problem(w, 503, "AUTH_UNAVAILABLE")
		return
	}
	token := r.Header.Get("X-Bootstrap-Token")
	var out any
	switch path {
	case "/api/admin/v1/setup/inspect", "/api/admin/v1/setup/calibrate":
		if _, err = object(raw); err != nil {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		if strings.HasSuffix(path, "inspect") {
			out, err = h.service.Inspect(ctx, token)
		} else {
			out, err = h.service.Calibrate(ctx, token)
		}
	case "/api/admin/v1/setup/plan":
		var in installplan.Input
		in, err = installplan.Decode(bytes.NewReader(raw))
		if err == nil {
			out, err = h.service.Plan(ctx, token, in)
		}
	case "/api/admin/v1/setup/apply":
		var in installplan.SetupApply
		in, err = installplan.DecodeSetupApply(bytes.NewReader(raw))
		if err == nil {
			out, err = h.service.Apply(ctx, token, in)
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, installplan.ErrInput):
			problem(w, 400, "INVALID_REQUEST")
		case errors.Is(err, pgstore.ErrSetupConflict):
			problem(w, 409, "SETUP_CONFLICT")
		case errors.Is(err, pgstore.ErrSetupReview):
			problem(w, 412, "SETUP_REVIEW_REQUIRED")
		default:
			fail(w, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

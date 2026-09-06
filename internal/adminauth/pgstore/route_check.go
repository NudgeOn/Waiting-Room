// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"net/http"
	"time"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/control"
)

const MaxRouteCheckBytes = 8192

type RouteCheckInput struct {
	Source string `json:"source"`
	URL    string `json:"url"`
}
type RouteCheckResult struct {
	Source     string        `json:"source"`
	Scope      string        `json:"scope"`
	Revision   int64         `json:"revision"`
	Generation int64         `json:"generation"`
	Match      control.Match `json:"match"`
	Mode       string        `json:"mode,omitempty"`
}

// RouteCheck is a read-only POST so target queries never enter the Admin request
// URL. No commands, audit payloads, idle refresh or remote calls are performed.
func (s *PublicationService) RouteCheck(ctx context.Context, token string, r *http.Request, raw []byte) (ControlReply, error) {
	if r == nil || r.Method != "POST" || len(raw) > MaxRouteCheckBytes {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, state, err := s.control.begin(ctx, token, adminauth.ReadConfig, nil)
	if err != nil {
		return ControlReply{}, err
	}
	defer rollback(tx)
	if err = s.control.origin.CheckMutation(r, state.csrf); err != nil {
		return ControlReply{}, err
	}
	var in RouteCheckInput
	if control.DecodeExact(raw, &in) != nil || (in.Source != "draft" && in.Source != "published") || len(in.URL) == 0 || len(in.URL) > control.MaxRouteURLBytes {
		return replyProblem(400, "INVALID_REQUEST"), nil
	}
	out := RouteCheckResult{Source: in.Source, Scope: "configuration-only"}
	var c control.Config
	var d control.Delivery
	if in.Source == "draft" {
		c, err = readConfig(ctx, tx, false)
	} else {
		var document []byte
		err = tx.QueryRow(ctx, "SELECT document,generation FROM control_delivery WHERE singleton").Scan(&document, &out.Generation)
		if err == nil && (control.DecodeExact(document, &d) != nil || d.Validate() != nil || out.Generation < 0) {
			err = adminauth.ErrAuthUnavailable
		}
		c = d.Config
	}
	if err != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	out.Revision, out.Match = c.Revision, c.CheckRoute(in.URL)
	if in.Source == "published" && out.Match.RoomID != "" {
		if _, runtime, ok := d.Find(out.Match.RoomID); ok {
			out.Mode = runtime.Mode
		}
	}
	if tx.Commit(ctx) != nil {
		return ControlReply{}, adminauth.ErrAuthUnavailable
	}
	return jsonReply(200, out, ""), nil
}

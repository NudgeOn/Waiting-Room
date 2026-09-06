//go:build integration

// SPDX-License-Identifier: Apache-2.0
package pgstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"waiting-room/internal/adminauth"
)

func TestRouteCheckSnapshotsRolesAndNoWrites(t *testing.T) {
	f, s, g := publicationFixture(t)
	ctx := context.Background()
	next := draftDocument()
	next.Revision = 1
	next.Rooms[0].Active = true
	next.Rooms[0].ProtectPrefixes = []string{"/next"}
	if out, err := s.control.ReplaceDraft(ctx, g.SessionToken(), draftRequest(g, "route-check-draft", `"config-1"`), next.Bytes()); err != nil || out.Status != 200 {
		t.Fatal(out, err)
	}
	state := func() string {
		var raw []byte
		if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_object('draft',(SELECT document FROM control_config),'delivery',(SELECT document FROM control_delivery),'audit',(SELECT count(*) FROM control_audit),'commands',(SELECT count(*) FROM control_commands),'seen',(SELECT last_seen_at FROM auth_sessions LIMIT 1))`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	before := state()
	r := sessionRequest(g)
	r.Method = "POST"
	for _, role := range []string{"admin", "operator", "viewer"} {
		// Fixture-only role changes retain this session version to verify live RBAC.
		if _, err := f.pool.Exec(ctx, "UPDATE auth_accounts SET role=$1", role); err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{"draft", "published"} {
			raw, _ := json.Marshal(RouteCheckInput{Source: source, URL: "https://shop.example.test/shop?secret=never-store-me"})
			out, err := s.RouteCheck(ctx, g.SessionToken(), r, raw)
			if err != nil || out.Status != 200 {
				t.Fatal(role, source, out, err)
			}
			var result RouteCheckResult
			if json.Unmarshal(out.Body, &result) != nil || result.Scope != "configuration-only" {
				t.Fatal("response")
			}
			if source == "draft" && (result.Revision != 2 || result.Generation != 0 || result.Match.Decision != "unprotected" || result.Mode != "") {
				t.Fatal("draft mixed with delivery")
			}
			if source == "published" && (result.Revision != 1 || result.Generation != 1 || result.Match.Decision != "protected" || result.Mode != "HOLD") {
				t.Fatal("delivery mixed with draft")
			}
			if strings.Contains(string(out.Body), "secret") || strings.Contains(string(out.Body), "https:") {
				t.Fatal("target reflected")
			}
		}
	}
	if state() != before {
		t.Fatal("read-only diagnosis mutated saved state")
	}
	for _, bad := range []string{`{}`, `{"source":"draft","url":null}`, `{"source":"draft","source":"published","url":"https://shop.example.test/shop"}`, `{"Source":"draft","url":"https://shop.example.test/shop"}`, `{"source":"live","url":"https://shop.example.test/shop"}`} {
		out, err := s.RouteCheck(ctx, g.SessionToken(), r, []byte(bad))
		if err != nil || out.Status != 400 {
			t.Fatal("bad input", out, err)
		}
	}
	r.Header.Del("X-CSRF-Token")
	if _, err := s.RouteCheck(ctx, g.SessionToken(), r, []byte(`{"source":"draft","url":"https://shop.example.test/shop"}`)); err != adminauth.ErrForbidden {
		t.Fatal("CSRF bypass", err)
	}
	r = sessionRequest(g)
	r.Method = "POST"
	r.Header.Set("Origin", "https://other.test")
	if _, err := s.RouteCheck(ctx, g.SessionToken(), r, []byte(`{"source":"draft","url":"https://shop.example.test/shop"}`)); err != adminauth.ErrForbidden {
		t.Fatal("Origin bypass", err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE auth_accounts SET enabled=false"); err != nil {
		t.Fatal(err)
	}
	r = sessionRequest(g)
	r.Method = "POST"
	if _, err := s.RouteCheck(ctx, g.SessionToken(), r, []byte(`{}`)); err != adminauth.ErrUnauthenticated {
		t.Fatal("disabled account read", err)
	}
}

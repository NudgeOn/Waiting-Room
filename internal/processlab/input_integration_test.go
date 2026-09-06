//go:build integration

// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/lab"
)

func TestProcessPublicInputBoundary(t *testing.T) {
	binary := buildLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, e := Start(ctx, binary)
	if e != nil {
		t.Fatal(e)
	}
	defer assertClosed(t, c, false)
	for _, tt := range []struct{ name, content, body, duplicate string }{
		{"media-prefix", "application/json-invalid", `{"target":"/shop"}`, ""},
		{"charset", "application/json; charset=iso-8859-1", `{"target":"/shop"}`, ""},
		{"duplicate-field", "application/json", `{"target":"/shop","target":"/shop/item"}`, ""},
		{"case-field", "application/json", `{"Target":"/shop"}`, ""},
		{"invalid-utf8-query", "application/json", "{\"target\":\"/shop/?\x92\"}", ""},
		{"duplicate-media", "application/json", `{"target":"/shop"}`, "Content-Type"},
		{"duplicate-idempotency", "application/json", `{"target":"/shop"}`, "Idempotency-Key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, e := http.NewRequestWithContext(ctx, "POST", c.gateways[0]+"/_wr/v1/tickets", strings.NewReader(tt.body))
			if e != nil {
				t.Fatal("request fixture")
			}
			req.Header.Set("Content-Type", tt.content)
			req.Header.Set("Idempotency-Key", "process-input-boundary-fixture")
			if tt.duplicate != "" {
				req.Header.Add(tt.duplicate, req.Header.Get(tt.duplicate))
			}
			resp, e := client().Do(req)
			if e != nil {
				t.Fatal("HTTP failed")
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16384))
			if resp.StatusCode != 400 {
				t.Fatal("invalid public input accepted", resp.StatusCode)
			}
		})
	}
	for _, tt := range []struct{ path, header, value string }{{"/_wr/v1/rooms/" + lab.Room + "/status", "Authorization", "Bearer " + strings.Repeat("A", 43)}, {"/shop", "X-Waiting-Room-Admission", "fixture"}} {
		req, e := http.NewRequestWithContext(ctx, "GET", c.gateways[1]+tt.path, nil)
		if e != nil {
			t.Fatal("request fixture")
		}
		req.Header.Add(tt.header, tt.value)
		req.Header.Add(tt.header, tt.value)
		resp, e := client().Do(req)
		if e != nil {
			t.Fatal("HTTP failed")
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16384))
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatal("duplicate public credential accepted", resp.StatusCode)
		}
	}
	if originCount(t, c) != 0 {
		t.Fatal("invalid input reached origin")
	}
	result, e := lab.Quick20(ctx, c.Gateways())
	if e != nil || result.Admitted != 3 || result.Queued != 17 {
		t.Fatal("invalid requests consumed queue positions or valid flow regressed", e)
	}
	post := func(key, target string) (int, []byte) {
		req, e := http.NewRequestWithContext(ctx, "POST", c.gateways[1]+"/_wr/v1/tickets", strings.NewReader(`{"target":"`+target+`"}`))
		if e != nil {
			t.Fatal("request")
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, e := client().Do(req)
		if e != nil {
			t.Fatal("request failed")
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 16384))
		if e != nil {
			t.Fatal("read")
		}
		return resp.StatusCode, b
	}
	if code, _ := post("", "/shop"); code != 400 {
		t.Fatal("missing idempotency key accepted", code)
	}
	code, first := post("idempotency-conflict-fixture", "/shop")
	if code != 202 {
		t.Fatal("new join", code)
	}
	if code, _ = post("idempotency-conflict-fixture", "/shop/item"); code != 409 {
		t.Fatal("changed fingerprint accepted", code)
	}
	code, replay := post("idempotency-conflict-fixture", "/shop")
	if code != 202 || !bytes.Equal(first, replay) {
		t.Fatal("conflict altered original replay")
	}
}

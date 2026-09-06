//go:build integration

// SPDX-License-Identifier: Apache-2.0
package processlab

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	valkey "github.com/valkey-io/valkey-go"
	"io"
	"net/http"
	"testing"
	"time"
)

func closeConfigChild(t *testing.T, p *child) {
	t.Helper()
	p.close()
	if p.cmd.ProcessState == nil || !p.cmd.ProcessState.Success() {
		t.Error("config child exited with error/race or required force-kill")
	}
}

func TestGatewayProcessColdStartTrust(t *testing.T) {
	binary := buildLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Start(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	defer assertClosed(t, c, false)
	for _, name := range []string{"missing", "wrong-key", "runtime-mismatch", "expired"} {
		t.Run(name, func(t *testing.T) {
			in := c.gatewayConfig
			switch name {
			case "missing":
				in.SignedConfig = nil
			case "wrong-key":
				in.ConfigTrust = make([]byte, 32)
			case "runtime-mismatch":
				in.Origin = c.coordinator
			case "expired":
				var e error
				in, e = signGatewayConfig(in, time.Now().Add(-time.Hour), time.Minute)
				if e != nil {
					t.Fatal(e)
				}
			}
			p, e := startChild(ctx, binary, "gateway", in)
			if e != nil {
				t.Fatal("cold child must start liveness-only", e)
			}
			defer closeConfigChild(t, p)
			for _, path := range []string{"/shop", "/_wr/v1/tickets", "/_wr/assets/wait.js", "/admin", "/metrics"} {
				code, b, headers := get(t, client(), "GET", p.ready.URL+path, nil)
				var problem struct{ Code, RequestID string }
				if code != 503 || headers.Get("Cache-Control") != "no-store" || json.Unmarshal(b, &problem) != nil || problem.Code != "CONFIG_UNAVAILABLE" || problem.RequestID == "" {
					t.Fatal("untrusted config served traffic", path, code)
				}
			}
			code, _, _ := get(t, client(), "GET", p.ready.URL+"/livez", nil)
			if code != 204 {
				t.Fatal("cold liveness missing")
			}
			code, _, _ = get(t, client(), "POST", p.ready.URL+"/livez", nil)
			if code != 503 {
				t.Fatal("liveness method bypass")
			}
		})
	}
	if originCount(t, c) != 0 {
		t.Fatal("untrusted Gateway reached origin")
	}
}

func TestGatewayProcessConfigExpiry(t *testing.T) {
	binary := buildLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Start(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	defer assertClosed(t, c, false)
	now := time.Now()
	in, err := signGatewayConfig(c.gatewayConfig, now, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p, err := startChild(ctx, binary, "gateway", in)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConfigChild(t, p)
	code, _, _ := get(t, client(), "GET", p.ready.URL+"/shop", nil)
	if code != 429 {
		t.Fatal("valid signed config did not activate", code)
	}
	delay := time.Until(time.Unix(now.Unix()+5, 0).Add(50 * time.Millisecond))
	if delay > 0 {
		select {
		case <-ctx.Done():
			t.Fatal("expiry wait timed out")
		case <-time.After(delay):
		}
	}
	code, b, _ := get(t, client(), "GET", p.ready.URL+"/shop", nil)
	var problem struct{ Code string }
	if code != 503 || json.Unmarshal(b, &problem) != nil || problem.Code != "CONFIG_UNAVAILABLE" {
		t.Fatal("expired config still active", code)
	}
	code, _, _ = get(t, client(), "GET", p.ready.URL+"/livez", nil)
	if code != 204 {
		t.Fatal("expiry stopped liveness")
	}
	if originCount(t, c) != 0 {
		t.Fatal("expired Gateway reached origin")
	}
}

func TestCoordinatorProcessTrustAndExpiry(t *testing.T) {
	binary := buildLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	v, e := valkey.NewClient(valkey.ClientOption{InitAddress: []string{"127.0.0.1:16379"}, ForceSingleClient: true, DisableCache: true, DisableRetry: true})
	if e != nil {
		t.Fatal("Valkey fixture unavailable")
	}
	defer v.Close()
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		t.Fatal(e)
	}
	in := coordinatorInput{Address: "127.0.0.1:16379", Namespace: "wr:lab:process-" + hex.EncodeToString(nonce)}
	keys := []string{}
	for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
		keys = append(keys, in.Namespace+"{room:1}:"+suffix)
	}
	for _, suffix := range []string{"meta", "visitors", "idempotency"} {
		keys = append(keys, in.Namespace+"{installation:1}:"+suffix)
	}
	defer func() {
		if v.Do(context.Background(), v.B().Del().Key(keys...).Build()).Error() != nil {
			t.Error("owned key cleanup")
		}
	}()
	if p, e := startChild(ctx, binary, "coordinator", in); e == nil {
		p.close()
		t.Fatal("unsigned Coordinator started")
	}
	count, e := v.Do(ctx, v.B().Exists().Key(keys...).Build()).ToInt64()
	if e != nil || count != 0 {
		t.Fatal("unsigned Coordinator wrote state")
	}
	now := time.Now()
	in, e = signCoordinatorConfig(in, now, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	p, e := startChild(ctx, binary, "coordinator", in)
	if e != nil {
		t.Fatal(e)
	}
	defer closeConfigChild(t, p)
	join := func() int {
		req, e := http.NewRequestWithContext(ctx, "POST", p.ready.URL+"/_wr/v1/tickets", bytes.NewBufferString(`{"target":"/shop"}`))
		if e != nil {
			t.Fatal("request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-WR-Service", p.ready.Service)
		req.Header.Set("Idempotency-Key", "coordinator-expiry-fixture")
		resp, e := client().Do(req)
		if e != nil {
			t.Fatal("request failed")
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16384))
		return resp.StatusCode
	}
	if join() != 202 {
		t.Fatal("trusted Coordinator did not activate")
	}
	delay := time.Until(time.Unix(now.Unix()+5, 0).Add(250 * time.Millisecond))
	if delay > 0 {
		select {
		case <-ctx.Done():
			t.Fatal("expiry wait")
		case <-time.After(delay):
		}
	}
	if join() != 503 {
		t.Fatal("expired Coordinator still served join")
	}
	readClock := func() string {
		raw, e := v.Do(ctx, v.B().Hget().Key(keys[8]).Field("clock").Build()).ToString()
		if e != nil {
			t.Fatal("clock read")
		}
		return raw
	}
	first := readClock()
	select {
	case <-ctx.Done():
		t.Fatal("pump wait")
	case <-time.After(250 * time.Millisecond):
	}
	if first != readClock() {
		t.Fatal("expired config allowed background promotion writes")
	}
}

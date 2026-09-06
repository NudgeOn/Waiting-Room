// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type QuickResult struct {
	Visitors        int  `json:"visitors"`
	Admitted        int  `json:"admitted"`
	Queued          int  `json:"queued"`
	RetryStable     bool `json:"retryStable"`
	OriginProtected bool `json:"originProtected"`
}

func request(ctx context.Context, client *http.Client, method, address string, body []byte, headers map[string]string) (int, []byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return 0, nil, e
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := client.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 16384))
	return resp.StatusCode, b, e
}

// Quick20 is app JSON only. It never targets a non-loopback origin or claims a profile badge.
func Quick20(ctx context.Context, gateways []string) (QuickResult, error) {
	result := QuickResult{Visitors: 20, RetryStable: true}
	if len(gateways) != 2 {
		return result, fmt.Errorf("two lab Gateways required")
	}
	for _, g := range gateways {
		if _, e := loopbackURL(g); e != nil {
			return result, e
		}
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	code, _, e := request(ctx, client, "GET", gateways[0]+"/shop", nil, nil)
	if e != nil || code != 429 {
		return result, fmt.Errorf("origin bypass guard: %d %v", code, e)
	}
	result.OriginProtected = true
	tokens := []string{}
	run := randomToken()
	for i := 0; i < 20; i++ {
		headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": fmt.Sprintf("%s-%d", run, i)}
		code, b, e := request(ctx, client, "POST", gateways[i%2]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers)
		if e != nil || code != 202 {
			return result, fmt.Errorf("join %d: %d %v", i, code, e)
		}
		var data struct{ TicketToken string }
		if json.Unmarshal(b, &data) != nil || data.TicketToken == "" {
			return result, fmt.Errorf("invalid join")
		}
		tokens = append(tokens, data.TicketToken)
		status, retry, e := request(ctx, client, "POST", gateways[(i+1)%2]+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), headers)
		if e != nil || status != 202 || !bytes.Equal(b, retry) {
			return result, fmt.Errorf("join replay changed")
		}
	}
	// Allow the lab scheduler to promote; use a bounded poll deadline, never mutate via GET.
	deadline := time.Now().Add(5 * time.Second)
	for {
		ready := 0
		for i, token := range tokens {
			code, b, e := request(ctx, client, "GET", gateways[i%2]+base+"/status", nil, map[string]string{"Authorization": "Bearer " + token})
			if e != nil {
				return result, e
			}
			var data struct{ State string }
			_ = json.Unmarshal(b, &data)
			if code == 200 && data.State == "ready" {
				ready++
			}
		}
		if ready == 3 {
			break
		}
		if time.Now().After(deadline) {
			return result, fmt.Errorf("expected exactly three READY, got %d", ready)
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	for i, token := range tokens {
		headers := map[string]string{"Authorization": "Bearer " + token}
		code, b, e := request(ctx, client, "GET", gateways[i%2]+base+"/status", nil, headers)
		if e != nil {
			return result, e
		}
		var state struct{ State string }
		_ = json.Unmarshal(b, &state)
		if i >= 3 {
			if code != 202 || state.State != "queued" {
				return result, fmt.Errorf("FIFO inversion at %d", i)
			}
			result.Queued++
			continue
		}
		if code != 200 || state.State != "ready" {
			return result, fmt.Errorf("FIFO head missing")
		}
		code, b, e = request(ctx, client, "POST", gateways[i%2]+base+"/admissions", nil, headers)
		if e != nil || code != 200 {
			return result, fmt.Errorf("claim: %d %v", code, e)
		}
		code, retry, e := request(ctx, client, "POST", gateways[(i+1)%2]+base+"/admissions", nil, headers)
		if e != nil || code != 200 || !bytes.Equal(b, retry) {
			return result, fmt.Errorf("claim replay changed")
		}
		var admitted struct{ AdmissionToken string }
		_ = json.Unmarshal(b, &admitted)
		code, _, e = request(ctx, client, "GET", gateways[(i+1)%2]+"/shop", nil, map[string]string{"X-Waiting-Room-Admission": admitted.AdmissionToken})
		if e != nil || code != 200 {
			return result, fmt.Errorf("origin admission: %d %v", code, e)
		}
		result.Admitted++
	}
	return result, nil
}

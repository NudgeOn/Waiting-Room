// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"waiting-room/internal/trafficlab"
)

func trafficPeer(r *http.Request, name string) {
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{Subject: pkix.Name{CommonName: name}}}}}
}
func TestTrafficRPCBoundaryBeforeExecutor(t *testing.T) {
	body := `{"runId":"` + strings.Repeat("A", 43) + `","preset":"quick-20"}`
	for _, tc := range []struct {
		peer, host, method, path, body string
		want                           int
	}{
		{"gateway", "coordinator:19446", "POST", "/internal/v1/traffic-lab", body, 403},
		{"", "coordinator:19446", "POST", "/internal/v1/traffic-lab", body, 403},
		{"control", "evil:19446", "POST", "/internal/v1/traffic-lab", body, 403},
		{"control", "coordinator:19446", "GET", "/internal/v1/traffic-lab", body, 400},
		{"control", "coordinator:19446", "POST", "/internal/v1/traffic-lab?url=x", body, 400},
		{"control", "coordinator:19446", "POST", "/internal/v1/traffic-lab", strings.Replace(body, "quick-20", "100k", 1), 400},
		{"control", "coordinator:19446", "POST", "/internal/v1/traffic-lab", strings.TrimSuffix(body, "}") + `,"url":"https://customer.test"}`, 400},
		{"control", "coordinator:19446", "POST", "/internal/v1/traffic-lab", strings.Repeat("x", 1025), 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Host = tc.host
		trafficPeer(r, tc.peer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		called := false
		serveTraffic(func(context.Context, trafficlab.Input, func(trafficlab.Report) error) (trafficlab.Report, error) {
			called = true
			return trafficlab.Report{}, nil
		}, w, r)
		if called || w.Code != tc.want {
			t.Fatal("internal boundary", tc.peer, w.Code, called)
		}
	}
}
func TestTrafficRPCStreamsProgressAndInterruptedTerminal(t *testing.T) {
	execute := func(ctx context.Context, in trafficlab.Input, emit func(trafficlab.Report) error) (trafficlab.Report, error) {
		r := trafficlab.NewReport(in.Preset)
		if err := emit(r); err != nil {
			return r, err
		}
		return r, trafficlab.ErrRun
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trafficPeer(r, "control"); serveTraffic(execute, w, r) }))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/internal/v1/traffic-lab", strings.NewReader(`{"runId":"`+strings.Repeat("A", 43)+`","preset":"quick-20"}`))
	req.Host = "coordinator:19446"
	req.Header.Set("Content-Type", "application/json")
	client := server.Client()
	client.Timeout = time.Second
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatal("stream unavailable", res.StatusCode)
	}
	decoder := json.NewDecoder(res.Body)
	for _, state := range []string{"running", "interrupted"} {
		var report trafficlab.Report
		if err = decoder.Decode(&report); err != nil || !report.Valid("quick-20") || report.State != state {
			t.Fatal("invalid stream", err, report.State)
		}
	}
}

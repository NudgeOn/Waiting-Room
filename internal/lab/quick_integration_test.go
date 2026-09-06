//go:build integration

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func TestQuick20TwoGatewaysAndFailClosed(t *testing.T) {
	addr := os.Getenv("WR_TEST_VALKEY")
	if addr == "" {
		t.Fatal("WR_TEST_VALKEY required")
	}
	cfg := model.DefaultConfig()
	cfg.LeaseCap = 3
	cfg.Rate = 6
	cfg.AdmissionTTL = 60000
	store, e := valkeystore.Open(context.Background(), addr, fmt.Sprintf("wr:lab:quick-%d", time.Now().UnixNano()), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	coordinator, e := NewCoordinator(store, cfg)
	if e != nil {
		t.Fatal(e)
	}
	internal := httptest.NewServer(coordinator.Handler())
	defer internal.Close()
	var reached atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Waiting-Room-Admission") != "" || r.Header.Get("X-WR-Service") != "" {
			t.Error("origin secret leak")
		}
		reached.Add(1)
		writeJSON(w, 200, map[string]string{"origin": "deterministic-lab"})
	}))
	defer origin.Close()
	h, e := NewGateway(internal.URL, origin.URL, coordinator.ServiceKey, coordinator.Public)
	if e != nil {
		t.Fatal(e)
	}
	one := httptest.NewServer(h)
	defer one.Close()
	two := httptest.NewServer(h)
	defer two.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_, _ = store.Promote(ctx, 128)
			}
		}
	}()
	result, e := Quick20(ctx, []string{one.URL, two.URL})
	if e != nil {
		t.Fatal(e)
	}
	if result.Admitted != 3 || result.Queued != 17 || !result.RetryStable || !result.OriginProtected || reached.Load() != 3 {
		t.Fatal(result, reached.Load())
	}
	cancel()
	<-done
	internal.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	code, _, e := request(context.Background(), client, "POST", one.URL+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), map[string]string{"Content-Type": "application/json", "Idempotency-Key": "failure-test-key"})
	if e != nil || code != 503 {
		t.Fatal("coordinator loss bypass", code, e)
	}
	code, _, e = request(context.Background(), client, "POST", two.URL+"/shop", []byte("unsafe-body"), nil)
	if e != nil || code != 429 || reached.Load() != 3 {
		t.Fatal("unsafe origin reach", code, e)
	}
	code, _, e = request(context.Background(), client, "GET", one.URL+"/api/admin/v1/users", nil, nil)
	if e != nil || code != 404 {
		t.Fatal("admin exposed", code, e)
	}
	t.Logf("Quick20 visitors=%d admitted=%d queued=%d retryStable=%t originProtected=%t", result.Visitors, result.Admitted, result.Queued, result.RetryStable, result.OriginProtected)
}

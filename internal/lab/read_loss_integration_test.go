//go:build integration

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

type disconnectedStatusConn struct {
	net.Conn
	armed *atomic.Bool
	drop  atomic.Bool
}

func (c *disconnectedStatusConn) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte("\r\nwr_r6_status\r\n")) && c.armed.CompareAndSwap(true, false) {
		c.drop.Store(true)
	}
	return c.Conn.Write(b)
}
func (c *disconnectedStatusConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 && c.drop.CompareAndSwap(true, false) {
		_ = c.Conn.Close()
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestHTTPReadDisconnectionRecoversWithoutRestart(t *testing.T) {
	address := os.Getenv("WR_TEST_VALKEY")
	if address != "127.0.0.1:16379" {
		t.Fatal("dedicated loopback Valkey required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{address}})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = valkeystore.InstallRecoveryLibrary(ctx, owner); err != nil {
		t.Fatal(err)
	}
	var armed atomic.Bool
	opt := valkey.ClientOption{InitAddress: []string{address}, DialCtxFn: func(ctx context.Context, address string, dialer *net.Dialer, _ *tls.Config) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		return &disconnectedStatusConn{Conn: conn, armed: &armed}, nil
	}}
	ns := fmt.Sprintf("wr:lab:http-read-loss-%d", time.Now().UnixNano())
	cfg := model.DefaultConfig()
	cfg.LeaseCap = 1
	q, err := valkeystore.OpenRecoveryRoom(ctx, opt, ns, Room, cfg, valkeystore.StandardInstallation(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	defer func() {
		keys := []string{}
		for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
			keys = append(keys, ns+"{"+Room+":1}:"+suffix)
		}
		for _, suffix := range []string{"meta", "visitors", "idempotency"} {
			keys = append(keys, ns+"{installation:1}:"+suffix)
		}
		keys = append(keys, ns+"{epoch:1}:meta")
		if err := owner.Do(context.Background(), owner.B().Del().Key(keys...).Build()).Error(); err != nil {
			t.Error("fixture cleanup", err)
		}
	}()
	coord, err := NewCoordinator(q, cfg)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := httptest.NewServer(coord.Handler())
	defer coordinator.Close()
	var reached atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(200) }))
	defer origin.Close()
	handler, err := NewGateway(coordinator.URL, origin.URL, coord.ServiceKey, coord.Public)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(handler)
	defer gateway.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	join := func(key string) []byte {
		code, body, err := request(ctx, client, "POST", gateway.URL+"/_wr/v1/tickets", []byte(`{"target":"/shop"}`), map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
		if err != nil || code != 202 {
			t.Fatalf("HTTP join failed: %d %v", code, err)
		}
		return body
	}
	first := join("retained-read-loss")
	var ticket struct{ TicketToken, StatusURL string }
	if json.Unmarshal(first, &ticket) != nil || ticket.TicketToken == "" || ticket.StatusURL == "" {
		t.Fatal("missing original ticket")
	}
	headers := map[string]string{"Authorization": "Bearer " + ticket.TicketToken}
	armed.Store(true)
	code, body, err := request(ctx, client, "GET", gateway.URL+ticket.StatusURL, nil, headers)
	var failure struct{ Code string }
	if err != nil || code != 503 || json.Unmarshal(body, &failure) != nil || failure.Code != "QUEUE_UNAVAILABLE" || armed.Load() {
		t.Fatal("selected real read failure was not exposed as retryable HTTP 503", code, err)
	}
	// Follow the public three-second retry interval. Neither application nor
	// Valkey is restarted; the same pool must reconnect on its own.
	time.Sleep(3 * time.Second)
	code, _, err = request(ctx, client, "GET", gateway.URL+ticket.StatusURL, nil, headers)
	if err != nil || code != 202 {
		t.Fatal("read disconnection left the HTTP queue unavailable", code, err)
	}
	if !bytes.Equal(join("retained-read-loss"), first) {
		t.Fatal("read loss changed the original HTTP join response")
	}
	second := join("unrelated-read-loss")
	var next struct{ TicketToken string }
	if json.Unmarshal(second, &next) != nil {
		t.Fatal("invalid unrelated ticket")
	}
	oldRow, err := q.Status(ctx, valkeystore.Hash(ticket.TicketToken))
	if err != nil || oldRow.Ticket == nil || oldRow.Ticket.Sequence != 1 {
		t.Fatal("original FIFO ticket was not retained", err)
	}
	newRow, err := q.Status(ctx, valkeystore.Hash(next.TicketToken))
	if err != nil || newRow.Ticket == nil || newRow.Ticket.Sequence != 2 {
		t.Fatal("unrelated join did not retain FIFO order", err)
	}
	state, err := q.MaintainRecovery(ctx)
	if err != nil || state.Mode != "ACTIVE" || state.Fence != 1 {
		t.Fatal("read loss created a shared recovery hold", state, err)
	}
	if _, err := q.Configure(ctx, cfg, 1, control.Runtime{Revision: 1, Epoch: 1, Mode: "AUTO"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Promote(ctx, 1); err != nil {
		t.Fatal(err)
	}
	code, body, err = request(ctx, client, "POST", gateway.URL+base+"/admissions", nil, headers)
	var admission struct{ AdmissionToken string }
	if err != nil || code != 200 || json.Unmarshal(body, &admission) != nil || admission.AdmissionToken == "" {
		t.Fatal("retained FIFO head could not claim", code, err)
	}
	if reached.Load() != 0 {
		t.Fatal("unadmitted visitor reached origin")
	}
	code, _, err = request(ctx, client, "GET", gateway.URL+"/shop", nil, map[string]string{"X-Waiting-Room-Admission": admission.AdmissionToken})
	if err != nil || code != 200 || reached.Load() != 1 {
		t.Fatal("same retained visitor could not reach protected origin", code, err)
	}
	t.Log("actual read reply loss -> HTTP QUEUE_UNAVAILABLE -> same-process retry -> exact join replay/FIFO -> claim -> protected origin")
}

// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServeWaitsForInFlightResponseOnShutdown(t *testing.T) {
	certificate := httptest.NewTLSServer(http.NotFoundHandler())
	tlsConfig := certificate.TLS.Clone()
	certificate.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, listener, tlsConfig, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			w.Write([]byte("committed-response"))
		}))
	}()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	defer client.CloseIdleConnections()
	received := make(chan string, 1)
	go func() {
		response, err := client.Get("https://" + listener.Addr().String())
		if err != nil {
			received <- "request failed"
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		received <- string(body)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request never entered handler")
	}
	cancel()
	select {
	case err := <-served:
		t.Fatal("Serve returned before its active response finished", err)
	case <-time.After(50 * time.Millisecond):
	}
	// Release the request without closing its signal twice in cleanup.
	release <- struct{}{}
	if body := <-received; body != "committed-response" {
		t.Fatal("shutdown lost the committed response", body)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish after the active response drained")
	}
}

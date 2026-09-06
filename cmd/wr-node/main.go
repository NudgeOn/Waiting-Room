// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"waiting-room/internal/localcontrol"
	"waiting-room/internal/runtimeplane"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Local data-plane unavailable. Check role identity, signed config and dependencies; credentials are not logged.")
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		client := http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://127.0.0.1:20444/readyz")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			return errors.New("node not ready")
		}
		return nil
	}
	if len(os.Args) != 2 {
		return errors.New("role required")
	}
	role := os.Args[1]
	identity, err := localcontrol.LoadIdentity("/identity", role)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	tlsConfig, err := identity.TLS(false)
	if err != nil {
		return err
	}
	address := "0.0.0.0:19446"
	if role == "gateway" {
		address = "0.0.0.0:20443"
		tlsConfig.ClientAuth = tls.NoClientCert
	}
	if role == "demo-origin" {
		address = "0.0.0.0:20445"
	}
	l, err := net.Listen("tcp4", address)
	if err != nil {
		return err
	}
	defer l.Close()
	var handler http.Handler
	ready := func() bool { return true }
	if role == "demo-origin" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if runtimeplane.Peer(r) != "gateway" {
				w.WriteHeader(403)
				return
			}
			if r.URL.Path == "/health" {
				w.WriteHeader(204)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(`{"service":"protected-demo-origin","admitted":true}`))
		})
	} else {
		node, err := runtimeplane.OpenNode(identity, "/data")
		if err != nil {
			return err
		}
		done := make(chan struct{})
		go func() { defer close(done); node.Run(ctx) }()
		defer func() { cancel(); <-done; node.Close() }()
		handler = node
		ready = node.Ready
	}
	health, err := net.Listen("tcp4", "127.0.0.1:20444")
	if err != nil {
		return err
	}
	defer health.Close()
	healthServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || (r.URL.Path != "/livez" && r.URL.Path != "/readyz") {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/readyz" && !ready() {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}), ReadHeaderTimeout: time.Second}
	defer healthServer.Close()
	go healthServer.Serve(health)
	return runtimeplane.Serve(ctx, l, tlsConfig, handler)
}

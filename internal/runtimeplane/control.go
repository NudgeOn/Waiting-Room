// SPDX-License-Identifier: Apache-2.0
// Package runtimeplane binds signed operator state to isolated data-plane roles.
package runtimeplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"time"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/control"
)

func Peer(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return ""
	}
	return r.TLS.VerifiedChains[0][0].Subject.CommonName
}
func InternalControl(s *pgstore.PublicationService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		node := Peer(r)
		if node != "gateway" && node != "coordinator" {
			w.WriteHeader(403)
			return
		}
		if r.Host != "control:19445" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.EscapedPath() != r.URL.Path {
			w.WriteHeader(400)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/internal/v1/config" {
			raw, err := s.Envelope(r.Context())
			if err != nil {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write(raw)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/internal/v1/ack" {
			if r.Header.Get("Content-Type") != "application/json" {
				w.WriteHeader(400)
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
			var ack pgstore.NodeAck
			if err != nil || control.DecodeExact(raw, &ack) != nil {
				w.WriteHeader(400)
				return
			}
			if err = s.Acknowledge(r.Context(), node, ack); err != nil {
				w.WriteHeader(409)
				return
			}
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
	})
}
func Worker(ctx context.Context, s *pgstore.PublicationService) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = s.Refresh(ctx)
		_ = s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func Serve(ctx context.Context, l net.Listener, tlsConfig *tls.Config, h http.Handler) error {
	server := &http.Server{Handler: h, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if server.Shutdown(shutdown) != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	err := server.ServeTLS(l, "", "")
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func unavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "3")
	w.WriteHeader(503)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": 503, "code": "RUNTIME_UNAVAILABLE"})
}

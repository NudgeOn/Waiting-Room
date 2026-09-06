// SPDX-License-Identifier: Apache-2.0
// Package preview exposes non-secret, stateless planning only on loopback.
// It has no bootstrap, database, shell execution, provisioning or apply path.
package preview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"waiting-room/internal/installplan"
)

const Address = "127.0.0.1:18770"

func Handler(ui fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		return nil, errors.New("build Admin UI first")
	}
	files := http.FileServerFS(ui)
	slots := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(peer).IsLoopback() || r.Host != Address {
			http.Error(w, "Forbidden", 403)
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.EscapedPath() != r.URL.Path {
			http.Error(w, "Invalid request", 400)
			return
		}
		if r.URL.Path == "/preview/plan" || r.URL.Path == "/preview/estimate" || r.URL.Path == "/preview/report" {
			if r.Method != "POST" {
				w.Header().Set("Allow", "POST")
				http.Error(w, "Method not allowed", 405)
				return
			}
			if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != "http://"+Address || r.Header.Get("X-WR-Preview") != "1" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				http.Error(w, "Forbidden", 403)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				http.Error(w, "Busy", 503)
				return
			}
			body := http.MaxBytesReader(w, r.Body, installplan.MaxInputBytes)
			var result any
			if r.URL.Path == "/preview/plan" {
				var in installplan.Input
				in, err = installplan.Decode(body)
				if err == nil {
					result, err = installplan.Build(in)
				}
			} else if r.URL.Path == "/preview/report" {
				var in installplan.ReportInput
				in, err = installplan.DecodeReport(body)
				if err == nil {
					result, err = installplan.BuildReport(in)
				}
			} else {
				var in installplan.CostInput
				in, err = installplan.DecodeCost(body)
				if err == nil {
					result, err = installplan.Estimate(in)
				}
			}
			if err != nil {
				http.Error(w, "Invalid planning input", 422)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/install-preview", http.StatusTemporaryRedirect)
		case "/livez":
			w.WriteHeader(204)
		case "/favicon.ico":
			w.WriteHeader(204)
		case "/install-preview":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == "GET" {
				_, _ = w.Write(index)
			}
		default:
			if !strings.HasPrefix(r.URL.Path, "/assets/") || strings.Contains(r.URL.Path, "..") {
				http.NotFound(w, r)
				return
			}
			info, err := fs.Stat(ui, strings.TrimPrefix(r.URL.Path, "/"))
			if err != nil || !info.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
		}
	}), nil
}

func Run(ctx context.Context, ui fs.FS, out io.Writer) error {
	h, err := Handler(ui)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", Address)
	if err != nil {
		return errors.New("preview loopback port unavailable")
	}
	s := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(io.Discard, "", 0)}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if s.Shutdown(c) != nil {
				_ = s.Close()
			}
		case <-done:
		}
	}()
	_, _ = io.WriteString(out, "Planning preview only: http://"+Address+"/install-preview (no installation; Ctrl-C to stop)\n")
	err = s.Serve(listener)
	close(done)
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return errors.New("preview server stopped unexpectedly")
}

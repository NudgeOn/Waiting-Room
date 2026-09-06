// SPDX-License-Identifier: Apache-2.0
// Package adminserver serves the private Admin UI and authenticated API.
// The caller owns listener isolation, TLS and persistent key/DB provisioning.
package adminserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/authhttp"
	"waiting-room/internal/adminauth/controlhttp"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminauth/sessionhttp"
)

func Certificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Waiting Room local lab only"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

type Options struct {
	Origin      string
	Setup       bool
	TOTP        bool
	KeyID       string
	Persistent  bool
	Publication *pgstore.PublicationService
}

func Handler(store *pgstore.Store, passwords *adminauth.PasswordHasher, fingerprint [32]byte, ui fs.FS, options Options) (http.Handler, error) {
	origin, setup, totp := options.Origin, options.Setup, options.TOTP
	index, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		return nil, errors.New("build Admin UI first")
	}
	login, err := pgstore.NewLoginService(store, passwords, fingerprint)
	if err != nil {
		return nil, err
	}
	sessions, err := pgstore.NewSessionService(store, origin, false)
	if err != nil {
		return nil, err
	}
	recovery, err := pgstore.NewRecoveryService(store, passwords)
	if err != nil {
		return nil, err
	}
	enrollment, err := pgstore.NewEnrollmentService(store, passwords, options.KeyID, options.Publication != nil)
	if err != nil {
		return nil, err
	}
	backend, err := authhttp.NewProvisioningBackend(login, store, recovery, sessions, enrollment)
	if err != nil {
		return nil, err
	}
	auth, err := authhttp.NewEnrollment(backend, origin)
	if err != nil {
		return nil, err
	}
	session, err := sessionhttp.New(sessions)
	if err != nil {
		return nil, err
	}
	operations, err := pgstore.NewControlService(store, origin)
	if err != nil {
		return nil, err
	}
	drafts, err := controlhttp.New(operations)
	if err != nil {
		return nil, err
	}
	var runtime *controlhttp.RuntimeHandler
	if options.Publication != nil {
		security, e := pgstore.NewSecurityService(store, passwords, fingerprint, origin, options.KeyID)
		if e != nil {
			return nil, e
		}
		runtime, err = controlhttp.NewRuntime(options.Publication, security)
		if err != nil {
			return nil, err
		}
	}
	var bootstrap *authhttp.Handler
	if setup {
		bootstrap, err = authhttp.NewBootstrap(backend, origin)
		if err != nil {
			return nil, err
		}
	}
	files := http.FileServerFS(ui)
	host := strings.TrimPrefix(origin, "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if r.TLS == nil || r.Host != host {
			http.Error(w, "Forbidden", 403)
			return
		}
		if (r.URL.RawQuery != "" && r.URL.Path != "/api/admin/v1/audit-events") || r.URL.ForceQuery || r.URL.EscapedPath() != r.URL.Path {
			http.Error(w, "Invalid request", 400)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// Bound body reads for me/logout too. Auth resets its own deadline before KDF.
			rc := http.NewResponseController(w)
			if rc.SetReadDeadline(time.Now().Add(5*time.Second)) != nil {
				http.Error(w, "Unavailable", 503)
				return
			}
			defer rc.SetReadDeadline(time.Time{})
			if controlhttp.RuntimePath(r.URL.Path) {
				if setup || runtime == nil {
					http.NotFound(w, r)
				} else {
					runtime.ServeHTTP(w, r)
				}
				return
			}
			switch r.URL.Path {
			case "/api/admin/v1/config/draft", "/api/admin/v1/audit-events":
				if setup {
					http.NotFound(w, r)
				} else {
					drafts.ServeHTTP(w, r)
				}
			case "/api/admin/v1/bootstrap":
				if bootstrap == nil {
					http.NotFound(w, r)
				} else {
					bootstrap.ServeHTTP(w, r)
				}
			case "/api/admin/v1/auth/me", "/api/admin/v1/auth/logout":
				session.ServeHTTP(w, r)
			default:
				auth.ServeHTTP(w, r)
			}
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		switch r.URL.Path {
		case "/livez":
			w.WriteHeader(204)
		case "/lab-info":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"lab": !options.Persistent, "persistent": options.Persistent, "setup": setup, "totpEnabled": totp, "draftAuthoring": !setup})
		case "/", "/auth/login", "/setup":
			if r.URL.Path == "/setup" && !setup {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == "GET" {
				_, _ = w.Write(index)
			}
		default:
			if applicationUIRoute(r.URL.Path, setup) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if r.Method == "GET" {
					_, _ = w.Write(index)
				}
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/assets/") || strings.Contains(r.URL.Path, "..") {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
		}
	}), nil
}

// Serve requires a private listener supplied by the caller. Never publish the
// setup listener: provisioning independently requires a literal loopback peer.
func Serve(ctx context.Context, listener net.Listener, cert tls.Certificate, handler http.Handler) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			if server.Shutdown(shutdown) != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	err := server.ServeTLS(listener, "", "")
	close(done)
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

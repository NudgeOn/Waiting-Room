// SPDX-License-Identifier: Apache-2.0
// Disposable auth lab. Does not implement wr-control or production installation.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminlab"
	"waiting-room/internal/adminserver"
	"waiting-room/internal/queue/valkeystore"
	"waiting-room/internal/trafficlab"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Admin Lab failed; check local DB, built UI and ports. No credentials logged.")
		os.Exit(1)
	}
}
func run() error {
	port := flag.Int("port", 18443, "loopback Admin HTTPS port")
	setupPort := flag.Int("setup-port", 18444, "loopback setup HTTPS port")
	totp := flag.String("totp", "on", "lab initial TOTP policy: on or off")
	wizard := flag.Bool("setup-wizard", false, "exercise real setup storage/calibration in a disposable schema; no Docker runtime")
	trafficFlag := flag.Bool("traffic-lab", false, "enable fixed sample Traffic Lab using the dedicated loopback Valkey")
	flag.Parse()
	if flag.NArg() != 0 || (*totp != "on" && *totp != "off") || *port < 1024 || *port > 65535 || *setupPort < 1024 || *setupPort > 65535 || *port == *setupPort {
		return errors.New("invalid lab flags")
	}
	if os.Getenv("WR_TEST_AUTH_DB") != "local" {
		return errors.New("WR_TEST_AUTH_DB=local required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	const db = "postgres://wr_auth_lab:local-test-only@127.0.0.1:15432/wr_auth_lab?sslmode=disable"
	start, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	root, err := pgx.Connect(start, db)
	if err != nil {
		return err
	}
	defer root.Close(context.Background())
	var version string
	if err = root.QueryRow(start, "SHOW server_version").Scan(&version); err != nil || !strings.HasPrefix(version, "17.11") {
		return errors.New("wrong lab DB")
	}
	nonce, _, err := adminauth.NewCSRFToken()
	if err != nil {
		return err
	}
	schema := "wr_admin_lab_" + strings.ToLower(strings.ReplaceAll(nonce[:16], "-", "_"))
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = root.Exec(start, "CREATE SCHEMA "+ident); err != nil {
		return err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, e := root.Exec(cleanup, "DROP SCHEMA "+ident+" CASCADE"); e != nil {
			fmt.Fprintln(os.Stderr, "Lab schema cleanup failed; consult local DB operator.")
		}
	}()
	cfg, err := pgxpool.ParseConfig(db)
	if err != nil {
		return err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(start, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	for _, sql := range []string{pgstore.Migration001, pgstore.Migration002, pgstore.Migration003, pgstore.Migration004, pgstore.Migration005, pgstore.Migration013} {
		if _, err = pool.Exec(start, sql); err != nil {
			return err
		}
	}
	if *wizard {
		for _, sql := range []string{pgstore.Migration006, pgstore.Migration010, pgstore.Migration011} {
			if _, err = pool.Exec(start, sql); err != nil {
				return err
			}
		}
	}
	if *trafficFlag {
		if !*wizard {
			if _, err = pool.Exec(start, pgstore.Migration010); err != nil {
				return err
			}
		}
		if _, err = pool.Exec(start, pgstore.Migration012); err != nil {
			return err
		}
	}
	if _, err = pool.Exec(start, "UPDATE auth_policy SET totp_enabled=$1", *totp == "on"); err != nil {
		return err
	}
	var key, fingerprint [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return err
	}
	if _, err = rand.Read(fingerprint[:]); err != nil {
		return err
	}
	vault, err := pgstore.NewVault(map[string][]byte{"lab": key[:]})
	if err != nil {
		return err
	}
	store, err := pgstore.New(pool, vault)
	if *wizard {
		store, err = pgstore.NewAudited(pool, vault)
	}
	if err != nil {
		return err
	}
	if err = store.InitializeControl(start, "standard-10k", "local"); err != nil {
		return err
	}
	passwords, err := adminauth.NewPasswordHasher(ctx, 2)
	if err != nil {
		return err
	}
	if *wizard {
		passwords = adminauth.NewPasswordHasherSource(store.PasswordIterations)
	}
	cert, err := adminlab.Certificate()
	if err != nil {
		return err
	}
	// Never overwrite a token path. It is readable only by the current OS user.
	if err = os.MkdirAll(".cache", 0700); err != nil {
		return err
	}
	tokenDir, err := os.MkdirTemp(".cache", "admin-lab-")
	if err != nil {
		return err
	}
	tokenPath := filepath.Join(tokenDir, "bootstrap-token")
	defer os.Remove(tokenDir)
	defer os.Remove(tokenPath)
	token, err := store.IssueBootstrapToken(ctx)
	if err != nil {
		return err
	}
	if err = os.WriteFile(tokenPath, []byte(token.Token()), 0600); err != nil {
		return err
	}
	listeners := make([]net.Listener, 0, 2)
	for _, p := range []int{*port, *setupPort} {
		l, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", p))
		if e != nil {
			return e
		}
		listeners = append(listeners, l)
		defer l.Close()
	}
	results := make(chan error, 2)
	handlers := make([]http.Handler, 2)
	var traffic *pgstore.TrafficService
	if *trafficFlag {
		options := valkey.ClientOption{InitAddress: []string{"127.0.0.1:16379"}, DisableCache: true, ForceSingleClient: true}
		client, e := valkey.NewClient(options)
		if e != nil {
			return e
		}
		e = valkeystore.InstallRuntimeLibrary(ctx, client)
		client.Close()
		if e != nil {
			return e
		}
		traffic, e = pgstore.NewTrafficService(store, "https://"+listeners[0].Addr().String(), trafficlab.NewExecutor(options))
		if e != nil {
			return e
		}
		done := make(chan struct{})
		go func() { defer close(done); traffic.Worker(ctx) }()
		defer func() { cancel(); <-done }()
	}
	for i, l := range listeners {
		origin := "https://" + l.Addr().String()
		handler, e := adminlab.Handler(store, passwords, fingerprint, os.DirFS("build/admin-ui"), origin, i == 1, *totp == "on")
		if *wizard || *trafficFlag {
			handler, e = adminserver.Handler(store, passwords, fingerprint, os.DirFS("build/admin-ui"), adminserver.Options{Origin: origin, AdminOrigin: "https://" + listeners[0].Addr().String(), Setup: i == 1, TOTP: *totp == "on", KeyID: "lab", Persistent: *wizard, Traffic: traffic})
		}
		if e != nil {
			return e
		}
		handlers[i] = handler
	}
	for i, l := range listeners {
		go func() { results <- adminlab.Serve(ctx, l, cert, handlers[i]) }()
	}
	absolute, _ := filepath.Abs(tokenPath)
	fmt.Printf("Disposable Admin Lab. Data removed on normal shutdown. Self-signed local TLS only.\nAdmin: https://127.0.0.1:%d\nSetup: https://127.0.0.1:%d/setup\nBootstrap token file: %s\n", *port, *setupPort, absolute)
	first := <-results
	cancel()
	second := <-results
	if first != nil {
		return first
	}
	return second
}

// SPDX-License-Identifier: Apache-2.0
// Persistent local Docker control-plane developer preview. Not a Beta release.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/adminauth"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/adminserver"
	"waiting-room/internal/localcontrol"
	"waiting-room/internal/publicguard"
	"waiting-room/internal/queue/valkeystore"
	"waiting-room/internal/recoveryarchive"
	"waiting-room/internal/runtimeplane"
)

const stateDir = "/state"
const adminOrigin = "https://127.0.0.1:19443"
const setupOrigin = "https://127.0.0.1:19444"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Local Control failed. Check initialization, matching state/DB, permissions and health. Credentials are not logged.")
		os.Exit(1)
	}
}

func database(ctx context.Context, owner bool) (*pgxpool.Pool, error) {
	user, secret := localcontrol.RuntimeRole, filepath.Join(stateDir, "runtime-password")
	if owner {
		user, secret = "wr_owner", "/run/secrets/owner_password"
	}
	password, err := localcontrol.ReadPrivate(secret, 64)
	if err != nil || len(password) != 64 {
		return nil, errors.New("DB secret unavailable")
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(user, string(password)), Host: "postgres:5432", Path: "/waiting_room", RawQuery: "sslmode=disable"}
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, errors.New("invalid database config")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = localcontrol.Schema
	cfg.MaxConns = 8
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("database unavailable")
	}
	if pool.Ping(ctx) != nil {
		pool.Close()
		return nil, errors.New("database unavailable")
	}
	return pool, nil
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("explicit subcommand required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch os.Args[1] {
	case "archive-create", "archive-verify", "archive-restore":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		if os.Args[1] == "archive-restore" {
			return recoveryarchive.Restore(ctx, "/backup", "/volumes")
		}
		var report recoveryarchive.Manifest
		var err error
		if os.Args[1] == "archive-create" {
			report, err = recoveryarchive.Create(ctx, "/volumes", "/backup")
		} else {
			report, err = recoveryarchive.Verify(ctx, "/backup")
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)

	case "keys-stage", "keys-activate", "keys-retire", "keys-revoke", "keys-status":
		if len(os.Args) != 2 {
			return errors.New("unexpected key operation args")
		}
		state, err := localcontrol.LoadState(stateDir)
		if err != nil {
			return err
		}
		owner, err := database(ctx, true)
		if err != nil {
			return err
		}
		defer owner.Close()
		operation := strings.TrimPrefix(os.Args[1], "keys-")
		report, err := localcontrol.RotateKeys(ctx, owner, state, "/identities", operation)
		if err != nil {
			return err
		}
		if operation != "status" {
			for _, role := range []string{"control", "gateway", "coordinator"} {
				if err = os.Chown(filepath.Join("/identities", role, "identity.json"), 65532, 65532); err != nil {
					return err
				}
			}
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	case "queue-init", "queue-upgrade":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		s, err := localcontrol.LoadState(stateDir)
		if err != nil {
			return err
		}
		client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{"valkey:6379"}, Username: "wr_initializer", Password: localcontrol.QueueOwnerPassword(s), DisableCache: true, ForceSingleClient: true})
		if err != nil {
			return err
		}
		defer client.Close()
		if err = valkeystore.InstallRuntimeLibrary(ctx, client); err != nil {
			return err
		}
		if err = valkeystore.InstallRecoveryLibrary(ctx, client); err != nil {
			return err
		}
		if err = publicguard.Install(ctx, client); err != nil {
			return err
		}
		if err = valkeystore.InstallMigrationLibrary(ctx, client); err != nil {
			return err
		}
		plan, err := valkeystore.InspectRuntimeMigration(ctx, client, "wr:runtime:local")
		if err != nil {
			return err
		}
		if plan.State == "prepared" {
			if os.Args[1] != "queue-upgrade" {
				return errors.New("legacy queue requires explicit queue-upgrade after a verified backup")
			}
			plan, err = valkeystore.ApplyRuntimeMigration(ctx, client, "wr:runtime:local", plan.Digest)
			if err != nil {
				return err
			}
		}
		return json.NewEncoder(os.Stdout).Encode(plan)
	case "tunnel":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		// Raw TLS bytes through operator-authorized docker exec. No HTTP peer or
		// Forwarded header rewriting. Setup sees a real container-loopback socket.
		conn, err := net.DialTimeout("tcp4", "127.0.0.1:19444", 5*time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		go func() { <-ctx.Done(); _ = conn.Close() }()
		go func() { _, _ = io.Copy(conn, os.Stdin); _ = conn.(*net.TCPConn).CloseWrite() }()
		_, err = io.Copy(os.Stdout, conn)
		return err
	case "token":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		token, err := localcontrol.ReadPrivate(filepath.Join(stateDir, "bootstrap-token"), 43)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(token)
		return err
	case "health":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		s, err := localcontrol.LoadState(stateDir)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(s.Certificate) {
			return errors.New("local TLS unavailable")
		}
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
		r, err := client.Get(adminOrigin + "/livez")
		if err != nil {
			return err
		}
		defer r.Body.Close()
		if r.StatusCode != 204 {
			return errors.New("not healthy")
		}
		return nil
	case "init", "upgrade":
		upgrade := os.Args[1] == "upgrade"
		if (upgrade && len(os.Args) != 2) || (!upgrade && (len(os.Args) != 3 || (os.Args[2] != "on" && os.Args[2] != "off"))) {
			return errors.New("explicit initial TOTP on/off required")
		}
		if os.Geteuid() != 0 {
			return errors.New("initializer owns volume provisioning")
		}
		owner, err := database(ctx, true)
		if err != nil {
			return err
		}
		defer owner.Close()
		password, err := localcontrol.ReadPrivate("/run/secrets/runtime_password", 64)
		if err != nil {
			return err
		}
		// A fresh named volume is root-owned 0755. Only this explicit initializer
		// owns /state; runtime never repairs or recreates missing files.
		info, err := os.Lstat(stateDir)
		if err != nil || !info.IsDir() {
			return errors.New("state volume required")
		}
		if err = os.Chmod(stateDir, 0700); err != nil {
			return err
		}
		var s localcontrol.State
		if upgrade {
			s, err = localcontrol.Upgrade(ctx, owner, stateDir, string(password))
		} else {
			s, err = localcontrol.Initialize(ctx, owner, stateDir, string(password), os.Args[2] == "on")
		}
		if err != nil {
			return err
		}
		if err = localcontrol.ProvisionIdentities(s, "/identities"); err != nil {
			return err
		}
		for _, node := range []string{"control", "gateway", "coordinator", "demo-origin"} {
			dir := filepath.Join("/identities", node)
			if err = os.Chown(filepath.Join(dir, "identity.json"), 65532, 65532); err != nil {
				return err
			}
			if err = os.Chown(dir, 65532, 65532); err != nil {
				return err
			}
		}
		for _, dir := range []string{"/gateway-data", "/coordinator-data"} {
			if err = os.Chmod(dir, 0700); err != nil {
				return err
			}
			if err = os.Chown(dir, 65532, 65532); err != nil {
				return err
			}
		}
		if err = localcontrol.ProvisionQueueACL(s, "/queue-config", upgrade); err != nil {
			return err
		}
		runtimeFile := filepath.Join(stateDir, "runtime-password")
		previous, err := localcontrol.ReadPrivate(runtimeFile, 64)
		if errors.Is(err, os.ErrNotExist) {
			err = writeCredential(runtimeFile, password)
		} else if err == nil && string(previous) != string(password) {
			return errors.New("runtime credential mismatch")
		}
		if err != nil {
			return err
		}
		for _, name := range []string{"installation.json", "runtime-password"} {
			if err = os.Chown(filepath.Join(stateDir, name), 65532, 65532); err != nil {
				return err
			}
		}
		if err = os.Chown(stateDir, 65532, 65532); err != nil {
			return err
		}
		runtime, err := database(ctx, false)
		if err != nil {
			return err
		}
		defer runtime.Close()
		if _, err = localcontrol.Verify(ctx, runtime, s); err != nil {
			return err
		}
		fmt.Println("Initialized persistent local Control. Existing keys, accounts, policy and drafts were preserved. Explicit bootstrap-token command is required for first setup.")
		return nil
	case "bootstrap-token":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		// Explicit rotation, never on startup; DB refuses after first admin exists.
		s, err := localcontrol.LoadState(stateDir)
		if err != nil {
			return err
		}
		pool, err := database(ctx, false)
		if err != nil {
			return err
		}
		defer pool.Close()
		if _, err = localcontrol.Verify(ctx, pool, s); err != nil {
			return err
		}
		if err = localcontrol.BootstrapToken(ctx, pool, s, stateDir); err != nil {
			return err
		}
		fmt.Println("One-time bootstrap token written to /state/bootstrap-token (15 minutes). Existing token rotated. Use the local token command; never share it.")
		return nil
	case "serve":
		if len(os.Args) != 2 {
			return errors.New("unexpected args")
		}
		return serve(ctx, cancel)
	default:
		return errors.New("unknown subcommand")
	}
}

func writeCredential(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func serve(ctx context.Context, cancel context.CancelFunc) error {
	s, err := localcontrol.LoadState(stateDir)
	if err != nil {
		return err
	}
	pool, err := waitDatabase(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	totp, err := localcontrol.Verify(ctx, pool, s)
	if err != nil {
		return err
	}
	vault, err := pgstore.NewVault(map[string][]byte{"local-v1": s.Vault[:]})
	if err != nil {
		return err
	}
	store, err := pgstore.NewAudited(pool, vault)
	if err != nil {
		return err
	}
	_, err = store.PasswordIterations(ctx)
	if err != nil {
		return err
	}
	passwords := adminauth.NewPasswordHasherSource(store.PasswordIterations)
	cert, err := s.TLS()
	if err != nil {
		return err
	}
	identity, err := localcontrol.LoadIdentity("/identity", "control")
	if err != nil || identity.Binding != s.Binding() {
		return errors.New("control identity unavailable")
	}
	publication, err := pgstore.NewPublicationService(store, adminOrigin, identity.Installation, identity.ConfigPrivate)
	if err == nil {
		err = publication.WithDeploymentKeys(identity.Keys)
	}
	if err != nil {
		return err
	}
	internalTLS, err := identity.TLS(false)
	if err != nil {
		return err
	}
	internalListener, err := net.Listen("tcp4", "0.0.0.0:19445")
	if err != nil {
		return err
	}
	defer internalListener.Close()
	go runtimeplane.Worker(ctx, publication)
	trafficExecutor, closeTraffic, err := runtimeplane.TrafficExecutor(identity)
	if err != nil {
		return err
	}
	defer closeTraffic()
	traffic, err := pgstore.NewTrafficService(store, adminOrigin, trafficExecutor)
	if err != nil {
		return err
	}
	trafficDone := make(chan struct{})
	go func() { defer close(trafficDone); traffic.Worker(ctx) }()
	defer func() { cancel(); <-trafficDone }()
	var listeners []net.Listener
	var handlers []http.Handler
	for i, address := range []string{"0.0.0.0:19443", "127.0.0.1:19444"} {
		origin := adminOrigin
		if i == 1 {
			origin = setupOrigin
		}
		h, err := adminserver.Handler(store, passwords, s.Fingerprint, os.DirFS("/ui"), adminserver.Options{Origin: origin, Setup: i == 1, TOTP: totp, KeyID: "local-v1", Persistent: true, Publication: publication, Traffic: traffic})
		if err != nil {
			return err
		}
		l, err := net.Listen("tcp4", address)
		if err != nil {
			return err
		}
		defer l.Close()
		listeners = append(listeners, l)
		handlers = append(handlers, h)
	}
	results := make(chan error, 3)
	go func() {
		results <- runtimeplane.Serve(ctx, internalListener, internalTLS, runtimeplane.InternalControl(publication))
	}()
	for i, l := range listeners {
		go func() { results <- adminserver.Serve(ctx, l, cert, handlers[i]) }()
	}
	fmt.Println("Persistent local Control ready at https://127.0.0.1:19443. Draft-only developer preview; Beta NO-GO. Setup is unpublished container loopback; use the local setup tunnel.")
	first := <-results
	cancel()
	second := <-results
	third := <-results
	if first != nil {
		return first
	}
	if second != nil {
		return second
	}
	return third
}

// Docker may restart the DB and Control together. Retry connection only; never
// migrate, initialize, rotate secrets or relax schema verification on startup.
func waitDatabase(ctx context.Context) (*pgxpool.Pool, error) {
	startup, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		pool, err := database(startup, false)
		if err == nil {
			return pool, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-startup.Done():
			timer.Stop()
			return nil, errors.New("database startup timeout")
		case <-timer.C:
		}
	}
}

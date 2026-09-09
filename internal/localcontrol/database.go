// SPDX-License-Identifier: Apache-2.0
package localcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"waiting-room/internal/adminauth/pgstore"
)

const Schema = "waiting_room"
const RuntimeRole = "wr_runtime"

var migrations = []string{pgstore.Migration001, pgstore.Migration002, pgstore.Migration003, pgstore.Migration004, pgstore.Migration005, pgstore.Migration006, pgstore.Migration007, pgstore.Migration008, pgstore.Migration009, pgstore.Migration010, pgstore.Migration011, pgstore.Migration012, pgstore.Migration013, pgstore.Migration014}

// Initialize is an explicit owner operation, never a serve/startup side effect.
// All DDL, ledger entries, identity and initial policy commit in one transaction.
// Existing installs require the same key binding, policy and immutable SQL hashes.
func Initialize(ctx context.Context, owner *pgxpool.Pool, dir, runtimePassword string, totp bool) (State, error) {
	return initialize(ctx, owner, dir, runtimePassword, &totp)
}

// Upgrade applies immutable pending migrations without overwriting a policy
// changed through the authenticated UI. It never creates a fresh installation.
func Upgrade(ctx context.Context, owner *pgxpool.Pool, dir, runtimePassword string) (State, error) {
	previous, err := ReadPrivate(dir+"/runtime-password", 64)
	if err != nil || string(previous) != runtimePassword {
		return State{}, errors.New("matching existing runtime credential required")
	}
	return initialize(ctx, owner, dir, runtimePassword, nil)
}

func initialize(ctx context.Context, owner *pgxpool.Pool, dir, runtimePassword string, totp *bool) (State, error) {
	if len(runtimePassword) != 64 {
		return State{}, errors.New("invalid generated DB credential")
	}
	if _, err := hex.DecodeString(runtimePassword); err != nil {
		return State{}, errors.New("invalid generated DB credential")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := owner.Begin(ctx)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='20s'"); err != nil {
		return State{}, err
	}
	// Role provisioning includes a generated password. Suppress SQL text even
	// on an unexpected role-creation error; never rely only on application logs.
	if _, err = tx.Exec(ctx, "SET LOCAL log_statement='none'; SET LOCAL log_min_error_statement='panic'; SET LOCAL log_min_duration_statement=-1"); err != nil {
		return State{}, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(72819431061)"); err != nil {
		return State{}, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", Schema).Scan(&exists); err != nil {
		return State{}, err
	}
	if !exists && totp == nil {
		return State{}, errors.New("upgrade requires an existing installation")
	}
	s, err := LoadState(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || exists {
			return State{}, errors.New("state missing/invalid; restore matching state and database")
		}
		s, err = createState(dir)
		if err != nil {
			return State{}, err
		}
	}
	if !exists {
		if _, err = tx.Exec(ctx, "CREATE SCHEMA waiting_room"); err != nil {
			return State{}, err
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL search_path=waiting_room"); err != nil {
		return State{}, err
	}
	if !exists {
		if _, err = tx.Exec(ctx, "CREATE TABLE install_identity(singleton boolean PRIMARY KEY CHECK(singleton), key_binding text NOT NULL); CREATE TABLE schema_migrations(version integer PRIMARY KEY, digest text NOT NULL)"); err != nil {
			return State{}, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO install_identity VALUES(true,$1)", s.Binding()); err != nil {
			return State{}, err
		}
	} else {
		var binding string
		if err = tx.QueryRow(ctx, "SELECT key_binding FROM install_identity WHERE singleton").Scan(&binding); err != nil || binding != s.Binding() {
			return State{}, errors.New("database/key binding mismatch")
		}
	}
	for i, sql := range migrations {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(sql)))
		var actual string
		err = tx.QueryRow(ctx, "SELECT digest FROM schema_migrations WHERE version=$1", i+1).Scan(&actual)
		if err == nil {
			if actual != digest {
				return State{}, errors.New("migration checksum mismatch")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return State{}, err
		}
		if _, err = tx.Exec(ctx, sql); err != nil {
			return State{}, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations VALUES($1,$2)", i+1, digest); err != nil {
			return State{}, err
		}
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != len(migrations) {
		return State{}, errors.New("unsupported schema version")
	}
	if !exists {
		if _, err = tx.Exec(ctx, "UPDATE auth_policy SET totp_enabled=$1 WHERE singleton", *totp); err != nil {
			return State{}, err
		}
		// The control profile is a configuration limit, not capacity qualification.
		if _, err = tx.Exec(ctx, `INSERT INTO control_config(singleton,revision,document) VALUES(true,0,'{"schemaVersion":1,"revision":0,"profile":"standard-10k","regionId":"local","rooms":[]}'::jsonb)`); err != nil {
			return State{}, err
		}
	} else if totp != nil {
		var actual bool
		if err = tx.QueryRow(ctx, "SELECT totp_enabled FROM auth_policy WHERE singleton").Scan(&actual); err != nil || actual != *totp {
			return State{}, errors.New("initialization cannot change an existing TOTP policy")
		}
	}
	var roleExists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", RuntimeRole).Scan(&roleExists); err != nil {
		return State{}, err
	}
	if !roleExists {
		// Password is a generated 64-byte hex string, never caller SQL or logged.
		if _, err = tx.Exec(ctx, "CREATE ROLE wr_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '"+runtimePassword+"'"); err != nil {
			return State{}, errors.New("runtime role creation failed")
		}
	} else if !exists {
		return State{}, errors.New("runtime role already exists outside this installation")
	}
	if _, err = tx.Exec(ctx, `REVOKE ALL ON SCHEMA waiting_room FROM PUBLIC;
GRANT USAGE ON SCHEMA waiting_room TO wr_runtime;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA waiting_room TO wr_runtime;
REVOKE INSERT,UPDATE,DELETE ON install_identity,schema_migrations FROM wr_runtime;
REVOKE UPDATE,DELETE ON control_audit FROM wr_runtime;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA waiting_room TO wr_runtime`); err != nil {
		return State{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return State{}, err
	}
	return s, nil
}

// Verify is read-only: a missing/mismatched migration or key prevents startup.
func Verify(ctx context.Context, pool *pgxpool.Pool, s State) (bool, error) {
	var binding string
	var totp bool
	if err := pool.QueryRow(ctx, "SELECT i.key_binding,p.totp_enabled FROM install_identity i CROSS JOIN auth_policy p WHERE i.singleton AND p.singleton").Scan(&binding, &totp); err != nil || binding != s.Binding() {
		return false, errors.New("installation binding unavailable")
	}
	rows, err := pool.Query(ctx, "SELECT version,digest FROM schema_migrations ORDER BY version")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var v int
		var digest string
		if rows.Scan(&v, &digest) != nil || i >= len(migrations) || v != i+1 || digest != fmt.Sprintf("%x", sha256.Sum256([]byte(migrations[i]))) {
			return false, errors.New("schema verification failed")
		}
		i++
	}
	if rows.Err() != nil || i != len(migrations) {
		return false, errors.New("schema verification failed")
	}
	return totp, nil
}

func BootstrapToken(ctx context.Context, pool *pgxpool.Pool, s State, dir string) error {
	vault, err := pgstore.NewVault(map[string][]byte{"local-v1": s.Vault[:]})
	if err != nil {
		return err
	}
	store, err := pgstore.New(pool, vault)
	if err != nil {
		return err
	}
	token, err := store.IssueBootstrapToken(ctx)
	if err != nil {
		return err
	}
	return writePrivate(dir, "bootstrap-token", []byte(token.Token()), true)
}

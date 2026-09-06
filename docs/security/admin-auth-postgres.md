# Backoffice TOTP PostgreSQL slice

Source: `internal/adminauth/pgstore`. **Storage-only**, not a working login service.
No production public/private Admin listener or React UI is wired to this adapter.
The separate [session adapter](admin-sessions.md) is exercised on temporary TLS test servers.
Migration 002 adds password storage and a separate [password login service](password-login.md).
Migration 003 adds the [first-admin bootstrap service](admin-bootstrap.md); setup CLI/HTTP
and general account provisioning remain pending.

## Transaction boundary

`CompleteTOTP(ctx, opaqueChallenge, code)` loads trusted current DB records itself.
The opaque challenge must have been issued after password verification and login throttling;
the password login service now issues it after actual Argon2id verification. TOTP-only tests
also use direct SQL fixtures. There is no public “password verified” flag,
challenge creation API or password bypass endpoint.

Locks use one order: policy (shared) → account (exclusive) → credential → challenge → session.
Future reset, role/policy changes and enrollment writers must follow that same order.
Each call uses a five-second context deadline, four-second statement timeout, explicit
rollback on cancellation and `synchronous_commit=on`. No network calls happen inside the
transaction except PostgreSQL. Challenge/user lookup and foreign keys are indexed.

- Validate enabled user, active credential version and challenge-bound user/policy versions.
- Read `clock_timestamp()` **after** row-lock waits; reject expired/future-dated challenges.
- AES-256-GCM decrypt using a caller-provided Control-only keyring. Bind ciphertext to user,
  credential version and key ID. The schema stores 60-byte nonce+ciphertext, never plaintext.
- Verify six-digit ±1-window TOTP; guard `last_counter < new_counter`.
- Consume the challenge and insert session+CSRF hashes in the **same transaction**.
- Return raw 256-bit session/CSRF credentials only after confirmed commit. Grant logging/JSON
  is redacted; raw accessors are exclusively for a future authenticated HTTP adapter.
- Invalid/replayed codes atomically consume one of five attempts. Expired/consumed/stale
  challenges cannot mint sessions. Session insertion errors roll back the counter and challenge.
- Unknown commit returns unavailable and no credentials; a successful-but-lost response can
  require a fresh password challenge and next OTP. Commit uncertainty has a unit double test,
  not a real network fault injection test.

OFF is honored as a policy value but **not** used as a bypass inside this TOTP-only method.
A separate password login service now supports explicit OFF sessions. Session reads, internal
idle refresh and partial HTTP logout exist separately; policy-transition/session-rotation APIs
and full lifecycle/idempotency/audit are pending.

## Migration and deployment boundary

`migrations/001_auth.sql` is embedded for an explicit, one-time migration call; Store.New
does not run DDL. It is a minimal schema slice, not a production migration/upgrade runner.
Use a dedicated migration role and deny DDL to runtime roles when deployment wiring lands.
The included Compose credentials are deliberately public **test fixtures**, loopback only;
they are not a production secret-management example. Never point this test at a customer DB.

Control key-file loading, rotation, permissions, audit events, recovery code storage,
installation calibration approval, last-Admin protection and deployment
`forced_on` override wiring are NOT implemented. Neither this schema nor tests certify
backup/restore, PostgreSQL restart/failover, multi-process Control or 10K/100K capacity.
Local Argon2id calibration tooling and shared login limits are documented in the password slice.

## Local execution and proof

See [auth DB lab](../operators/admin-auth-lab.md) and [local evidence](../evidence/admin-store-summary.md).
Two independent pools in one Go process verify durable DB arbitration. Test schemas are
fresh and cleaned after each test; the Compose volume is retained. Session insert failure
is injected with a test-only SQL constraint. An expiry-during-lock-wait case uses real DB
time and independently observed lock contention (not a transaction-cached statistics view).

The design follows PostgreSQL's [row-lock semantics](https://www.postgresql.org/docs/17/explicit-locking.html)
and [pgx transaction API](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.10.0/pgxpool).
These sources inform implementation; PASS claims come from repository test logs.
SUB-PRD-04 and MAIN delivery remain **NO-GO**.

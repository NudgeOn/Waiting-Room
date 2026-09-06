# Password login storage slice

Source: `internal/adminauth/password.go`, `internal/adminauth/pgstore/login.go`.
This is a callable server-side service, **not an HTTP login endpoint or Backoffice UI**.
Separate me/logout HTTP adapters now exist; see [session boundary](admin-sessions.md).
The separate [auth HTTP adapter](admin-auth-http.md) now connects login/OTP/recovery on temporary TLS.
Tests seed local accounts and password hashes, then exercise real password verification
to create TOTP challenges. There is no caller-provided “password verified” flag.
First-admin [bootstrap service](admin-bootstrap.md) now exists; setup HTTP/CLI, general account
provisioning and changing an existing password remain pending.

## Password format and resource bounds

- `wrp1$argon2id$v=19$m=65536,t=N,p=1$...`: versioned parameter envelope, 16-byte random
  salt, 32-byte hash, 64 MiB memory, one lane, calibrated iterations 2–10.
- Strict canonical parser rejects excessive memory/iterations/parallelism, invalid Base64,
  wrong versions and trailing data before any attacker-selected KDF allocation.
- Creation requires at least 15 Unicode code points; max 1,024 UTF-8 bytes. Verification
  never trims, truncates or normalizes passwords. Dictionary/breached-password checks are pending.
- At most two active Argon2 operations per Go process, shared by all hasher instances.
  Excess work returns unavailable; there is no unbounded worker queue. This is an active-work
  bound, not a promise that process RSS is 128 MiB (Go allocation/GC overhead exists).
- Missing/disabled users use a dummy hash with the same configured cost. Comparisons use
  constant-time byte comparison. This reduces enumeration signals but is not a full
  HTTP/network timing indistinguishability qualification.
- All active records must match the configured installation cost. Do not change cost after
  provisioning without a hash migration plan; unsupported/corrupt records fail closed.
- Hash, hasher, login service/snapshot/result default formatting and JSON are redacted.
  Explicit storage/token accessors remain sensitive and must not be logged.

`make auth-calibrate` takes three samples per iteration count and reports the first median
in 250–500 ms. It creates no account, modifies no installation setting and prints no hash
or password. Failure to reach the target exits nonzero. Run on the actual reference host
without competing workload. Local Mac evidence is not installation calibration approval.

## DB flow and lock order

1. Validate bounded internal user ID and a trusted socket peer. Current storage service uses
   exact case-sensitive account IDs, not an email/username UX contract.
2. Commit an attempt reservation before querying account existence or doing Argon2 work.
3. Read the current user/password/policy snapshot; verify password **outside all transactions**.
4. Lock policy → account → password credential → TOTP credential → challenges → session.
   TOTP-only verification skips the password row but preserves this relative order.
5. Re-read and compare enabled/role, user/password/policy versions, hash and policy values.
   A changed snapshot cannot create a challenge/session.
6. TOTP ON with an enrolled credential: replace pending login challenges with a fresh
   five-minute challenge. An unenrolled user instead receives a separate five-minute
   enrollment proof, replacing the previous one; never a session before OTP completion.
   Secret provisioning/verification now exist in the separate [enrollment service](admin-enrollment.md);
   [recovery-code consumption](admin-recovery.md) is a separate server service; HTTP/UI remain pending.
7. Configurable OFF: insert a random session+CSRF hash with `mfa_verified=false`.
   This honors an explicit OFF but does **not** implement the policy-switch API.
8. Return tokens only after confirmed commit. Insert failures roll back challenge replacement;
   the earlier attempt reservation remains consumed.

Reservations use a separate short transaction, installation → source → account order.
Session/challenge transactions never hold throttle locks, and Argon2 holds neither set.
Future password changes must increment both password version and user session version,
revoke pending challenges/sessions, and follow the same lock order; this write API is pending.
Fresh-session lookup, internal idle refresh and partial HTTP logout are implemented in
the separate session slice; full lifecycle/idempotency/audit remain pending.

## Login limits and privacy

Local alpha limits count **all attempts**, including successful logins: account 5,
source 20, installation 60, each per anchored one-minute window (not a rolling-window claim).
A denied narrower bucket does not refund earlier bucket reservations; process failures and
unknown commits never refund attempts. Wrong/disabled/missing/throttled requests share the
same unauthenticated result. Operational per-account lockout/DoS tradeoffs still need UI/runbooks.

Account keys are HMAC-SHA-256 identifiers. Source identifiers rotate by DB UTC day using a
separate HMAC domain. IPv4-mapped IPv6 normalizes to IPv4; IPv6 addresses aggregate by /64.
All Control replicas must share the same private fingerprint key. Raw IP/input is not stored
in bucket rows. A daily rotation can reset the source budget at midnight; the account and
installation budgets remain in force. This is explicit alpha behavior, not strict rolling rate.

Callers must derive peer from the authenticated socket or a separately configured trusted
proxy. Never trust X-Forwarded-For/JSON directly. HTTP/proxy trust configuration is not wired.
The installation bucket is reserved first, bounding new identifiers; expired buckets are
pruned on the next admitted reservation. Background retention for sessions/challenges and
audit events remains future work. TOTP code attempts still have the existing five-attempt
challenge guard; global/source traffic limits for the future OTP HTTP endpoint are pending.

## Evidence and remaining boundaries

[Local evidence](../evidence/password-login-summary.md) includes real Argon2→TOTP→session,
explicit OFF, the earlier missing-enrollment denial, multi-pool limits, expired-window reset, rollback,
stale snapshot checks and a live policy-update transaction racing login.
The fixture hash uses two iterations to keep race tests bounded; calibration is a separate
non-race run, not silently applied to fixture or installation credentials.

The newer [bootstrap evidence](../evidence/bootstrap-summary.md) covers restricted enrollment
proof issuance instead of the previous unenrolled-user error; dashboard access stays blocked.
Migrations 001→002→003→004 are explicit DDL inputs; production migration ledger, least-privilege
roles, bootstrap HTTP/CLI and user management, recovery/enrollment HTTP/UI, key mounting/rotation, deployment
forced-on override, audit and private HTTP/UI remain pending. No real operator credentials,
production traffic, cloud services or external settings are changed.

Sources: [Go Argon2id](https://pkg.go.dev/golang.org/x/crypto/argon2),
[OWASP password storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
The bounded defaults exceed OWASP's minimum memory/iteration floor; actual strength and
availability still depend on deployment calibration and the missing lifecycle controls.
SUB-PRD-04 and MAIN delivery remain **NO-GO**.

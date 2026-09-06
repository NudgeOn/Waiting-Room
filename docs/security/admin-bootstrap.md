# First-admin bootstrap storage slice

Source: `internal/adminauth/pgstore/bootstrap.go`, migration `003_bootstrap.sql`.
This is a server-side service primitive, **not a working setup screen, CLI or HTTP endpoint**.
Only local test fixture accounts are created by the current runner.
The separate [provisioning HTTP adapter](admin-provisioning-http.md) now connects this service
over temporary loopback TLS; it does not provide a production setup command or wizard.

## Installation token

- An operator explicitly calls `Store.IssueBootstrapToken`. It must never be exposed as
  an unauthenticated HTTP action or automatically called by a service constructor/startup.
- Generates a random 256-bit token, stores only SHA-256, expires after 15 minutes of DB time.
  Reissuance invalidates the old token through both hash replacement and generation increment.
- Default formatting/JSON are redacted. The explicit `Token()` accessor is sensitive;
  a future local terminal flow may display it once, but logs/audit/URLs must not contain it.
- Migration 003 starts permanently closed when accounts already exist. A completed bootstrap
  retains an independent tombstone even if all accounts are later deleted. Recovery is not
  re-bootstrap; a future operator-only recovery command must implement its own controls.
- Apply migrations 001 → 002 → 003 once, with the application stopped and a migration role.
  A production migration ledger/role separation/backup-and-restore procedure is still pending.

## Atomic first administrator

1. Validate a literal loopback socket peer, bounded internal ID and canonical install token.
   IPv4-mapped loopback is normalized; remote, missing or zone-qualified peers are rejected.
   This is not proof of local human identity. Never pass client JSON/Forwarded headers here.
2. Check token hash/generation/expiry, empty accounts and current policy before Argon2 work.
3. Hash the new password outside a transaction using the existing bounded Argon2id service.
4. Lock policy (share) → bootstrap singleton (update), recheck policy/token/generation/empty
   accounts and **DB time after any lock wait**, then insert Admin and password credential.
5. TOTP ON: issue only a five-minute enrollment proof. TOTP OFF: issue a password-only
   session/CSRF pair with `mfa_verified=false`. The trusted DB policy is authoritative.
6. Set the permanent completed tombstone and erase install hash/expiry in the same commit.
   No credentials are returned before commit. Insertion failures roll back the whole operation.

Only one concurrent bootstrap can win across independent database pools. Future user
provisioning writers must follow policy → bootstrap → account lock order and require
completed bootstrap; arbitrary direct SQL writes are outside this service guarantee.
The service does not accept a role or client-asserted TOTP policy. The local HTTP adapter
accepts only username/password; the unreleased OpenAPI skeleton now matches its 15-code-point,
1,024-byte creation bounds. Wizard policy selection is still not implemented.

## Restricted enrollment proof

- Separate `auth_enrollment_challenges` table: SHA-256 token, user/policy/session versions,
  creation/expiry and consumed state. Foreign-key and expiry indexes are explicit.
- It is neither an ordinary session nor a TOTP login challenge; Me and CompleteTOTP reject it.
  No dashboard access, secret, QR or MFA success is granted by this slice.
- A fresh successful password login for an unenrolled ON user replaces their pending
  enrollment proof. This supports response-loss/expiry retry without reopening bootstrap.
- The separate [enrollment service](admin-enrollment.md) now rechecks current state and
  atomically consumes the proof with encrypted credential/counter/recovery hashes/session.
  Password token rotation also erases the old pending ciphertext (migration 004 required).
- [Recovery-code consumption](admin-recovery.md) now exists as a server service.
  QR/HTTP/registration/recovery screens and retention cleanup remain pending.
  Storage of the restricted proof alone is not completed TOTP enrollment.

## Remaining HTTP/operator boundary

The future setup adapter must bind loopback, reject untrusted proxy/Host/Origin requests,
enforce an explicit browser CSRF boundary, throttle requests and set secure cookies only
for actual sessions. SSH tunnels need explicit operator guidance. There is no setup listener,
token-issuing CLI, full HTTP command idempotency, audit stream, wizard policy write, password
blocklist or production installation calibration approval in this slice.

[Local evidence](../evidence/bootstrap-summary.md) distinguishes unit/service DB testing from
HTTP/browser/MFA-app testing. SUB-PRD-04 and MAIN remain **NO-GO**.

# TOTP initial enrollment service

Sources: `internal/adminauth/pgstore/enrollment.go`, migration `004_enrollment.sql`.
This is a **server service**, not a setup/auth HTTP endpoint or a rendered QR screen.
The separate [provisioning HTTP adapter](admin-provisioning-http.md) now provides manual-key
Begin/Complete and initial cookie responses on temporary TLS. QR and browser UI remain pending.
It accepts only the restricted proof issued by [bootstrap/password login](admin-bootstrap.md).
No raw user ID, secret, policy version or “password verified” flag is accepted from a client.

## Begin: prepare a pending secret

- Lock policy → account → optional active credential → enrollment proof. Require ON, enabled
  known-role account, current user/policy versions, an unconsumed/unexpired proof and no
  active credential. An existing credential always denies this initial-enrollment path.
- Generate a 160-bit secret once and store only AES-GCM ciphertext. Pending AAD includes
  user/version/key ID **and enrollment token hash**, distinct from active-credential AAD.
- Repeat Begin returns the same key and original expiry; it cannot extend the five-minute TTL.
  An exhausted five-attempt proof cannot restart Begin. Password login must issue a new proof.
- `ManualKey()` is an explicit sensitive accessor for a future private local-rendered screen.
  Default formatting/JSON redact it. No third-party QR service, URI or QR image is generated.

## Complete: bounded preparation, atomic commit

1. In a short transaction recheck current state and reserve one of **five total attempts**.
   Every admitted attempt counts, even malformed OTP, cancellation, KDF capacity rejection or
   insertion failure. Failed work never refunds this separately committed reservation.
2. Reject malformed six-digit input. For shape-valid input generate ten independent random
   256-bit recovery codes and salted Argon2id hashes **outside all database locks**.
3. Re-lock and re-read current user/policy/credential/proof, including DB time after waits.
   The unexported reservation binds token, user/version, policy and attempt number.
4. Decrypt the pending secret and verify the OTP using the existing six-digit/30-second/±1
   verifier. Its CounterStore consumer inserts the initial active credential with the accepted
   counter; this is not a reusable stateless “OTP valid” result.
5. In the same transaction consume the proof, re-encrypt the active credential under its
   own AAD, insert ten recovery hashes, revoke prior sessions/challenges, erase pending
   ciphertext and insert exactly one fresh MFA-verified session plus CSRF hash. Commit before
   returning any session token or recovery code. Counter/challenge/recovery/session failures
   roll back together; the earlier attempt reservation remains spent.

The fifth reserved request can succeed. Once five are reserved, no new request is admitted;
already-reserved requests may finish if the proof is still live. Racing finalizations have one
winner. Wrong OTP leaves no active credential/recovery/session, and consumed enrollment OTP
counters cannot be reused in normal TOTP login. All roles follow the same enrollment rule.

## Recovery material and resource limits

- Ten slots, each 43-character canonical Base64URL random material, returned only with a
  confirmed success. Hash input is `wr-recovery-v1:` + raw code using the configured versioned
  Argon2id PasswordHasher. Separate salts; no plaintext/reversible recovery storage.
- The DB primary/foreign keys bind each slot to its active user/credential version. Default
  result/bundle formatting and JSON are redacted; explicit accessor results remain sensitive.
- Two active Argon2 jobs per process and a 20-second Complete context bound expensive work.
  Ten hashes are generated sequentially. Even a wrong **well-formed** OTP can incur this work;
  five proof reservations and existing password-login limits bound retries, not global CPU
  or multi-replica traffic. Private HTTP source/global limits and load qualification remain
  mandatory before exposing endpoints. This is not installation calibration approval.
- [Recovery-code login/one-time consumption](admin-recovery.md) now exists as a separate service;
  code regeneration/reset and HTTP/UI remain pending. A successful commit whose response is lost does not redisplay recovery codes:
  use the enrolled authenticator for login; safe regeneration needs a future reauth flow.
- Consumed/pending proof retention cleanup is still pending; completion/rotation erase pending
  secret ciphertext. No account version history is deleted or reset by this service.

## Integration boundaries

Apply 001 → 002 → 003 → 004 once using a migration role with the application stopped. Runtime
constructors never run DDL. Migration ledger, secret key-file management/rotation and production
least-privilege setup remain pending. Future resets must preserve monotonic credential version
history, remove old recovery rows and follow the documented lock order; Begin is not a reset API.

No private listener, secure initial cookie, browser CSRF/Origin boundary, QR/manual-key screen,
authenticator-app device test, command idempotency/audit or policy-switch wizard is claimed.
Unknown-commit failure injection is a unit double, not a real network commit-loss experiment.

[Local evidence](../evidence/enrollment-summary.md) · [SUB-PRD-04](../sub-prd_04.md).
Scoped service validation and delivery gates are separate: SUB-PRD-04 and MAIN remain **NO-GO**.

# Backup-code login service

Source: `internal/adminauth/pgstore/recovery.go`. Uses migrations 001–004; no new DDL.
This completes a **password-issued TOTP login challenge** with one recovery code. It is not
password reset, TOTP reset, last-Admin recovery, action-bound reauthentication or an HTTP API.
The separate [auth HTTP adapter](admin-auth-http.md) now wires this service; production listener/UI remain pending.

## Entry and resource boundary

- The existing throttled password login must first return a live TOTP challenge. The service
  accepts only that opaque challenge and a recovery code, never caller-supplied identity,
  credential version, slot, hash, policy or a “password verified” flag.
- Require enabled known-role account, ON policy, active enrolled credential and current
  user/policy/credential versions. Session and enrollment tokens are different domains.
- Reserve an attempt in a short transaction before checking the recovery code. Share the
  existing five-minute/five-attempt challenge with ordinary OTP login: recovery reserves
  every admitted attempt, OTP consumes failed attempts. Switching methods cannot reset it.
- Once five slots are reserved/spent, neither method admits new work. A previously reserved
  recovery request, including the fifth, may finish while its challenge remains live.
  Failures/cancellation/capacity rejection/unknown commits do not refund reservations.
- Load ten ordered recovery records under the same user/credential version. Incomplete or
  corrupt storage fails unavailable. Validate the exact 43-character canonical Base64URL
  code and verify all ten Argon2id hashes outside every DB lock, including consumed slots.
  The hash input is `wr-recovery-v1:` + code, matching enrollment generation.
- There is no early successful-slot exit. Wrong and used codes return the same generic
  invalid/replayed result. This is not a full HTTP timing-indistinguishability qualification.
- Existing per-process two-active-Argon2 gate and a 20-second context bound the service.
  Ten sequential verifications are still expensive; private HTTP source/global limits,
  multi-replica resource qualification and installation calibration remain required.

## Atomic finalization

Lock order: policy → account → active credential → challenge → selected recovery row → session.
Account locking serializes ordinary OTP and recovery operations for one user across pools.

After KDF, re-read current versions/role/enabled state and selected hash/consumed state.
Read database time after challenge lock waits, then recheck expiry at the consuming write.
In one transaction:

1. Mark only the verified user/version/slot/hash recovery row consumed, if still unused.
2. Consume the still-live password challenge, within the shared attempt limit.
3. Insert a random session/CSRF hash pair with `mfa_verified=true` and current versions.
4. Commit before returning raw credentials. A failed write or uncertain commit returns no grant.

Any late failure rolls back both consumptions and the session insertion. The earlier attempt
reservation remains spent. One winner is permitted for the same code across different
challenges, different codes on the same challenge, and OTP/recovery racing one challenge.
The private finalizer is not a public “verified slot” API; only Complete runs it after KDF.

`mfa_verified` means password plus the backup factor completed; it is not proof of a fresh
TOTP code for dangerous actions. Those actions still require a separate one-time reauth flow.
Recovery login leaves the TOTP secret/counter, other nine codes and existing sessions intact.
It neither disables MFA nor regenerates codes. Used rows are retained to keep ten-slot work
and version binding; future regeneration/reset must replace them under the same lock order.

## Remaining delivery work

- Recovery HTTP adapter, request/response schema linkage, secure initial cookie, Origin/CSRF,
  private listener, browser UI and audit/idempotency are not implemented by this slice.
- QR/manual-key enrollment screens, backup-code download/display UX, safe reissue/reset,
  policy ON/OFF transition and operator recovery remain pending.
- A committed response lost in transit may have consumed the code: use another unused code
  after password login. There is no raw session recovery or completed-command replay API yet.
- No real network commit-loss/restart/failover, MFA app/device, load or production test is claimed.
  Unit doubles exercise uncertain-commit handling separately from PostgreSQL concurrency.

[Evidence](../evidence/recovery-summary.md) · [Enrollment](admin-enrollment.md) ·
[SUB-PRD-04](../sub-prd_04.md). SUB-PRD-04 and MAIN delivery remain **NO-GO**.

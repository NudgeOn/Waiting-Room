# Backoffice authorization core — implementation boundary

Source: `internal/adminauth`. This is a tested library, **not** a running authentication
service. A partial me/logout adapter now uses it in temporary TLS tests; no production
Admin listener exists. Core helpers create no user
account or public listener. A separate [PostgreSQL adapter](admin-auth-postgres.md) now
persists encrypted fixture credentials and atomically issues hashed sessions in local tests.
The existing Web/App Lab is unchanged.

## Shared policy

- Version 1, 19 explicit actions, three roles; unknown roles/actions deny.
- Template/Room config changes: Admin only. Runtime AUTO/HOLD and events: Admin/Operator.
- Dashboard/config/audit/runtime/result reads: all three roles.
- Users/security/key management: Admin. Sensitive changes require separate action-bound reauth.
- `Capabilities` projects the same rules for a future UI adapter. It is not a credential.
- `CheckOrdinaryAction` rejects dangerous actions even for an MFA-authenticated Admin;
  the one-time reauth transaction has not been implemented.

## Session and CSRF checks

Callers must load **current, authoritative** account, session and global policy records.
Never construct those records from a request JSON or trust a client role/capability flag.

Session checks enforce enabled account, matching user and version, MFA when required,
30-minute idle and eight-hour absolute expiry. Expiry boundaries are exclusive and
backward/future activity timestamps fail closed. These read helpers never extend a session.
Configurable OFF is honored explicitly; a missing/invalid policy is not treated as OFF.
Forced-on plus OFF is invalid.

The CSRF helper requires an exact deployment-pinned origin and a 256-bit token bound
through its hash to the server-side session. It rejects null/cross-origin/duplicate headers.
It does not derive trust from Host/Forwarded. HTTP requires an explicit literal-loopback
exception; the helper does not enable CORS or authenticate the session by itself.
Internal error constants are not a complete HTTP problem mapping.

## TOTP verification and persistence contract

HMAC-SHA1, six digits, 30-second steps, previous/current/next step. Secrets are randomly
generated 160-bit values; enrollment export is canonical unpadded Base32. Logging and JSON
of the Secret type are redacted, but explicitly exported provisioning text is sensitive.
No QR endpoint/service or authenticator-device test is included. Persistent encryption is
provided by the separate PostgreSQL slice, not by this Secret value type.

`VerifyAndConsume` requires a `CounterStore`. The reference and secret must come from
one trusted credential snapshot. Stateless matching is deliberately unexported.
A successful store operation must atomically:

1. Check the user and still-active credential version.
2. Accept only a counter greater than the previous accepted counter (initial sentinel -1).
3. Consume the still-live, throttled password/TOTP challenge.
4. Complete its corresponding session/enrollment transition.
5. Commit before reporting success.

The PostgreSQL adapter binds its transaction to a stored, versioned challenge.
All Control replicas must share durable state. Secret reset must increment the credential
version; old snapshots cannot consume counters against the replacement credential.
An unknown commit returns unavailable, never success. Users may need the next code
after a committed-but-lost response. A future-step success also blocks older steps still
inside the tolerance window. Adjacent digit collisions consume the newest matching counter.

The core concurrency test uses a mutex-protected **test-only store double**. Its
64 submissions produce exactly one success, but this is not multi-process PostgreSQL
or complete login/challenge atomicity evidence. The separate [PostgreSQL evidence](../evidence/admin-store-summary.md)
tests two independent pools against a real database. There is no in-memory production store.

## Next wiring tasks

- [x] Minimal PostgreSQL account/session/credential/challenge schema and TOTP-login transaction adapter
- [x] Argon2id hash·local calibration tool·shared password login limits (server service)
- [x] DB session read/touch/logout and partial me/logout HTTP adapter; [boundary](admin-sessions.md)
- [x] First-admin bootstrap service and restricted enrollment proof; [boundary](admin-bootstrap.md)
- [x] Initial TOTP enrollment/OTP commit/recovery hashes/MFA session service; [boundary](admin-enrollment.md)
- [x] Backup-code login with shared challenge budget and atomic consumption; [boundary](admin-recovery.md)
- [x] Password/OTP/recovery HTTP, pinned transport boundary and shared limits; [boundary](admin-auth-http.md)
- [ ] Installation calibration approval, OTP HTTP traffic limits and recovery-code storage
- [x] AES-256-GCM vault with injected Control keyring and user/version/key-ID binding
- [ ] Control-only key-file loading/rotation and enrollment QR/manual-key UI
- [ ] Atomic TOTP ON/OFF policy/version switch and all-role session revocation
- [ ] Action-bound single-use reauth, last-Admin guard and redacted audit events
- [ ] Private Admin API integration and capability-driven React UI
- [ ] Backoffice template selection/preview/save/signed publish

## References and evidence

Original implementation using Go standard cryptographic primitives; test values are from
[RFC 4226 Appendix D](https://www.rfc-editor.org/rfc/rfc4226#appendix-D) and
[RFC 6238 Appendix B](https://www.rfc-editor.org/rfc/rfc6238#appendix-B).
[RFC 6238 section 5.2](https://www.rfc-editor.org/rfc/rfc6238#section-5.2) requires rejecting
reused successful OTPs. Broader lifecycle requirements:
[OWASP MFA guidance](https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html).

Run `make test-unit PRD=04`; [local evidence](../evidence/admin-auth-summary.md).
Local [bootstrap/enrollment HTTP](admin-provisioning-http.md) now connects the existing
services; production listener, wizard and rendered Backoffice are still separate work.
SUB-PRD-04 and MAIN remain NO-GO.

# Bootstrap and initial enrollment HTTP slice

Source: `internal/adminauth/authhttp`. No production listener or UI is started.
This extends the [authentication HTTP boundary](admin-auth-http.md), preserving direct TLS,
pinned Host/Origin, `X-WR-Auth: 1`, no cookie/CSRF credential mixing, strict 4 KiB JSON,
body deadline, two in-flight requests and shared PostgreSQL source/installation reservations.

## Three opt-in routes

| Constructor | POST route | Credential and body | Confirmed result |
|---|---|---|---|
| `NewBootstrap` | `/api/admin/v1/bootstrap` | One `X-Bootstrap-Token`; username/password JSON | ON: enrollment proof only; OFF: password-only session |
| `NewEnrollment` | `/api/admin/v1/auth/totp/enroll` | One enrollment Bearer proof; required `{}` | Stable manual Base32 key and original expiry; no cookie |
| `NewEnrollment` | `/api/admin/v1/auth/totp/enroll/verify` | One enrollment Bearer proof; code JSON | MFA cookie/session/CSRF plus ten unique recovery codes |

`New` retains its original three login/OTP/recovery routes. `NewEnrollment` adds only the
two enrollment routes; it cannot serve bootstrap. `NewBootstrap` serves only bootstrap.
The PostgreSQL provisioning constructor requires all existing services plus EnrollmentService.
Neither constructors nor HTTP routes issue/rotate installation tokens or migrate a database.

## Local bootstrap boundary

- Mount only on a separate loopback-bound TLS server. The constructor requires a literal
  loopback IP in the HTTPS origin, not localhost DNS, wildcard IP, remote IP or plain HTTP.
- Each request requires loopback remote socket and a loopback TCP LocalAddrContextKey.
  Missing/zone-qualified/nonloopback addresses fail closed. Proxy headers are not trusted.
  These per-request checks do **not** prove the server bind address; binding and lifecycle
  remain obligations of the future production command. Never mount on public Gateway.
- Installation token is canonical 256-bit material in exactly one header, not URL/body;
  Authorization/cookie/session-CSRF mixing is rejected. Exact Origin plus custom marker
  and JSON is the pre-auth browser boundary, not an authenticated session CSRF grant.
- Existing service checks the 15-minute installation token, empty accounts and permanent
  tombstone. First Admin creation is atomic; subsequent requests fail generically and the
  token is never issued/reopened automatically. The HTTP listener itself is not shut down.
- Current DB policy is authoritative. Request `totpEnabled`, role and secret are rejected;
  this intentionally narrows the unreleased Bootstrap skeleton. Wizard policy selection,
  forced-on deployment configuration and token-issuing operator CLI remain pending.
- Password creation requires 15 Unicode code points and at most 1,024 UTF-8 bytes.
  No production calibration or password blocklist approval is implied.

## Enrollment and recovery material

Begin exposes only a manual key and the original five-minute proof expiry. Repeating it
does not rotate the key or extend TTL. No otpauth URI, third-party QR service or QR renderer
is used. The initial service still rejects active credentials, stale proof or policy and
reserves at most five completion attempts. This is not a reset endpoint.

Complete waits for the credential/counter/recovery/session transaction to commit, checks
ten distinct canonical codes, then re-reads the current session before setting its cookie.
Response shape is `EnrollmentResult`: authenticated state, CurrentSession, CSRF and codes.
It does not repeat the manual key or expose a raw session token in JSON. Response structs
redact default formatting; their intentional JSON response is sensitive and must not be logged.

Consumed proof cannot repeat Begin or Complete. A successful response lost after commit
cannot redisplay the recovery codes; the user must use their newly enrolled authenticator.
Regeneration requires a future reauthentication flow. This slice has no durable command
idempotency or audit implementation and does not claim those full PRD contracts are met.

## Verification and remaining delivery work

Temporary separate TLS setup/admin servers verify HTTP/1.1 and HTTP/2, ON/OFF bootstrap,
real generated manual key and ten separately hashed codes, initial cookie→Me→logout,
recovery login and code replay rejection. Only policy configuration is seeded by SQL;
the initial account/password/credential/recovery/session are created through HTTP/services.
The installation token is issued explicitly by the test's operator-service fixture.

CookieJar shares cookies for the same loopback host across the two test ports; cookies
are not port-isolated. This does not establish a cross-host setup handoff or browser proof.
Production origin/listener topology, secure certificate/key provisioning, policy wizard,
CLI, listener shutdown, QR/manual-key/recovery UX, browser/a11y and full load/failure testing
remain pending. Existing service concurrency tests do not prove concurrent HTTP load.

[Local evidence](../evidence/provisioning-http-summary.md) · [SUB-PRD-04](../sub-prd_04.md).
Local adapter GO is separate from SUB-PRD-04 and MAIN delivery: **NO-GO**.

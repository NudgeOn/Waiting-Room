# Private authentication HTTP slice

Source: `internal/adminauth/authhttp`, `pgstore/http_limit.go` and Admin OpenAPI.
Three POST adapters are implemented: `/api/admin/v1/auth/login`, `/auth/totp/verify`,
`/auth/totp/recover` (the latter two share the same `/api/admin/v1` prefix).
They start **no listener** and must never be mounted on the public Gateway.
The separate opt-in [bootstrap/enrollment adapters](admin-provisioning-http.md) now extend
this boundary; the original `New` constructor still mounts only these three routes.

## Transport and pre-auth request rules

- Direct TLS only; request Host and exactly one Origin must match the pinned HTTPS admin
  origin. Forwarded/X-Forwarded-Proto/For are not authentication or peer trust sources.
  TLS-terminating proxy deployment is not supported by this slice.
- Require `X-WR-Auth: 1`, JSON content type (optional UTF-8 charset), and same-origin Fetch
  Metadata when supplied. The marker is not a credential: it prevents simple browser form
  submission together with exact Origin, no CORS and JSON. No pre-auth session CSRF is implied.
- All Cookie and X-CSRF-Token headers are rejected on these pre-auth/challenge routes.
  Login accepts no Authorization; verify/recover require one canonical Bearer challenge.
  Logout first, including clearing an expired cookie via the existing 401 logout path.
- Reject encoded paths, queries, unsupported methods, content encoding, JSON duplicate or
  unknown/case-variant keys, null/non-string values, trailing JSON and bodies over 4,096 bytes.
  Core input validators additionally bound exact user IDs/password UTF-8 bytes/code formats.
- A body read deadline of five seconds is required via ResponseController; wrappers must
  support it or Unwrap the underlying writer. Unsupported deadlines fail unavailable.
  Reset it after reading so it does not become an HTTP/2 KDF deadline. Slow-upload execution
  is not a dedicated test in this slice; the deadline support/failure guard is unit tested.
- The mounting private server must configure ReadHeaderTimeout, MaxHeaderBytes and
  WriteTimeout. Temporary test servers use 5 seconds / 16 KiB / 30 seconds respectively.
  A production server/bootstrap command, TLS certificates and deployment wiring remain pending.

## Shared limits and expensive work

- Separate auth HTTP source/installation buckets in PostgreSQL: 20/source and 60/installation
  per anchored minute, shared across the three endpoints. Reserve before reading JSON/KDF.
  Pre-auth transport/header rejects happen earlier. 429 includes Retry-After: 60.
- Socket peers normalize IPv4-mapped IPv6 and IPv6 /64; HMAC source keys rotate by DB UTC day.
  No raw IP is stored. All replicas must use the same private fingerprint key.
- Global row first, then source. HTTP/password namespaces prune only their own expired rows
  so cleanup cannot invert the other transaction's lock order. No migration beyond 001–004.
- Existing password account/source/install limits and OTP/recovery shared challenge limits
  still apply. This outer limit is not a replacement or a queue admission rate.
- At most two active auth HTTP operations across Handler instances per Go process, no queue.
  Capacity rejection is 503. KDF has its own two-job limit; an outer 25-second context bounds
  service calls. This does not prove cluster-wide CPU/DoS safety or installation calibration.

## Replies and session cookie

- Password login returns either `totp_required`, `enrollment_required`, or `authenticated`.
  Challenge expiry is returned by the actual INSERT transaction, not computed by the adapter.
  A challenge reply never sets a session cookie or includes a CSRF token.
- Successful OTP, recovery or explicit-OFF password login obtains a committed Grant and
  re-reads the current session before issuing `__Host-wrs; Secure; HttpOnly; SameSite=Strict;
  Path=/` with an eight-hour Max-Age and DB absolute expiry. DB idle/absolute rules remain authoritative.
- JSON authenticated reply contains `state`, `session` (CurrentSession) and `csrfToken`.
  The raw session token is **cookie only**. This intentionally replaces the old M0 skeleton's
  `user: User` shape, whose roles differed; no public release/generated client is claimed.
- Both reply types use no-store/nosniff/CSP. Error replies redact backend details; no cookie
  is issued on failure. Default reply formatting redacts secrets; JSON serialization is the
  intentional sensitive response path and must never be used for request/response logging.
- Existing me/logout remain separate adapters. The TLS test combines them to verify issued
  cookies and CSRF end-to-end; they are not new production listener routes.

## Remaining work and evidence bounds

Production bootstrap/enrollment listeners, QR/Backoffice/browser UI, recovery reissue/reset, complete command
idempotency/audit, policy transitions, key/migration operations, mounting listener hardening,
reverse-proxy trust and load/failure qualification remain pending. Commit followed by a lost
HTTP response is not replayable; unused sessions expire and recovery retry may need another code.
Successful TLS CookieJar tests are not browser SameSite or device evidence. OpenAPI fixtures
and Go wire assertions are tested, but generated types/full schema middleware remain pending.

[Evidence](../evidence/auth-http-summary.md) · [SUB-PRD-04](../sub-prd_04.md).
Scoped local adapter validation does not change SUB-PRD-04 or MAIN delivery: **NO-GO**.

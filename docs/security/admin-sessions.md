# Admin session read, activity and logout slice

Sources: `internal/adminauth/pgstore/sessions.go`, `internal/adminauth/sessionhttp`.
No production listener, public Gateway route, login screen or React UI is added.

## Current behavior

- `SessionService.Me`: reads current user, policy, TOTP enrollment and session from PostgreSQL;
  checks idle 30 minutes/absolute eight hours, enabled user, versions and MFA requirements.
  It changes no application session fields and never extends idle TTL.
- `SessionService.Touch`: internal POST activity operation, requiring exact pinned Origin
  and the session's CSRF token. Updates only lastSeenAt, not creation time/token/absolute expiry.
  It has **no HTTP endpoint or automatic polling integration yet**.
- `SessionService.Logout`: validates the same session/Origin/CSRF and deletes only the current
  session. Other pools observe deletion; concurrent activity never recreates the row.
- Failure/unknown commit returns unavailable, never a successful mutation response.
  SQL-trigger rollback tests cover update/delete failure, not real commit-response loss.

All three operations use short transactions and the same order:
policy (share) → account (share) → optional TOTP credential (share) → session (share for Me,
exclusive for Touch/Logout). No lock upgrade is used. Final TTL checks use DB wall clock
after waits, and Touch rechecks TTL at UPDATE. Five-second context/four-second statement
limits and explicit rollback bound abandoned work. Future credential/user/policy writers
must preserve this order and increment the appropriate session version.

The returned SessionView is **display data, not an authorization proof for future writes**.
Capabilities always come from the current role. Business mutations must check authority
and consume any required reauth inside their own transaction. No blanket RBAC middleware
or action-bound proof consumer is completed here.

## HTTP adapter

Mount only on a future **private Control** listener with the real SessionService backend.
The adapter starts no listener. An ephemeral TLS test server exercises it locally.

| Method | Path | Result |
|---|---|---|
| GET | /api/admin/v1/auth/me | 200 CurrentSession; no Set-Cookie or idle refresh |
| POST | /api/admin/v1/auth/logout | 200 Ack after deletion and cookie clear |

Only one `__Host-wrs` cookie is accepted. Missing/duplicate cookies or mixed Authorization
fail; method/path/encoded-path/query/body guards apply. No CORS is enabled. Responses are
no-store, nosniff, CSP-restricted; problem bodies use fixed safe codes and generated request
IDs, never raw DB errors. Authentication storage failure is `503 AUTH_UNAVAILABLE`.

Logout clears the cookie with Secure, HttpOnly, SameSite=Strict, Path=/, no Domain and an
expired Max-Age. It does not clear a valid cookie on CSRF failure or unavailable DB.
The session expires independently in the DB; cookie flags are not authentication proof.
No TLS bypass/dev cookie behavior was added to this adapter.

Current logout retry after successful deletion returns 401 (no second deletion), and
expired/revoked sessions are refused. **Durable 24-hour command replay results, Idempotency-Key
enforcement, audit events and the full SUB-PRD-04 mutation contract remain incomplete**.
This explicit partial behavior is documented in OpenAPI; it is not a release-ready logout
workflow. Touch retry semantics likewise are not a public command contract.

GET /auth/me now uses a dedicated CurrentSession shape rather than the earlier User skeleton:
userId, core lowercase role ID, MFA/enrollment state, capability version/list and four timestamps.
It contains no username alias, token, CSRF value/hash, password hash or auth-version internals.
The legacy User resource retains its display-role labels; generated clients remain pending.

## Tests and remaining work

[Evidence](../evidence/session-service-summary.md) distinguishes:

- ordinary unit tests for projection/HTTP routing/cookie clear/error redaction;
- real PostgreSQL state, expiry, role/policy changes, CSRF, mutation rollback and two-pool races;
- temporary TLS HTTP → real PostgreSQL with **SQL-seeded session**, not browser login.

Closing the test's connection pool proves the adapter fails closed when its DB backend is
unavailable; it is not a database restart/network outage/failover test.

The [bootstrap service](admin-bootstrap.md) now exists without a setup HTTP/CLI adapter.
The [enrollment service](admin-enrollment.md) now issues an MFA session after OTP commit.
Backup-code login also issues sessions through the [recovery service](admin-recovery.md).
The separate [auth HTTP adapter](admin-auth-http.md) now verifies initial cookie issuance over temporary TLS.
The separate [provisioning adapter](admin-provisioning-http.md) adds bootstrap/enrollment HTTP.
Still pending: setup/enrollment UI, production listener and browser cookie behavior,
browser CSRF delivery/restoration UX, idempotency/audit, deployment forced-on/key files,
full Admin middleware/rate limits, user/role write APIs, browser/a11y and
Backoffice templates. Neither view capabilities nor this local slice certify those features.

Design references: [PostgreSQL row locks](https://www.postgresql.org/docs/17/explicit-locking.html),
[Go HTTP cookie API](https://pkg.go.dev/net/http#Cookie). SUB-PRD-04 and MAIN remain NO-GO.

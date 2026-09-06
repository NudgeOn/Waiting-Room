# Backoffice auth core — 2026-09-05

Implementation is library-only. SUB-PRD-04 and MAIN remain **NO-GO**.
Auth-core unit slice: **GO** after local review; this is not an Admin-service delivery GO.
Final local bundle **PASS**: [report](admin-auth-20260905-core/report.json),
[environment](admin-auth-20260905-core/environment.json).

- Run: 2026-09-05 07:28:13–07:28:36 UTC, Go 1.26.1 / Node 24.12.0, macOS arm64.
- Source SHA-256: `446110287783bb10c847558186bbe44abf1c77fb9f2eaff42f13e7ddcd4b06ac`.
- New auth unit tests: **17 PASS**, with the race detector; fuzz seeds are counted separately.
- TOTP fuzz: **779,984 executions / 10 seconds PASS**, two workers. This is bounded input testing, not a security certification.
- `make check`: existing/new Go unit tests, Node tooling/schema tests, format/vet, nine PRDs and two OpenAPI lints PASS.
- Final guard correctly refused eight NO-GO sub-PRDs. The actual final suite remains NOT RUN.
- Source unchanged during the bundle; all four evidence log hashes validated.
- Reviewer: Codex local code/test review; not independent human approval.

Logs: [foundation](admin-auth-20260905-core/foundation.log),
[auth units](admin-auth-20260905-core/admin-auth.log),
[fuzz](admin-auth-20260905-core/totp-fuzz.log),
[final guard](admin-auth-20260905-core/final-guard.log).
Reproduce with `node scripts/run-adminauth.mjs unique-run-id`.

Implemented: 19-action central role policy, capability projection, ordinary-action
reauth refusal, current-session policy/user version and half-open expiry checks,
exact pinned Origin/session-bound CSRF, six-digit TOTP, ±1 step, and atomic counter-store contract.

Test boundaries:

- RFC 4226 HOTP vectors and RFC 6238 SHA1 vectors, including post-2038 values.
- 64 concurrent calls: exactly one success against a mutex-protected test double.
- Old credential version, older accepted steps, malformed codes, storage errors and unknown writes fail closed.
- Explicit configurable OFF is honored, forced-on OFF and stale policy versions are denied.
- TOTP Secret logging/JSON redacts by default; provisioning export remains sensitive.

No PostgreSQL counter/challenge/session transaction, HTTP auth endpoint, login,
Argon2id/throttle, QR enrollment, recovery codes, ON/OFF transition, one-time reauth
or Backoffice UI was added. No runtime security feature was activated.
The existing waiting-page UI was not changed or browser-tested in this run.
No external service, database, user account, runtime policy, commit or remote push was changed.

[Implementation contract and references](../security/admin-auth-core.md).

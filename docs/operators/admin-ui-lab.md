# Run the local authentication Backoffice

This is a **disposable local lab**, not `wr-control`, `wrctl setup`, an installer or a production
Room dashboard. It creates a fresh random schema in the dedicated auth DB and removes that
schema, its test accounts and installation-token file on normal shutdown. Do not use real
passwords, actual operator accounts or production database credentials.

```sh
make lab-auth-db
make admin-lab
```

Default addresses: Admin `https://127.0.0.1:18443`, Setup `https://127.0.0.1:18444/setup`.
The command prints only a **path** to a mode-0600 bootstrap-token file in a new mode-0700
`.cache/admin-lab-*` directory. Open that exact file locally and paste the token into Setup.
No token goes in the URL or command arguments. Do not paste it in issue trackers or logs.

The certificate is self-signed, short-lived and only valid for 127.0.0.1. The OS trust store
is never modified. Use an isolated local test browser with a temporary certificate exception;
the in-app browser may reject it with ERR_CERT_AUTHORITY_INVALID. Never disable TLS checking
globally or use this certificate/exception for production. A trusted production setup remains pending.

Create a test Admin with a 15-character-or-longer password. Default TOTP is ON: generate a
manual key, use the browser-local QR or manual entry in an authenticator, verify the OTP,
then save the ten recovery codes before continuing. Recovery codes are shown once only.
Lost registration response requires the newly enrolled authenticator; regeneration is not implemented.

To exercise OFF in a new disposable lab:

```sh
npm run build:admin
WR_TEST_AUTH_DB=local go run ./cmd/wr-admin-lab -port 18445 -setup-port 18446 -totp off
```

This flag initializes the lab's private test schema. It is **not** the future wizard ON/OFF
or deployment forced-on policy. Only one loopback region is involved. 10K/100K installation,
profile selection, Room template publish and capacity qualification are not implemented here.

## Session and privacy behavior

- Login/enrollment proofs, passwords, manual key, QR and recovery codes stay in component memory;
  there are no external fonts, analytics, remote QR services or credential-containing URLs.
- The server session is Secure/HttpOnly/SameSite=Strict `__Host-wrs`. Only the **CSRF proof** is
  saved in versioned per-tab sessionStorage so reload→logout works. Logout clears it.
- A new tab may have a session cookie without the matching CSRF proof: it shows read-only
  current-session data and directs logout to the original tab. A stale cookie from a prior
  lab is cleared through the existing unauthorized logout path before new login.
- Local ports share hostname-scoped cookies. Public waiting-ticket/admission cookies and
  other unrelated cookies do not block admin login and are retained. An admin session
  cookie still requires logout before password or challenge authentication.
- Session idle TTL is not silently refreshed. The current screen does not implement Room
  activity/touch, policy switching, reauthentication, recovery reset, audit or command replay.

Ctrl-C stops both listeners, waits for requests, removes only this process's random schema
and bootstrap-token file, and leaves the dedicated DB named volume intact. SIGKILL/crashes
may leave a schema/file requiring exact-target operator cleanup; no broad cleanup command runs.
Then stop the DB if no other auth tests need it:

```sh
docker compose -f deploy/compose/auth-lab.yaml stop
```

## Repeatable validation

```sh
make check
make test-unit PRD=05 # local certificate slice only, not full architecture qualification
PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" npm run test:admin-browser
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-admin-ui-run --admin-ui --auth-http --sessions
```

For direct npm browser runs, set GOCACHE/GOMODCACHE to local cache paths if your sandbox
restricts the system Go cache. Browser tests compile the lab binary, start ON/OFF instances
at 18453–18456, create only fixture accounts, and stop/verify cleanup. Chromium is the
current automated target; production TLS, Firefox/WebKit, physical authenticator and full
screen-reader/a11y verification remain pending. [Evidence](../evidence/admin-ui-summary.md).

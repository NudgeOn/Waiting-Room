# Mixed Web/App journey evidence

## Outcome and release blockers

Fixed a reproduced browser journey failure: two independently constructed Gateways generated different
return AEAD keys, so a return sealed by Gateway 1 produced HTTP 400 at Gateway 2. Previous two-port tests
reused one handler and did not exercise this boundary. The initial reproduction failed at visitor 0 with
`cross-Gateway waiting page rejected 0 400`; that preliminary run is not the final evidence bundle.

The lab now creates independent handlers/transports with one ephemeral installation-scoped Gateway return key.
Same-public-authority browser requests can switch Gateway; wrong key, host/port or ticket remains rejected.
No keys are placed in CLI arguments, logs, database or test artifacts. Key distribution/mount/rotation is not implemented.

**All sub-PRD and MAIN delivery decisions remain NO-GO.** No feature-slice GO label is used.
The reproduced non-affinity failure is resolved in the local runtime. Separate Gateway OS processes,
installation-wide capacity, signed configuration/cold-start behavior and distributed recovery remain release blockers.

## Frozen run

- Run: `mixed-journey-20260906-local`
- UTC: 2026-09-05 23:47:25–23:49:18 (KST 2026-09-06 08:47:25–08:49:18)
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Valkey 8.1.6; Docker 29.1.3; Compose 2.40.3-desktop.1
- Source SHA-256: `14ff544d6425bc983431d03eca2791d171c1aa7d7631649e8802f885f40b82a0`
- Source unchanged during run: true; evidence excluded from digest by shared tooling
- [Report and six check hashes](mixed-journey-20260906-local/report.json), [environment](mixed-journey-20260906-local/environment.json)

## Verification

| Check | Actual result |
|---|---|
| Foundation `make check` | PASS: format/vet/race, tooling, PRDs, OpenAPI/contracts, React build/client units, planning schema/CLI |
| Public lab/admission/waiting unit JSON | PASS: 11 top-level tests; includes new shared-key ownership/wrong-key/host/input test |
| Valkey + HTTP integration runner | PASS: 16 top-level tests, including re-executed lab units, plus 5 model trace subtests |
| New mixed journey | PASS: 90.29 seconds, real HTTP/Valkey and real wall-clock expiry |
| Dedicated Valkey restart | PASS: normal restart retains state and fails closed; not crash/failover/automatic recovery proof |
| CLI Quick20 | PASS: 20 visitors, 3 admitted, 17 queued, retryStable=true, originProtected=true |
| Final guard | Expected NO-GO exit 2 for all eight children; final integration NOT_RUN |

All six runner checks passed. The new mixed journey is part of the integration check, not a seventh runner check.
`make test-unit PRD=03` was also invoked separately and passed for the same three packages.
The guard's expected refusal is not a successful MAIN final test.

## Mixed journey evidence

Ten browser-cookie HTTP clients and ten app clients join sequentially through one loopback public authority.
A test-only reverse proxy explicitly switches requests between two distinct Gateway HTTP servers.
It does not send a client-controlled routing header. One Coordinator and one Valkey primary serve the test.

1. Browser join at Gateway 1 → waiting page at Gateway 2; app join replay at the other Gateway is byte-stable.
2. All 20 ticket sequences preserve mixed-client FIFO. Pre-admission unsafe origin requests are blocked.
3. Promote → first 3 READY and remaining 17 queued.
4. For the first three, the proxy discards the upstream claim response **after** the Valkey ADMITTED commit,
   returning 503 without an admission cookie. Retry on the other Gateway succeeds. A further retry returns
   identical app body/browser Set-Cookie; stored JTI, token expiry and entire ticket are unchanged.
5. Browser/app authorized requests reach their exact path/query. Origin receives exactly 3 requests;
   Waiting Room credentials/spoofed internal/Forwarded headers are removed, customer OAuth/session are preserved.
6. After the real 60-second admission lifetime, promotion still leaves the next visitor queued throughout
   the 30-second reserved verifier leeway. No fake clock, edited store expiry or pre-expired token is used.
7. After the full lease expires, sequences 4–6 become READY and 7–20 remain queued. An explicitly resent old
   admission bearer is rejected. Closing Coordinator causes claim 503 and no additional origin request.

Raw details: [integration log](mixed-journey-20260906-local/integration.log),
[unit JSON](mixed-journey-20260906-local/public-lab-unit.log),
[Quick20](mixed-journey-20260906-local/quick20.log), [restart](mixed-journey-20260906-local/restart.log).

## Remaining scope and cleanup

Browser behavior here means cookie/redirect HTTP contracts, **not 20 rendered Chromium instances**.
No UI changes, browser engine rerun or screenshots were performed this turn. Historical rendered-template
evidence remains separate. This is not separate-OS-process HA, production TLS/secure cookies, persistent
return keys, 10K/100K qualification or complete Gateway configuration/normalization/recovery.

The previously stopped project Valkey was started for testing, normally restarted by the persistence test,
and stopped again afterward. Its named volume and old/new isolated namespaces were preserved; no data was deleted.
The auth PostgreSQL container remained stopped. No commit, push or deployment was performed.

Reproduce: `node scripts/run-m1.mjs mixed-journey-20260906-local` with a **new unique run ID** on subsequent runs;
this runner refuses to overwrite evidence and restarts only the dedicated project Valkey. [Local guide](../operators/local-lab.md).

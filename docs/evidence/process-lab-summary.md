# Separate-process lab evidence

## Outcome and remaining release blockers

`wr-process-lab` now starts Gateway 1, Gateway 2, Coordinator and a sample origin as four distinct OS child
processes. The lab process-separation gap is implemented and tested. This does not complete production process
isolation: same OS user, loopback plain HTTP and development Valkey remain in use. All sub-PRD and MAIN delivery
decisions remain **NO-GO**; no feature-slice GO label is used.

Still blocking release: production role images/users/network controls, durable secret mounts/key rotation,
signed configuration/cold-start rejection, installation-wide caps and distributed fencing/recovery/HA qualification.

## Frozen final run

- Run: `process-lab-20260906-final`, UTC 2026-09-06 00:12:37–00:14:32 (KST 09:12:37–09:14:32)
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Valkey 8.1.6, Docker 29.1.3, Compose 2.40.3-desktop.1
- Source SHA-256: `836bde49861a009c88e326c4ede461921737c87baf877bbb00228dbbd807d553`
- Source unchanged during final run: true; evidence excluded from source digest by shared tooling
- [Final report and artifact hashes](process-lab-20260906-final/report.json), [environment](process-lab-20260906-final/environment.json)

The [preliminary run](process-lab-20260906-local/report.json) is preserved as FAIL because its source changed
during evidence collection when child exit-status assertions were added; all eight commands in that preliminary
run passed. It is not the final source evidence and does not supersede the final run.

## Checks

| Runner check | Result |
|---|---|
| Foundation `make check` | PASS: format/vet/race, tooling, PRDs, OpenAPI/contracts, React build/client units, plan schema/CLI |
| Public lab/admission/waiting unit JSON | PASS: existing 11 top-level tests |
| Process integration | PASS: 4 new IPC/config/summary/launch units + 2 actual process integration tests |
| Process CLI Quick20 | PASS: four child PIDs `[84384,84386,84387,84389]`, 3 admitted/17 queued, retryStable/originProtected true |
| Existing Valkey/HTTP integration | PASS: includes mixed browser-cookie/app 20-client journey and real 60s token + 30s leeway |
| Dedicated Valkey restart | PASS: normal restart fail-closed test; not HA failover/automatic recovery proof |
| Existing single-process CLI Quick20 | PASS: 3 admitted/17 queued |
| Final guard | Expected NO-GO exit 2 for all eight children; MAIN final integration NOT_RUN |

All eight runner checks passed. `make test-unit PRD=05` also passed separately for adminlab and processlab.
The expected final guard rejection is not a passed MAIN final integration test.

## Actual process and failure evidence

The new integration tests build a race-instrumented child executable. They verify:

- Each child PID differs from the test parent and from every peer.
- App join/status/claim across separate Gateway processes reaches the separate origin exactly three times.
- Public Gateway requests cannot access Admin routes or the sample origin's private test counter.
- Actual Coordinator SIGKILL leaves both Gateway processes running; join returns 503, unsafe origin request
  returns 429, and the origin count stays at 3.
- A browser-cookie HTTP journey starts at one Gateway and opens its waiting page through the other under one
  public authority. Gateway 2 is then stopped and replaced with a new PID using the same private configuration.
- The previous ticket and sealed return remain valid after replacement; claim returns the original path/query,
  and the destination response contains the sample origin child's PID. No new browser ticket is allocated.
- Parent control-pipe EOF ends the child. Gracefully stopped/replaced children must exit successfully; a race
  detector exit or unexpected nonzero status fails the test. Only the intentionally killed Coordinator may
  have a signal termination status. All owned children are waited/reaped during cleanup.

Logs: [process tests](process-lab-20260906-final/process-integration.log),
[process Quick20](process-lab-20260906-final/process-quick20.log),
[existing mixed journey](process-lab-20260906-final/integration.log).

## Credential and scope boundaries

The admission private key and replay key are created inside Coordinator and not exported. Gateway receives
only its service credential, admission public key, return key and loopback Coordinator/origin endpoints.
Initial configuration/readiness travel over private stdin/stdout pipes with bounded frames. The normal CLI
summary contains only PID/public Gateway URLs/scope, not service credentials or keys. Child stderr is suppressed
and errors are generic; integration tests separately check child exit status so this does not hide exit failures.

This is a trusted-parent lab protocol, not an authenticated setup endpoint. Same-user processes can access the
unauthenticated development Valkey if compromised; absence of a Valkey configuration field is not a network ACL.
The parent retains Gateway material only in memory; a full supervisor restart does not restore old tickets/keys.
Gateway replacement was deliberately orchestrated by the test, not an implemented HA scheduler.

No production binaries/containers, persistent key files, TLS/mTLS, OS/network isolation, Coordinator restart
recovery, real-browser rendering, 10K/100K or final release qualification was performed. The browser test here
uses real HTTP cookies/redirects, not a rendered browser engine. No UI changes or screenshots were made.

## Cleanup and reproduction

After the final run, a process-name/PID-only inspection found no `wr-process-lab` or processlab test processes.
The project Valkey was returned to its original stopped state; the auth PostgreSQL remained stopped. Existing
volumes and isolated lab namespaces were preserved; no data was deleted. No commit, push or deployment occurred.

Run with `make process-lab-quick`, or reproduce the bundle with a new unique run ID and `--processes`.
The runner performs a normal restart of the dedicated project Valkey. [Process lab guide](../operators/process-lab.md).

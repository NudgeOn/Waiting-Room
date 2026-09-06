# Clock diagnostic local evidence

- Run: `clock-diagnostic-20260906-local`
- UTC: 2026-09-05 23:35:16–23:36:31 (KST 2026-09-06 08:35:16–08:36:31)
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Source SHA-256: `b1cb59b46a15496817dc8be153a3f199ddb4cfa969fdbd6bd6e7563287ed8087`
- Source unchanged during run: true; evidence excluded from source digest by shared tooling
- [Report and artifact hashes](clock-diagnostic-20260906-local/report.json), [environment](clock-diagnostic-20260906-local/environment.json)

## Decision

**Collector/parser/CLI boundary slice GO; Linux live-clock qualification, SUB-PRD-06 and MAIN NO-GO.**

The new `wrctl doctor-clock` command validates the existing plan input and performs a bounded,
read-only Linux chrony query. Unsupported, failed or ambiguous observations remain unverified.
No clock change, software installation, external NTP query, database startup or application activation occurs.
Fixture success is not proof of this host's clock or a production Linux daemon.

## Tests

| Check | Actual result |
|---|---|
| `make check` | PASS: format/vet/race, tooling, 9 PRDs, OpenAPI/contracts, React build + 8 client units, schema/CLI |
| planner/preflight/preview/clock/CLI units | PASS: 34 top-level Test functions (including one subprocess helper) + 5 fuzz seed suites; 545 pass events including nested subtests |
| race repetition | PASS: five packages, count=3 |
| schema/CLI | PASS: existing 4 groups / 73 cases; doctor reuses existing plan schema |
| local CLI boundary smoke | PASS: actual macOS process reports `CLOCK_PLATFORM_UNSUPPORTED`, clock `UNVERIFIED`, exit 3 |
| fuzz | PASS: five targets, 10 seconds / two workers each |
| new tracking parser fuzz | PASS: 1,277,014 executions, no failing input |
| final guard | Expected NO-GO exit 2 for all eight children; MAIN final integration NOT_RUN |

All 11 runner checks passed. The expected final-guard rejection is a successful guard check,
not a successful final integration test. Raw logs and hashes are linked from the report above.

## Behaviors exercised

- Signed System-time parsing, ±2s/±5s policy boundaries and exact nanosecond arithmetic.
- Stratum/unsynchronized/local-reference/leap-event/stale/future/uncertainty rejection paths.
- Duplicate/missing/unknown fields, case mismatch, malformed date, NaN/Inf/exponent/overflow,
  excessive precision, negative unsigned fields, control bytes and oversized stdout.
- Invalid plans rejected before subprocess invocation; plan edits change artifact binding.
- Bounded child-process success, stderr failure, stdout overflow and timeout using an actual
  test executable, not a real chrony daemon. `io.Copy` cannot bypass the output cap.
- Unsupported OS, runner failure/cancellation and invalid collection-time handling.
- CLI 0/3/2/1 semantics, no arbitrary command/host arguments, output errors and input non-reflection.
- Source IDs/peer IP/command output excluded from sanitized reports; activation remains false and
  missing store/origin checks remain blocking even when the clock fixture passes.

## Remaining risk and next gate

Linux `/usr/bin/chronyc` plus a real synchronized/unsynchronized daemon was **NOT_RUN** in this macOS session.
The Linux parser and command adapter are implemented, but must be verified on supported installation hosts,
including distro formatting, daemon access and OS clock steps. The five-minute freshness and five-second
uncertainty limits are conservative initial collector policy, not fleet qualification or independent UTC proof.
The host executable/daemon are trusted; peer authenticity and host compromise are outside this diagnostic.

No UI changes, browser rerun, database/auth integration, production bootstrap, report persistence, multi-node
collection, runtime activation or installation/capacity qualification occurred. No commit, push or deployment.

Command: `node scripts/run-installplan.mjs clock-diagnostic-20260906-local --clock-local`.
Usage, source reference and exit semantics: [clock diagnostic guide](../operators/clock-diagnostic.md).

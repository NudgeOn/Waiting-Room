# Planning report local evidence

- Run: `planning-report-20260905-local`, 2026-09-05 14:55:11–14:56:20 UTC
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Source SHA-256: `beac3e555ff40fe7d672644ae28e1f9600fc33c1ad0788a40e4f3a0754c42c81`
- Source unchanged during run: true; evidence excluded from source digest by shared tooling
- [Report and artifact hashes](planning-report-20260905-local/report.json), [environment](planning-report-20260905-local/environment.json)

## Summary and environment

**Offline planning export slice GO; SUB-PRD-06 and MAIN delivery NO-GO.**

The preview now downloads a plan-only or plan-and-cost JSON file. `wrctl report` provides the same
input-only recomputation contract over stdin. Reports explicitly retain installation/qualification
`NOT_RUN` and activation `false`; a checksum is not a signature or installation authorization.

URL: `http://127.0.0.1:18770/install-preview`; Chromium desktop 1440×1050 and mobile 360×900.
Browser plugin/skill not available, so regular project Playwright was used per frontend-testing-debugging.
The React guidance influenced explicit button-triggered requests, validated input snapshots, and a busy guard
instead of effect-triggered exports. Browser tests verified actual downloaded bytes and independent checksums.

## Tests

| Check | Actual result |
|---|---|
| `make check` | PASS: format/vet/race, tooling, PRDs, OpenAPI/contracts, React build + 8 client units, schema/CLI |
| planner/preflight/preview/CLI units | PASS: 28 top-level Test functions + 4 fuzz seed suites; 490 pass events including nested subtests |
| HTTP boundary subset | PASS: one top-level suite with 39 endpoint subtests, plus static/forbidden route assertions |
| race repetition | PASS: four packages, count=3 |
| schema/CLI | PASS: 4 test groups, 73 cases |
| actual Chromium flow | PASS: desktop/mobile/error-retry × two repetitions = 6 tests, no retries |
| decoder/clock/cost/report fuzz | PASS: all four targets, 10 seconds each, two workers |
| final guard | Expected NO-GO exit 2 for all eight children; MAIN final integration NOT_RUN |

All ten runner checks passed. Raw logs and their hashes are linked from the report above.
The expected final-guard rejection is a successful guard check, not a successful final integration test.

## Rendered checks and interaction loop

| UI check | Result |
|---|---|
| Page identity | Exact URL and expected title verified |
| Nonblank | Profile/security headings, labels and controls visible |
| Framework overlay | None observed |
| Browser console and runtime errors | No unexpected app errors; deliberately aborted requests separately tested |
| Screenshot review | Desktop cost/download state and 360px mobile form viewed; no observed overlap or horizontal overflow |
| Interaction | Actual Go plan/estimate/report responses and actual downloaded JSON verified |

Flow: load → TOTP OFF → Standard plan → plan-only download (`cost: null`) → edit → High profile/forced ON →
synthetic cost estimate (76.00/888.00 USD) → combined download → parse actual file/check SHA-256 →
repeat download with identical bytes → edit price removes stale cost/export button → re-estimate with
zero Standard/volume prices → new download with different checksum and 0.00 subtotal → refresh resets state.
Plan and report request failures show recoverable errors; retry succeeds after the intentional abort is removed.
No external browser requests or localStorage/sessionStorage writes were observed. User-triggered downloaded
files can persist on disk; the server remains stateless. Prices are synthetic fixtures, not provider quotes.

## Screenshots

Outside-repository output directory:
`/Users/youngjoo/.codex/visualizations/2026/09/05/01a06f66-9f03-7fc2-8b6d-8d03d8e41ffe/report-export`

| File | SHA-256 |
|---|---|
| wizard-cost.png | `1a8667270dad7b08a07d9871b6608c6af076ad6983cb7ad6932d590882b0f0c4` |
| wizard-mobile.png | `71595dd6e8a46fb792a0ee6d7ad8f0a45467691b4fcd98262a627062006ae2ef` |

These local screenshots are not part of the source bundle and may not exist on another machine.
No passwords, keys or tokens were captured.

## Remaining risk

No production bootstrap authorization, DB persistence, policy application, environment probes, manifest
installation, capacity qualification, signed configuration backup/import or final integration was run.
Checksums use documented compact Go JSON encoding, not RFC 8785 or a signature. Unknown fields and computed
client claims are rejected, but ordinary accepted input values remain in the downloaded file; do not enter secrets.
Loopback-only plain HTTP preview must not be published. Chromium QA does not establish Safari/Firefox,
screen-reader or physical-device compatibility. Auth DB/browser regressions were not rerun in this slice.
No commit, push or deployment was performed.

Run locally with `make preview`; CLI and payload details: [planning report guide](../operators/planning-report.md).

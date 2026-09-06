# Installation preview local evidence

- Run: `installation-preview-20260905-local`, 2026-09-05 14:35:16–14:36:11 UTC
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Source SHA-256: `9206393021a4a49f6330ce3a6f21daf33aacaae579cbf15c99e1b370907687e2`
- Source unchanged during run: true; evidence excluded from source digest by shared tooling
- [Report and artifact hashes](installation-preview-20260905-local/report.json), [environment](installation-preview-20260905-local/environment.json)

## Summary and environment

**Local preview UI/Go HTTP slice GO; SUB-PRD-06 and MAIN delivery NO-GO.**

URL: `http://127.0.0.1:18770/install-preview`; Chromium desktop 1440×1050 and mobile 360×900.
Browser plugin/skill not available, so regular project Playwright was used per frontend-testing-debugging.
React guidance influenced lazy loading of the preview module and explicit submit handlers instead of effect-triggered requests.
The server is a built, stateless local Go preview, not a Vite dev server or production bootstrap listener.

## Tests

| Check | Actual result |
|---|---|
| `make check` | PASS: format/vet/race, tooling, PRDs, OpenAPI/contracts, React build + 7 client units, schema/CLI |
| planner/preflight/preview/CLI units | PASS: 24 top-level Test functions + 3 fuzz seed suites; 408 pass events including nested subtests |
| HTTP boundary subset | PASS: one top-level suite with 26 endpoint subtests, plus static/forbidden route assertions |
| race repetition | PASS: four packages, count=3 |
| schema/CLI | PASS: 3 suites, 65 cases |
| actual Chromium flow | PASS: desktop/mobile/error-retry × two repetitions = 6 tests, no retries |
| decoder/clock/cost fuzz | PASS: all three existing targets |
| final guard | Expected NO-GO exit 2 for all eight children; MAIN final integration NOT_RUN |

Raw logs: [foundation](installation-preview-20260905-local/foundation.log),
[unit JSON](installation-preview-20260905-local/install-plan-unit.log),
[repeat](installation-preview-20260905-local/install-plan-repeat.log),
[schema/CLI](installation-preview-20260905-local/schema-cli-contract.log),
[browser](installation-preview-20260905-local/preview-browser.log),
[input fuzz](installation-preview-20260905-local/input-fuzz.log),
[clock fuzz](installation-preview-20260905-local/clock-fuzz.log),
[cost fuzz](installation-preview-20260905-local/cost-fuzz.log),
[guard](installation-preview-20260905-local/final-guard.log).

## Rendered checks and interaction loop

| UI check | Result |
|---|---|
| Page identity | Expected preview route opened and exact title verified |
| Nonblank | Profile/security heading, labels and controls visible |
| Framework overlay | None |
| Browser console and runtime errors | None during successful flows; intentional request abort separately tested |
| Screenshot review | Desktop, 360px mobile and cost result viewed; mobile margins centered after review; no observed overlap or horizontal overflow |
| Interaction | Actual Go plan/estimate endpoint results, not mocked successful responses |

Flow: load → keyboard Tab focus → TOTP OFF → Standard plan shows OFF → edit → 10,001 visitor states
blocked under Standard without HTTP submission → High selection/100,000 → forced ON locks OFF → Go plan
shows high cap/forced policy → user price input → 76.00/888.00 USD synthetic fixture subtotals and 11.6842 ratio →
price edit removes old result → Standard/volume zero → explicit unavailable ratio → refresh clears input.
Separately, an aborted plan request shows a recoverable error; removing the abort allows successful retry.
No unsupported policy/region options, external browser requests, localStorage or sessionStorage writes were observed.

## Screenshots

Outside-repository output directory:
`/Users/youngjoo/.codex/visualizations/2026/09/05/01a06f66-9f03-7fc2-8b6d-8d03d8e41ffe`

| File | SHA-256 |
|---|---|
| wizard-desktop.png | `9cc3e2e0900dc6c4cb5f3578ef424f9830b1ed29a2e186d7472c6fdada040e81` |
| wizard-mobile.png | `5a66b48ba62ad2fcb30f8fc6871ab21e739ca9e4aa3a1026ff1a7575cd48efb7` |
| wizard-cost.png | `49ace9714aa421ed1be494a9fc70527134c7f2aa2ff54b67ce8f23e240ac796b` |

These local screenshot files are not part of the source bundle and may not exist on another machine.
Cost screenshot inputs are explicitly synthetic, not provider prices. No passwords/keys/tokens were captured.

## Remaining risk

Preview HTTP is loopback-only plain HTTP and accepts no credentials; it must not be published or reused as
production setup. No one-time installation authorization, DB persistence, account creation, policy application,
actual environment probes, manifest installation or capacity qualification occurs. Rate/body/deadline bounds are
implementation safeguards, not a load/slow-client certification. Browser QA covers Chromium only, not Safari/Firefox,
screen-reader WCAG certification or actual mobile devices. Existing auth DB/browser flows were not rerun in this slice;
their historical evidence remains separate. No commit, push or deployment was performed.

Run locally with `make preview`; full command and security boundary: [preview guide](../operators/installation-preview.md).

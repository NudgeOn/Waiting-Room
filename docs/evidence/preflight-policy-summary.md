# Preflight policy local evidence

- Run: `preflight-policy-20260905-local`, 2026-09-05 13:17:04–13:17:44 UTC
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Source SHA-256: `6018a3cf1636fb28e690ab45d0022716c8b7194c837e53b003161cb364ba4bce`
- Source unchanged during run: true. Evidence excluded from source digest by shared tooling.
- [Report and artifact hashes](preflight-policy-20260905-local/report.json), [environment](preflight-policy-20260905-local/environment.json)

## Results

| Check | Result |
|---|---|
| `make check` | PASS: format/vet/Go race, tooling, PRD, OpenAPI, contracts, React build/UI units, install schema/CLI |
| planner + preflight + CLI race units | PASS: 17 top-level Test functions and 2 fuzz seed suites across three packages; 260 pass events including nested subtests |
| preflight subset | PASS: 9 top-level Test functions + FuzzClockFailClosed seeds; 100 pass events including nested subtests |
| three repetitions | PASS: all three packages with race detector, count=3 |
| schema/CLI contract | PASS: both profile suites, 42 cases |
| clock fuzz | PASS: requested 10 seconds, 458,226 executions, no failing input |
| existing JSON decoder fuzz regression | PASS: requested 10 seconds, 2,312 executions plus cached seed corpus; no failing input |
| final guard | Expected NO-GO exit 2 with all eight sub-PRDs blocked; MAIN final integration NOT_RUN |

Logs: [foundation](preflight-policy-20260905-local/foundation.log),
[unit JSON](preflight-policy-20260905-local/install-plan-unit.log),
[repeat](preflight-policy-20260905-local/install-plan-repeat.log),
[schema/CLI](preflight-policy-20260905-local/schema-cli-contract.log),
[clock fuzz](preflight-policy-20260905-local/clock-fuzz.log),
[decoder fuzz](preflight-policy-20260905-local/input-fuzz.log),
[final guard](preflight-policy-20260905-local/final-guard.log).

During exploratory testing the parallel determinism test initially reused a plan variable later mutated
by another assertion in the parent test. The fixture now uses a separate changed-plan copy. This was a
test-isolation defect, not a waived failing policy check; the frozen race/repeat run above passed.

## Verified boundary and decision

**Pure preflight policy slice GO; SUB-PRD-06 and MAIN delivery remain NO-GO.**

- Clock ±2 seconds inclusive PASS; >2 to 5 inclusive WARN; beyond ±5 FAIL, including extreme int64 durations without absolute-value overflow.
- Missing/stale/future/mismatched-plan or invalid-source evidence cannot produce a passing clock/probe check.
- Both profiles require store authentication/role/read-write; High additionally requires TLS/connection switch. Known failures take priority over unknown evidence.
- Origin requires a current active-probe PASS; missing/unsupported probes remain UNVERIFIED.
- High operator HA acknowledgement is WARN, never HA verification or profile qualification.
- Event deadline snapshots: minimum available replicas at T−10m, all desired pods Ready and p95 RTT strictly below 2ms at T−5m. Late/stale/wrong-event samples cannot replace deadline evidence.
- Plan digest includes profile, region, TOTP and limits. Output uses fixed codes/sanitized references and independent result copies; 50 parallel determinism subtests passed.
- Even a report without blocking checks always has activationAllowed=false and qualification=NOT_RUN.

No actual clock, storage, Kubernetes or origin probe was performed. Collector trust/credentials,
sampling quality, HA record validation, scheduler persistence/revision control, live activation recheck,
wizard/CLI preflight UI and production activation remain unimplemented. No DB/browser rerun,
deployment, commit, push or 10K/100K qualification occurred in this slice.

Policy assumptions and integration requirements: [preflight contract](../operators/preflight-policy.md).

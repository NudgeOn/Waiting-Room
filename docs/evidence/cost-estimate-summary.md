# Cost estimate local evidence

- Run: `cost-estimate-20260905-local`, 2026-09-05 13:30:11–13:31:00 UTC
- Reviewer: Codex local self-review; independent release review not performed
- Commit: none, uncommitted snapshot; Go 1.26.1 / Node 24.12.0 / macOS arm64
- Source SHA-256: `6b0a818a591ad76d475082b11aa679af0a094178df3820a3e99c69c9e05ae27d`
- Source unchanged during run: true. Evidence files excluded from source digest by shared tooling.
- [Report + log hashes](cost-estimate-20260905-local/report.json), [environment](cost-estimate-20260905-local/environment.json)

## Results

| Check | Actual result |
|---|---|
| `make check` | PASS: format/vet/race, tooling/PRD, OpenAPI/contracts, React build/UI units, install/cost schema and CLI |
| planner/preflight/CLI units | PASS: 23 top-level Test functions + 3 fuzz seed suites; 381 pass events including nested subtests |
| race repeat | PASS: all three packages, count=3 |
| JSON schema/CLI agreement | PASS: 3 suites, 42 profile cases + 23 cost cases |
| cost arithmetic fuzz | PASS: requested 10 seconds, 715,492 executions; independent rational oracle compared exact subtotal and rounded results |
| decoder + clock fuzz regression | PASS: both existing fuzz targets |
| final guard | Expected NO-GO exit 2 with all eight sub-PRDs blocked; MAIN final integration NOT_RUN |

Logs: [foundation](cost-estimate-20260905-local/foundation.log),
[unit JSON events](cost-estimate-20260905-local/install-plan-unit.log),
[repeat](cost-estimate-20260905-local/install-plan-repeat.log),
[schema/CLI](cost-estimate-20260905-local/schema-cli-contract.log),
[cost fuzz](cost-estimate-20260905-local/cost-fuzz.log),
[decoder fuzz](cost-estimate-20260905-local/input-fuzz.log),
[clock fuzz](cost-estimate-20260905-local/clock-fuzz.log),
[final guard](cost-estimate-20260905-local/final-guard.log).

## Verified scope and decision

**Offline reference subtotal calculator + CLI slice GO; SUB-PRD-06 and MAIN delivery NO-GO.**

- `wrctl estimate` reads bounded, exact-key stdin JSON and emits both profiles, original price assumptions, provider/region/currency/as-of, quantities, formulas, rounding and exclusions.
- Standard uses one 4 vCPU/8 GiB host and 50 GiB storage. High uses three 8 vCPU/16 GiB workers and explicitly priced per-worker volumes. Pod resource requests are not billed twice.
- Explicit zero prices are accepted; missing/negative/non-decimal/overprecision prices are rejected. Zero Standard subtotal yields null ratio, never infinity or a fabricated multiplier.
- Exact big-integer micro-unit arithmetic, subtotal-after-summing half-up rounding, large inputs beyond int64 multiplied range, and repeatability/independent result copies passed.
- Shared strict JSON decoder refactoring passed existing plan/preflight/CLI regression tests. The plan's cost-status guidance now points to `estimate`; canonical plan digests consequently change and old evidence must not be silently reused.
- Synthetic example prices produce Standard 76.00 and High 888.00, ratio 11.6842 for included subtotals only. These are test values, not provider quotes.

No real price lookup, exchange rate, database, browser, infrastructure probe, deployment, commit or push occurred.
External HA stores, LB/CDN, egress and other listed charges remain excluded rather than assumed free.
Provider pricing validity, production wizard/report persistence, install/apply, actual preflight and 10K/100K
qualification remain unfinished. Formula/input bounds and remaining integration work are in the
[cost guide](../operators/cost-estimate.md).

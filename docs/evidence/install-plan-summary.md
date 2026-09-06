# Install plan local evidence

- Run: `install-plan-20260905-local`, 2026-09-05 12:59:03–12:59:30 UTC
- Reviewer: Codex (local self-review; independent release review not performed)
- Commit: none, uncommitted snapshot; Go 1.26.1, Node 24.12.0, macOS arm64
- Source SHA-256: `075627de964370bc6c584e83aad7ecdc8f9720cb66c02ffcc1aaf8b127656637`
- Source unchanged during run: true. Evidence files excluded from source digest by the shared runner.
- [Report + log hashes](install-plan-20260905-local/report.json), [environment](install-plan-20260905-local/environment.json)

## Results

| Check | Actual result |
|---|---|
| `make check` | PASS: Go format/vet/race, tooling, PRD, OpenAPI, contracts, React build/UI units and install input schema/CLI |
| planner + CLI race units | PASS: 7 top-level Test functions + FuzzDecode seed suite; 159 pass events including parent/subtests, not 159 independent top-level tests |
| race repetition | PASS: both packages, count=3 |
| schema/CLI agreement | PASS: 2 profile suites, 21 cases per profile (42 cases) |
| decoder fuzz | PASS: requested 10 seconds, 514,300 executions, no crash or failing input |
| final guard | PASS as a guard: expected exit 2 with all eight sub-PRDs NO-GO; MAIN final integration NOT_RUN |

Raw logs: [foundation](install-plan-20260905-local/foundation.log),
[unit JSON events](install-plan-20260905-local/install-plan-unit.log),
[repeat](install-plan-20260905-local/install-plan-repeat.log),
[schema/CLI](install-plan-20260905-local/schema-cli-contract.log),
[fuzz](install-plan-20260905-local/input-fuzz.log),
[guard](install-plan-20260905-local/final-guard.log).

## Verified scope and decision

**Offline planning slice GO; SUB-PRD-06 and MAIN delivery NO-GO.**

- Standard 10K / High Scale 100K input caps, lease/rate/TTL bounds, FIFO-only and single region slug validated.
- Configurable TOTP ON/OFF accepted; forced-on OFF and omitted enabled flag rejected.
- 100 parallel determininism subtests verify equivalent reordered JSON input yields byte-identical plans and independent owned slices; CLI bytes repeated separately.
- Unknown/nested secret fields, duplicate and escaped-duplicate keys, wrong-case keys, null, arrays, trailing documents and oversized input rejected without reflecting values/errors.
- Both profiles emit reference BOM ownership and outstanding checks, never executable/activation/HA/performance approval.
- CLI input comes only from stdin; no credentials accepted. Valid non-secret fields are retained verbatim. This does not certify production secret storage/redaction or prevent an operator from placing secrets in shell arguments or legitimate fields.
- No DB, browser, cloud or network dependency checks were run in this slice. Prior auth/browser evidence remains separate.

Remaining: complete environment/secret-reference/config schemas, actual wizard UI, preflight/clock/pre-scale checks,
price inputs/calculator, runtime aggregate activation guard, production manifests/image identity, approved apply,
drain/migration tooling, 10K/100K qualification and all dependency delivery gates.

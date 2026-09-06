# Waiting Room

Apache-2.0 self-hosted waiting room for websites and apps.

**Status: local Web + App skeleton with Calm, plus a persistent local-Docker Control preview (authentication, TOTP, Room drafts and audit). Drafts do not activate queues. Beta, production services and 10K/100K qualification remain NO-GO.**

Start with [the main PRD](docs/main-prd.md). Each sub-PRD owns its implementation checklist,
unit-test evidence and delivery decision. Final integration/release decisions belong to MAIN.

## Local development

For persistent Docker Control setup and restart-safe data, see the
[local Docker guide](docs/local-docker.md). It is separate from the disposable labs.

Requirements: Go 1.26.1 and Node.js 22.12+ (Node 24.12.0 used locally).

```sh
npm ci --ignore-scripts
make check
make test-unit PRD=01
make test-unit PRD=02
make check-docs
node scripts/run-m0.mjs my-unique-run-id
```

The Go reference model is separate from the new single-room Valkey Functions store.
App JSON and browser cookie/redirect handlers run in the loopback lab; the separate Admin Lab now connects first-admin setup, local QR enrollment, recovery login and session/logout UI. Room operations and production handlers remain incomplete.
`make test-final` fails until all sub-PRD gates have valid delivery evidence and a final
runner exists. A successful unit test never qualifies the product for production.

Local results: [M0 evidence](docs/evidence/m0-summary.md). The evidence command writes
raw logs and hashes under `docs/evidence/`; it refuses to overwrite a prior run.

## Project map

Try the non-secret installation planner UI: `make preview`, then open
`http://127.0.0.1:18770/install-preview`. Profile/TOTP plan and cost subtotal comparison
use the Go validators; no install or database writes occur. [Preview guide](docs/operators/installation-preview.md).
The preview can download a non-secret JSON planning report; CLI equivalent: `wrctl report`.
See the [report format and checksum boundary](docs/operators/planning-report.md).

Read-only Linux clock checks are available with `wrctl doctor-clock` (chrony only;
unsupported/unavailable hosts stay unverified). See the [diagnostic guide](docs/operators/clock-diagnostic.md).

Run the app Quick20 lab: `make lab-valkey` then `make lab-quick`. Browser: `make lab`, open `/shop` on port 18080. See the
[local lab guide](docs/operators/local-lab.md) and [M1 evidence](docs/evidence/m1-summary.md).
For separate Gateway/Coordinator/origin OS processes, use `make process-lab-quick` or `make process-lab`;
see the [process lab boundary](docs/operators/process-lab.md).
The built-in `calm` template is selectable with `-template calm`; see
[template extension instructions](docs/design/calm.md) and [browser evidence](docs/evidence/browser-template-summary.md).

- `internal/policy`: supported FIFO policy and eligibility ordering
- `internal/queue/model`: executable state, rate, reservation and recovery oracle
- `internal/waiting`: trusted template registry, semantic page and shared browser behavior
- `internal/adminauth`: RBAC/CSRF, Argon2id, PostgreSQL auth/session and atomic Room draft/audit APIs; runtime publication and advanced security lifecycle remain incomplete; [local auth DB test](docs/operators/admin-auth-lab.md)
- `internal/localcontrol`, `internal/adminserver`, `cmd/wr-control`: explicit persistent local-Docker initialization and private Admin runtime; [local Docker guide](docs/local-docker.md)
- `apps/admin`, `internal/adminlab`, `cmd/wr-admin-lab`: React authentication UI and disposable loopback TLS runtime; [start guide](docs/operators/admin-ui-lab.md)
- `internal/installplan`, `cmd/wrctl`: offline 10K/100K profile/TOTP validation and deterministic proposal (no setup/apply); [planning guide](docs/operators/install-plan.md)
- `wrctl estimate`: user-priced compute/volume subtotal comparison with exact decimal arithmetic; [cost guide](docs/operators/cost-estimate.md)
- `api/openapi`: public and administrator contracts
- `scripts`: PRD/dependency/gate and evidence validation
- `test`: contracts, fixtures and future distributed tests
- `docs`: PRDs, ADRs, security model, benchmark manifests and local evidence

The Go module is temporarily `waiting-room` until the public hosting namespace is chosen.
No external telemetry, remote repository or publication is configured.

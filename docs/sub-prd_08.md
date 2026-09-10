---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-08
title: Open Source Repository, Milestones and Release Artifacts
version: 0.1.0
spec_status: ready_for_m0
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [SUB-PRD-01, SUB-PRD-03, SUB-PRD-04, SUB-PRD-05, SUB-PRD-06, SUB-PRD-07]
blocks: [MAIN-PRD-FINAL-TEST]
milestones: [M0, M1, M2, M3, M4, M5, M6]
last_updated: "2026-09-10"
---

# SUB-PRD-08 — OSS·Repository·Milestone·Release

> 읽기 순서: [main-prd.md](main-prd.md) → 필요한 계약 sub-PRD → SUB-PRD-07 → 이 문서. Repository governance, CI cadence와 artifact packaging의 source of truth다. 무엇을 검증하는지는 SUB-PRD-07, 최종 release GO는 MAIN이 소유한다.

## 1. 목표

Apache-2.0 community가 재현 가능하게 build·test·release하고, Standard/High Scale이 같은 production image digest에서 검증됐음을 artifact와 evidence로 확인할 수 있게 한다.

## 2. Open-source 정책

- repository source license 목표는 Apache-2.0이다.
- `LICENSE`, `NOTICE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`와 DCO 절차를 둔다.
- release마다 SPDX 또는 CycloneDX SBOM, third-party notice와 dependency license result를 생성한다.
- license가 불명확한 source를 복사하지 않는다.
- GPL/AGPL source를 Apache production binary에 복사·링크하지 않는다. 별도 process인 test tool도 license·redistribution 조건을 inventory와 review에 기록한다.
- outbound telemetry는 기본 OFF이며 운영 metric은 설치 환경에 남는다.
- security report channel과 supported-version policy를 `SECURITY.md`에 공개한다.

## 3. Repository layout

아래는 목표 layout이다. M0는 `internal/policy`, `internal/queue/model`, API·docs·scripts·contract fixtures만 생성했으며 실행 서버·배포 폴더는 후속 단계다.

```text
/
├── cmd/
│   ├── wr-gateway/
│   ├── wr-control/
│   ├── wr-coordinator/
│   ├── wrctl/
│   └── wr-lab/
├── internal/
│   ├── queue/ gateway/ token/ control/
│   ├── auth/ storage/ observability/
│   └── testkit/
├── api/openapi/
│   ├── public-v1.yaml
│   └── admin-v1.yaml
├── web/admin/
├── db/migrations/
├── deploy/
│   ├── compose/
│   └── helm/waiting-room/
├── test/
│   ├── model/ contract/ e2e/ load/ chaos/ fixtures/
├── docs/
│   ├── main-prd.md
│   ├── sub-prd_01.md ... sub-prd_08.md
│   ├── adr/ operators/ security/ benchmarks/
│   └── evidence/
├── scripts/
└── support-matrix.yaml
```

- one Go module, production multi-role image와 별도 lab image를 만든다.
- React build는 `wr-control`에 embed하고 production frontend server를 별도 운영하지 않는다.
- `pkg/`를 만들지 않는다. public developer contract는 OpenAPI, CLI와 config schema다.
- `docs/`와 `*.md`는 `.gitignore`에 넣지 않는다. CI는 `git check-ignore docs/main-prd.md`가 ignored로 판정되면 실패한다.
- PRD 변경은 checklist/evidence를 포함하며 예전 대형 계획 파일과 이중으로 관리하지 않는다.

## 4. Release artifact inventory

- amd64/arm64 production image manifest
- 별도 lab image
- Standard production/lab Compose bundle
- High Scale Helm chart와 values schema
- `wrctl` binaries
- Public/Admin OpenAPI와 generated sample clients
- database migration과 support matrix
- checksum, SBOM, provenance, signature
- `LICENSE`, `NOTICE`, third-party notices
- docs/operator/security/backup/recovery/upgrade runbook
- qualification evidence manifest와 raw artifact locator

production installer가 lab image를 선택할 수 없어야 한다. 모든 artifact는 source commit과 manifest digest로 서로 연결한다.

## 5. Support matrix와 compatibility

M0에서 다음을 정확히 고정한다.

- Go/Node build version
- Linux architecture와 Docker/Compose version
- PostgreSQL·Valkey supported/minimum/tested version
- Kubernetes·Helm API/version
- browser support
- upgrade 가능한 이전 schema/config version
- external Valkey topology와 PostgreSQL capability

OpenAPI/config schema는 v1 GA 때 freeze한다. additive compatible change, deprecation 기간과 migration/rollback policy를 ADR에 둔다.

## 6. Milestone gate

### M0 — Contract skeleton

- Apache governance files·DCO
- ADR, Public/Admin OpenAPI skeleton
- queue reference model·failure matrix·threat model
- `support-matrix.yaml`, benchmark/evidence schema
- PRD front matter·requirement ownership·dependency validator

### M1 — FIFO walking skeleton

- static Room, two Gateways, Coordinator, Valkey, deterministic origin
- model/Valkey randomized trace 일치
- idempotent join/status/claim과 Quick 20
- race·restart persistence test

### M2 — Safe Gateway alpha

- reverse proxy, browser/app flow, route matcher
- token/return, OFF/AUTO/HOLD/DRAINING/RECOVERY_HOLD
- fail-closed cold start와 last-known-good config
- service auth, config/key rotation contract

### M3 — Operable beta

- PostgreSQL migration, Admin API/UI와 setup/Room Wizard
- RBAC/TOTP/session/CSRF/reauth/audit
- event/runtime revision과 safe theme
- 360px·browser·WCAG flow

### M4 — Standard 10K RC

- production/lab Compose
- `wrctl doctor/dry-run/apply/backup/restore/upgrade`
- Quick 20, Smoke 1K, Standard qualification 3회
- 10K visitor·20K idempotency cap

### M5 — High Scale 100K RC

- Helm/HPA/PDB/topology/NetworkPolicy와 external store preflight
- N-1, failover, zone loss, reconnect
- High Scale qualification 3회와 2시간 soak
- 100K visitor·200K idempotency cap

### M6 — v1.0 candidate

- API/config freeze, supply-chain artifacts와 external clean install
- Standard/High Scale same production image digest
- MAIN final test 진입. 이 문서는 release GO를 단독으로 선언하지 않는다.

## 7. CI cadence

### Pull request

- Go format/vet/static/unit/targeted race
- React lint/typecheck/unit
- OpenAPI lint와 generated diff
- migration clean apply
- queue property test, path/token fuzz smoke
- Admin session/CSRF와 internal service-auth contract
- Compose Smoke 1K와 Quick 20
- PRD schema/link/requirement ownership/gate truth-table
- secret/vulnerability/dependency license scan
- M5부터 Helm lint/template/schema

PR gate 목표 시간은 15분이며 장기 suite는 Nightly/RC로 이동한다.

### Nightly

- full race와 time-box fuzz
- multi-Gateway/Coordinator randomized trace
- Valkey/Control/Coordinator/origin kill·restart·partition
- primary uncertainty safe window와 clock skew
- browser/mobile/accessibility matrix
- Standard 10K 15분 regression
- Compose clean install·backup·restore

### Weekly/RC

- Kubernetes Helm install·N-1 upgrade·rollback
- High Scale distributed qualification
- Valkey failover·RECOVERY_HOLD runbook
- full join→poll→claim→proxy→expiry workload
- 2-hour soak, amd64/arm64 clean install
- same RC digest qualification 3회

CI는 SUB-PRD-07의 test/threshold를 호출하고 여기서 재정의하지 않는다.

## 8. Evidence와 publish policy

- `verified` 상태는 immutable run ID와 evidence path가 있을 때만 사용한다.
- required test의 failed/skipped가 하나라도 있으면 해당 gate는 NO-GO다.
- image/config/chart/support-matrix digest가 바뀌면 관련 qualification은 stale이다.
- local Quick/Smoke 결과로 10K/100K badge를 만들 수 없다.
- G5/100K가 실패하면 v1.0 GA를 지연한다.
- 필요하면 v0.x Helm preview만 배포하고 `High Scale 100K` 이름·badge는 사용하지 않는다.
- commit, build, local install, registry push와 release publish는 각각 별도 evidence field다.

## 9. Acceptance criteria

- [x] docs가 Git에 추적 가능하고 `.gitignore`에 의해 제외되지 않음 (아직 commit 전)
- [ ] production/lab image가 content와 installer 선택에서 분리됨
- [ ] 모든 release artifact가 같은 commit/manifest digest에 연결됨
- [ ] dependency license·SBOM·provenance·signature gate가 자동화됨
- [ ] M0~M6 entry/exit가 dependency 순서와 모순되지 않음
- [ ] G5 failure가 release·badge publish를 강제로 막음
- [ ] final GO는 MAIN 외 문서에서 발행되지 않음

## 10. 구현 Checklist

- [x] 로컬 Git repository와 M0 directory scaffold
- [x] Apache-2.0 governance·security·DCO files
- [ ] 공개 전 비공개 security/conduct 연락처 확정
- [ ] dependency license policy/allowlist
- [ ] production/lab build separation
- [x] support matrix 후보와 exact-version/schema validator
- [ ] 실제 compatibility validator
- [x] M0 PR workflow 파일 (원격 실행 미확인)
- [ ] Nightly/Weekly/RC workflows
- [x] PRD metadata/link/requirement/dependency gate validator
- [ ] artifact manifest·checksum·SBOM·provenance·signature
- [ ] release gate evaluator와 badge policy
- [ ] clean install/upgrade/rollback automation
- [ ] external reproduction runbook

## 11. Unit test Checklist와 결과

진입점: `make test-unit PRD=08`. 현재 PRD-07과 공통 tooling 10개를 실행한다 (M0 당시 9개).

| UT-ID | Repository/release test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-08-01 | support matrix schema | invalid range/capability 거부 | PASS | exact version/topology/status 검사; [M0](evidence/m0-summary.md) |
| UT-08-02 | license classifier | disallowed/unknown dependency fail | NOT RUN | — |
| UT-08-03 | artifact manifest completeness | missing artifact/digest fail | NOT RUN | — |
| UT-08-04 | digest consistency | mixed commit/image/config fail | NOT RUN | — |
| UT-08-05 | gate dependency | failed child가 상위 GO 차단 | PASS | synthetic failed-child 검사; [M0](evidence/m0-summary.md) |
| UT-08-06 | 100K fallback | G5 fail이면 v1/badge publish 불가 | PARTIAL | 최종 진입 guard PASS, publish pipeline 미구현; [M0](evidence/m0-summary.md) |
| UT-08-07 | lab isolation | production profile에서 lab 선택 불가 | NOT RUN | — |
| UT-08-08 | docs tracking | main/sub PRD ignored이면 fail | PARTIAL | 현재 9개 PRD not-ignored 검사 PASS, negative fixture 미구현; [M0](evidence/m0-summary.md) |

### Unit test 실행 로그

| Run | Commit | Command | Passed/Failed | Result | Evidence |
|---|---|---|---:|---|---|
| M0-20260905 | uncommitted snapshot | `make check` → `npm test`; `make check-docs` | tooling 공통 9/0 | PASS | [M0](evidence/m0-summary.md); commit/CI/push 없음 |
| M1-local | uncommitted snapshot | `make check`; `node scripts/run-m1.mjs RUN_ID` | 부분 suite 결과는 evidence 참조 | PARTIAL | [M1](evidence/m1-summary.md); 원격 CI 미실행 |

2026-09-10 로컬 M1 종료 조건은 모델 trace·혼합 Web/App Quick20·race·재시작
보존의 누적 실제 증거로 완료했다. [MAIN의 현재 단계표](main-prd.md)와
[최신 후보별 실행 결과](evidence/beta-20260910-logo-maintenance.md)를 따른다.
위 M1-local PARTIAL 행은 초기 실행의 역사적 결과다.

## 12. GO/NO-GO 판정

- 명세/M0 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 이유: M0와 로컬 M1, Linux 후보 빌드·원격 CI는 검증했다. Beta 가용성 수용과 배포용 supply-chain·release artifact·10K/100K qualification을 포함한 이 sub-PRD 전체 delivery는 남아 있다.
- GO 조건: checklist·unit/CI/supply-chain test PASS, SUB-PRD-01~07 GO, P0/P1·Critical 0건, reviewer·UTC 시각 기록.
- 이 문서의 GO 뒤에도 MAIN final test가 PASS하기 전 release는 NO-GO다.

## 13. 참고 구현 사례

- [AWS Virtual Waiting Room on AWS](https://github.com/aws-solutions/virtual-waiting-room-on-aws)
- [Vercel Labs Next.js Waiting Room](https://github.com/vercel-labs/nextjs-waiting-room)

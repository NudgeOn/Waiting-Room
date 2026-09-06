---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-06
title: Installation Wizard, Scale Profiles and Region
version: 0.1.0
spec_status: ready_for_implementation
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [SUB-PRD-01, SUB-PRD-02, SUB-PRD-04, SUB-PRD-05]
blocks: [SUB-PRD-07, SUB-PRD-08]
milestones: [M0, M3, M4, M5]
last_updated: "2026-09-06"
---

# SUB-PRD-06 — Installation Wizard·10K/100K·Region

> 읽기 순서: [main-prd.md](main-prd.md) → SUB-PRD-01·02·04·05 → 이 문서. 배포 책임, reference BOM, profile guard와 region 확장의 source of truth다. Workload 수치는 SUB-PRD-07이 소유한다.

## 1. 목표

사용자가 비용·신뢰성 차이를 이해하고 Standard 10K 또는 High Scale 100K를 선택한다. `wrctl setup`은 사용자가 준비한 환경을 검사하고 deterministic plan을 보여준 뒤 애플리케이션만 설치한다.

## 2. Profile 의미

| Profile | Hard cap | 배포 | 운영 의미 |
|---|---:|---|---|
| Standard 10K | visitor state 10,000 | Docker Compose, single host | 낮은 비용, HA 보장 없음 |
| High Scale 100K | visitor state 100,000 | Helm/Kubernetes | multi-Gateway, pre-scale, external HA stores |

- 두 profile은 같은 production image·public API·config schema를 사용한다.
- cap은 한 hot Room과 설치 전체의 `WAITING + READY + token expiry 및 최대 30초 leeway 종료 전 ADMITTED` 합계다.
- public idempotency budget은 Standard 20,000, High Scale 200,000이다.
- `maxActiveAdmissionLeases`는 profile cap 이하로 제한한다.
- admission rate 상한은 Standard 6,000/min, High Scale 60,000/min이다.
- 숫자는 TCP connection, origin concurrency나 실제 online user 보장이 아니다.

## 3. Setup Wizard

`wrctl setup`은 loopback one-time UI를 열고 remote server에서는 SSH tunnel을 안내한다. terminal에 표시된 hashed/single-use bootstrap token이 필요하며 완료 뒤 endpoint와 token을 영구 비활성화한다.

1. Profile: Standard 10K / High Scale 100K, BOM·상대 비용·HA 차이
2. Environment: `regionId`, public/admin host, TLS termination, DNS·port·storage·clock
3. Dependency: Docker/cluster capability, topology, PostgreSQL/Valkey endpoint
4. Security: first Admin·TOTP policy·secret recipient/reference
5. Plan: redacted deterministic dry-run, 생성 resource와 외부 책임 확인
6. Apply: 명시적 승인, 단계별 진행·실패 지점 재시도
7. Verify: liveness/readiness, signed config, origin protection 상태, Quick 20 진입

host clock offset은 2초 이내를 권장하고 5초를 넘으면 production activation을 차단한다. 검사 방법과 관측 source를 install report에 남긴다.

## 4. 설치 책임 matrix

| 항목 | Standard | High Scale |
|---|---|---|
| `wrctl` 설치 | app+PostgreSQL+Valkey Compose | application Helm resources only |
| 사용자가 사전 제공 | Linux, Docker, DNS/TLS, origin ACL/mTLS | Kubernetes, Ingress/LB, DNS/TLS, external PostgreSQL/Valkey, origin ACL/mTLS |
| 제품이 만들지 않음 | VM, Docker engine, DNS, certificate, firewall | cluster, cloud VM, HA store, DNS, cert-manager |
| 제품 검사 | port, volume, endpoint, TLS location, clock | cluster API/capability, topology labels, endpoint TLS/auth/role/read-write/RTT/connection switch |

외부 endpoint 검사만으로 replica·zone·backup·RPO/RTO를 증명하지 않는다. 이는 운영자 입력·확인과 qualification evidence로 분리하고 UI에는 `High Scale 필수 연결 검사 통과`라고만 표시한다.

origin ACL/mTLS active probe가 불가능하면 `unverified`로 남기며 bypass 방지가 확인됐다고 표현하지 않는다.

## 5. Standard 10K reference BOM

### SUT

- Linux 1 host: 4 vCPU, 8 GiB RAM, 50 GiB SSD, 1 Gbps
- Gateway: 1 CPU / 1 GiB
- Control+UI: 0.25 CPU / 768 MiB
- Coordinator: 0.5 CPU / 512 MiB
- Valkey: 0.75 CPU / 2 GiB, maxmemory 1 GiB, 10 GiB SSD
- PostgreSQL: 0.5 CPU / 1.5 GiB, 20 GiB SSD

CPU 값은 Compose planning share이고 dedicated core가 아니다. 남는 CPU·memory는 OS, container runtime와 spike headroom이다.

production Compose SUT에는 lab origin·logical users·k6·long-term Prometheus storage를 포함하지 않는다. Qualification용 generator와 origin은 SUB-PRD-07처럼 분리한다. SSD model/storage class, IOPS와 측정 fsync p95를 manifest에 기록한다.

Standard는 AOF everysec+RDB를 사용하지만 host loss·disk failure의 HA나 zero data loss를 약속하지 않는다.

## 6. High Scale 100K reference BOM

### Application cluster

- single region, 3 failure domains
- worker 3개, 각각 8 vCPU / 16 GiB
- Gateway: min 4, event pre-scale 8, max 12
- Gateway pod: request 1 CPU / 768 MiB, limit 2 CPU / 1.5 GiB
- Control: 2 replicas, 각각 500m CPU / 512 MiB
- Coordinator: min 4, max 8
- Coordinator pod: request 1 CPU / 1 GiB, limit 2 CPU / 2 GiB
- Gateway·Coordinator HPA: CPU 60%, aggressive scale-up, downscale stabilization 10분
- PDB: Gateway minAvailable 3, Control 1, Coordinator 2
- topology spread와 anti-affinity를 failure domain에 적용

HPA는 scheduling·image pull·Ready 완료 시간을 보장하지 않는다. 예약 event 10분 전 Gateway 8·Coordinator 4를 확보하고 5분 전 모든 pod Ready와 store RTT를 확인하지 못하면 activation을 차단한다.

### External stores

- Valkey reference: primary 1+replica 2, 각각 4 vCPU/8 GiB, 50 GiB SSD
- self-managed Sentinel: 3 failure domain의 Sentinel 3개, quorum 2
- PostgreSQL reference: primary+standby, 각각 2 vCPU/8 GiB, 100 GiB SSD
- Coordinator↔Valkey p95 RTT < 2ms

위 구성은 qualification reference BOM이다. 임의 managed endpoint의 HA 보장이 아니며 provider RPO/RTO, backup, failover와 zone 배치를 운영자가 기록한다. bundled single-node stores는 lab-only이고 High Scale badge에 사용할 수 없다.

## 7. Room 활성화와 profile 이동

- Room Wizard는 예상 peak, 전체 visitor state, lease와 rate를 profile cap에 대조한다.
- Standard에서 설치 전체 10K를 넘는 event는 활성화하지 않고 High Scale migration을 안내한다.
- production activation은 profile preflight, signed config, origin protection 상태와 Quick 20 결과를 보여준다.
- Standard→High Scale은 v1 zero-downtime migration을 약속하지 않는다.
- queue safe drain → config/secret export → target import/doctor → empty-state verification → DNS/LB cutover 순서를 사용한다.
- live ticket를 profile·region 사이에서 이동하지 않는다.

## 8. 비용 표시

- cloud pricing API를 호출하지 않는다.
- CPU, memory, storage, replica, load balancer와 예상 egress 항목을 보여준다.
- Standard 대비 상대 비용과 사용자가 입력한 provider/region/currency/as-of 단가로 월 비용을 계산한다.
- app compute·volume은 reference BOM 기본 포함이다.
- CDN/LB, egress, external HA stores, backup object storage, Prometheus retention와 운영 인력은 기본 제외 항목으로 따로 표시한다.
- calculation input·formula·rounding과 포함/제외 항목을 install report에 저장한다.

## 9. Region 계약

- v1은 하나의 `regionId`와 single Home Region Coordinator만 허용한다.
- 해외 사용자는 CDN edge에서 static asset을 받을 수 있지만 join/status/claim은 Home Region으로 간다.
- ticket ID·token은 특정 edge Gateway instance에 종속되지 않는다.
- multi-region 구현 전 Wizard에는 disabled 선택지도 노출하지 않는다.
- 후속 첫 단계는 near-user Gateway + single Home Region Coordinator로 global FIFO를 유지한다.
- active-active queue, global consensus와 Home Region DR은 post-v1 별도 ADR과 qualification이 필요하다.

## 10. Acceptance criteria

- [ ] 동일 image digest가 Standard·High Scale manifest에 사용됨
- [ ] Wizard dry-run이 같은 입력에 byte-stable plan을 생성함
- [ ] secret이 dry-run, process args와 log에 노출되지 않음
- [ ] profile cap을 넘는 Room/event 활성화가 차단됨
- [ ] High Scale missing dependency를 HA 증명과 구분해 fail/warn함
- [ ] scheduled event pre-scale readiness 실패 시 activation 차단
- [ ] v1 multi-region config와 live-ticket migration이 거부됨
- [ ] 비용 결과가 포함/제외와 단가 기준일을 항상 표시함

## 11. 구현 Checklist

- [x] 오프라인 proposal 전용 입력 schema·10K/100K/TTL/FIFO/TOTP cross-field 검증
- [x] `wrctl plan` stdin 전용 byte-stable JSON proposal·오류 값 비반사·명시적 미검증 상태
- [x] pure preflight policy: plan-bound 관측·clock/store/origin severity·High event deadline snapshot 판정 (collector/activation 연결 아님)
- [x] `wrctl estimate`: 사용자 단가·CPU/memory bundle+volume 소계·정확한 소수 계산·비용 배율·산식/제외 provenance (wizard/install report 저장 후속)
- [x] `wrctl preview`: 3단계 React profile/TOTP·계획·비용 UI와 stateless loopback Go HTTP 연결 (production bootstrap 아님)
- [x] `wrctl report`/preview JSON 다운로드: 입력 재검증·비용 재계산·동일 리전·provenance/checksum (실제 설치 결과/서명 아님)
- [x] `wrctl doctor-clock`: 고정 Linux chrony 읽기·정제 관측·기존 판정 연결·실패/미지원 미검증 (fixture/subprocess·macOS 경계 검증; Linux 실측/활성화 후속)
- [x] prebuilt local Preview CLI: embedded Compose·GHCR digest/compatibility 검증·`install/up/upgrade/setup`·private state 보존·실패 journal (fake Docker 및 Compose config 검증; 공개 artifact/실제 release smoke는 별도 증거)
- [ ] profile/config JSON schema
- [ ] loopback bootstrap server와 one-time token
- [ ] deterministic redacted dry-run/apply plan
- [ ] Standard production/lab Compose
- [ ] High Scale Helm values/schema
- [ ] HPA/PDB/topology/NetworkPolicy templates
- [ ] external store connection preflight와 operator HA record
- [ ] NTP/clock skew diagnostic
- [ ] pre-scale Ready activation gate
- [ ] visitor/idempotency/profile guard UI 연결
- [ ] BOM·cost calculator
- [ ] Standard→High drain/export/import runbook
- [ ] unsupported multi-region capability hiding

## 12. Unit test Checklist와 결과

부분 진입점: `make test-unit PRD=06` — pure planner·CLI unit과 JSON schema/실행 파일 계약을 검사한다.
실제 설치/위자드/활성화 검증은 포함하지 않는다. [사용법](operators/install-plan.md),
[검증 기록](evidence/install-plan-summary.md). 전체 profile/config schema와 apply checklist는 미완료를 유지한다.

| UT-ID | Unit/schema test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-06-01 | profile cross-field validation | cap/rate/lease 위반 거부 | PASS (offline input) | TestValidationBoundaries + schema/CLI parity; [기록](evidence/install-plan-summary.md) |
| UT-06-02 | dry-run determinism | 같은 input 같은 redacted plan | PARTIAL | offline proposal 100회 동시 반복 PASS, manifest/apply 미구현; [기록](evidence/install-plan-summary.md) |
| UT-06-03 | secret redaction | plan/log/args secret 0 | PARTIAL | unknown secret 필드/오류 비반사 PASS, 실제 secret reference·apply 경계 후속; [기록](evidence/install-plan-summary.md) |
| UT-06-04 | preflight severity | pass/warn/fail 책임 경계 일치 | PARTIAL | pure policy fixture PASS, 실제 probes/HA record 후속; [기록](evidence/preflight-policy-summary.md) |
| UT-06-05 | clock diagnostic | >5s activation block | PARTIAL | pure ±2/±5초 판정 + chrony collector fixture·bounded subprocess·macOS 미지원 경계 PASS; Linux 실측/활성화 후속; [policy](evidence/preflight-policy-summary.md), [collector](evidence/clock-diagnostic-summary.md) |
| UT-06-06 | event pre-scale gate | NotReady/RTT 실패 시 block | PARTIAL | T−10/T−5 deadline·NotReady·RTT 판정 PASS, scheduler latch/실제 gate 후속; [기록](evidence/preflight-policy-summary.md) |
| UT-06-07 | cost calculator | formula·rounding·exclusion 정확 | PASS (offline subtotal) | 정확한 산술·반올림·0/누락·상한·schema/CLI; [기록](evidence/cost-estimate-summary.md) |
| UT-06-08 | unsupported region | multi-region config 거부/숨김 | PARTIAL | offline 다중 region 거부 + preview UI 선택지 미노출 PASS, production wizard 후속; [기록](evidence/installation-preview-summary.md) |
| UT-06-09 | migration precondition | non-empty source queue 거부 | NOT RUN | — |

### Unit test 실행 로그

#### 영속 로컬 Docker Control 추가 Checklist — 2026-09-06

- [x] private state/key 파일 생성·재사용·권한/심볼릭 링크 거부·redaction unit 실행.
- [x] scratch image·PostgreSQL 전용 볼륨·runtime 제한 DB role로 초기화.
- [x] 명시적 최초 TOTP ON 선택, 반복 init 보존 및 기존 정책 변경 거부 시험.
- [x] 미게시 setup loopback과 로컬 stdio 터널·실제 브라우저 최초 관리자 시험.
- [x] DB/Control 동시 재시작 후 계정/TOTP/세션/초안/audit 보존 시험.
- [ ] runtime queue까지 포함한 complete Compose 설치와 preflight/백업복원.
- [ ] 10K/100K qualification 및 완성된 installer/wizard.

`go test -race ./internal/localcontrol` 3 PASS, 격리 Docker 7 checks PASS.
[설치 안내](local-docker.md), [Docker 시험](evidence/waiting-room-local-beta-test-9ee8c0c0.json).
Control 개발판 검증으로 UT-06 전체 또는 M4 Standard 10K GO를 대체하지 않는다.

| Run | Commit | Command | Passed/Failed | Result | Evidence |
|---|---|---|---:|---|---|
| Install-plan-local | uncommitted snapshot | `make test-unit PRD=06`; `node scripts/run-installplan.mjs install-plan-20260905-local` | 최종 집계는 evidence 참조 | PARTIAL | [실행 로그·hash](evidence/install-plan-summary.md) |
| Preflight-policy-local | uncommitted snapshot | `make test-unit PRD=06`; `node scripts/run-installplan.mjs preflight-policy-20260905-local` | 최종 집계는 evidence 참조 | PARTIAL | [실행 로그·hash](evidence/preflight-policy-summary.md) |
| Cost-estimate-local | uncommitted snapshot | `make test-unit PRD=06`; `node scripts/run-installplan.mjs cost-estimate-20260905-local` | 최종 집계는 evidence 참조 | PARTIAL | [실행 로그·hash](evidence/cost-estimate-summary.md) |
| Installation-preview-local | uncommitted snapshot | `make test-unit PRD=06`; `npm run test:preview-browser`; runner `--preview-ui` | 최종 집계는 evidence 참조 | PARTIAL | [HTTP unit·UI·browser·hash](evidence/installation-preview-summary.md) |
| Planning-report-local | uncommitted snapshot | `make test-unit PRD=06`; runner `planning-report-20260905-local --preview-ui` | 최종 집계는 evidence 참조 | PARTIAL | [보고서 unit·download/browser·hash](evidence/planning-report-summary.md) |
| Clock-diagnostic-local | uncommitted snapshot | runner `clock-diagnostic-20260906-local --clock-local` | 최종 집계는 evidence 참조 | PARTIAL | [collector unit·subprocess·local boundary·hash](evidence/clock-diagnostic-summary.md) |

판정 기반의 관측 freshness·deadline·한계는 [preflight policy 계약](operators/preflight-policy.md)을 따른다.
실제 관측은 별도 [clock collector](operators/clock-diagnostic.md)의 지원 범위에서만 표시한다.
macOS의 미지원 결과나 fixture 성공은 Linux 호스트 시계 측정 성공이 아니다.

## 13. GO/NO-GO 판정

- 명세의 구현 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 이유: 오프라인 proposal/preflight/비용 UI와 영속 로컬 Docker Control 초기화·재시작을 부분 검증했다. 전체 queue Compose/Helm/profile runtime·실제 preflight·install report 저장·production bootstrap 위자드와 나머지 UT-06은 미완료다. 전체 delivery는 NO-GO를 유지한다.
- GO 조건: checklist, unit/schema/install preflight tests PASS, 의존 sub-PRD GO, P0/P1 0건, reviewer·UTC 시각 기록.

## 14. 참고

- [Kubernetes Horizontal Pod Autoscaling](https://kubernetes.io/docs/concepts/workloads/autoscaling/horizontal-pod-autoscale/)
- [Valkey Helm chart](https://github.com/valkey-io/valkey-helm)

---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-07
title: Local Test Lab and Profile Qualification
version: 0.1.0
spec_status: ready_for_implementation
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [SUB-PRD-02, SUB-PRD-03, SUB-PRD-04, SUB-PRD-05, SUB-PRD-06]
blocks: [SUB-PRD-08, MAIN-PRD-FINAL-TEST]
milestones: [M0, M1, M2, M3, M4, M5]
last_updated: "2026-09-08"
---

# SUB-PRD-07 — Local Test Lab·Qualification

> 읽기 순서: [main-prd.md](main-prd.md) → 대상 계약 sub-PRD → 이 문서. 이 문서는 test 내용·workload·evidence 형식의 source of truth다. 실행 cadence와 CI는 SUB-PRD-08, 전체 출시 판정은 MAIN이 소유한다.

## 1. 목표

개발자는 laptop에서 기능을 빠르게 확인하고, release 후보는 분리된 reference 환경에서 10K/100K 보장을 재현한다. local 성공, profile qualification과 production 증거를 서로 혼동하지 않는다.

## 2. 검증 계층

| 계층 | 목적 | 환경 | Badge evidence |
|---|---|---|---|
| Quick 20 | 눈으로 이해하는 browser/app flow | local lab | 불가 |
| Smoke 1K | protocol·기본 race regression | local/CI Compose | 불가 |
| Local visitor tiers 1K/2K/5K/10K | 직접 Store의 합계·중복·FIFO 회귀 | 32 workers·4 Rooms·전용 local Valkey | 불가 |
| Local HTTP tiers 1K/2K/5K/10K | app HTTP join/status·교차 retry·origin 보호 | 32 workers·4 child PID·전용 local Valkey | 불가 |
| Standard 10K | 단일-host reference qualification | 분리 SUT/generator/origin | Standard만 가능 |
| High Scale 100K | distributed throughput·failure | reference Kubernetes+external HA | High Scale만 가능 |
| RC soak/recovery | 장시간·failover·upgrade | release environment | MAIN input |

## 3. Local Traffic Lab

목표 진입점:

```bash
docker compose --profile lab up
```

위 profile 명령의 전체 수동 lab 구성은 미구현이다. 로컬 Docker 관리자 화면 `/traffic-lab`과 Room 검증 탭에서는 **Quick 20·Smoke 1K를 실제 실행**하고 저장된 결과를 확인한다. [Traffic Lab 사용법과 범위](operators/traffic-lab.md).
CLI의 `make lab-valkey`·`make lab-quick` 앱 전용 Quick20도 유지한다. [로컬 가이드](operators/local-lab.md).

`WR_TEST_VALKEY=127.0.0.1:16379 make test-local-tiers`는 1K/2K/5K/10K Store 직접 호출 시험이다.
[실행 범위와 안전 한도](operators/local-visitor-tiers.md). HTTP Smoke1K나 qualification을 대체하지 않는다.
`make test-http-tiers`는 같은 인원 단계의 별도 PID app HTTP 경로를 추가 검증한다.
3명 입장/나머지 대기·10K cap·Coordinator 종료를 확인하며 지속 부하 qualification은 아니다.

Lab 구성:

- production과 같은 Gateway·Control·Coordinator code path
- local PostgreSQL·Valkey
- deterministic sample storefront origin
- logical browser/app clients와 Traffic Lab UI
- ephemeral Prometheus metrics
- sample data와 failure toggle

기본 수동 시나리오:

1. `/shop` 보호, `/assets`·`/health` 제외
2. active admission lease 3, admissions 6/min
3. 20 visitor 생성
4. FIFO, refresh/reconnect, multi-tab return, gradual claim 확인
5. OFF/AUTO/HOLD, event와 safe DRAINING 확인
6. 같은 state를 app JSON sample client로 확인
7. Valkey·Control failure에서 기존 token pass와 신규 safe hold 확인

Quick 20은 human-readable timeline에 sequence, state, expected/actual outcome을 보여준다. Traffic Lab은 production origin을 대상으로 실행할 수 없다.

### 관리자 실행 범위 — 2026-09-08

Quick 20은 순차 10 browser-cookie/10 app 방문자, Smoke 1K는 8 workers의 app 방문자와 중복 join/claim을 검사한다. 모두 실제 HTTP·Valkey runtime-v4를 사용하고 3명 입장/나머지 대기, FIFO, 재시도, 원본 보호, 샘플 Coordinator 종료를 판정한다. 결과의 타임라인은 실제 sequence 기준 첫 20명이다. OFF/이벤트/Valkey failover 전체 수동 시나리오는 이 두 버튼의 통과 범위에 포함하지 않는다.

Control은 PostgreSQL에 작업을 저장하고 기존 mTLS로 Coordinator에 고정 preset을 전달한다. Coordinator의 별도 `wr_traffic` ACL은 `wr:lab:traffic{*`에만 접근하며 운영 키 조회가 거부됨을 확인한 뒤 시작한다. 샘플 Gateway 2개·Coordinator·origin은 같은 Coordinator 프로세스의 임시 loopback HTTP 서버다. CPU/메모리·Valkey 서버는 설치와 공유하므로 성능·자원 격리 증거가 아니다. 최대 90초, 동시 1건, 종료 후 전용 키 삭제/비정상 종료 시 10분 TTL, 중단 작업 자동 재실행 없음.

## 4. Deterministic fixture와 time

- origin은 fixed latency, error rate, health transition과 request log를 seed로 재현한다.
- browser/app fixture는 같은 scenario ID와 expected invariant를 공유한다.
- fake clock은 pure model/unit test에서만 사용한다.
- Valkey integration과 qualification은 실제 Valkey server time을 사용한다.
- random property/load test는 seed를 evidence에 기록한다.
- expected fault response(의도한 429/503)와 unexpected error를 별도 counter로 계산한다.

## 5. 자동 test suite

- Go unit, race, fuzz, property
- pure Go queue model ↔ Valkey Function randomized trace
- Public/Admin OpenAPI contract
- browser E2E, 360px/mobile viewport, accessibility
- k6 protocol load와 logical visitor state machine
- Compose/Helm clean install·upgrade·rollback
- Gateway/Coordinator/Control/Valkey/origin fault injection
- security tests: CSRF, service auth, header/path/return fuzz, SSRF, config/key rotation

계획 command contract이며 현재 파일/target은 존재하지 않는다.

```text
make test-unit
make test-contract
make test-e2e
make load-smoke
wrctl benchmark --profile standard-10k
wrctl benchmark --profile high-scale-100k --distributed
make test-chaos
make verify-evidence
```

## 6. 공통 invariant oracle

- FIFO inversion 0, sequence/ticket/READY/admission duplicate 0
- visitor state·active lease·rolling 60-second rate exceed 0
- Gateway 경유 non-admitted origin reach 0
- GET status state mutation 0
- lost claim response retry의 다른 admission 결과 0
- primary uncertainty safe window 신규 admission 0
- cold start trust config 없이 liveness 외 통과 0
- visitor/idempotency churn의 eviction/OOM 0
- secret·token·raw target/IP의 log/metric leak 0

## 7. Standard 10K qualification

### Environment

- SUT: SUB-PRD-06의 4 vCPU/8 GiB/50 GiB production Compose host
- generator: 별도 4 vCPU/8 GiB
- deterministic origin: 별도 2 vCPU/4 GiB
- SUT budget에서 Traffic Lab, k6와 long-term Prometheus storage 제외

### Workload

1. Population: HOLD에서 10,000 ticket을 200 joins/s로 50초 유입하고 status 500 req/s를 15분 유지, heartbeat 5분 주기
2. 별도 빈 epoch의 steady: 신규 join/claim 각 30/s + status 500/s + admitted proxy 500/s를 15분 유지
3. 별도 빈 epoch의 peak: 신규 join/claim 각 100/s를 60초 유지
4. steady/peak 설정: admission TTL 60초, verifier leeway 30초, idempotency TTL 기본 600초, lease cap 9,500. steady idempotency 18K, lease 2.7K 예상
5. 별도 abandonment에서 READY 10% 미claim과 expiry 회수 검증; 기본 admission TTL 15분의 정상 saturation·새 join 거부도 별도 검증
6. phase reset은 격리된 시험 배포 또는 Gateway ACK/fencing이 입증된 epoch에서만 수행

### Threshold

- ticket/status/claim p95 ≤ 200ms, p99 ≤ 500ms
- direct deterministic origin 대비 proxy 추가 latency p95 ≤ 20ms
- 정상 구간 unexpected error ≤ 0.1%
- 각 SUT role·generator의 1분 평균 CPU p95 < 70%
- generator dropped iteration = 0
- Valkey used_memory/maxmemory < 60%
- OOM·eviction·unexpected restart = 0
- 공통 invariant 위반 = 0

같은 production image/config digest로 3회 연속 PASS해야 Standard 10K evidence가 된다.

## 8. High Scale 100K qualification

### Environment

- SUT: SUB-PRD-06 High Scale application cluster와 external store reference BOM
- generator: 3대 × 4 vCPU/8 GiB, execution segment 고정
- deterministic origin: 별도 4 vCPU/8 GiB
- Coordinator↔Valkey p95 RTT < 2ms
- Gateway 8, Coordinator 4 Ready 상태에서 시작

### Workload

1. Population: HOLD에서 100,000 ticket을 2,000 joins/s로 50초 유입
2. `nextPollAt`을 지키며 status 평균 5,000 req/s, 5분 peak 5,500 req/s; heartbeat 5분 주기
3. Population의 100,000 ticket reconnect를 30초 안에 주입하고 조기 poll을 429로 제한
4. 별도 빈 epoch의 steady: 신규 join/claim 각 300/s + status 5,000/s + admitted proxy 5,000/s 동시 유지
5. 별도 빈 epoch의 peak: 신규 join/claim 각 1,000/s를 60초 유지
6. steady/peak 설정: admission TTL 60초, verifier leeway 30초, idempotency TTL 기본 600초, lease cap 95,000. steady idempotency 180K, lease 27K 예상
7. 별도 abandonment에서 READY 10% 미claim 처리와 expiry 회수 검증; 기본 admission TTL 15분의 안전한 saturation 별도 검증
8. steady 중 Coordinator 1개 종료 상태로 5분 유지; 30분 qualification을 같은 manifest digest로 3회 반복
9. 같은 steady 설정에서 2시간 soak 1회. 모든 phase reset은 Standard와 같은 격리/ACK 조건 적용

목표 manifest는 [profiles.yaml](benchmarks/profiles.yaml)이다. 최대 저장 인원, 지속 유량, 60초 peak는 별도 지표이며 현재 모두 **미측정 목표**다. 1,000 신규 join/s를 기본 idempotency TTL로 계속 유지하면 600K record가 필요하므로 200K profile에 대한 지속 성능으로 주장하지 않는다. 시험 전 산술 검증은 `make check-docs`, 실제 qualification은 M4/M5에서 수행한다.

### Threshold

- Standard와 같은 API latency·unexpected error·invariant 기준
- direct origin 대비 proxy 추가 latency p95 ≤ 20ms
- warm-up 이후 각 process RSS 증가 ≤ 10%, 지속 단조 증가 없음
- load 종료 5분 뒤 goroutine·open connection·FD가 steady baseline ±5% 이내
- 각 SUT role·각 generator의 1분 평균 CPU p95 < 70%
- dropped iteration = 0, generator network/socket saturation 없음
- Valkey used_memory/maxmemory < 60%
- OOM·eviction·unexpected restart = 0

3회 qualification과 2시간 soak가 모두 PASS해야 High Scale 100K evidence가 된다.

## 9. Fault matrix

| Fault | 허용 동작 | 금지 동작 |
|---|---|---|
| Gateway N-1 | 다른 Gateway 처리 | sticky dependency·duplicate state |
| Coordinator N-1 | latency threshold 내 계속 처리 | capacity/rate/FIFO 위반 |
| Control down 30m | valid LKG 사용 | unsigned/default config 적용 |
| config 없는 cold start | liveness 외 503 | route 추측·origin bypass |
| Valkey primary uncertainty | old admission pass, RECOVERY_HOLD | unsafeUntil 전 new admission |
| origin unhealthy | HOLD/추천·안내 | automatic unsafe bypass |
| internal auth failure | 401/503 fail-closed | credential 없이 queue write |
| single zone loss | old admission pass, safe stop 가능 | invariant 위반·무조건 무중단 주장 |
| host clock >5s | activation block | token/TOTP test 진행 |

Zone loss와 failover 뒤 recovery는 runbook에 따라 명시적으로 수행하며 queue absolute zero-loss를 합격 조건으로 두지 않는다.

## 10. Evidence manifest

각 run은 다음을 immutable artifact로 남긴다.

- run ID, start/end UTC, scenario seed와 fault timeline
- source commit, production/lab image manifest digest
- config/Compose/Helm/chart digest
- SUT/generator/origin BOM, topology와 region
- OS/kernel, PostgreSQL/Valkey/Kubernetes/Docker/k6 version
- storage class/IOPS/fsync p95, network RTT와 clock offset
- requested/actual rate, threshold result와 invariant counter
- generator CPU/network/dropped iteration
- raw summary·metric·log artifact path와 reviewer signature

image/config/chart digest나 required dependency version이 바뀌면 이전 qualification evidence는 새 release에 재사용하지 않는다.

## 11. Acceptance criteria

- [x] Quick 20이 browser/app expected state를 사람이 확인 가능하게 표시 (고정 샘플 HTTP 시나리오)
- [ ] Smoke 1K가 PR 시간 예산 안에 correctness regression 탐지
- [ ] model/Valkey oracle이 같은 seed·trace에서 결과 일치
- [ ] Standard/High workload와 threshold evaluator가 deterministic
- [ ] fault별 expected error와 invariant violation을 구분
- [ ] evidence digest가 변경되거나 누락되면 PASS 판정 거부
- [ ] local run이 profile badge로 잘못 표시되지 않음

## 12. 구현 Checklist

- [ ] deterministic origin·browser/app fixtures
- [x] M1 deterministic origin·앱 Quick20 및 교차 Gateway fixture
- [x] 1K/2K/5K/10K local Store 방문 상태·4 Room 합계·32 workers·메모리 안전 검사
- [x] 1K/2K/5K/10K 4-PID app HTTP·replay·3명 claim/origin·10K 신규 cap·Coordinator 종료
- [x] Quick 20 Traffic Lab과 Smoke 1K: 관리자/Room 검증 탭, 실 HTTP, 결과 저장·중지·JSON 다운로드
- [x] 단일 스레드 reference-model invariant oracle
- [ ] Valkey implementation과 같은 trace 비교
- [ ] race/fuzz/property suite
- [x] Public/Admin OpenAPI schema contract suite
- [x] join JSON·signed snapshot pure fuzz 각각 10K evaluations 및 invalid UTF-8 query corpus 회귀 ([기록](evidence/input-boundaries-summary.md))
- [ ] 실제 handler와 OpenAPI 일치 suite
- [ ] browser/mobile/a11y E2E
- [ ] Standard 10K workload/threshold
- [ ] High Scale distributed workload/threshold
- [ ] reconnect/early-poll/expiry workload
- [ ] Coordinator loss/Valkey failover/zone loss chaos
- [ ] two-hour soak와 resource recovery evaluator
- [x] M0 evidence writer·schema·source/log digest validator
- [ ] production qualification manifest·image/config/환경 검증

## 13. Unit test Checklist와 결과

진입점: `make test-unit PRD=07` (현재 PRD-08과 공통 tooling 10개). 전체 qualification workload generator는 미구현이다.

| UT-ID | Test-harness unit test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-07-01 | workload phase generator | 목표 rate/duration 정확 | NOT RUN | — |
| UT-07-02 | invariant evaluator | synthetic violation 전부 탐지 | NOT RUN | — |
| UT-07-03 | percentile/error evaluator | expected fault 제외 정확 | NOT RUN | — |
| UT-07-04 | reconnect scheduler | 100K logical ticket 분산 정확 | NOT RUN | — |
| UT-07-05 | resource recovery evaluator | RSS/goroutine/FD 경계 판정 | NOT RUN | — |
| UT-07-06 | evidence schema | 필수 field 누락 거부 | PARTIAL | M0 schema PASS; qualification schema 미구현; [M0](evidence/m0-summary.md) |
| UT-07-07 | digest consistency | mismatch가 PASS 무효화 | PARTIAL | source/log hash PASS; image/config 결합 미구현; [M0](evidence/m0-summary.md) |
| UT-07-08 | badge derivation | local evidence로 badge 불가 | PARTIAL | local/M0 delivery evidence 거부 unit PASS; badge publish 미구현; [M1](evidence/m1-summary.md) |

### Unit test 실행 로그

| Run | Commit | Command | Passed/Failed | Result | Evidence |
|---|---|---|---:|---|---|
| M0-20260905 | uncommitted snapshot | `make check` → `npm test` | tooling 공통 9/0 | PASS | [M0](evidence/m0-summary.md); qualification NOT RUN |
| M1-local | uncommitted snapshot | `make test-integration`; `make lab-quick` | 부분 suite 결과는 evidence 참조 | PARTIAL | [M1](evidence/m1-summary.md); full Quick20/qualification 미완료 |

## 14. GO/NO-GO 판정

- 명세의 구현 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 로컬 visitor tiers: 4단계 PASS, HTTP·지속 부하 qualification은 미완료. [설치 합계/단계별 기록](evidence/installation-capacity-summary.md).
- 별도 HTTP tiers: 4단계 PASS, 지속 rate/clock·resource recovery·qualification은 미완료. [HTTP 기록](evidence/http-visitor-tiers-summary.md).
- 이유: oracle·schema·앱 local server는 부분 검증했지만 전체 workload·chaos·10K/100K qualification은 미실행이다.
- M1 추가: 실제 Valkey·앱 server·Quick20을 부분 검증했다. [M1 증거](evidence/m1-summary.md). 현재 관리자 Traffic Lab의 browser/app Quick20·Smoke1K는 구현했다. 전체 수동 시나리오·fault/qualification은 남아 있으며 local scope는 delivery GO 증거로 사용할 수 없다.
- GO 조건: checklist와 UT-07 PASS, required contract suites PASS, valid 10K·100K qualification evidence, 의존 sub-PRD GO, reviewer·UTC 시각 기록.
- 이 문서의 GO는 MAIN 최종 test 진입 자격일 뿐 release GO가 아니다.

## 15. 참고

- [Grafana k6 running large tests](https://grafana.com/docs/k6/latest/testing-guides/running-large-tests/)
- [Grafana k6 website load testing](https://grafana.com/docs/k6/latest/testing-guides/load-testing-websites/)

---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-02
title: Queue State, Admission Control and Recovery
version: 0.1.0
spec_status: ready_for_implementation
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [SUB-PRD-01]
blocks: [SUB-PRD-03, SUB-PRD-04, SUB-PRD-05, SUB-PRD-07]
milestones: [M0, M1, M2, M5]
last_updated: "2026-09-06"
---

# SUB-PRD-02 — Queue State·Admission·Recovery

> 읽기 순서: [main-prd.md](main-prd.md) → [sub-prd_01.md](sub-prd_01.md) → 이 문서. 이 문서는 FIFO correctness와 runtime safety invariant의 source of truth다.

## 1. 목표

여러 Gateway·Coordinator가 동시에 요청을 처리해도 정상 단일-primary epoch에서 FIFO, capacity lease와 admission rate를 위반하지 않는다. 상태를 확신할 수 없는 장애에서는 origin 보호를 우선하고 신규 admission을 중지한다.

## 2. 범위와 비범위

### 포함

- anonymous ticket, FIFO sequence와 state machine
- READY reservation, idempotent admission claim과 TTL lease
- 최근 60초 rolling admission limit
- Room·installation visitor-state hard cap
- runtime idempotency·poll scheduling
- primary epoch, fencing, RECOVERY_HOLD와 안전 재개
- pure Go reference model과 Valkey Function 일치

### 비범위

- HTTP·cookie encoding: SUB-PRD-03
- process credential·key storage·backup: SUB-PRD-05
- sharded/multi-primary Valkey와 global active-active
- user-account binding과 stolen bearer token 판별

v1 Valkey topology는 **unsharded single writable primary + replicas**다. 설치 전체 hard cap을 여러 cluster hash slot에서 근사 계산하지 않는다.

## 3. State machine

```text
NONE ──join──▶ WAITING ──FIFO + capacity + rate──▶ READY
                  │                                   │
          idle/absolute TTL                       READY TTL
                  │                                   │
                  ▼                                   ▼
               EXPIRED                            EXPIRED

READY ──idempotent POST claim──▶ ADMITTED ──admission TTL──▶ EXPIRED
```

- 빈 queue에 capacity·rate 여유가 있으면 atomic join-and-claim으로 `NONE → ADMITTED`가 가능하다.
- user cancel과 `CANCELED` state는 v1에 없다.
- GET status는 idle TTL을 포함한 queue state를 변경하지 않는다. WAITING client는 5분마다 명시적 POST heartbeat로 idle TTL만 갱신하며 absolute TTL은 연장하지 않는다.
- EXPIRED ticket은 원래 sequence로 복원하지 않는다.

## 4. Runtime 계약

### Q-01 FIFO sequence

- sequence는 client clock이 아닌 Valkey primary에서 원자 발급한다.
- WAITING ticket이 존재하면 새 ticket이 앞질러 admission되지 않는다.
- 만료 ticket만 FIFO 비교 대상에서 제외한다.
- 모든 Room runtime key는 `{roomId:epoch}` hash tag를 사용한다.

### Q-02 READY와 claim

- promotion은 visitor-state·active lease·rolling rate budget을 하나의 Valkey Function에서 검사하고 소비한다.
- READY 생성 시 `jti`, room, epoch, issued/not-before/expiry와 audience claims를 고정한다.
- `admissionUntil = promotedAt + admissionTTL`, `readyUntil = min(promotedAt + 120s, admissionUntil)`다. 늦은 claim은 남은 token 수명만 받으며 만료된 READY는 reservation을 반환한다.
- rate budget은 READY 생성 시 소비하고 미claim 시 되돌리지 않는다.
- claim은 같은 ticket에 대해 deterministic Ed25519 payload를 사용하며 경쟁·응답 유실 재시도에도 같은 admission 결과를 반환한다.

### Q-03 Capacity와 rate

- `maxActiveAdmissionLeases`는 READY reservation + ADMITTED lease의 상한이다. ADMITTED는 token expiry + 최대 verifier leeway 30초까지 capacity를 점유한다.
- profile cap은 `WAITING + READY + leeway 종료 전 ADMITTED`의 Room 및 설치 전체 합계다.
- Standard는 10,000, High Scale은 100,000 visitor state를 넘지 않는다.
- rate는 Valkey server time 기준 `(now - 60s, now]` sliding window다. 어떤 연속 60초 구간도 `admissionsPerMinute`를 넘지 않는다.
- profile별 최대 rate는 Standard 6,000/min, High Scale 60,000/min이다.
- admission TTL은 60~3,600초, READY TTL은 기본 120초, token clock skew는 최대 30초다.

### Q-04 Ticket과 idempotency

- queue token은 256-bit random이고 runtime에는 SHA-256 hash만 둔다.
- ticket idle TTL은 10분, absolute TTL은 24시간이다.
- public join idempotency record는 10분 보관한다.
- public idempotency budget은 Standard 20,000, High Scale 200,000이다.
- visitor/idempotency budget 80%에서 경고한다. cap 도달 후 기존 key의 동일 replay·기존 ticket status/claim은 허용하고 새 key·ticket만 fail-closed한다.
- rotating source fingerprint quota와 Room 전체 quota를 함께 쓰며 raw IP를 영구 저장·log하지 않는다.

### Q-05 Poll scheduling

- Coordinator는 ticket별 `nextPollAt`을 3~20초 범위와 deterministic jitter로 계산한다.
- 그 전 요청은 queue data를 읽거나 idle TTL을 갱신하지 않고 rate-limit 결과만 낸다.
- reconnect 시 모든 ticket을 즉시 poll시키지 않고 ticket seed로 다음 20초 구간에 분산한다.

### Q-06 Atomicity와 replica 경쟁

- FIFO selection, visitor cap, READY reservation, rolling rate, claim state와 cardinality counter는 versioned Valkey Function의 짧은 atomic execution으로 처리한다.
- two or more Coordinator가 같은 command를 실행해도 sequence·READY·admission·counter가 중복되지 않는다.
- function version은 data schema version과 호환성을 검사하며 배포가 실패하면 구 version을 유지한다.

## 5. 장애와 복구

### Q-07 정상 장애 동작

- 유효한 admission token은 Gateway가 Valkey 없이 검증해 expiry까지 통과시킨다.
- 신규·WAITING·READY admission은 primary write가 확실할 때만 진행한다.
- Control 장애는 queue state를 바꾸지 않고 last-known-good config를 사용한다.
- OFF의 고객 origin route는 Valkey와 무관하게 통과한다.

### Q-08 Primary uncertainty

primary 변경, write 결과 불확실, fencing 실패 또는 `noeviction` write error를 감지하면 RECOVERY_HOLD로 들어간다.

새 writable primary를 얻은 첫 recovery transaction은 Valkey server time으로 다음을 원자 기록한다.

```text
unsafeUntil = recoveryHandshakeAt + max(configured admission TTL, READY TTL, 60s) + 30s
```

이 값은 새 primary의 fenced recovery record에 저장하고 모든 Coordinator가 동일 값을 읽는다. `unsafeUntil` 전에는 기존 admission token만 통과하고 READY 생성·claim·direct admission을 모두 금지한다. 실제 장애 감지보다 늦은 handshake 기준이므로 보수적으로 더 오래 HOLD할 수 있지만 일찍 재개하지 않는다.

`unsafeUntil` 뒤 보존된 queue와 counter invariant를 검증하면 같은 epoch를 재개한다. 검증할 수 없으면 추정 복원하지 않고 Admin 재인증을 통한 새 epoch만 허용한다. 새 epoch는 기존 queue와 admission token을 모두 무효화하므로 영향 수와 불확실 수를 표시한다.

새 epoch로 안전 대기를 단축하려면 모든 serving Gateway의 새 epoch 적용 ACK와 stale/unreachable Gateway의 traffic path 제거를 먼저 증명해야 한다. 그렇지 않으면 기존 안전 대기를 유지한다. 세부 시간 계약: [ADR-0002](adr/0002-state-and-time.md).

### Q-09 DRAINING

안전 종료 시작 시 `drainCutoffSequence`를 고정한다. 그 이하 WAITING만 promotion하고 새 join은 만들지 않는다. cutoff queue가 비고 SUB-PRD-01의 안전 종료 조건이 충족돼야 OFF가 된다. HOLD는 promotion을 멈추므로 drain 완료 수단으로 제안하지 않는다.

## 6. 공식 불변조건

- INV-Q-01: 정상 primary epoch에서 만료를 제외한 FIFO inversion = 0
- INV-Q-02: sequence, ticket, READY, admission `jti` duplicate = 0
- INV-Q-03: active admission lease와 visitor-state cap exceed = 0
- INV-Q-04: 모든 연속 60초 window의 admission count exceed = 0
- INV-Q-05: 미입장 Gateway request의 origin reach = 0
- INV-Q-06: uncertainty safe window 안의 신규 admission = 0
- INV-Q-07: GET status에 의한 state change = 0
- INV-Q-08: idempotency retry가 새 ticket/admission을 생성한 수 = 0
- INV-Q-09: cap churn 중 eviction/OOM = 0

## 7. Acceptance criteria

- [ ] pure Go model과 Valkey implementation이 randomized command trace에서 동일
- [ ] 2개 이상 Coordinator의 concurrency에서 INV-Q-01~09 위반 0
- [ ] restart persistence 뒤 ticket sequence와 expiry 보존
- [ ] primary uncertainty가 모든 Coordinator에 같은 `unsafeUntil`을 강제
- [ ] abandoned READY와 expired admission lease가 정해진 시간에 회수
- [ ] 100K reconnect burst에서 poll 분산과 조기 rate-limit이 state를 변경하지 않음

## 8. 구현 Checklist

- [x] Go reference state model과 command/result type (single-threaded oracle, production store 아님)
- [x] M1 lab 단일 Room Valkey Function·library source/config guard
- [x] M1 lab WAITING/expiry index·READY·claim·rolling rate·HOLD/DRAINING
- [x] M1 lab 두 client 동시성 및 실제 서버 시간 경계 검사
- [x] unsharded lab 설치 합계 visitor/idempotency cap·80% snapshot·공유 dirty fail-closed
- [x] lab 설치 v2: 소유 Room의 실제 삭제까지 용량 유지·Room 유실 시 sequence 초기화 거부 ([회귀 범위](evidence/installation-retention-summary.md))
- [ ] versioned Valkey Function library와 schema guard
- [ ] FIFO sequence·WAITING set·expiry index
- [ ] READY reservation과 deterministic claim payload
- [ ] active lease·rolling 60-second window counter
- [ ] Room·installation visitor-state atomic counter
- [ ] public idempotency budget·source quota
- [ ] ticket heartbeat·absolute expiry·nextPollAt
- [ ] DRAINING cutoff
- [ ] epoch·fencing·RECOVERY_HOLD·unsafeUntil
- [ ] invariant metrics와 recovery audit event

## 9. Unit test Checklist와 결과

단위 진입점: `make test-unit PRD=02`는 Go oracle 10개와 설치 설정/안전 key 검사 2개를 실행한다. Valkey 통합 시험은 별도 진입점으로 분리한다.

추가 진입점: `WR_TEST_VALKEY=127.0.0.1:16379 make test-integration`.
기존 전체 checklist는 production 범위를 유지한다. M1 완료 범위는 위 lab 체크 항목과
[M1 기록](evidence/m1-summary.md)을 따른다.

| UT-ID | Unit/property test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-02-01 | FIFO randomized trace | model/Valkey 결과 동일, inversion 0 | PARTIAL | 실제 Valkey 5×100 join/promotion/claim trace PASS; 전체 명령 미완료; [M1](evidence/m1-summary.md) |
| UT-02-02 | concurrent join idempotency | ticket 한 건 | PASS | 두 Store client·40개 동일 요청; [M1](evidence/m1-summary.md) |
| UT-02-03 | concurrent promotion/claim | READY·jti·lease 한 건 | PASS | 20 promotion/40 claim 경쟁; [M1](evidence/m1-summary.md) |
| UT-02-04 | rolling 60-second boundary | 모든 window rate 이하 | PARTIAL | oracle와 실제 60초 단일 Room 경계 PASS; 전체 property 후속; [M1](evidence/m1-summary.md) |
| UT-02-05 | visitor/idempotency caps | 80% 경고, 신규만 fail-closed | PARTIAL | lab 4 Room 합계·80%·재시도·claim·부분 오류 차단 및 1K/2K/5K/10K PASS; production/HA 미완료; [기록](evidence/installation-capacity-summary.md) |
| UT-02-06 | idle/absolute/READY/lease expiry | 각 state·counter 정확히 회수 | PARTIAL | heartbeat/idle/lease grace 및 lab v2 owner cleanup 전 용량 유지 회귀; 전체 store 만료 조합 후속; [M1](evidence/m1-summary.md), [v2](evidence/installation-retention-summary.md) |
| UT-02-07 | early poll/reconnect | state 불변, schedule 분산 | NOT RUN | — |
| UT-02-08 | function retry after response loss | 결과 중복 없음 | NOT RUN | — |
| UT-02-09 | failover safe window | unsafeUntil 전 admission 0 | PARTIAL | oracle PASS, actual failover 미검증; [M0](evidence/m0-summary.md) |
| UT-02-10 | drain cutoff race | cutoff 이후 join 0, 이전 FIFO 유지 | NOT RUN | — |

### Unit test 실행 로그

| Run | Commit | Valkey version | Command | Passed/Failed | Result | Evidence |
|---|---|---|---|---:|---|---|
| M0-20260905 | uncommitted snapshot | 미사용 | `make check` → `go test -race -count=1 ./...` | oracle 10/0 | PASS | [M0](evidence/m0-summary.md) |
| M1-local | uncommitted snapshot | 8.1.6 | `make test-integration`; `make test-persistence` | 부분 suite 결과는 evidence 참조 | PARTIAL | [M1](evidence/m1-summary.md), 전체 UT 범위 미완료 |

## 10. GO/NO-GO 판정

- 명세의 구현 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 이유: 단일 Room 및 unsharded lab 설치 합계 cap을 부분 검증했다. production cap 연결·source quota·공유 recovery/fencing·100K와 전체 trace는 남아 있다. process/replica HA는 미검증이다.
- GO 조건: 모든 invariant·acceptance·unit/integration test PASS, dependency GO, P0/P1 0건, reviewer·UTC 판정 기록.

---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-01
title: Product Behavior, Operations and Queue Policy
version: 0.1.0
spec_status: ready_for_implementation
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [MAIN-PRD]
blocks: [SUB-PRD-02, SUB-PRD-03, SUB-PRD-04]
milestones: [M0, M1, M2, M3]
last_updated: "2026-09-09"
---

# SUB-PRD-01 — 제품 동작·운영·Queue Policy

> 읽기 순서: [main-prd.md](main-prd.md) → 이 문서. 이 문서는 사용자가 보게 되는 Waiting Room의 제품 동작과 policy 확장 경계의 source of truth다.

## 1. 목표

소규모 웹팀이 코드 수정 없이 Waiting Room을 켜고, 보호 경로·입장 유량·일정·대기 화면을 이해 가능한 용어로 운영하게 한다. v1은 FIFO correctness에 집중하되 lottery·priority·weighted를 API 재설계 없이 후속 추가할 수 있어야 한다.

## 2. 사용자와 주요 사례

- 설치자: 개발자 또는 DevOps 담당자
- 일상 운영자: 이벤트·쇼핑몰·티켓·예약 담당자
- 연동자: 기존 웹과 앱 API 개발팀

주요 사례는 오픈 전 사전대기, flash sale, 예약·티켓 오픈, 갑작스러운 폭주와 장애 복구 중 점진 입장이다.

## 3. 범위

### 포함

- 범용 reverse-proxy형 트래픽 gate
- 브라우저와 JSON API의 동일 queue 의미
- hostname + segment-aware path prefix, 보호·제외 rule
- 활성 admission lease, 분당 신규 admission, admission TTL 제어
- OFF/AUTO/HOLD와 내부 DRAINING·RECOVERY_HOLD
- 일회성 예약 event와 고정 유량
- 안전한 theme field와 KR/EN 기본 문구
- FIFO policy와 future policy capability 계약

### 비범위

- 실제 queue state·원자성 구현: SUB-PRD-02
- HTTP endpoint·token 계약: SUB-PRD-03
- 관리자 화면·TOTP/RBAC: SUB-PRD-04
- multi-region active-active, 공식 mobile SDK, user-account dedupe, CAPTCHA, SaaS

## 4. 핵심 제품 요구사항

### P-01 경로 판정

- hostname과 segment-aware path prefix를 사용한다.
- 제외 rule이 보호 rule보다 우선한다.
- query string은 v1 route 판정에 쓰지 않는다.
- active rule 충돌은 저장 단계에서 거부한다.
- assets, health, admin, webhook 제외 후보를 Wizard가 추천한다.
- URL 검사기는 보호·제외·충돌 결과와 이유를 보여준다.
- encoded slash, dot segment, double encoding과 forwarded-header spoofing으로 우회할 수 없어야 한다.

### P-02 운영 모드

- `OFF`: Waiting Room reserved endpoint 이외의 고객 origin route를 통과시킨다.
- `AUTO`: capacity lease와 최근 60초 admission rate 안에서 점진 입장시킨다.
- `HOLD`: 기존 유효 admission은 통과시키고 신규·대기 사용자의 promotion은 멈춘다.
- `DRAINING`: 안전 종료 시작 sequence cutoff 이전 ticket만 계속 promotion한다.
- `RECOVERY_HOLD`: runtime 일관성이 불확실할 때 신규 admission을 금지한다.

DRAINING cutoff 이후 새 방문에는 ticket을 발급하지 않고 503 안내를 반환한다. cutoff 이전 WAITING·READY가 0이고 origin이 정상이며 최근 5분 신규 유입률이 설정 admission rate 이하일 때만 OFF로 전환한다. 그렇지 않으면 AUTO 복귀 또는 Admin 재인증이 필요한 즉시 우회를 선택한다.

### P-03 예약과 수동 override

한 event는 `prequeueAt → admitAt → drainAt`의 UTC 시각을 갖고 UI는 선택한 timezone으로 표시한다. 같은 Room의 event가 겹치거나 비활성 Room을 대상으로 하면 저장·활성화를 거부한다.

runtime에는 독립 revision이 있다. 예약 전환과 수동 변경은 예상 revision을 조건으로 적용한다. 수동 변경이 우선하며 active event는 `paused_by_override`로 바뀌고 명시적인 재개 또는 취소 전에는 다음 전환을 실행하지 않는다.

### P-04 유량 의미

운영자는 다음 세 값을 설정한다.

1. 최대 활성 admission lease 수
2. 분당 신규 admission 수
3. admission token TTL

admission lease는 실제 online user·TCP connection이 아니라 아직 만료되지 않은 bearer token 한 건이다. admission 이후의 개별 origin 요청 수를 제한하는 범용 API rate limiter는 v1 비범위다.

### P-05 Queue policy 확장

`QueuePolicy`는 현재 시각과 batch limit을 받아 **입장 자격이 있는 ticket의 순서화된 stream**만 만든다. capacity lease, 최근 60초 rate와 token 발행은 모든 policy가 공유한다.

| Policy | 단계 | 선택 규칙 |
|---|---|---|
| `fifo` | v1 | Valkey가 원자 발급한 sequence 오름차순 |
| `lottery` | post-v1 | 마감 cohort를 seed commit-reveal로 재현 가능한 shuffle |
| `priority` | post-v1 | 서버가 서명한 lane claim, lane 내부 FIFO |
| `weighted` | post-v1 | lane 사이 weighted deficit round-robin, lane 내부 FIFO |

Admin capability registry가 `supportedPolicies`와 schema version을 반환한다. Wizard는 설치가 실제 지원하는 policy만 보여준다. active queue에서 policy를 바꿀 수 없으며 안전 종료 뒤 변경하거나 영향 수를 확인하고 새 epoch를 시작해야 한다.

### P-06 Browser·App 경험

- 브라우저 top-level GET/HEAD는 대기 페이지로 이동하고 cookie로 순서를 유지한다.
- unsafe method body는 저장·재생하지 않고 앱이 처리할 problem response를 반환한다.
- 앱은 `pass`, `queued`, `ready`, `admitted`, `expired`, `unavailable`을 처리한다.
- 앱 비밀키는 존재하지 않으며 기존 OAuth와 별도 admission header를 쓴다.
- 공식 SDK 대신 v1 OpenAPI와 sample client를 제공한다.

### P-07 대기 화면

허용 항목은 logo, 색상·배경, 제목·안내 문구, 예상 대기 범위 표시, KR/EN과 responsive/accessibility 설정뿐이다. 이미지 크기·pixel 수를 제한하고 decode/re-encode한다. SVG, 임의 HTML/CSS/JS와 외부 script는 금지하며 strict CSP를 사용한다.

### P-08 자동화 경계

v1은 운영자가 승인한 고정 capacity와 admission rate를 사용한다. origin health와 Traffic Lab 결과로 추천은 제공할 수 있지만 자동 적용하거나 완전 자동 조절하지 않는다.

## 5. Acceptance criteria

- [ ] 비개발 운영자가 Wizard 용어만으로 OFF/AUTO/HOLD와 안전 종료 차이를 설명할 수 있음
- [ ] 모든 URL fixture가 적용·제외·충돌 중 하나와 이유를 가짐
- [ ] DRAINING이 새 ticket을 만들지 않고 유한하게 종료되거나 blocker를 명시함
- [ ] 수동 override와 예약의 우선순위가 deterministic함
- [ ] v1 UI/API에 FIFO 외 미구현 policy가 선택 가능하게 노출되지 않음
- [ ] future policy가 안전 제어를 우회할 수 없는 interface를 가짐
- [ ] browser와 app이 동일한 제품 state 의미를 사용함

## 6. 구현 Checklist

- [x] `QueuePolicy` interface와 FIFO capability 등록 (`internal/policy`)
- [x] 로컬 route matcher·conflict validator·URL explanation 구현
- [x] 로컬 mode state와 runtime revision 구현
- [x] 로컬 event overlap·inactive-room·manual-override 규칙 구현
- [x] 로컬 DRAINING cutoff와 신규 유입 503 처리 구현
- [x] safe theme schema·PNG/JPEG image sanitizer·CSP 구현 (16 KiB/256×256px, 서버 decode/re-encode, 전체 서명 크기 제한; [검증](evidence/beta-20260910-logo-maintenance.md))
- [ ] browser/app 상태 용어를 OpenAPI와 UI copy에 일치시킴
- [x] 추천값과 운영자가 검토한 명시적 적용을 분리함
- [x] [ADR-0004](adr/0004-bearer-identity-boundary.md)에 bearer token 공유를 v1 non-goal로 기록

## 7. Unit test Checklist와 결과

진입점: `make test-unit PRD=01`. policy·control·waiting의 현행 단위/race 검사를 포함한다.
PostgreSQL 경쟁·예약은 `make test-auth-db`, 실제 Valkey는 `make test-integration`이다.
아래 PASS는 이 로컬 검사 범위이며 전체 delivery GO와 구분한다.

| UT-ID | Unit test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-01-01 | segment-aware include/exclude precedence | 모든 fixture가 기대 rule과 일치 | PASS | TestRouteMatching, TestCheckRouteUsesCanonicalRulesWithoutReflectingURL; [고정 소스 검증](evidence/beta-20260909-read-recovery.md) |
| UT-01-02 | encoded path·dot·double encoding | 우회 입력 전부 거부 | PASS | control의 정규 경로 및 URL fixture; [고정 소스 검증](evidence/beta-20260909-read-recovery.md) |
| UT-01-03 | route/event conflict | 충돌 저장 409 | PASS | TestRouteConflicts, TestEventsOverlapOverrideResumeAndCancel; 같은 소스의 PG CI PASS |
| UT-01-04 | runtime revision race | 경쟁 변경 한 건만 성공 | PASS | TestRuntimeRevisionRaceAndOperatorBoundary: 12개 경쟁 중 200 한 건·412 열한 건; PG CI PASS |
| UT-01-05 | manual override | active event가 pause되고 후속 transition 없음 | PASS | TestEventOrderingOverrideAndOverlap, TestEventsOverlapOverrideResumeAndCancel; PG 시각 fixture 경계와 실제 Docker 예약 여정은 구분 |
| UT-01-06 | DRAINING cutoff | cutoff 이후 ticket 0, 이전 순서 유지 | PASS | TestRuntimeConfigurationMetricsAndReplay 및 runtime v5 모델 대조; 새 join 거부·기존 재시도 보존; Valkey CI PASS |
| UT-01-07 | policy registry | v1은 FIFO만 노출 | PASS | TestCapabilityRegistry; [M0](evidence/m0-summary.md) |
| UT-01-08 | unsafe theme input | SVG/HTML/script와 oversized image 거부 | PASS | PNG/JPEG 서버 decode/re-encode·메타데이터 제거·입출력/픽셀/서명 크기 경계, PG 거부·재시도·감사 및 3엔진 업로드/제거; [검증](evidence/beta-20260910-logo-maintenance.md) |

### Unit test 실행 로그

| Run | Commit | Command | Passed/Failed | Result | Evidence |
|---|---|---|---:|---|---|
| M0-20260905 | uncommitted snapshot | `make check` → `go test -race -count=1 ./...` | policy 2/0 | PASS | [M0](evidence/m0-summary.md) |
| Local-20260909 | serving source 533150e; PRD runner follow-up | `make test-unit PRD=01` | policy/control/waiting 전부 PASS | PASS | [현재 진입점 로그](evidence/beta-20260909-read-recovery/product-policy-unit.log); PG/Valkey는 [고정 소스 CI](evidence/beta-20260909-read-recovery.md) |

## 8. GO/NO-GO 판정

- 명세의 구현 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 이유: 로컬 route·운영 모드·예약·이미지 sanitizer를 포함한 안전한 theme와 위 단위/통합 범위는 검증했다. 비개발 운영자 수용 검증, 최신 후보 mode/failure 대조 및 MAIN 의존 gate가 남는다. [최신 후보의 수용 범위](evidence/beta-20260910-logo-maintenance.md).
- GO 조건: 구현·acceptance checklist 완료, 모든 unit/contract test PASS, 의존 문서 GO, 열린 P0/P1 0건, reviewer·UTC 시각 기록.

## 9. 참고

- [Cloudflare Waiting Room configuration](https://developers.cloudflare.com/waiting-room/reference/configuration-settings/)
- [Cloudflare Waiting Room bypass rules](https://developers.cloudflare.com/waiting-room/additional-options/waiting-room-rules/bypass-rules/)
- [Cloudflare Waiting Room events](https://developers.cloudflare.com/waiting-room/additional-options/create-events/)

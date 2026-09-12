---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-03
title: Public Web and App API
version: 0.1.0
spec_status: ready_for_implementation
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [SUB-PRD-01, SUB-PRD-02]
blocks: [SUB-PRD-05, SUB-PRD-07]
milestones: [M0, M1, M2]
last_updated: "2026-09-10"
---

# SUB-PRD-03 — 공개 Web·App API

> 읽기 순서: [main-prd.md](main-prd.md) → [sub-prd_01.md](sub-prd_01.md) → [sub-prd_02.md](sub-prd_02.md) → 이 문서. 이 문서는 공개 HTTP, browser cookie와 app token 표현의 source of truth다.

## 1. 목표

브라우저와 native app이 같은 queue state를 안전하게 사용하되 기존 서비스의 request body·OAuth 계약을 훼손하지 않는다. `GET status`는 read-only이고 admission 발행은 idempotent `POST claim`으로 분리한다.

## 2. 범위와 비범위

### 포함

- `/_wr/v1` JSON API와 `/_wr/wait/*`
- join-or-resume, status, admission claim
- protected browser/app request 처리
- queue/admission token, cookie, return URL
- polling·idempotency·problem response·cache policy
- waiting page의 안전한 delivery contract

### 비범위

- Admin API: SUB-PRD-04
- queue 내부 state transition: SUB-PRD-02
- 공식 iOS/Android SDK와 범용 browser CORS
- unsafe method body 저장·자동 replay
- 앱의 기존 OAuth 설계
- stolen bearer token과 정상 재사용의 온라인 구별

## 3. 공통 HTTP 계약

- JSON field는 camelCase, time은 UTC RFC 3339, duration은 `*Seconds` 또는 `*Ms`다.
- 인증·대기·오류 response는 `Cache-Control: no-store`다.
- 오류 media type은 `application/problem+json`이다.
- `/_wr/*`와 public liveness는 고객 route보다 우선한다.
- Admin·metrics·internal endpoint는 public listener에 존재하지 않는다.
- public API는 same-origin이고 native app은 CORS 영향을 받지 않는다. v1 범용 CORS 설정은 없다.
- `roomPublicId`는 cookie name과 URL에 안전한 lower-case base32 20자로 고정한다.

## 4. Endpoint

| Method | Path | 역할 |
|---|---|---|
| POST | `/_wr/v1/tickets` | 앱 join-or-resume |
| GET | `/_wr/v1/rooms/{roomPublicId}/status` | 부작용 없는 현재 state 조회 |
| POST | `/_wr/v1/rooms/{roomPublicId}/heartbeat` | WAITING idle TTL만 갱신, 204 |
| POST | `/_wr/v1/rooms/{roomPublicId}/admissions` | READY ticket의 idempotent claim |
| GET | `/_wr/wait/{roomPublicId}?return={sealed}` | 브라우저 waiting page |

### API-01 Join

```json
{
  "target": "/checkout?item=123"
}
```

- `target`은 현재 request host의 `/`로 시작하는 상대 URL이며 최대 2,048자다.
- scheme-relative, absolute URL, userinfo, control character와 normalization 뒤 host 변경은 거부한다.
- app 요청에는 `Idempotency-Key`가 필수다.
- browser protected GET은 Gateway가 내부 join-or-resume하고 JavaScript가 join API를 다시 부르지 않는다.

### API-02 공통 성공 response

```json
{
  "apiVersion": "v1",
  "state": "queued",
  "roomId": "abcdefghijklmnopqrst",
  "pollAfterMs": 5000,
  "heartbeatAfterMs": 300000,
  "usersAhead": 123,
  "estimatedWaitSeconds": {"min": 240, "max": 360},
  "ticketToken": "app_only_opaque_token",
  "expiresAt": "2026-09-05T12:00:00Z",
  "statusUrl": "/_wr/v1/rooms/abcdefghijklmnopqrst/status"
}
```

- state는 `pass|queued|ready|admitted`다. 필요 없는 field는 생략한다.
- `usersAhead`와 예상 시간은 계산할 수 없으면 `null`이며 SLA가 아니다.
- status의 선택 필드 `admissionPaused`는 새 입장 중지 여부다. 순번은 실제 대기 인덱스,
  예상 범위는 최근 입장 수와 설정 제한으로 계산하며 join 재시도 응답은 바꾸지 않는다.
  [페이지 연결·대기 안내](operators/page-and-wait-progress.md)를 따른다.
- `pass|ready|admitted`는 200, `queued`는 202다.
- `expired|unavailable`은 app-side 상태이며 서버의 410·503 problem을 매핑한다.

### API-03 Status와 polling

- app은 `Authorization: Bearer <ticketToken>`, browser는 queue cookie를 쓴다.
- GET status는 queue state·예상값만 읽고 READY→ADMITTED를 일으키지 않는다. 조기 요청 판정에 쓰는 `nextPollAt`과 요청 카운터는 queue 밖의 제한 메타데이터로 분리한다.
- idle TTL 갱신은 POST heartbeat로 분리한다. 기본 10분 idle TTL에서는 5분 간격이며, 더 짧은 TTL을 설정하면 응답의 `heartbeatAfterMs = min(5분, idle TTL / 2)`를 따른다. 같은 ticket credential을 사용하며 cookie 요청은 same-origin/CSRF 검증을 통과해야 한다. 만료 ticket은 되살리지 않는다.
- Coordinator가 정한 3~20초+jitter 이전 요청은 queue를 읽거나 idle TTL을 갱신하지 않고 429·`Retry-After`를 반환한다.
- reconnect 때 ticket별 다음 20초 window로 polling을 분산한다.

### API-04 Admission claim

- READY 뒤 POST admissions를 호출한다.
- claim response가 유실돼도 같은 ticket은 expiry까지 동일한 `jti`·claims·signature를 반환한다.
- browser cookie credential은 `Set-Cookie` 후 검증된 `return`으로 303을 받는다.
- app Bearer credential은 JSON admission token을 받는다.
- cookie와 Bearer가 동시에 들어온 모호한 요청은 400으로 거부한다.
- app은 origin API 재시도 때 기존 OAuth와 별도의 `X-Waiting-Room-Admission`을 사용한다.

## 5. Protected request truth table

| 조건 | Browser top-level GET/HEAD | App/unsafe method |
|---|---|---|
| exclude 또는 OFF customer route | origin 전달 | origin 전달 |
| valid admission | local verify 후 origin | local verify 후 origin |
| queue 필요 | 303 waiting page | 429 `WAITING_ROOM_REQUIRED` |
| DRAINING 신규 방문 | 503 안내 page | 503 `QUEUE_DRAINING` |
| runtime write 불확실 | 503 안내 page | 503 `QUEUE_UNAVAILABLE` |
| signed config 없는 cold start | liveness 외 503 | liveness 외 503 |

- POST/PUT/PATCH/DELETE body는 저장하거나 자동 재생하지 않는다.
- client의 `X-Waiting-Room-Admission`은 검증 입력으로만 소비하고 origin에 전달하지 않는다.
- 그 밖의 Waiting Room internal header는 client 입력에서 즉시 폐기한다.
- queue/admission cookie와 내부 header는 origin 전달 전에 제거하고 검증 결과만 내부 transport metadata로 전달한다.

## 6. Cookie와 token

| 이름 | 용도 |
|---|---|
| `__Host-wrq_{roomPublicId}` | browser opaque queue token |
| `__Host-wra_{roomPublicId}` | browser signed admission token |
| `Idempotency-Key` | app join retry; claim은 ticket 자체로 idempotent하며 별도 key 불필요 |
| `Retry-After` | 다음 허용 poll 또는 retry |
| `X-Request-ID` | redacted trace correlation |

- production cookie는 `Secure; HttpOnly; SameSite=Lax; Path=/`, Domain 없음이다.
- loopback HTTP Traffic Lab만 별도 `wr_dev_*` cookie를 허용한다.
- queue token은 256-bit random이고 Valkey에는 SHA-256 hash만 저장한다.
- admission token은 Ed25519이며 claim은 `kid, room, epoch, jti, iat, nbf, exp, aud`만 포함한다.
- admission token은 TTL 동안 여러 origin request에 쓰는 bearer capability다.
- token과 log에는 IP, User-Agent, email, original URL을 넣지 않는다.

## 7. Return URL과 waiting page

- same-host relative path만 Gateway 전용 AEAD로 봉인한다.
- sealed value는 `kid`, host, room, path, issued/expiry를 authenticate한다.
- expiry는 ticket absolute TTL보다 늦을 수 없다.
- current/previous key rotation을 지원하고 access log에서 전체 query value를 redaction한다.
- tab별 return을 URL에 둬 여러 tab이 cookie 하나를 덮어쓰지 않게 한다.
- waiting page는 logo·안전한 theme field·KR/EN·responsive·keyboard/screen-reader를 지원한다.
- image는 size/pixel 제한 후 decode/re-encode하고 SVG·원격 script·임의 HTML/CSS/JS는 거부한다.
- strict CSP와 no-store를 사용한다.

### 기본 제공 template

- 기본 시안은 `calm`으로 등록한다. [디자인·확장 계약](design/calm.md).
- template은 신뢰된 내장 CSS만 제공하고 semantic HTML·KR/EN·poll/heartbeat·claim은 공유한다.
- 등록된 ID로 선택하며 unknown/remote ID는 거부한다. 사용자 파일 업로드 기능은 아니다.
- 로컬 선택은 `wr-lab -template calm`; Room별 영속 설정과 Backoffice 선택/preview는 SUB-PRD-04 M3다.

## 8. 공개 problem code

| Code | HTTP | 의미 |
|---|---:|---|
| `INVALID_REQUEST` | 400 | schema·credential 조합 오류 |
| `NOT_FOUND` | 404 | Room 없음 |
| `IDEMPOTENCY_CONFLICT` | 409 | 같은 key의 다른 payload |
| `TICKET_EXPIRED` | 410 | ticket 재발급 필요 |
| `WAITING_ROOM_REQUIRED` | 429 | unsafe/app request가 queue 필요 |
| `API_RATE_LIMITED` | 429 | nextPollAt 이전 또는 abuse limit |
| `QUEUE_DRAINING` | 503 | 안전 종료 중 신규 join 금지 |
| `QUEUE_CAPACITY_EXCEEDED` | 503 | 새 visitor/idempotency budget 없음 |
| `CONFIG_UNAVAILABLE` | 503 | cold-start trust config 없음 |
| `QUEUE_UNAVAILABLE` | 503 | 안전하게 queue write 불가 |

problem에는 stable `type`, `code`, safe `title/detail`, `status`, `requestId`만 포함하고 내부 exception·token·target을 노출하지 않는다.

## 9. Acceptance criteria

- [ ] Public OpenAPI가 위 endpoint·schema·problem을 완전 기술함
- [ ] GET status의 repeated call이 queue state를 바꾸지 않음
- [ ] lost claim response retry가 byte-identical admission token을 반환함
- [ ] unsafe body가 memory·disk·log 어디에도 저장되지 않음
- [ ] browser와 app truth table이 모든 mode/failure에서 일치함
- [ ] invalid return/path/header가 origin 우회나 open redirect를 만들지 않음
- [ ] production cookie와 CSP가 browser test로 확인됨

## 10. 구현 Checklist

- [x] `api/openapi/public-v1.yaml` skeleton과 schema fixture (runtime contract 검증은 후속)
- [x] M1 lab 앱 join/status/heartbeat/claim 및 교차 Gateway retry
- [x] M1 lab deterministic Ed25519 서명·검증과 보호 origin 경로
- [x] 로컬 `calm` template registry·선택·KR/EN·360px 대기/입장/오류 화면
- [x] 로컬 browser cookie·303·ticket-bound return AEAD·CSRF·새로고침/탭별 복귀
- [x] 독립 Gateway handler의 설치 단위 return key 공유·same-host 비고정 라우팅 HTTP 여정 (lab memory-only; 배포 key rotation 후속)
- [x] Web/App 혼합 20명·claim commit 뒤 응답 유실/교차 재시도·origin 보호·실제 token/lease 만료 통합 시험
- [x] 4-PID app HTTP 1K/2K/5K/10K join/status·replay·origin 보호·10K cap·Coordinator 종료 (단기 local fixture)
- [x] process lab Gateway의 signed config 누락/불일치/만료에서 GET liveness 외 503 ([범위](operators/signed-config-lab.md))
- [x] lab JSON media/body/UTF-8/단일 target·credential header 경계, 실제 HTTP conflict/replay 및 bounded fuzz ([범위](operators/input-boundaries.md))
- [ ] join-or-resume handler
- [ ] read-only status handler
- [ ] idempotent admission claim handler
- [ ] protected-request truth-table middleware
- [ ] browser cookie·303 flow와 app JSON flow
- [ ] adaptive polling·early limit
- [ ] problem+json·no-store middleware
- [ ] Ed25519 verifier·audience/epoch/clock validation
- [ ] return AEAD·rotation·query redaction
- [ ] reserved header/cookie stripping
- [ ] waiting page sanitizer·CSP·accessibility
- [x] [Node 앱 연동 참조 예제](../examples/app-client/README.md): 동일 join intent·서버 간격·독립 heartbeat·claim 재시도·410/취소. Native OS background/안전한 재개 저장소와 실제 기기 acceptance는 미검증.

## 11. Unit test Checklist와 결과

부분 진입점: `make test-unit PRD=03` — lab/admission/waiting 단위 시험. 전체 production UT-03 완료가 아니다.
실제 Valkey 혼합 여정은 `WR_TEST_VALKEY=127.0.0.1:16379 make test-integration`으로 별도 실행한다.
HTTP 규모 회귀는 `WR_TEST_VALKEY=127.0.0.1:16379 make test-http-tiers` — 네 단계 PASS,
신규 business unit 추가가 아닌 통합 scenario다. [실행·범위](evidence/http-visitor-tiers-summary.md).

| UT-ID | Unit/fuzz test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-03-01 | same-host relative target parser | invalid/open redirect input 거부 | PARTIAL | lab static path parser PASS; 일반 normalizer/fuzz 후속; [M1](evidence/m1-summary.md) |
| UT-03-02 | join idempotency required/conflict | missing·conflict 정확한 problem | PARTIAL | lab 단일 header·missing400·conflict409·original replay 보존, 실제 HTTP PASS; production 후속; [입력 기록](evidence/input-boundaries-summary.md) |
| UT-03-03 | state↔HTTP mapping | 표와 완전 일치 | NOT RUN | — |
| UT-03-04 | GET status repeat | state mutation 0 | PARTIAL | Store tiers 전원 read-only ticket 보존·HTTP 단계별 status PASS; 전체 production matrix 후속; [Store 기록](evidence/installation-capacity-summary.md), [HTTP 기록](evidence/http-visitor-tiers-summary.md) |
| UT-03-05 | early poll | queue read·TTL update 0 | PARTIAL | guard 전 queue 접근 0, 공유 poll 제한·메타데이터 상한 race PASS; [공개 API](operators/public-api-validation.md) |
| UT-03-06 | lost claim response retry | 동일 token bytes | PARTIAL | lab browser/app claim commit 뒤 proxy가 응답 제거 → 다른 Gateway 재시도·token/cookie replay와 JTI/expiry 불변; production 후속; [혼합 여정](evidence/mixed-journey-summary.md) |
| UT-03-07 | protected request matrix | 모든 cell 기대 동작 | NOT RUN | — |
| UT-03-08 | unsafe body handling | persistence/replay 0 | NOT RUN | — |
| UT-03-09 | admission verifier | sig/aud/epoch/time 경계 | PARTIAL | lab Ed25519 binding·time 경계 PASS; keyring/rotation 후속; [M1](evidence/m1-summary.md) |
| UT-03-10 | cookie flags | prod/lab 속성 일치 | PARTIAL | local Docker TLS Secure/HttpOnly/Lax 및 실제 sealed return PASS; 공개 배포/전체 matrix 후속; [62250138](evidence/waiting-room-local-beta-test-62250138.json) |
| UT-03-11 | AEAD return fuzz | tamper/expiry/wrong host·room 거부 | PARTIAL | lab tamper/host/port/ticket/key/time 단위 검증 PASS; fuzz/rotation 후속; [template](evidence/browser-template-summary.md) |
| UT-03-12 | header/cookie stripping | origin leak 0 | PARTIAL | real Valkey browser→origin leak 0, OAuth/session 보존; 일반 matrix 후속; [template](evidence/browser-template-summary.md) |
| UT-03-13 | problem/log redaction | secret·PII 0 | NOT RUN | — |
| UT-03-14 | template registry·render·asset allowlist | unknown ID 거부·escaped markup·공통 control 보존 | PASS | renderer unit 3개; [template](evidence/browser-template-summary.md) |

### Unit test 실행 로그

| Run | Commit | Command | Passed/Failed | Result | Evidence |
|---|---|---|---:|---|---|
| M0-20260905 | uncommitted snapshot | `make lint-api`; `make test-contract` | Public/Admin 공통 schema 6/0, lint 2/0 | PASS | [M0](evidence/m0-summary.md); UT-03 runtime은 NOT RUN |
| M1-local | uncommitted snapshot | `make check`; `make test-integration`; `make lab-quick` | 부분 suite 결과는 evidence 참조 | PARTIAL | [M1](evidence/m1-summary.md); browser NOT RUN |
| Calm-local | uncommitted snapshot | `make check`; `make test-integration`; `make test-browser` | 신규 Go 7개 + schema 1개 + Chromium 5개 PASS | PARTIAL | [template](evidence/browser-template-summary.md); production 계약 전체 PASS 아님 |
| Mixed-journey-local | uncommitted snapshot | `make test-unit PRD=03`; `node scripts/run-m1.mjs mixed-journey-20260906-local` | 최종 집계는 evidence 참조 | PARTIAL | [독립 Gateway·혼합 20명·응답 유실·만료·hash](evidence/mixed-journey-summary.md) |

### 2026-09-06 추가 회귀

- [x] idle TTL 60/120/600초에 heartbeat 주기가 만료 전에 도달하는 Go unit PASS.
- [x] 서버가 제시한 heartbeat 주기 사용·주기적 poll이 heartbeat를 계속 미루지 않는 Chromium 회귀 PASS (visitor 전체 6 tests, 13.3s).
- [x] Docker Gateway/Coordinator 재시작 후 join replay·동일 admission, 실제 Valkey 재시작 뒤 기존 admission 만료 거부와 Web/App 입장 PASS ([62250138](evidence/waiting-room-local-beta-test-62250138.json)).
- [x] 로컬 공유 early-poll/source quota와 메타데이터 상한, 기존 티켓 보존 race ([범위](operators/public-api-validation.md)).
- [ ] 전체 공개 API matrix·운영 proxy/source quota·브라우저 3종/a11y qualification.
- [x] 후속 Chromium/Firefox/WebKit visitor 18 PASS (38.3s): WebKit select 44px 터치 영역 수정·잘못된 복귀 400·heartbeat·claim·장애 재시도 ([최신 기록](evidence/beta-runtime-progress.md)). 전체 a11y/공개 matrix GO를 뜻하지 않음.

## 12. GO/NO-GO 판정

- 명세의 구현 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 이유: 앱/브라우저 local lab과 template은 부분 검증됐다. production data plane 및 전체 UT-03 증거는 없다.
- M1 추가: 앱 handler·두 Gateway Quick20과 browser template slice를 구현·부분 검증했다. [M1 기록](evidence/m1-summary.md), [template 기록](evidence/browser-template-summary.md). production cookie/return rotation·early poll·전체 schema/runtime 동치·만료 후 exact join replay는 미완료라 NO-GO를 유지한다.
- GO 조건: 구현 checklist와 unit/fuzz/OpenAPI contract PASS, SUB-PRD-01·02 GO, P0/P1 0건, reviewer·UTC 시각 기록.

### 2026-09-09 로컬 배포 추가 검증

- [x] v6 신규 join 기록의 만료 후 원래 202 body, 설정 변경 후 원래 TTL, 보존 기간 종료 후 새 join.
- [x] 배포 current/previous keyring의 기존 claim bytes와 browser return, 새 admission의 실제 origin 도달.
- [x] Room·epoch·key ID·목적에 결합된 새 재시도/복귀 AEAD와 변조 거부.
- [x] 새 epoch 뒤 이전 복귀 키의 긴급 폐기 및 계속 유지되는 RECOVERY_HOLD.
- [x] cookie 없는 최초 browser join 응답 전체 유실 및 동시 최초 탭의 join-or-resume 수용 검증.

[실행·한계](evidence/beta-20260909-key-replay.md). 이 항목은 위 역사적 lab 기록을 대체하지
않으며, 구형 row에서 이미 사라진 최초 응답 metadata를 복구했다는 의미가 아니다.

첫 접속 후속은 [2026-09-09 browser 기록](evidence/beta-20260909-browser-join.md)을 따른다.
세 엔진의 최초 응답 전체 유실/동시 5개 탭/쿠키 차단, 두 독립 Gateway의 12개 HTTP 재시도와
만료 후 새 순번, 최대 URL 쿠키 크기 및 실제 HTTPS Secure/HttpOnly 회복을 검증했다.
원인 미상 과거 장애와 최종 고정 이미지의 장시간 복구 검증은 별도 출시 조건이다.

### 2026-09-10 M2 로컬 단계 수용

M2 로컬 수용은 **GO**다. 고정 서비스 이미지 e2cb0b9의 488개 공개 장애 조합·72개
기본 조합, 실제 Valkey 일시 중단의 네 API 503과 재개 후 같은 대기표 복귀,
10K 전원 조회·콜드 보존·FIFO 입장을 검증했다. 확인된 join 응답은 보조 poll 등록
실패로 덮지 않으며 선행 quota와 실제 status/claim/heartbeat 제한은 유지한다.
[수정 전후 회귀와 고정 이미지 증거](evidence/beta-20260910-m2-latency.md).
운영 proxy/NAT·지속 부하·보조기기 실사용과 전체 SUB-PRD delivery 판정은 별도다.

### 2026-09-12 대기 순번·예상 범위

- [x] 실제 큐 순위와 최근 promotion·설정 한도로 status의 선택적 순번/예상 범위를 제공한다. join 재시도 응답, 입장 판정과 TTL은 유지한다.
- [x] KR/EN 순번·시간 범위·중지·오류·재조회 표시, 기존 자동 입장 및 다중 탭 회귀 3엔진 39개 PASS.
- [x] 실제 Valkey에서 뒤쪽 합류/앞쪽 입장/읽기 전용/READY 무변경 검증 PASS. `make check` PASS. [정확한 계산 범위](operators/page-and-wait-progress.md).

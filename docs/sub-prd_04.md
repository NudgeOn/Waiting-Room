---
schema: waiting-room.sub-prd/v1
id: SUB-PRD-04
title: Admin API, RBAC, TOTP and Admin UX
version: 0.1.0
spec_status: ready_for_implementation
delivery_decision: NO-GO
evidence_status: PARTIAL
depends_on: [SUB-PRD-01, SUB-PRD-02]
blocks: [SUB-PRD-05, SUB-PRD-06, SUB-PRD-07]
milestones: [M0, M3]
last_updated: "2026-09-10"
---

# SUB-PRD-04 — Admin API·RBAC·TOTP·UX

> 읽기 순서: [main-prd.md](main-prd.md) → SUB-PRD-01·02 → 이 문서. Admin 권한·인증 의미와 관리 화면 흐름의 source of truth다. 암호 key 저장·rotation은 SUB-PRD-05가 소유한다.

## 1. 목표

설치는 개발자가 안전하게 bootstrap하고, 이후 비개발 운영자가 최소 화면에서 Room을 생성·운영·검증한다. API와 UI는 같은 RBAC policy를 사용하며 TOTP ON/OFF 전환 중 MFA 없는 session이 남지 않는다.

## 2. 범위와 비범위

### 포함

- `/api/admin/v1` OpenAPI와 optimistic concurrency
- one-time bootstrap, local account, session, CSRF, reauthentication
- Admin/Operator/Viewer RBAC
- TOTP ON/OFF, `forced_on`, enrollment, recovery, reset
- config·runtime·event·user·security·audit 관리
- `/setup`, `/auth/*`, Dashboard, Room workspace, `/settings`
- 360px 운영과 WCAG 2.2 AA 핵심 flow

### 비범위

- queue atomic implementation: SUB-PRD-02
- public Web/App API: SUB-PRD-03
- key storage/rotation와 Valkey HA: SUB-PRD-05
- cloud/cluster provisioning, SSO, multiple organizations

## 3. 역할과 중앙 권한표

| 기능 | Admin | Operator | Viewer |
|---|---:|---:|---:|
| Dashboard·metrics·설정·audit 조회 | 허용 | 허용 | 허용 |
| Room 생성·origin·route·theme·첫 활성화 | 허용 | 거부 | 거부 |
| AUTO/HOLD·유량·event·safe drain | 허용 | 허용 | 거부 |
| 즉시 OFF·새 epoch | 허용+재인증 | 거부 | 거부 |
| Traffic Lab 기능 시험 | 실행 | 실행 | 결과 조회 |
| 사용자·역할·TOTP policy·key action | 허용+재인증 | 거부 | 거부 |

API middleware와 UI capability adapter가 동일한 versioned policy table을 사용한다. 권한이 없는 사용자에게 실행 control을 disabled 상태로 보여주지 않고 read-only 결과만 표시한다.

마지막 활성 Admin은 삭제·비활성화·강등할 수 없다. 마지막 Admin credential 복구는 UI/API가 아닌 server의 one-time `wrctl recover-admin`만 허용한다.

## 4. Admin endpoint 계약

Base path는 `/api/admin/v1`이다.

| Method | Path | 최소 권한·의미 |
|---|---|---|
| POST | `/bootstrap` | one-time install token으로 첫 Admin 생성 |
| POST | `/auth/login` | password 확인, session 또는 TOTP challenge |
| POST | `/auth/totp/verify` | login challenge 완료 |
| POST | `/auth/totp/enroll` | enrollment secret·local QR 시작 |
| POST | `/auth/totp/enroll/verify` | code 확인, recovery code 10개 1회 표시 |
| POST | `/auth/totp/recover` | recovery code 1회 소비 |
| POST | `/auth/reauth` | action-bound one-time reauth token |
| POST | `/auth/logout` | current session revoke |
| GET | `/auth/me` | user·role·MFA·capability |
| GET | `/capabilities` | policy/profile/feature capability |
| GET/PUT | `/config` | Viewer read, Admin update |
| POST | `/config/validate` | draft route/event/config 검사 |
| GET/PATCH | `/rooms/{id}/runtime` | Viewer read, Operator update |
| GET/POST | `/rooms/{id}/events` | Viewer read, Operator create |
| PUT/DELETE | `/events/{id}` | Operator update/cancel |
| GET/POST | `/users` | Admin list/create |
| PATCH/DELETE | `/users/{id}` | Admin update/delete, last-Admin guard |
| POST | `/users/{id}/totp-reset` | Admin+reauth, target session revoke |
| GET/PUT | `/security/totp` | Admin+reauth global policy |
| GET | `/audit-events` | Viewer, cursor pagination |

- mutating command는 `Idempotency-Key`가 필수이고 결과는 PostgreSQL transaction에 24시간 보관한다.
- config PUT은 config ETag, runtime PATCH와 event transition은 runtime ETag의 `If-Match`를 요구한다.
- 누락은 428, stale revision은 412, 같은 idempotency key의 다른 request digest는 409다.
- manual runtime override는 active event를 `paused_by_override`로 만들며 resume/cancel이 필요하다.

Room config의 최소 stable shape는 다음과 같다.

```json
{
  "queuePolicy": {
    "kind": "fifo",
    "ticketIdleTtlSeconds": 600,
    "ticketMaxTtlSeconds": 86400,
    "readyTtlSeconds": 120
  },
  "limits": {
    "maxActiveAdmissionLeases": 1000,
    "admissionsPerMinute": 600,
    "admissionTtlSeconds": 900
  }
}
```

v1은 `queuePolicy.kind=fifo`만 허용하고 다른 값은 422다. `maxActiveAdmissionLeases`, rate와 TTL은 SUB-PRD-02·06의 profile bound를 통과해야 publish된다.

## 5. Session·password·reauth

- session ID는 256-bit opaque random이며 PostgreSQL에는 hash만 저장한다.
- cookie는 `__Host-wrs; Secure; HttpOnly; SameSite=Strict; Path=/`다.
- idle TTL 30분, absolute TTL 8시간이고 login·privilege change 때 session ID를 rotate한다.
- state-changing request는 exact same-origin `Origin`과 session-bound `X-CSRF-Token`이 모두 필요하다.
- password는 parameter version이 있는 Argon2id로 hash하고 설치 reference host에서 약 250~500ms가 되도록 calibration한다.
- login은 account·rotating source fingerprint·installation bucket으로 throttle한다.
- TOTP challenge는 5분 또는 5회 실패 중 먼저 도달하면 폐기한다.
- 오류는 account 존재 여부와 password/TOTP 중 어느 단계가 틀렸는지 구분하지 않는다.

`POST /auth/reauth`는 password와 policy가 ON이면 TOTP를 다시 확인한다. 성공 결과는 session, action class, target resource ID와 request digest에 묶인 one-time token이며 5분 안에 `X-Reauth-Token`으로 한 번만 사용할 수 있다.

위험 action은 TOTP OFF, 즉시 OFF, 새 epoch, key emergency revoke, last-Admin에 영향을 줄 수 있는 변경이다.

## 6. TOTP 계약

- policy는 `configurable` 또는 deployment `forced_on`, 값은 ON/OFF다.
- 기본은 `configurable + ON`; `forced_on`은 UI/API OFF를 403으로 거부한다.
- ON이면 모든 역할이 첫 login 전에 enrollment를 완료해야 한다.
- TOTP는 6-digit, 30-second, HMAC-SHA1, ±1 step이다.
- 같은 user의 `lastAcceptedCounter`는 transaction에서 원자 갱신해 같은 step replay 한 건만 성공시킨다.
- secret은 local QR와 manual key로 제공하고 외부 QR service를 사용하지 않는다.
- recovery code 10개는 한 번만 보여주고 Argon2id hash로 저장·원자 소비한다.

정책 전환:

- ON→OFF: Admin password+현재 TOTP reauth가 필요하다.
- OFF→ON: password reauth 후 현재 Admin의 enrollment·verify가 먼저 완료돼야 commit된다.
- 양방향 모두 global `authPolicyVersion`을 증가시킨다.
- 현재 재인증된 Admin session만 새 version으로 rotate하고 나머지 Admin·Operator·Viewer session은 즉시 revoke한다.
- 미등록 사용자는 다음 login에서 enrollment 전 Dashboard에 들어갈 수 없다.
- user TOTP reset·role 변경은 해당 user session version과 session을 즉시 revoke한다.

## 7. Audit 계약

각 event는 UTC timestamp, actor ID/role, action, target type/ID, redacted before/after digest, result, request ID, config/runtime revision을 가진다. password, TOTP secret/code, recovery code, session/token, return target와 raw IP는 기록하지 않는다.

필수 event는 login/lockout, TOTP enroll/reset/policy, user/role, config publish/rollback, mode/rate/event, drain/instant OFF/new epoch, key operation, backup/restore와 benchmark 시작·결과다.

관리 API problem code는 `UNAUTHENTICATED` 401, `TOTP_INVALID_OR_REPLAYED` 401, `FORBIDDEN` 403, `TOTP_POLICY_LOCKED` 403, `NOT_FOUND` 404, `IDEMPOTENCY_CONFLICT` 409, `ROUTE_CONFLICT` 409, `EVENT_OVERLAP` 409, `TOTP_ENROLLMENT_REQUIRED` 409, `REVISION_MISMATCH` 412, `UNSUPPORTED_CAPABILITY` 422와 `PRECONDITION_REQUIRED` 428을 사용한다.

## 8. Admin UX 정보 구조

| 화면군 | Route | 목적 |
|---|---|---|
| Setup | `/setup` | profile·환경·첫 Admin·TOTP·dry-run/apply |
| Auth | `/auth/*` | login·TOTP enroll/verify/recovery |
| Dashboard | `/` | system health와 Room 목록 |
| Room | `/rooms/{roomId}` | 운영·설정·일정·검증 |
| Settings | `/settings` | users·security·install info·audit |

Room workspace tab은 네 개다.

1. 실시간 운영: mode, queue/READY/lease, rate, origin, recovery
2. 기본 설정: origin, route, flow, admission, theme
3. 일정: prequeue/admit/drain one-time event
4. 검증: URL 판정, Quick 20, Smoke 1K, profile benchmark capability

Setup은 loopback one-time bootstrap UI다. remote host는 SSH tunnel을 안내하며 terminal token이 있어야 한다. 완료 뒤 bootstrap endpoint와 token은 영구 비활성화한다.

Room Wizard는 연결 → 경로 → 유량/policy → 대기 화면 → 검토/Quick 20/활성화 순서다. High Scale HA endpoint가 실제로 증명됐다고 표현하지 않고 필수 연결 검사와 운영자 확인을 구분한다.

대기 화면 단계에서 기본 제공 template 목록·미리보기·KR/EN·safe theme field를 제공한다.
`theme.templateId`는 기본 `calm`이며 [template registry](design/calm.md)의 등록 ID만 허용한다.
Admin만 변경·publish하고 Viewer/Operator는 결과만 조회한다. 선택은 Room config에 저장하며
서명된 config로 Gateway에 반영한다. 현재는 schema와 lab CLI만 존재하고 이 UI/영속 연동은 M3다.

360px에서 HOLD·상태 확인·safe drain이 가능해야 하며 Chromium·Firefox·WebKit 핵심 flow는 WCAG 2.2 AA를 만족해야 한다.

Traffic Lab의 Quick 20·Smoke 1K는 lab/sample origin으로 제한한다. production profile benchmark는 별도 명령·명시적 권한과 reference environment를 요구한다.

## 9. Acceptance criteria

2026-09-09 로컬 Beta 한정: 사용자 요청으로 VoiceOver 검증을 제외한다. 360px·키보드·세 엔진
자동 접근성 검사는 유지하며, 보조기기 사용자 인증을 PASS로 전환하지 않는다.

- [ ] 역할×endpoint×UI capability가 중앙 권한표와 완전 일치
- [ ] 모든 Admin endpoint에 method/request/response/problem schema 존재
- [ ] TOTP ON/OFF·forced_on·enroll·recovery·reset state machine이 deterministic
- [ ] policy 전환 뒤 MFA 없는 stale session의 성공 0건
- [ ] last Admin invariant를 UI/API/DB transaction 모두 보장
- [ ] concurrent config/runtime update 한 건만 commit
- [ ] 위험 action 모두 one-time action-bound reauth와 audit을 가짐
- [ ] 360px와 keyboard/screen-reader 핵심 운영 flow PASS

## 10. 구현 Checklist

- [x] Admin OpenAPI skeleton과 schema fixture
- [x] `internal/adminauth` 중앙 권한표·capability projection (API/UI 연결 전)
- [x] 세션 버전/TTL/MFA·pinned-Origin CSRF 검증 함수와 단위 테스트
- [x] RFC TOTP 검증·재사용 방지 store interface·64개 동시 요청 단위 시험
- [x] PostgreSQL TOTP challenge·counter·세션 hash 원자 저장과 두 pool 동시성 검증
- [x] 5회 실패·5분 상한·잠금 후 만료 검증 및 AES-GCM user/version/key-ID binding
- [x] Argon2id versioned hash·bounded parser·dummy verify·per-process 작업 제한
- [x] 비밀번호 검증→TOTP challenge/OFF session 및 공유 account/source/install login 제한
- [x] 해시 검증 중 계정/정책 변경 거부·실제 정책 row-lock 경합 시험
- [x] DB 세션 조회·명시적 idle 갱신·로그아웃 삭제 및 TTL/CSRF/동시성 검증
- [x] me/logout HTTP 부분 adapter·임시 TLS→PostgreSQL 시험 (production listener 아님)
- [x] 최초 Admin bootstrap service: install token hash·15분 TTL·단일 소비·완료 tombstone
- [x] TOTP ON 미등록 사용자에게 session 대신 5분 등록 전용 proof 발급·재로그인 교체
- [x] 등록 service: token-bound 비밀키 암호화·OTP/counter·복구 hash 10개·MFA session 원자 저장
- [x] 등록 5회 예약 한도·비밀키/TTL 유지·완료/회전 시 임시 비밀키 제거 (QR 미연결)
- [x] 복구 로그인 service: password challenge·OTP 공유 한도·코드/challenge/session 원자 소비
- [x] 로그인/OTP/복구 HTTP adapter·TLS/Origin/JSON 경계·보안 cookie·DB 공유 요청 제한
- [x] 분리된 loopback bootstrap HTTP·등록 Begin/Complete·실제 생성 복구 코드 로그인 (임시 TLS)
- [x] React 로컬 인증 UI: 첫 Admin·로그인·TOTP/로컬 QR·복구 코드 확인·세션/로그아웃
- [ ] 로그아웃 포함 durable command idempotency·감사 로그·완전한 API lifecycle
- [x] 로컬 Control calibration 승인·parameter 저장·재시작 적용 ([위자드](operators/setup-wizard.md)).
- [ ] 기존 계정 hash upgrade/migration 및 production reference host acceptance.
- [ ] generated types와 handler contract 일치
- [ ] central RBAC policy/middleware/UI adapter
- [ ] bootstrap operator CLI·운영 loopback listener·wizard policy 선택·endpoint shutdown 연결
- [ ] login/challenge/session/reauth state machine
- [ ] Argon2id calibration과 login/TOTP throttle
- [ ] global/user auth policy version
- [ ] TOTP enrollment/recovery/reset atomic transaction
- [ ] last Admin invariant
- [ ] config/runtime optimistic concurrency와 manual override
- [ ] audit taxonomy·redaction·pagination
- [ ] 5 route·4 Room tab Admin UI
- [x] Dashboard/Room/Settings 주소 연결·4개 tab navigation·직접 접속/reload/history·5단계 초안 작성 부분 구현 ([workspace](evidence/admin-workspace-summary.md)). 검증 탭 전체 기능은 미완료.
- [ ] Room별 template 선택·미리보기·저장·signed publish 및 권한 검증
- [ ] responsive/a11y/browser test fixtures

## 11. Unit test Checklist와 결과

진입점: `make test-unit PRD=04` — auth core/vault 단위 suite.
DB 및 me/logout 임시 TLS 통합은 `WR_TEST_AUTH_DB=local make test-auth-db`다.
전체 인증 API와 실제 UI 통합은 후속이다.

| UT-ID | Unit test | 기대 결과 | 현재 결과 | Evidence |
|---|---|---|---|---|
| UT-04-01 | role×endpoint matrix | allow/deny 전부 일치 | PARTIAL | 3 roles × 19 actions PASS; endpoint/UI 미연결; [auth core](evidence/admin-auth-summary.md) |
| UT-04-02 | cookie/session expiry·rotation | flag와 idle/absolute 규칙 일치 | PARTIAL | DB 만료/갱신/삭제·logout cookie clear PASS; 최초 발급·권한 변경 rotation 후속; [sessions](evidence/session-service-summary.md) |
| UT-04-03 | CSRF/Origin | 누락·cross-origin 거부 | PARTIAL | DB mutation 무변경 및 실제 TLS logout 거부 PASS; 전체 Admin middleware 후속; [sessions](evidence/session-service-summary.md) |
| UT-04-04 | login/TOTP throttle | 5회 challenge 폐기·정보 비노출 | PARTIAL | DB 5회 TOTP guard, account/source/install login 제한·만료 갱신·공유 pool PASS; HTTP timing/traffic qualification 후속; [login slice](evidence/password-login-summary.md) |
| UT-04-05 | RFC TOTP vector/replay race | valid window, same counter 1건 성공 | PARTIAL | RFC core와 PostgreSQL 두 pool·같은/다른 challenge 각각 64개 중 1건 성공 PASS; 전체 login/enrollment/recovery는 후속; [DB slice](evidence/admin-store-summary.md) |
| UT-04-06 | recovery-code race | 같은 code 1건 성공 | PARTIAL | DB 동일/다른 challenge·다른 code·OTP 경쟁 결과는 [recovery](evidence/recovery-summary.md); HTTP/UI는 후속 |
| UT-04-07 | policy transition | all-role stale session 0 | PASS | PG 역할별 session revoke·현재 Admin rotation과 실제 Docker OFF→ON→OFF cookie/CSRF 교체; [62250138](evidence/waiting-room-local-beta-test-62250138.json) |
| UT-04-08 | forced_on | OFF 요청 403 | PARTIAL | PG API 403 PASS; 배포 forced_on 선택 연결 후속; [runtime 기록](evidence/beta-runtime-progress.md) |
| UT-04-09 | last Admin | delete/disable/demote 거부 | PASS | PostgreSQL API/DB trigger guard·두 writer 경쟁·브라우저 disabled guard; [runtime 보안 기록](evidence/beta-runtime-progress.md) |
| UT-04-10 | action-bound reauth | wrong target/digest/reuse 거부 | PASS | target/exact bytes/session/revision, 8개 동시 소비 중 1건, audit failure rollback; [runtime 보안 기록](evidence/beta-runtime-progress.md) |
| UT-04-11 | config/runtime race | expected revision 한 건만 commit | PARTIAL | draft 16경쟁 1 commit·15 stale, 2 pool/24h replay PASS; runtime publish 후속; [Beta 개발](evidence/beta-development.md) |
| UT-04-12 | audit redaction | secret·PII 필드 0 | PARTIAL | draft audit digest/secret 비포함·audit 실패 전체 rollback PASS; 전체 운영/auth audit 후속; [Beta 개발](evidence/beta-development.md) |
| UT-04-13 | TOTP secret type / invalid input | CSPRNG·Base32·기본 로그/JSON redaction·잘못된 code 무변경 | PASS | secret/parser 단위 검증; 실제 audit pipeline 아님; [auth core](evidence/admin-auth-summary.md) |
| UT-04-14 | encrypted credential binding / atomic session | user/version/key-ID 변조 거부·counter/challenge/session 동시 commit | PARTIAL | AEAD binding·INSERT 오류 rollback·unknown commit 단위 실패 처리 PASS; key-file/rotation·실제 commit 응답 유실 주입 후속; [DB slice](evidence/admin-store-summary.md) |
| UT-04-15 | password hash / login proof | 과도한 KDF·틀린 암호·오래된 snapshot 거부 | PARTIAL | Argon2id·dummy·bounded parser·동시 변경은 [login slice](evidence/password-login-summary.md), 부분 [HTTP](evidence/auth-http-summary.md); 설치 calibration 후속 |
| UT-04-16 | session current-state / logout race | 최신 role/policy, 삭제 session 부활 0 | PARTIAL | 두 pool 경쟁·실제 policy wait·만료 wait·쓰기 rollback PASS; 명령 idempotency/audit 후속; [sessions](evidence/session-service-summary.md) |
| UT-04-17 | bootstrap / enrollment restriction | 최초 Admin 1건·설치 token 재사용/만료 거부·ON session 0 | PARTIAL | [bootstrap DB](evidence/bootstrap-summary.md), [설정/등록 HTTP](evidence/provisioning-http-summary.md); CLI·운영 listener·wizard 후속 |
| UT-04-18 | initial TOTP enrollment / recovery storage | 5회 상한·등록 경쟁 1건·실패 시 credential/recovery/session 0 | PARTIAL | [enrollment DB](evidence/enrollment-summary.md), [등록→복구 HTTP](evidence/provisioning-http-summary.md); QR/UI·재발급 후속 |
| UT-04-19 | recovery challenge / atomic grant | password proof 필수·OTP 공유 5회·stale/실패 시 소비 0 | PARTIAL | unit/DB 결과는 [recovery](evidence/recovery-summary.md); 설치 부하·전체 HTTP/audit는 후속 |
| UT-04-20 | auth HTTP / issued cookie / shared limit | 교차 출처·모호한 입력 거부·인증 전 cookie 0·출처/설치 상한 | PARTIAL | [auth HTTP](evidence/auth-http-summary.md); 생성 client·실제 browser·운영 listener는 후속 |
| UT-04-21 | bootstrap/enrollment HTTP | local 설정 경계·등록 proof 분리·완료 전 session/복구 0·재사용 거부 | PARTIAL | [provisioning HTTP](evidence/provisioning-http-summary.md); listener lifecycle·wizard·browser는 후속 |
| UT-04-22 | local auth UI / browser | 실제 DB setup→enroll→recovery→reload/logout·secret 비저장 | PARTIAL | [Admin UI](evidence/admin-ui-summary.md); unit 5 + Chromium ON/OFF·360px, full a11y·Room UI 후속 |
| UT-04-23 | console route boundary | exact Room/tab·잘못된 경로 거부·setup의 운영 route 차단 | PASS | JS 2 tests + Go route/race suite, Admin unit 총 20; [workspace](evidence/admin-workspace-summary.md) |
| UT-04-24 | read-only URL route diagnosis | 정규 URL·기존 matcher·strict HTTP·draft/published 분리·역할/CSRF·저장 상태 무변경 | PASS | Go domain/HTTP race·PG integration PASS, contract 15; [URL 판정](evidence/admin-route-check-summary.md) |

### Unit test 실행 로그

#### 로컬 Docker Control 추가 Checklist — 2026-09-06

- [x] strict draft JSON·route/limits/theme domain 및 HTTP unit 실행.
- [x] draft DB 원자성·revision 경쟁·재시도·RBAC/CSRF·audit rollback 시험 실행.
- [x] Room 작성/저장/재조회·경쟁 412·360px 브라우저 시험 실행.
- [x] 영속 Docker TOTP 등록·DB/Control 재시작·신규 TOTP 로그인 실행.
- [x] 로컬 Room publish/ACK·모드/유량/예약 runtime 연결 (전체 profile qualification과 구분).
- [x] action-bound 재인증·계정/RBAC/last Admin API와 DB 동시성.
- [x] TOTP 정책 ON/OFF·forced_on·OFF Admin enrollment·session/CSRF rotation.
- [x] 실제 Docker browser 계정 관리·OFF→등록→ON→OFF·즉시 OFF·360px 및 dialog Escape/focus.
- [x] 62250138: 실제 Valkey 재시작 후 system 복구 감사와 UTC 발생 시각 UI, live OpenAPI 응답 검증.
- [x] 6e55a23e: 반복 upgrade가 변경한 OFF policy·계정·초안·key binding을 보존, init의 덮어쓰기 거부.
- [x] 보안 boolean 필드 누락/null 거부 단위 시험; 2026-09-06 PG policy/users/reauth/security 회귀 PASS (5.558s).
- [x] 예약 수정 UI·자동 갱신·실제 scheduler HOLD→수동 pause→AUTO resume→DRAINING·360px ([50f6d88a](evidence/waiting-room-local-beta-test-50f6d88a.json), 8 checks PASS).
- [x] 예약 시간 UTC 직렬화·유효하지 않은 날짜/과거/순서/366일 범위 단위 시험 PASS (Admin unit 17 기준).
- [x] 최초 Admin·login/lockout/logout/enrollment/recovery 감사 원자성 PG 전체 race PASS (193.039s), 실제 Docker 감사·재시작·upgrade [a05d423f](evidence/waiting-room-local-beta-test-a05d423f.json) PASS.
- [x] Firefox 3 tests (7.5s), WebKit 3 tests (13.2s): auth lab ON/OFF bootstrap·등록/복구·세션/reload/logout·Room 저장/충돌/360px PASS. 전체 Docker 운영 기능의 3종 검증은 별도다.
- [x] 로컬 Dashboard/Room, 3역할·3엔진 81 화면 상태의 axe/320px/키보드, 실제 API 응답 계약과 감사 taxonomy 회귀 ([재현](operators/admin-validation.md)).
- [ ] 실제 보조기기 사용자 및 production 설치 acceptance.

#### Workspace 후속 Checklist — 2026-09-06

- [x] `make check` PASS: Go vet/race·Admin unit 20·기존 contract/schema/PRD 검사.
- [x] Chromium/Firefox/WebKit 12 tests PASS (20.4s): 실제 PG 인증/5단계 생성/저장/reload/history/412 입력 보존 9개 + native 날짜 0~59초 경계 3개.
- [x] [36780af4](evidence/waiting-room-local-beta-test-36780af4.json) Docker 17 checks PASS: 실제 운영/주소/검증 조회/예약/Valkey 복구/보안, 360px 및 예상 밖 console 오류 0.
- [x] [672fce77](evidence/waiting-room-local-beta-test-672fce77.json) 새 설치/5단계 저장/재시작/재로그인/upgrade 8 checks PASS.
- [x] 읽기 전용 URL 판정: 단위/PG PASS, Docker 8개 URL 시나리오·360px·console 오류 0 ([근거](evidence/admin-route-check-summary.md)).
- [x] 로컬 설치 wizard의 plan/apply·calibration·first Admin/TOTP와 콘솔 이동.
- [x] 검증 탭 Quick 20·Smoke 1K 실행·결과·중지·다운로드 ([Traffic Lab](operators/traffic-lab.md)).
- [x] 로컬 전 역할·axe·키보드 회귀.
- [ ] production 설치 및 실제 VoiceOver/NVDA 사용자 acceptance.
- 상세 실패 원인·시험 수정·이미지 identity: [workspace 근거](evidence/admin-workspace-summary.md).

2026-09-09 통합 후보에서는 Quick 20 방문자의 새 쿠키 확인 절차를 연결하고
backend 통합 CI에 해당 시나리오를 추가했다. 관리자 21개 browser 회귀와 같은 Docker
이미지의 81개 화면/32개 API 계약·키 회전·Admin 새 epoch ACK·Control 중단 회복을 통과했다.
[실행 기록](evidence/beta-20260909-browser-join.md). 수동 보조기기 acceptance와 공개 출시 판정은 별도다.

#### 운영 보안 추가 검증 — 2026-09-06

- `WR_TEST_AUTH_DB=local go test -tags integration ./internal/adminauth/pgstore -run 'TestPolicy|TestUsers|TestReauth|TestEnrollment' -count=1`: PASS (13.909s).
- `npm run build:admin && npm run test:admin-ui`: build PASS, 13 tests PASS.
- `WR_TEST_LOCAL_BETA=local WR_TEST_LOCAL_SECURITY=1 node test/localbeta/runtime-quick.mjs`:
  [ff78a6ec](evidence/waiting-room-local-beta-test-ff78a6ec.json) 10 checks PASS, source unchanged.
- 판정: **부분 기능 GO / SUB-PRD-04 delivery NO-GO**. 전체 API/OpenAPI 일치,
  감사 taxonomy·Room workspace·다중 브라우저/a11y acceptance는 계속 진행한다.

운영 보안 API 추가 계약:

- user PATCH/DELETE/reset은 `If-Match: "user-N"`, policy PUT과 enrollment 준비는
  `If-Match: "policy-N"`을 사용한다. 삭제 ID는 감사 이력을 위해 재사용하지 않는다.
- reauth `requestDigest`는 UTF-8 `wr-action/v1\n{METHOD}\n{targetId}\n{If-Match}\n{exact body}`의 SHA-256 소문자 hex다.
  body 없는 DELETE는 마지막 줄 뒤 0 bytes다. `X-Reauth-Token`을 같은 명령에 제출한다.
- OFF Admin의 등록 준비는 POST `/security/totp/enrollment` (`{}`, reauth target `totp-enrollment`).
  반환 proof는 현재 session에 결합된다. POST `/security/totp/enrollment/start|verify`는
  cookie+CSRF와 `{challengeToken, code?}`를 함께 검증한다. 기존 pre-auth API는 cookie를 계속 거부한다.
- policy PUT reauth target은 `totp`이며, 이미 등록한 Admin은 OFF→ON에도 새 TOTP로 확인한다.
- policy mutation의 durable JSON에는 세션 비밀을 저장하지 않는다. 확인된 최초 commit만
  `Set-Cookie`와 `X-CSRF-Token` 응답 header로 session을 교체한다. 응답 유실 시 새로 로그인하고
  GET policy로 확인한다. 인증된 exact command replay는 추가 session/정책 변경을 만들지 않는다.

결과와 scope: [Beta 개발 기록](evidence/beta-development.md), [Docker 재시작 7 checks](evidence/waiting-room-local-beta-test-9ee8c0c0.json).
명령 `WR_TEST_AUTH_DB=local go test -tags=integration -race ./internal/adminauth/pgstore ./internal/adminauth/controlhttp` PASS,
`make check` PASS, `npm run test:admin-browser` 3 PASS. 배포/최종 통합 테스트 PASS가 아니다.

| Run | Commit | Command | Passed/Failed | Result | Evidence |
|---|---|---|---:|---|---|
| M0-20260905 | uncommitted snapshot | `make lint-api`; `make test-contract` | Public/Admin 공통 schema 6/0, lint 2/0 | PASS | [M0](evidence/m0-summary.md); TOTP/RBAC runtime은 NOT RUN |
| Auth-core-local | uncommitted snapshot | `make test-unit PRD=04` | 17/0 | PARTIAL | [auth core](evidence/admin-auth-summary.md); HTTP/DB/UI 미구현 |
| Auth-PostgreSQL-local | uncommitted snapshot | `go test -race -v ./internal/adminauth/...`; `WR_TEST_AUTH_DB=local make test-auth-db` | core/vault unit 21/0, PG integration 6/0 (subtest 제외) | PARTIAL | [DB slice](evidence/admin-store-summary.md); 기존 unit 17 + 신규 unit 4, 실제 HTTP/UI 미연결 |
| Password-login-local | uncommitted snapshot | `make test-unit PRD=04`; `WR_TEST_AUTH_DB=local make test-auth-db` | unit·DB 최종 집계는 evidence 참조 | PARTIAL | [login slice](evidence/password-login-summary.md); 신규 password/fingerprint unit 5, password DB 시나리오 7; HTTP/UI 미연결 |
| Session-service-local | uncommitted snapshot | `make test-unit PRD=04`; `WR_TEST_AUTH_DB=local make test-auth-db` | 최종 집계는 evidence 참조 | PARTIAL | [sessions](evidence/session-service-summary.md); 신규 unit 4 + session DB 8 + TLS HTTP 1; 전체 UI 미연결 |
| Bootstrap-service-local | uncommitted snapshot | `make test-unit PRD=04`; `WR_TEST_AUTH_DB=local make test-auth-db` | 최종 집계는 evidence 참조 | PARTIAL | [bootstrap](evidence/bootstrap-summary.md); 신규 redaction/input unit 1 + bootstrap DB 7; 등록 proof만 구현 |
| Enrollment-service-local | uncommitted snapshot | `make test-unit PRD=04`; `WR_TEST_AUTH_DB=local make test-auth-db` | 최종 집계는 evidence 참조 | PARTIAL | [enrollment](evidence/enrollment-summary.md); 신규 unit 3 + enrollment DB 5; 수동 키 service, QR/복구 로그인 미구현 |
| Recovery-service-local | uncommitted snapshot | `make test-unit PRD=04`; `WR_TEST_AUTH_DB=local make test-auth-db` | 최종 집계는 evidence 참조 | PARTIAL | [recovery](evidence/recovery-summary.md); 신규 unit 2 + recovery DB 5; service만 구현 |
| Auth-HTTP-local | uncommitted snapshot | `make check`; `WR_TEST_AUTH_DB=local make test-auth-db` | 최종 집계는 evidence 참조 | PARTIAL | [auth HTTP](evidence/auth-http-summary.md); 신규 HTTP unit 3 + shared-limit DB 1 + TLS HTTP 1 (HTTP/1.1·2 subtest) |
| Provisioning-HTTP-local | uncommitted snapshot | `make check`; `WR_TEST_AUTH_DB=local make test-auth-db` | 최종 집계는 evidence 참조 | PARTIAL | [provisioning HTTP](evidence/provisioning-http-summary.md); 신규 unit 2 + TLS integration 1 (HTTP/1.1·2 × ON/OFF subtest) |
| Admin-UI-local | uncommitted snapshot | `make check`; `npm run test:admin-browser` | 최종 집계는 evidence 참조 | PARTIAL | [Admin UI](evidence/admin-ui-summary.md); 실제 인증 화면만, Room/dashboard 운영은 미연결 |

## 12. GO/NO-GO 판정

- 명세의 구현 착수 준비: **GO**
- 현재 delivery 판정: **NO-GO**
- 이유: 로컬 runtime publish·계정/정책 원자 전환·재인증·인증/복구 감사·예약과 Dashboard/Room 주소/탭·5단계 초안 작성·읽기 전용 URL 판정까지 부분 검증했다. 로컬 설치 wizard/calibration 후속은 [설치 위자드](operators/setup-wizard.md)를 따른다. 고정 샘플 Traffic Lab은 연결했다. 구현된 로컬 명령의 재시도·감사와 browser/axe 검증은 [관리자 검증](operators/admin-validation.md)을 따른다. Production 및 실제 보조기기 acceptance는 별도다.
- 구현 경계와 다음 연결 순서: [auth core 계약](security/admin-auth-core.md). 위험 action은 일회성 reauth가 연결되기 전 core에서 거부한다.
- GO 조건: checklist와 unit/contract/browser/a11y test PASS, SUB-PRD-01·02 GO, P0/P1 0건, reviewer·UTC 시각 기록.

### 운영·보안 회귀 — 2026-09-08

- 로그아웃 24시간 해시 영수증과 단일 감사, 8중 동시 재시도·감사 실패 롤백.
- runtime 6종(새 epoch 포함)·일정 4종·사용자 4종의 감사 실패/응답 유실/중복 키 DB 회귀 14개, 기존 전체 PostgreSQL 인증/보안 race PASS.
- 3엔진 × 3역할 × 9개 실제 운영 화면에서 axe 위반·320px 넘침 0. 본문 건너뛰기, 재인증 Tab 순환·Escape 복원, 응답 유실 후 키보드 재시도 검증.
- 인증/설치/Room/Traffic 브라우저 회귀 21개 전체 3엔진 PASS. 실제 응답의 OpenAPI 참조/schema 검증 포함.
- 색상 대비·긴 감사 식별자 줄바꿈·감사 cursor 조회·권한 없는 보안 화면·예약 JSON 공백 처리 수정.
- 범위와 실행 명령: [관리자 운영·보안 검증](operators/admin-validation.md). 실제 보조기기 인증과 production GO는 포함하지 않는다.
- 새 epoch는 Admin 재인증·Room revision·설치 전체 generation에 결합하고 모든 Room과 예약을 한 DB transaction으로 HOLD/일시정지한다. 확인 이후 다른 배포가 있으면 412와 거부 감사 1건을 남기며 epoch를 바꾸지 않는다. [복구 절차](operators/recovery-upgrade.md).

### 2026-09-10 최신 로컬 기술 검증

같은 고정 서비스 이미지 e2cb0b9의 Admin/Operator/Viewer × 세 브라우저 × 9개 화면,
총 81개 화면의 320px·키보드·자동 WCAG 검사와 32개 관측 API 계약이 PASS다.
Quick 20/Smoke 1K 실행·저장 결과·감사 한 건·재시작 보존을 확인했다.
[고정 이미지 증거](evidence/beta-20260910-m2-latency.md).
실제 비개발 운영자의 용어 이해·독립 과제 수행은 미실행이며 M3/B6 수용으로 남긴다.
VoiceOver 제외를 사람의 보조기기 사용 검증 PASS로 바꾸지 않는다.

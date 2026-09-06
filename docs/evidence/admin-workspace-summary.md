# Admin workspace / Room wizard — 2026-09-06

Owner: SUB-PRD-04. **Beta NO-GO 유지. 이 문서는 부분 구현/회귀 근거다.**

## 변경

- `/`: draft/delivery revision, 양 role 응답/generation, Room 목록. 조회 실패를 정상으로 표시하지 않는다.
- `/rooms`, `/rooms/new`: 초안 목록과 연결 → 경로 → 유량 → 대기 화면 → 검토의 5단계 생성.
  뒤로 이동해도 입력 유지. 저장 성공 후 실제 Room 설정 주소로 이동한다.
- `/rooms/{id}/operations|settings|schedule|verification`: 4개 화면, 직접 접속·reload·history 지원.
  `/rooms/{id}`는 운영 화면이다. `new` ID의 Room은 명시적 `/operations` 주소로 이동한다.
- `/settings`: 기존 실제 사용자/보안 기능 연결. TOTP 등록 완료 시 해당 화면으로 복귀한다.
- 서버는 허용한 HTML 경로만 제공한다. Setup listener는 운영 route를 제공하지 않고,
  모든 데이터 API의 인증/권한/CSRF 경계는 그대로다. 비로그인 직접 접속은 로그인 화면이다.
- `verification`은 현재 양 role 적용/원본 상태만 표시한다. URL 판정·Quick 20·Smoke 1K는
  **미지원**으로 명시한다. 원본에 시험 트래픽을 자동 발생시키지 않는다.
- React 경로 state는 `useSyncExternalStore`, 선택 Room은 route+응답에서 도출한다.
  독립 draft/delivery 조회는 병렬이며 별도 권한 source를 만들지 않는다.

## 단위/브라우저 Checklist

- [x] `make check`: Go vet/race, script 10, contract 14, Admin unit 20,
  install schema 4, 9 PRD 검사, 양 OpenAPI lint PASS.
- [x] 신규 JS route unit 2개: exact ID/tab, 외부/encoded/query/unknown/prototype path 거부.
- [x] 신규 Go route 단위 suite: 운영 whitelist, setup 차단, 잘못된 경로 거부. race PASS.
- [x] Chromium/Firefox/WebKit 각각 3개, **9 tests PASS (19.5s)**.
  실제 PG Lab ON/OFF bootstrap·TOTP 등록/복구·logout/reload, 5단계 초안 저장,
  기존 입력 유지, 저장 후 Room 설정 reload, tab/back/forward, stale revision 412와 입력 보존.
- [x] 1586×992 / 360×900 화면: 의미 있는 페이지/제목/입력, overflow 없음,
  heading/단계 focus, 비로그인 Room 설정 숨김, 앱 JavaScript 오류/예상 밖 console 경고 없음.
- [x] Playwright WebKit 캡처의 empty stylesheet 삽입이 strict CSP에 거부되는 알려진
  도구 경고는 **캡처 전 앱 경고 0 → 캡처 중 정확히 한 건 → 이후 앱 경고 0**으로 검사.
  `style-src`나 앱 오류 필터를 완화하지 않았다. Browser plugin not available로 저장소 Playwright 사용.
- [x] 최종 **12 browser tests PASS (20.4s)**: 위 9개에 실제 native datetime-local의
  0~59초 입력 회귀를 브라우저별로 추가했다. 앱 시험과 synthetic 입력 경계 시험을 구분한다.

명령: `npm run build:admin`, `npm run test:admin-ui`,
`go test -race ./internal/adminserver`, `make check`,
`PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" npx playwright test --config playwright.admin.config.mjs --browser=all`.

최종 3종 Lab 캡처(WebKit): `/private/tmp/wr-workspace-final-3XaV3e/room-desktop.png`,
`/private/tmp/wr-workspace-final-3XaV3e/room-mobile.png`. 이미지/비밀정보는 Git에 추가하지 않는다.

## Docker 회귀

첫 실행 [ef687298](waiting-room-local-beta-test-ef687298.json)은 **FAIL / 11 checks PASS**다.
Room 직접 URL/reload/검증 탭, signed publish/양 ACK, Web/App 실제 입장, 두 role 재시작,
Valkey restart 43 denial probes, 360px 예약/실제 stage와 복구 audit,
계정 생성/강등/삭제까지 통과했다. 그 뒤 TOTP 등록 완료로 이미 `/settings`에 복귀한
화면의 같은 nav link를 시험이 다시 눌러 heading focus 기대와 충돌했다.
동일 주소에 있을 때 중복 이동하지 않도록 시험을 수정했다. 실패를 전체 PASS로 바꾸지 않는다.

- [c32d23cf](waiting-room-local-beta-test-c32d23cf.json): FAIL / 6 checks PASS.
  복구 후 예약 입력 `locator.fill`에서 실패. 당시 상세 날짜/메시지를 저장하지 않아
  이전 입력값을 확정할 수는 없다. 같은 실패 지점의 초 `00` 정규화 오류를 독립 재현했다.
  Chromium에서 `2026-09-06T17:18:00`은 `locator.fill: Error: Malformed value`,
  `2026-09-06T17:18`과 `2026-09-06T17:18:01`은 PASS다.
  설치된 Playwright `coreBundle.js`도 native `input.value !== value`이면 같은 오류를 던진다.
  시험 helper에서 초 `00`만 생략하고 60개 초 경계를 3종 브라우저에서 검증했다.
  실제 앱의 초 단위 처리/서버 날짜 검증/예약 제한을 바꾸지 않았다.
- [5e7a0dee](waiting-room-local-beta-test-5e7a0dee.json): **PASS / 15 checks**.
  복구 restart를 제외한 Web/App·routing·예약·TOTP 양방향·계정/즉시 OFF·console 전체 흐름.
  이미 보안 화면으로 돌아온 뒤 불필요하게 nav를 다시 클릭하는 시험을 수정해 TOTP 후속까지 PASS.
- [672fce77](waiting-room-local-beta-test-672fce77.json): **PASS / 8 checks**.
  TOTP ON 새 설치, 5단계 Room 저장, DB/Control restart 후 직접 설정 URL/입력/config/audit 보존,
  fresh TOTP login, OFF로 변경한 정책을 repeated upgrade가 보존, init overwrite 거부.
  두 실행은 동일 runtime 이미지이며 소스 digest는 시험 진단 추가로 서로 다르다.

검증 이미지 config: `sha256:3c328cb4fbbadfcc4d7b829a3ee42737f6701a8619a1c92d029284f820d4e33c`.
이 변경에서 application/runtime 이미지를 마지막으로 만든 뒤 UI/Go는 변경하지 않았다.

최종 [36780af4](waiting-room-local-beta-test-36780af4.json): **PASS / 17 checks**,
2026-09-06 08:19:14–08:23:33 UTC. 복구 재시작·44 denial probes·360px 일정·실제 scheduler·
복구 audit·계정·OFF→등록→ON→OFF·즉시 OFF를 같은 frozen source에서 모두 실행했다.
source digest: `e442a1afb447ec9bad8464f32955c03a46c031b137b47f163c80a62bbd13e0ba`.
마지막 기본 회귀 `make check` PASS. 이후 변경은 PRD/진행 문서뿐이다.

Docker 최종 이미지 캡처 디렉터리:
`/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-runtime-aIkaN3`.
`workspace-dashboard-desktop.png`와 `workspace-dashboard-mobile.png`는 새 Dashboard,
`runtime-schedule-mobile.png`는 실제 일정의 360px 화면이다.

시험 종료 후 사용자 기본 설치 `up/status`: 기존 DB/Valkey/identity 보존, 새 이미지의
6개 서비스 모두 healthy. 계정 수는 계속 0이며 계정·bootstrap token은 만들지 않았다.
Chromium으로 실제 `https://127.0.0.1:19443/rooms` 접속: HTTP 200, 정확한 콘솔 제목,
미인증 로그인 gate PASS. 최종 문서 갱신 후 `git diff --check`와 9 PRD 검사 PASS.
실패/성공 시험의 컨테이너와 network만 정리했고 모든 volume/secret은 보존했다.
별도 commit/push/GitHub release는 하지 않았다.

## 남은 acceptance

- [ ] 설치 wizard 전체 apply/calibration, Room URL 판정·Quick 20/Smoke 1K 실행.
- [ ] 전체 역할별 UI/API·screen reader/WCAG acceptance. 세 브라우저 Docker 운영 전 경로.
- [ ] 나머지 명령 lifecycle·복구 epoch·과거 부하 503 근거 및 MAIN M1~M3 review.
- [ ] 전체 Beta GO/README beta badge. 위 부분 PASS는 출시 판정이 아니다.

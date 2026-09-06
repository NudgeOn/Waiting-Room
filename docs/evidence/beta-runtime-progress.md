# Beta runtime 연결 진행 — 2026-09-06

**Beta NO-GO. 진행 중 기록이며 전체 M1~M3 acceptance 완료가 아니다.**

최신 UI 후속: [읽기 전용 URL 판정](admin-route-check-summary.md).
단위/HTTP/PG 경계 검증 및 Docker URL·Web/App·재시작 회귀 8 checks PASS.
이전 UI: [Admin workspace / Room wizard](admin-workspace-summary.md).
Dashboard·Room 4개 주소/탭·5단계 초안 작성, 12 browser tests,
최종 Docker 복구/예약/보안 17 checks와 새 설치/재시작 8 checks PASS.
아래는 앞선 구현의 시간순 기록이며 최신 경계/시험은 위 문서를 우선한다.

## 구현 및 부분 검증

- [x] PostgreSQL migration 006: signed delivery, node ACK, one-time event 저장소.
- [x] draft publish와 운영 명령의 revision·24h idempotency·audit atomic transaction.
- [x] 신규 Room 활성화 HOLD, active route/origin 변경은 drain 요구.
- [x] 예약 overlap 거부, 수동 변경 시 paused_by_override, 명시 재개/취소와 stage catch-up.
- [x] 안전 OFF는 두 노드 current/fresh ACK, WAITING/READY 0, origin health,
  5분 유입 관측을 모두 요구. paused event를 자동 재개하지 않음.
- [x] 실제 PostgreSQL publication/runtime/events integration 테스트 PASS.
- [x] immutable Valkey `wr_queue_runtime_v3` 설정 revision과 bounded expiry/READY counter.
  실제 Valkey HOLD/AUTO/DRAINING, repeat configuration, ready/claim/expiry metrics PASS.
- [x] Control/Gateway/Coordinator/demo-origin 역할별 mTLS identity 및 키 분리.
  Gateway에 config/admission private key나 Valkey credential을 제공하지 않음.
- [x] Docker role별 identity volume subpath, Gateway와 Valkey 네트워크 분리,
  Coordinator 전용 queue ACL, 명시 owner function library 설치.
- [x] 공개 원본 DNS의 private/special IP 거부 및 검증 IP에 직접 dial.
  컴파일된 정확한 `https://demo-origin:20445`만 설치 mTLS로 private origin 허용.
- [x] 영속 설정 Gate와 ACK, Room route/namespace·HTTPS secure cookie·persistent replay/admission key 연결.
- [x] Backoffice 실제 배포·운영 상태·유량·모드·예약 화면 구현 및 build PASS.
- [x] 새 Docker 구성에서 기존 TOTP ON·초안·restart regression 7개 PASS:
  [1db37d41](waiting-room-local-beta-test-1db37d41.json).
- [x] 실제 active draft 서명 배포와 Gateway/Coordinator 두 노드 ACK,
  첫 HOLD 및 앱 join/replay/status까지 확인.
- [x] 웹 첫 navigation 원인: Chromium이 Ed25519 TLS certificate에
  `ERR_SSL_VERSION_OR_CIPHER_MISMATCH`를 반환. TLS만 P-256 ECDSA로 변경;
  config/admission application signature는 Ed25519 유지.
- [x] [a579a9a1](waiting-room-local-beta-test-a579a9a1.json): 실제 HOLD 차단,
  app join/replay/status, secure cookie와 sealed web return, AUTO promotion,
  app admission 검증·브라우저 원래 target 복귀·mTLS origin 도달,
  Gateway/Coordinator 재시작 후 정확히 같은 join/admission replay PASS.
  OFF-policy disposable fixture이며 전체 TOTP/보안/복구 acceptance와 구분.
- [ ] 실제 theme 문구·색상·언어 renderer 연결, runtime UI a11y/다중 browser 검증.
- [x] migration 007: action/target/exact UTF-8 request/revision/session에 결합한
  5분·일회성 reauth. TOTP counter 원자 소비, 동시 소비 8건 중 1건, audit 실패 롤백.
- [x] migration 008: 계정 생성/목록/역할/활성/삭제/TOTP reset API, last Admin
  API 및 DB statement trigger guard. 삭제 시 credential/proof/session 제거와 ID tombstone.
- [x] migration 009: OFF-policy Admin의 재인증 후 등록, 전역 policy와 분리된
  enrollment 완료, ON/OFF policy version 증가·현재 Admin cookie/CSRF 교체·나머지 세션 폐기.
  forced_on OFF 거부. enrollment bearer proof는 command cache에서 AES-GCM 암호화.
- [x] 실제 PostgreSQL `TestPolicy|TestUsers|TestReauth|TestEnrollment` PASS (13.742s).
  정책과 계정 변경은 감사 저장 실패 시 세션 변경까지 함께 rollback.
- [x] Backoffice 계정/보안·재인증 dialog·즉시 OFF UI, React UI build 및 단위 13 PASS.
  유량 form의 편집 revision 고정, Room event 비동기 조회 응답 순서 guard.
- [x] [ff78a6ec](waiting-room-local-beta-test-ff78a6ec.json): frozen-source Docker
  runtime + security 10 checks PASS. 계정 create/demote/delete, Operator users API 403,
  target session revoke, last Admin UI guard, OFF에서 Admin TOTP 등록·복구 10개,
  ON/OFF 실제 다음 TOTP counter와 cookie/CSRF rotation, 이전 session 401,
  action-bound instant OFF 두 runtime ACK. JavaScript pageerror 0.
  360px 보안 화면·빈 재인증 dialog·desktop ON 화면을 실제 열어 확인했다.
  Browser plugin not available; 기존 Playwright 경로 사용. QR/복구/비밀번호 입력 화면 캡처 없음.
- [x] 전체 Go `go test -p=1 ./...` PASS; 인증 경계 수정 후 targeted PG
  `TestPolicy|TestUsers|TestReauth|TestEnrollment` PASS (13.909s).
- [ ] shared fencing/unsafeUntil recovery, quota, 보안 HTTP/browser 전체 회귀,
  전체 wizard/Settings/backup-restore와 PRD 필수 acceptance.

## 실패 기록과 운영상 발견

## v4 복구 검증 추가 (2026-09-06)

- [x] immutable v3는 유지하고 v4 library를 별도 설치. Valkey의 실제 run_id와
  공유 fence를 함수 내부에서 검사한다. 쓰기 불확실·시간 역행·상태 불일치는
  공유 RECOVERY_HOLD를 만든다. 안전 대기 이후에도 Room별 최대 128개 행씩
  ticket/lease/expiry/owner/idempotency/rate 불변조건을 검증한다. 추정 복원 없음.
- [x] candidate-02 v4 소스 SHA-256
  `31bde639c6851f84deff73ad4d05ac190a5458c65c8d0b09da3e5bc8fca3d68e`.
  두 client 동일 unsafeUntil/fence·오래된 fence 거부·owner index 손실 시 지속 HOLD,
  기존 runtime regression PASS (0.691s).
- [x] 실제 AOF 보존 candidate Valkey 재시작 PASS (91.639s). 안전 대기 90초를
  실제로 경과시켰으며 server clock/counter 수정 없음. 기존 admission은 만료,
  기존 WAITING은 FIFO 유지 후 입장. 재연결 응답 1건은 입장 차단 상태에서 재시도.
- [x] recovery audit: coordinator mTLS 관측으로 system 역할 held/resumed 기록,
  8개 중복 ACK에도 1개 감사, 감사 실패 시 ACK까지 rollback. PG runtime/security
  회귀 PASS (5.873s). Gateway가 system 감사 actor를 임의로 지정할 수 없음.
- [x] [a5c545bb](waiting-room-local-beta-test-a5c545bb.json): v4 포함 Docker
  관리자/웹/앱 10 checks PASS; source unchanged. runtimeplane/valkeystore/localcontrol
  Go race PASS. 도구 단위 10 + UI 단위 13 + PRD 검사 PASS.
- [x] 실제 Docker 전체 경로 Valkey 재시작·복구 UI/audit 검증 PASS (62250138, 아래 기록).
- [ ] 기존 v3 설치 업그레이드, 새 epoch 운영 복구, 백업/복원 및 전체 acceptance.

로컬 Compose의 AOF는 `appendfsync always`로 변경했다. 단일 로컬 primary의
응답한 쓰기 보존을 우선한 설정이며 처리량/HA qualification의 근거가 아니다.

### 복구·보안·upgrade 통합 회귀 — 2026-09-06 06:48 UTC

- [62250138](waiting-room-local-beta-test-62250138.json): 12 checks PASS.
  실제 Docker Valkey 재시작 후 90초 안전 대기와 43개 입장 차단 probe, WAITING 유지,
  기존 admission 만료 거부, held/resumed 감사 각 1건과 브라우저의 유효한 UTC 발생 시각 확인.
  OFF Admin 등록·ON→OFF/ OFF→ON·cookie/CSRF 회전·이전 session 거부·계정/reauth도 PASS.
- [6e55a23e](waiting-room-local-beta-test-6e55a23e.json): 8 checks PASS.
  ON 새 설치/등록/재시작 로그인 및 반복 명시 upgrade가 변경된 OFF 정책·계정·초안·key binding을 보존.
  init으로 정책을 덮어쓰려는 시도는 거부한다. v3 queue migration 시험이 아니다.
- 두 실행의 고정 source digest:
  `5543633381784a335924bb40c69fd93ccf9cfb6d2fb6aaaf104bc201a84ce300`.
- `ca52e887`의 앞선 복구 PASS에서 감사 날짜 검사가 약했던 점을 보완했다.
  HTTP field `at`를 사용하고 실제 `<time datetime>` 파싱/비어 있지 않음을 검사했다.
- `make check` PASS: Go race/vet, scripts 10, contracts 14, UI 15, install schema 4,
  docs 9, OpenAPI lint 0 errors (후속 예약 UI 추가 전 기준).
- PG signed refresh 5분/미배포 draft 비포함/clock rollback 거부 PASS (0.714s),
  policy/users/reauth/security PASS (5.558s), v4 cancel-before-submit/clock rollback/
  다중 Room bounded recovery 포함 integration PASS (1.115s; real restart는 별도 위 실행).
- visitor Chromium 6 tests PASS (13.3s): 서버 heartbeat 주기와 짧은 idle TTL 회귀 포함.
- 후속 origin health는 최대 8개 동시/전체 3초 예산, Gateway config lock 밖에서 실행하도록 개선.
  runtimeplane race PASS (1.807s). 예약 생성/수정·UTC 검증 UI 추가 후 Admin unit 17 PASS/build PASS.
  이 후속 source의 Docker 예약 시험은 별도 실행한다.

판정: 위 부분 시험 GO, **전체 Beta NO-GO**. 배지 변경·10K/100K qualification·GA 승인이 아니다.

### 인증 감사 및 예약 후속 — 2026-09-06

- persistent Control은 `NewAudited` Store를 필수로 사용한다. standalone 001~004 auth lab은
  별도 constructor를 유지한다. 운영 감사 저장소가 없거나 INSERT 실패하면 인증을 fail-closed한다.
- 최초 Admin/로그인 단계·성공/잠금/로그아웃/TOTP 등록/복구 코드 사용을 기록한다.
  성공 및 credential 소비 감사는 같은 TX에서 저장한다. 실패한 비밀번호 요청은 검증되지 않은
  username 대신 system actor를 쓴다. code/key/password/raw IP/session/challenge는 저장하지 않는다.
- 잠금은 bucket window의 첫 거부만 기록해 차단된 재시도가 audit를 무제한 증가시키지 않는다.
  auth before/after digest는 고정 taxonomy 전이의 digest이며 credential/state 본문 hash가 아니다.
- 새로운 PG 감사 원자성 4 tests PASS (4.159s), bootstrap audit rollback 추가 후
  **PG 전체 integration/race PASS (193.039s)**. 실패 trigger fixture의 반복 CREATE를 수정한
  앞선 2건 실패는 보존/구분한다. 감사 실패 뒤 동일 TOTP/복구 코드/설치 token 재시도 검증 포함.
- 최신 `make check` PASS: Go race/vet, scripts 10, contracts 14, Admin unit 18,
  install schema 4, PRD 9 및 OpenAPI lint. 최종 Docker 통합은 별도 기록한다.

### 통합 완료 기록 — 2026-09-06 07:15 UTC 이후

- [cb826212](waiting-room-local-beta-test-cb826212.json): **15 checks PASS**.
  복구 90초/44 probes·예약 CRUD/pause/resume/실제 DRAINING·보안/정책과 live schema 검사.
  이 실행은 실제 360px 예약 screenshot을 확인했다. 50f6d88a의 잘못된 viewport 표기를 대신한다.
- [a05d423f](waiting-room-local-beta-test-a05d423f.json): **8 checks PASS**.
  새 ON 설치, 최초 Admin/등록/로그인/로그아웃 감사, 재시작 뒤 exact config/audit 유지,
  반복 upgrade 정책/계정/key binding 보존. 원문 비밀이 audit에 없음을 검사했다.
- 두 실행 고정 source digest:
  `aefe9de92ed62584041a2b7ff8c596419be2764290f415efefb7e379917f180e`.
  이 후속 변경은 단독 예약 viewport 명시와 WebKit select CSS/브라우저 시험이다.
- Firefox Admin lab **3 PASS (7.5s)**, WebKit Admin lab **3 PASS (13.2s)**.
  ON/OFF bootstrap·TOTP 등록/복구·reload/logout·Room 초안/충돌/360px.
- `playwright test --config playwright.config.mjs --browser=all`:
  Chromium/Firefox/WebKit **18 PASS (38.3s)**. WebKit native select가 min-height를 무시해
  19px이던 실제 결함을 `appearance:none`과 CSS indicator로 수정했다. semantic select와
  keyboard 조작 유지. Firefox의 빈 400 탐색 오류는 실제 HTTP 응답과 분리해 검사한다.
- WebKit 캡처 경고는 Playwright `inPagePrepareForScreenshots`가 삽입하는 `body {}` 때문임을
  설치된 소스와 실행 단계로 확인했다. **서비스 interaction의 console/page error 0건을 먼저
  검증한 다음**, 캡처 단계에서 이 정확한 도구 경고만 별도 기대한다. CSP 완화 없음.
- 사용자 로컬 설치의 후속 CSS 포함 image config:
  `sha256:cb7353dbb3a7d2887b6197d1f5d8e0a2ab625c89c8815814d50d241ca21b7b85`.
  manifest list `sha256:be62b65731a51929c3d24b8cfa6b18f96d1db3737de1b2f12d7c4e2471b7ffdb`.
  build/up 완료, 6개 서비스 healthy. 이 CSS 후속 image에서 위 통합15+8을 다시 실행했다고
  주장하지 않는다. 별도 브라우저18 회귀와 실행 상태를 구분한다.

남은 Beta 판단은 [beta-plan](../beta-plan.md). UI 5 routes/4 tabs·wizard/검증 UX,
전체 명령 lifecycle, 과거 간헐 503 근거와 M1~M3 acceptance는 아직 완료되지 않았다.

최종 후속 확인: CSS/시험 변경 후 `make check` PASS, `git diff --check` 및 PRD 검사 PASS.
사용자 로컬 Admin을 Chromium에서 직접 열어 HTTP 200·`Waiting Room · 관리자 콘솔` 제목·
관리자 로그인 화면을 확인했다. 새 계정/설정/설치 token을 만들지 않았다.
브라우저 검증은 Browser plugin not available로 저장소 Playwright를 사용했다.

### 앞선 실패 기록

- [99fa9104](waiting-room-local-beta-test-99fa9104.json): 예약 생성/수정/중복 거부,
  실제 HOLD/pause/AUTO 재개 뒤 DRAINING 취소 UI 검사가 실패했다. 보존 DB의 안전한
  상태 집계는 200×9, 202×1, 409×1, **412×1**, 해당 event는 running이었다.
  API poll보다 3초 갱신 UI가 늦어 stale revision으로 취소한 것이다. 충돌 방어를 유지하고
  최신 화면 revision 확인을 추가해 [50f6d88a](waiting-room-local-beta-test-50f6d88a.json)
  8 checks PASS. 진단 컨테이너/network만 해제하고 DB volume은 보존했다.
  **50f6d88a의 `360px` check 이름은 부정확했다**: 실제 캡처는 1586px였다.
  예약 단독 시험의 viewport를 명시해야 한다. 복구 시험 이후 실행하는 현재 통합 시험은
  360px viewport이며 결과/이미지를 별도로 확인한다. 앞선 PASS를 모바일 증거로 쓰지 않는다.

- [18b4baf5](waiting-room-local-beta-test-18b4baf5.json),
  [e4e80d23](waiting-room-local-beta-test-e4e80d23.json): 4개 기본 runtime check 뒤
  Valkey 자체 재시작에서 Coordinator ACK 정지. Valkey 로그에 AOF `EXEC`의
  NOPERM이 반복됐다. [Valkey 공식 #3983](https://github.com/valkey-io/valkey/issues/3983)의
  default-off ACL/AOF 트랜잭션 재생 오류와 일치한다. 독립 candidate의 기본 ACL과
  실제 Docker ACL 차이 때문에 Store-only restart 시험에서는 발견되지 않았다.
  공식 우회 설정 `default off resetpass +@all ~* &*`를 적용했다. default는 계속
  비활성·등록 비밀번호 없음이며 네트워크 AUTH를 허용하지 않는다. Coordinator와
  initializer의 역할 권한은 확대하지 않는다. 재생 실패 볼륨은 삭제하지 않았다.
  후속 실제 Docker 재시험에서 AOF 오류 없음 및 shared recovery fence 2 관측;
  전체 안전 대기 후 최종 결과는 별도 기록한다.

- [9139caf5](waiting-room-local-beta-test-9139caf5.json),
  [1793f97a](waiting-room-local-beta-test-1793f97a.json): 앱 대기 처리 이후 웹 navigation 실패.
  아직 해결됐다고 판단하지 않음. 헤더/token 유출을 막기 위해 원문 Playwright error를 기록하지 않고
  allowlisted network error category와 test line만 기록하도록 개선.
- [0148efe8](waiting-room-local-beta-test-0148efe8.json),
  [058a8f1f](waiting-room-local-beta-test-058a8f1f.json): 반복 프로젝트의 Docker network 누적으로
  `all predefined address pools have been fully subnetted` 확인.
  이번 작업의 1db37d41/9139caf5/1793f97a/0148efe8 컨테이너·네트워크만 `compose down`으로 해제.
  **--volumes/이미지 삭제 없음. DB·Valkey·identity·secret 볼륨은 보존.**
  이후 테스트도 종료 시 네트워크를 해제한다. 사용자용 설치는 삭제하지 않음.
- 기본 Postgres/Valkey 이미지에 CA PEM bundle이 없어서 빌드 실패.
  TLS 검증을 끄지 않고 빌드 호스트의 공개 system CA bundle을 포함한다.
  현재 bundle SHA-256: `9dae8d76e55cb08991f2b672d58999ea15560d910759c16b544f843bdffbb994`.
- [33acb21a](waiting-room-local-beta-test-33acb21a.json): runtime 4개 check PASS 후
  보안 화면의 exact-label 사용자 역할 선택에서 timeout (security.mjs:19).
  계정 생성 요청 전 실패. 명시적 aria-label을 추가한 뒤 다음 실행에서 계정 흐름 PASS.
- [0805f799](waiting-room-local-beta-test-0805f799.json): 계정 관리까지 5 checks PASS,
  OFF Admin TOTP 등록 시작에서 멈춤. 기존 pre-auth API의 cookie 거부 경계가 원인.
  이를 완화하지 않고 session/CSRF/등록 proof를 함께 검증하는
  `/security/totp/enrollment/start|verify`를 분리했다. ff78a6ec 전체 흐름 PASS.
- 활성 vault key 선택을 명시하도록 바꾼 뒤 다중 키 fixture가 초기화 실패했다.
  fixture도 `k1`을 명시하고 재검증 PASS; production은 `local-v1`을 명시한다.
- 계정 동시성 초기 시험의 8건 400은 16자보다 짧은 fixture idempotency key 때문.
  허용 길이의 키로 수정한 후 1 success/7 consumed-proof denial과 DB last-Admin 경쟁 PASS.

사용자용 기존 설치는 스키마 1~5·계정 0개를 먼저 확인한 뒤 명시 upgrade/up을 완료했다.
키/기존 database/state 볼륨 보존, runtime 전용 볼륨 신규 생성. 6개 서비스 healthy 확인.
계정 생성이나 bootstrap token 발급은 하지 않았다. Admin은 https://127.0.0.1:19443 이다.
진행 중 사용자가 GitHub 초기 업로드를 완료했다. 현재 기준 commit은
초기 `5cbe348`, 후속 `2a3159c`이며 새 브랜딩/architecture 변경을 보존한다.
이 작업에서 별도 커밋·푸시·공개 배포는 하지 않았다.

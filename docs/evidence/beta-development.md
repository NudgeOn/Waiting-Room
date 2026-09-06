# Beta 개발 진행 기록 — 2026-09-06

사용자 요청: M3 Beta까지 구현. 후속 선택: **로컬 Docker부터 완성**.
[실행 계획](../beta-plan.md)을 따른다. 아직 Beta NO-GO이며 완료로 표시하지 않는다.

## 현재 추가한 범위

- `internal/control`: Config/Room/limits/theme 계약, strict bounded JSON, segment-aware route matcher,
  active route 충돌과 hostname/path 우회 입력 검사. 원본 DNS/SSRF/접속 검사는 아직 별도 미구현.
- migration 005와 `pgstore.ControlService`: draft 저장, current session/RBAC/CSRF 확인,
  If-Match/revision, actor별 24h replay, 10K command cap/128건 cleanup, 변경과 audit 원자 commit.
  lock 순서 auth policy/account/credential/session → config. 트랜잭션 안에 외부 호출 없음.
- 최초 SQL timestamp parameter inference 실패를 실제 DB 시험에서 확인하고 명시적 timestamptz cast로 수정.
  config 원자성/16개 경쟁/2 pool replay/권한·CSRF/감사 실패 rollback/실패 응답 replay 시험 5개 PASS.
- `/config/draft` GET/PUT와 audit 조회 adapter. draft-only header로 runtime publish와 구분.
  64KiB strict JSON, ambiguous cookie/header 거부. audit cursor/limit/items/nextCursor를 기존 OpenAPI와 정렬함.
- 기존 Admin Lab에 Room 초안·경로·유량·Calm 문구 편집/저장·조회 및 최근 감사 로그 UI 연결.
  기존 Lab은 종료 시 schema를 지우는 disposable 모드 그대로다. 별도 영속 Docker Control을 아래에 추가함.
- 실제 Chromium/TLS 3 tests PASS: 기존 TOTP ON/OFF + 새 Room 저장/새로고침/412 충돌/360px 저장.
  첫 브라우저 시험은 logout 완료 전 다른 port로 이동하는 test race로 timeout; 대기와 finally cleanup 수정.
  Browser plugin not available; 기존 Playwright 사용. 원격 요청/관련 console/page error 0.
  screenshot: `/tmp/wr-beta-ui-3pduLh/room-desktop.png` (1586px), `room-mobile.png` (360px), 눈으로 확인함.
  원문 credential/QR/복구 코드 화면 캡처 없음. 실제 runtime 연결/Firefox/WebKit/WCAG 전체 미검증.

## 503 조사

- 수정 전 동일 조건 HTTP tiers 5회(각 1K/2K/5K/10K) PASS; 원래 간헐 503은 미재현.
- 별도 결함 재현: 이미 취소된 caller의 Status가 Store 전체 failed latch를 세워 후속 정상 Join도 거부.
  `TestInstallationCanceledCallerDoesNotPoisonStore` 수정 전 FAIL.
- caller cancellation은 미제출 요청/primary INFO/read-only FCALL_RO에서 request-local 오류로 구분.
  다음 요청의 primary ID 검사는 유지. 실제 primary 변경/unknown write/부분 쓰기 오류는 계속 fail-closed.
- 수정 후 위 회귀 PASS. 전용 Valkey `CLIENT PAUSE 150 ALL`로 in-flight INFO timeout을 만든
  `TestInstallationCanceledPrimaryReadDoesNotPoisonStore`도 PASS. 설치 회귀 전체 PASS.
- 이것이 과거 5K의 503과 같은 원인이었는지는 아직 확정하지 않는다. 원래 실패 artifact 보존.

## 다음 작업 / 환경

- OpenAPI draft/audit 계약 정렬, current source 전체 회귀·증거 bundle 미완료.
- 사용자 선택대로 영속 로컬 Docker Control/설치 흐름, signed publish/ACK → Gateway/Coordinator 적용,
  runtime mode/rate/event 및 reauth/정책관리, 전체 E2E를 이어서 구현해야 Beta다.
- 전용 Valkey 16379와 auth PostgreSQL 15432는 현재 실행 중. 다른 서비스/volume 보존.
- 현재 git 전체 untracked인 사용자 작업 상태. 커밋/푸시/배포/새 automation/goal/subagent 없음.
- 사용 skill: PostgreSQL best practices(lock order/short transactions/index), frontend-testing-debugging,
  react-best-practices. 사용자 최초 docs/evidence 요구를 따라 PRD 증거는 repo에, screenshot은 /tmp에 둠.

## 영속 로컬 Docker Control — 추가 진행

- `wr-control init on|off`만 migration/초기화를 수행. SQL checksum ledger와 DB↔암호화/login key binding.
  기존 키·policy·draft를 보존하고 없는/mismatched state를 runtime에서 자동 생성하지 않는다.
- persistent private state, TOTP AES-GCM key, 로그인 fingerprint, local TLS와 runtime DB credential.
  Secret State의 fmt/JSON 로그 redaction, 파일 0600/dir 0700, symlink/oversize 거부 unit 3 PASS.
- runtime non-root/read-only/capability drop, DB owner credential 미제공, schema DDL와 audit UPDATE/DELETE 차단.
  초기화 SQL의 credential 문장 오류 로그도 억제. 현재 자체서명 TLS 1년; 갱신/백업복원 gate는 미완료.
- setup은 Docker 내부 loopback, 호스트 local TCP→docker exec stdio→실제 loopback TCP 터널.
  public Admin에서 bootstrap 404. Forwarded 헤더로 peer를 대체하지 않는다.
- 최초 Docker 시험: internal network만 연결했을 때 컨테이너 health PASS/호스트 ECONNREFUSED.
  [실패 기록](waiting-room-local-beta-test-aabc762f.json) 보존. 테스트 예외 메시지의 HTTP header 로그 위험을 확인하여
  원문 error 출력도 제거했다. 해당 일회성 테스트 설치는 정지 상태다.
- DB internal network + Control management bridge로 분리 후 [7 checks PASS](waiting-room-local-beta-test-9ee8c0c0.json).
  최초 관리자/TOTP → Room 저장 → 반복 init 보존/정책 덮어쓰기 거부/재bootstrap 거부 →
  PostgreSQL+Control 동시 재시작 → 동일 session/config/audit → 신규 password+TOTP 로그인 PASS.
- Desktop 1586px/mobile 360px 캡처 직접 확인: blank/overlay/수평 넘침 없음, 관련 console/page/remote 요청 0.
  `/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-docker-beta-kbKNbS/`의 `persistent-desktop.png`, `persistent-mobile.png`.
  credential/QR/recovery 화면은 캡처하지 않음. Firefox/WebKit/full WCAG는 미검증.
- `make check` PASS; `go test -tags=integration -race ./internal/adminauth/pgstore ./internal/adminauth/controlhttp`
  PASS (PG 133.543s); OpenAPI lint warnings 0, contract 12 PASS, UI unit 11 PASS.
- 전용 Valkey pause 주입 시험이 다른 package 시험과 겹치지 않도록 integration은 `go test -p=1`로 직렬화.
- 설치 안내: [local-docker](../local-docker.md). 일반 설치는 초기화만 했고 첫 관리자 계정을 만들지 않았다.
  격리 테스트 프로젝트는 정지했으며 진단용 볼륨/secret은 보존한다. 삭제 명령 없음.

현재 상태는 **로컬 영속 Control 개발판**, 제품 Beta GO가 아니다.
남은 핵심은 draft→signed publish/ACK→Gateway/Coordinator, 운영 모드/유량/예약,
완성된 wizard/Dashboard/보안 lifecycle/백업복원과 M1~M3 acceptance다.

## 최종 고정 소스 회귀 — 2026-09-06 03:40 UTC

- sourceDigest: `eb6914c73198322638fde548054919f03aec8d9773880786b29fea53bea8f1a6`.
- [로컬 회귀 report](beta-control-regression-20260906/report.json), [환경](beta-control-regression-20260906/environment.json):
  03:36:10.498Z–03:40:16.413Z, sourceUnchanged=true, **16 checks PASS**.
  이는 실제 회귀 15개 + 의도된 NO-GO 거부 guard 1개다. MAIN FT는 **NOT_RUN**이다.
- 4 child PID·32 worker·GOMAXPROCS=2 HTTP 1K/2K/5K/10K를 3회 모두 PASS.
  세 번째 10K join p95 6.716ms/p99 9.008ms, status p95 5.486ms/p99 6.713ms,
  joined 10000/statusReads 10000, admitted 3, queue 9997. 장시간 동시 10K connection/전체방문자입장 인증이 아니다.
- public target/signed config/strict draft decoder fuzz 각각 10000회 PASS.
  실제 Valkey cancellation 두 회귀, 직렬 integration, Valkey restart fail-closed PASS.
  과거 간헐 HTTP 503과 동일 원인이라는 주장은 하지 않는다.
- 같은 sourceDigest의 [최종 Docker 시험](waiting-room-local-beta-test-cc20e08b.json) **7 checks PASS**.
  secret fmt/JSON redaction과 migration SQL 로그 억제 변경을 포함한 새 이미지로 검증함.
- 기존 Admin Lab Chromium **3 tests PASS (5.8s)**, ON/OFF와 Room/412/360px 포함.
- 실행 Control image ID: `sha256:c4a97265bc95b4586de08108308356ef11cdbb8c5255c32c42860fcba4c0a6e8`.
  사용자용 `waiting-room-local-beta` Control/PostgreSQL healthy, Admin `https://127.0.0.1:19443`.
  accounts=0/config revision=0/bootstrap completed=false 확인. 임의 사용자 계정/암호 없음.
  setup 터널은 닫혀 있고 발급된 사용자용 bootstrap token은 없다. 가이드의 명시적 단계로 시작한다.
- 최신 screenshot은 `/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-docker-beta-77mKWO/`.
  UI 소스는 앞서 직접 확인한 `kbKNbS` 캡처와 동일하다. 테스트용 프로젝트 3개는 정지, 볼륨/secret 보존.
- Git 전체 untracked 상태 보존. 커밋/푸시/공개 배포 없음. 기존 Valkey/auth DB lab은 실행 상태 유지.

**최종 판단: 영속 로컬 Control 부분 검증 PASS / 제품 Beta NO-GO.**

# Local Backoffice authentication UI evidence

React 인증 화면 + disposable Admin Lab의 제한된 로컬 검증: **GO**.
SUB-PRD-04·05와 MAIN delivery는 **NO-GO**. 전체 Backoffice/설치기/출시 완료가 아니다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별과 검사

- Run `admin-ui-20260905-local`, UTC 2026-09-05 12:39:10–12:43:19.
- Source SHA-256 `8f9d7da8ba162ee7ec42594ff8cd38601cb8e6b36cd629a8dd059eb0a89a02b7`.
- Uncommitted snapshot, commit 없음. docs/evidence 제외 source 실행 전후 불변·9개 로그 hash 확인.
- Go 1.26.1, Node 24.12.0, macOS arm64, PostgreSQL 17.11, React 19.2.8, Vite 8.2.2.
- Browser: Chromium, Playwright 1.63.0. IAB는 `ERR_CERT_AUTHORITY_INVALID`로 자체 서명
  local 인증서를 거부하여 격리된 context에서만 `ignoreHTTPSErrors`를 사용했다. OS trust 변경 없음.
- Test URL: `https://127.0.0.1:18453`/18454(ON), 18455/18456(OFF). 각 반복마다 새 schema.

[보고서](admin-ui-20260905-local/report.json) · [환경](admin-ui-20260905-local/environment.json)

| 검사 | 결과 | 로그 |
|---|---|---|
| 전체 Go race/vet/format·Node 10·PRD 9·OpenAPI 2·contract 10·Admin build/unit 5 | PASS | [foundation](admin-ui-20260905-local/foundation.log) |
| 인증 Go unit | 41 PASS, 0 FAIL; fuzz seed 별도 | [auth unit](admin-ui-20260905-local/admin-unit.log) |
| Lab certificate unit | 1 PASS | [lab unit](admin-ui-20260905-local/admin-lab-unit.log) |
| 실제 Chromium ON/OFF × 3회 | 6 PASS | [browser](admin-ui-20260905-local/admin-browser.log) |
| DB/TLS integration | 42 PASS; 함께 실행된 unit 20개 별도 | [integration](admin-ui-20260905-local/postgres-integration.log) |
| TOTP DB 6개 × 10회 | 60 PASS | [DB repeat](admin-ui-20260905-local/postgres-repeat.log) |
| Auth HTTP/공유 제한 2개 × 3회 | 6 PASS; unit 9개 별도 | [HTTP repeat](admin-ui-20260905-local/auth-http-repeat.log) |
| Session 9개 × 10회 | 90 PASS; projection unit 10개 별도 | [sessions](admin-ui-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2, SUB 8개 NO-GO. 실제 final integration 아님 | [guard](admin-ui-20260905-local/final-guard.log) |

환경의 provisioningHTTP/bootstrap/enrollment/recovery 값은 각 **전용 반복 flag** 선택 여부다.
이번에는 `--admin-ui --auth-http --sessions`; 기본 DB suite의 provisioning 회귀와 새 browser
setup/enrollment는 포함한다. 이전 service 전용 반복·password calibration/fuzz 재실행은 아니다.

## 실제 사용자 흐름과 발견한 결함

1. 이전 lab의 stale HttpOnly cookie → 현재 session 401 → 기존 logout 경로로 쿠키 정리 → 로그인.
2. 정상 Admin listener의 bootstrap은 404. 분리된 setup listener에서 terminal-file token으로
   첫 Admin을 만든다. 계정·비밀번호·TOTP credential을 SQL로 미리 생성하지 않는다.
3. ON: 등록 키 만들기 → 브라우저 내부 QR 표시 → 실제 key 기반 OTP → 코드 10개 생성 →
   보관 checkbox 전에는 Continue 비활성 → 현재 DB session 표시.
4. 새로고침 후 session 복원 → 탭 CSRF로 logout → password → recovery-code 로그인 → logout.
   OFF는 초기 password-only session과 후속 password login을 같은 브라우저에서 검증.
5. 외부 request 0, pageerror 0, 관련 app console warning/error 0. sessionStorage는 CSRF key 하나만,
   localStorage는 비어 있음. cookie Secure/HttpOnly/SameSite=Strict, 정상 logout 후 제거 확인.

발견·수정: HTML pattern의 하이픈이 Chromium UnicodeSets `v` 정규식에서 오류를 내어
클라이언트 검증이 무효가 됐다. 하이픈을 문자 class 밖의 alternative로 옮기고 Node `v` mode
검증을 추가했다. 서버 검증은 그대로 유지했으며 수정 뒤 browser 6회 PASS/console 0 확인.
이전 lab 쿠키 때문에 새 로그인에 실패할 수 있는 흐름도 시작 시 안전하게 정리하도록 수정했다.

## Visual QA / fidelity ledger

기준 [built-in Image Gen concept](../design/admin-login-concept.png),
[구현 inventory/brief](../design/admin-auth.md). 별도 사용자 시안 승인을 받았다고 주장하지 않는다.
동일 QA pass에서 concept과 최종 desktop/mobile/session 이미지를 `view_image`로 직접 비교했다.

| 비교 항목 | 최종 판단·수정 |
|---|---|
| 문구 | 로그인 above-the-fold copy diff 0. 새 문구는 명시한 setup/등록/session 기능 상태뿐 |
| Layout | 1586×992 원본 크기, open 2-column·100px header·footer·565px form 유지 |
| Typography | 제목이 작았던 초기 render를 h1 68px/h2 46px로 보정. 실제 OS 한글 glyph 차이는 허용 |
| Palette | 순백 배경·forest-green 제목/버튼·회색 divider. 이미지의 미세 raster 질감 대신 flat CSS |
| Controls/spacing | 70px 입력·8px radius·label hierarchy·여백 일치. fake 카드/지표/장식 추가 없음 |
| Assets | 시안을 UI로 붙이지 않음. 실제 HTML 텍스트/폼이며 QR은 local dynamic import로 생성 |
| Responsive/focus | 360×900에서 24px gutter·stack·scrollWidth 초과 0·첫 Tab으로 username focus 확인 |

선택한 시안의 구조·문구·시각 체계를 충실히 구현했으며 남은 중대한 시각 불일치는 없다.
의도적 차이: 시스템 폰트의 glyph, flat CSS 표면, 필요한 인증 후속 상태, 모바일 stacking.
이미지 자체의 미세 pixel 질감까지 같은 10/10 pixel-diff나 외부 디자인 승인이라는 뜻은 아니다.

최종 캡처:

- [로그인 desktop](admin-ui-20260905-local/screenshots/login-desktop.png), SHA-256 `7896ebaf53825c8342137518b881309b00ab3e06b8680c319a8593444f6cc9d1`
- [로그인 360px](admin-ui-20260905-local/screenshots/login-mobile.png), SHA-256 `bdd9673c5f4a80392cebaa3454a566522dd1109248168d80f8fd4484b83ff595`
- [실제 session](admin-ui-20260905-local/screenshots/session-desktop.png), SHA-256 `ab762f75acbe0ec84cdfb3c7af40c593c22cff0d78ce21f86f167a677294d590`

## 남은 범위와 정리

- frontend-app-builder/imagegen은 시안→tokens→실제 캡처 비교를, frontend-testing-debugging은
  콘솔 결함 발견을, React 지침은 event-handler mutation·QR lazy load·최소 저장을 이끌었다.
- 추가 패키지 버전 고정과 bundle MIT notices를 포함했다. CI 정의에 browser job을 연결했으나
  원격 CI 실행·SBOM/공급망 qualification·commit/push/배포는 하지 않았다.
- `wr-admin-lab`은 고정된 loopback fixture DB만 사용한다. production runtime·keyfile/ledger,
  migration role·durable accounts·trusted TLS·bootstrap endpoint shutdown이 아니다.
- 임의 lab 정책 flag는 설치 위자드의 TOTP ON/OFF가 아니다. 신규 탭 CSRF 복원은 미구현이며
  해당 탭은 read-only 안내를 한다. 새로고침은 per-tab CSRF storage로 검증했다.
- Room on/off·경로/유량·template 저장/publish, profile/cost wizard, audit/idempotency/reauth,
  forced-on/정책 원자 전환·복구 재발급/reset·10K/100K/HA/region qualification은 미완료.
- Chromium만 검증. Firefox/WebKit, 실제 인증 앱/기기, 전체 keyboard/screen-reader/WCAG,
  종료 중 장기 요청·SIGKILL recovery·운영 인증서 신뢰·production 환경은 미검증이다.
- 정상 종료 후 `wr_admin_lab_%` schema 0개, test token-file 제거 확인. 전용 PostgreSQL
  Exited (0), named volume 보존. 별도 Valkey lab/실사용 데이터는 변경하지 않았다.
- 사용자 요청의 PRD evidence 용도로 시안과 최종/초기 로컬 캡처를 보관한다. 민감한 등록 키·QR·
  복구 코드 화면은 screenshot/trace/video에 담지 않았다. MAIN 최종 테스트는 실행하지 않았다.

[실행 방법](../operators/admin-ui-lab.md) · [SUB-PRD-04](../sub-prd_04.md) · [SUB-PRD-05](../sub-prd_05.md)

# Admin URL 경로 판정 — 2026-09-06

Owner: SUB-PRD-04. **부분 기능 GO / SUB-PRD-04 delivery 및 Beta NO-GO.**
이 기록은 [이전 workspace 기록](admin-workspace-summary.md)의 URL 미지원 상태를 대체한다.

## 구현 범위

- Room 검증 탭에서 저장 초안 또는 배포 스냅샷을 선택해 전체 HTTPS URL을 검사한다.
- Go의 기존 `Config.MatchURL` 규칙을 사용한다. 보호/제외/미일치/내부 전용/잘못된 URL 구분.
  URL을 JS에서 정규화하지 않아 encoded path와 dot segment를 그대로 거부한다.
- 결과에 source, revision, generation, 일치 Room과 배포 스냅샷 모드를 표시한다.
  화면 버전과 다르면 경고하고 입력 변경 시 이전 결과를 제거한다.
- `POST /api/admin/v1/config/route-check`: 인증·ReadConfig 역할·Origin/CSRF·strict JSON,
  body 8192 bytes / URL 4096 bytes 제한, no-store. 순수 조회라 명령 idempotency는 요구하지 않는다.
- DNS/HTTP 호출, 설정/명령/감사 로그/세션 idle 갱신 없음. URL 및 query를 응답에 반사하지 않는다.
  PostgreSQL 스냅샷 조회는 짧은 트랜잭션이다. React 요청은 submit 이벤트에서만 실행한다.
- 이는 설정 진단이며 실제 입장, Gateway 적용, 포트/TLS/원본 연결 또는 부하 성능 보장이 아니다.
  Quick 20/Smoke 1K UI 실행은 여전히 미지원이다.

## 검증 Checklist

- [x] Go control/controlhttp/pgstore 단위 PASS; control/controlhttp race PASS.
- [x] `TestRouteCheckSnapshotsRolesAndNoWrites`: 실제 PostgreSQL integration race PASS (1.853s).
  서로 다른 draft/published revision·규칙, Admin/Operator/Viewer, CSRF/Origin 거부,
  비활성 계정 거부, config/delivery/command/audit/session last-seen 무변경 확인.
- [x] `make check` PASS: Go vet/race, script 10, contract 15, Admin unit 20,
  install schema 4, 9 PRD 검사, OpenAPI lint.
- [x] 기존 Admin Lab Chromium/Firefox/WebKit 12 tests PASS (22.0s).
  이는 인증/Room wizard/경로/날짜 회귀이며 신규 URL 화면의 3종 브라우저 검증은 아니다.
- [x] [3583012d](waiting-room-local-beta-test-3583012d.json): Docker Chromium **8 checks PASS**.
  URL 8개 시나리오(보호/제외/segment 불일치/다른 호스트/내부 경로/encoded/dot segment/저장 초안),
  live OpenAPI 응답, 수정 후 결과 제거, 대상 네트워크 요청 0, 360px overflow 없음.
  이어서 signed publish/양 role ACK/HOLD, Web/App 입장 및 Gateway/Coordinator 재시작 replay PASS.
- [x] 데스크톱 1586×992 / 모바일 360×900 전체 페이지 캡처 직접 확인.
  제목·입력·결과·한계 안내 정상 표시, 잘림 없음. 예상 밖 console 경고/JS 오류 0.
- [ ] 신규 URL 화면 Firefox/WebKit 및 전체 역할별 browser/a11y acceptance.
- [ ] Setup 전체 wizard/calibration, Quick 20/Smoke 1K UI, 전체 명령 lifecycle 및 M1~M3 review.

Docker 실행: 2026-09-06 09:13:10–09:13:41 UTC.
source digest: `bc9bcdb8eb694c6bd5167716304dfb528580c1c29d6de87ff23678cfd0570ed8`.
이미지 config: `sha256:238555bcd9d88a07997436adc4b1ba340dcbf7b521d3057b456fb1236db49b17`.
이후 문서만 갱신했다. 이전 17-check 전체 복구/예약/보안 시험은 이전 이미지의 별도 증거다.

명령: `make check`,
`WR_TEST_AUTH_DB=local go test -tags integration -race ./internal/adminauth/pgstore -run '^TestRouteCheck' -count=1`,
`WR_TEST_LOCAL_BETA=local PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" node test/localbeta/runtime-quick.mjs`.

캡처 디렉터리:
`/var/folders/j_/blpv946j115gq8l1z2sx975c0000gn/T/wr-runtime-7apIVF`.
`route-check-desktop.png`, `route-check-mobile.png`. Git에 이미지/비밀정보를 추가하지 않았다.
Browser plugin 미제공으로 저장소 Playwright를 사용했다.

## 실패를 보존한 수정 기록

[2206a026](waiting-room-local-beta-test-2206a026.json),
[febd5fef](waiting-room-local-beta-test-febd5fef.json)는 FAIL이다.
7개 URL 판정 HTTP 200 뒤 선택창의 exact `getByLabel`이 옵션 텍스트를 포함한 label과
일치하지 않아 timeout. 동일 HTML의 독립 Chromium 재현에서 exact label count 0,
partial count 1, 접근성 tree에는 `combobox "검사 기준"`을 확인했다.
시험을 해당 combobox 접근성 이름으로 수정해 최종 PASS. 제품/보안 규칙은 완화하지 않았다.
실패/성공 시험의 컨테이너·network만 정리했고 volume과 secret은 보존했다.

README Roadmap은 검증 진척과 남은 gate를 구분한다. M0 기반 GO 외 M1~M3는 NO-GO,
M4 10K qualification·M5 100K/HA·M6 GA는 미완료다. badge는 preview 유지.
commit/push/GitHub release는 수행하지 않았다.

시험 종료 후 기본 설치 `up/status`: 기존 PostgreSQL/Valkey 데이터와 identity를 유지하고
새 이미지로 6개 서비스 모두 healthy. README/PRD 갱신 후 `git diff --check`,
9 PRD 검사, contract 15 tests PASS.

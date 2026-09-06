# M3 Operable Beta 실행 계획

요청: 2026-09-06 Beta 버전까지 구현. 사용자는 **로컬 Docker부터 완성**을 선택했다.
기준은 MAIN/M1~M3 및 SUB-PRD-04이며,
로컬 데모에 beta 이름을 붙여 완료로 간주하지 않는다. 기존 PRD 범위를 축소하지 않는다.

## 완료 순서와 gate

- [ ] B0: 간헐 503 원인 규명 및 회귀. 단순 재실행 PASS는 해결 증거가 아니다.
- [ ] B1: M1/M2 runtime 경계, route/mode/복귀/서명 설정과 장애 차단 검증.
- [ ] B2: PostgreSQL 기반 운영 명령, RBAC/CSRF, revision, durable idempotency, 감사 로그.
- [ ] B3: Room 생성/설정/유량/모드와 예약의 실제 runtime 연결 및 안전한 template publish.
- [ ] B4: 설정/Room wizard, Dashboard/Room/Settings UI, 인증/TOTP/reauth lifecycle 연결.
- [ ] B5: 새 환경 실행 문서와 360px·keyboard·browser/app 핵심 E2E, 전체 회귀 evidence.
- [ ] B6: B0~B5와 M1/M2/M3 acceptance 검토 후 Beta GO/NO-GO 기록.

10K/100K 운영 qualification/Helm HA/GA FT는 M4 이후이며 Beta 결과와 구분한다.
FIFO/단일리전은 v1 범위다. 공개 배포·푸시·유료 인프라는 별도 승인 없이 수행하지 않는다.
자료/실패 artifact/기존 사용자 변경을 보존하고 각 단계 결과는 owner sub-PRD에 기록한다.

## 현재 판정

**Beta NO-GO — 구현 진행 중.** 최신 [runtime·보안 기록](evidence/beta-runtime-progress.md)을 먼저 확인한다.
로컬 Docker의 signed publish/양 role ACK·Web/App 입장·실제 Valkey 복구·TOTP 양방향 정책
전환과 새 설치/반복 upgrade 보존 시험은 PASS다. M3 전체 완료와 동일하지 않다.

Dashboard·Room 4개 주소/탭·5단계 초안 생성과 실제 운영 연결을 추가했다.
[workspace 증거](evidence/admin-workspace-summary.md): 12 browser tests,
Docker 복구/예약/보안 17 checks 및 새 설치/재시작 8 checks PASS.
이는 B4 전체 완료가 아니다. 검증 탭은 role ACK/원본 상태와 읽기 전용 URL 경로 판정을 제공한다.
[URL 판정 근거](evidence/admin-route-check-summary.md): domain/HTTP 단위·PG 역할/무변경 시험,
Docker URL 8개 시나리오를 포함한 회귀 8 checks PASS. 실제 대상 URL에 접속하지 않는다.

남은 우선 작업은 B0 과거 부하 503 재현/원인 근거, 설치 wizard 전체 apply/calibration,
Quick 20·Smoke 1K UI 실행, 전체 명령 lifecycle와 M1~M3 acceptance다.
예약 생성/수정/중복 거부/수동 pause·resume/실제 HOLD→AUTO→DRAINING browser 시험은 PASS다.
인증 감사 PG 전체 race와 실제 Docker 새 설치/재시작/upgrade도 PASS다.
기존 v3 runtime 데이터는 조용히 v4로 재해석하지 않으며 epoch 복구·이행은 별도 미완료다.
README `preview` → `beta`는 B6 판정 뒤 변경한다. 100K/HA/GA 범위를 Beta에 합치지는 않는다.

### B4 부분 Checklist

- [x] Dashboard의 실제 draft/delivery/role 응답과 Room 목록.
- [x] Room 4개 주소/탭, 직접 접속·reload·back/forward·비로그인 gate.
- [x] 5단계 Room 초안 생성, 입력 유지, 저장 후 설정 주소 이동, stale revision 입력 보존.
- [x] 실제 예약·운영·보안 화면의 주소 연결과 Docker E2E.
- [x] 저장 초안/배포 스냅샷 URL 판정, 결과 버전 표시·입력 변경 시 결과 제거·360px.
- [ ] Setup 전체 wizard·Traffic Lab·역할/a11y 전체 acceptance.

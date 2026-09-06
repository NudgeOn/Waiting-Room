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

**Beta NO-GO — 구현 진행 중.** [시간별 개발 기록](evidence/beta-development.md)을 먼저 확인한다.
기존 runtime 및 Admin 인증 lab은
출발점이며 운영 Room API/UI와 설치가 이미 완성되었다는 의미가 아니다.

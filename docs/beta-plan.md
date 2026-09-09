# M3 Operable Beta 실행 계획

요청: 2026-09-06 Beta 버전까지 구현. 사용자는 **로컬 Docker부터 완성**을 선택했다.
기준은 MAIN/M1~M3 및 SUB-PRD-04이며,
로컬 데모에 beta 이름을 붙여 완료로 간주하지 않는다. 기존 PRD 범위를 축소하지 않는다.

## 완료 순서와 gate

- [ ] B0: 간헐 503 원인 규명 및 회귀. 단순 재실행 PASS는 해결 증거가 아니다.
- [ ] B1: M1/M2 runtime 경계, route/mode/복귀/서명 설정과 장애 차단 검증.
- [x] B2: PostgreSQL 기반 운영 명령, RBAC/CSRF, revision, durable idempotency, 감사 로그.
- [x] B3: Room 생성/설정/유량/모드와 예약의 실제 runtime 연결 및 안전한 template publish.
- [x] B4: 설정/Room wizard, Dashboard/Room/Settings UI, 인증/TOTP/reauth lifecycle 연결.
- [ ] B5: 새 환경 실행 문서와 360px·keyboard·browser/app 핵심 E2E, 전체 회귀 evidence.
- [ ] B6: B0~B5와 M1/M2/M3 acceptance 검토 후 Beta GO/NO-GO 기록.

10K/100K 운영 qualification/Helm HA/GA FT는 M4 이후이며 Beta 결과와 구분한다.
FIFO/단일리전은 v1 범위다. 공개 배포·푸시·유료 인프라는 별도 승인 없이 수행하지 않는다.
자료/실패 artifact/기존 사용자 변경을 보존하고 각 단계 결과는 owner sub-PRD에 기록한다.

## 현재 판정

**Beta NO-GO — 구현 진행 중.** 최신 [runtime·보안 기록](evidence/beta-runtime-progress.md)을 먼저 확인한다.

2026-09-09 최신 후속은 [최초 접속·Traffic Lab 통합 검증](evidence/beta-20260909-browser-join.md)이다.
첫 응답 전체 유실·동시 탭의 대기표 보존을 구현했고 세 엔진 및 실제 HTTPS 시험을 통과했다.
같은 이미지의 관리자 81개 화면/32개 API/Control 중단 회복도 PASS다.
최종 고정 이미지의 전체 안전 대기와 원인 미상 과거 두 장애의 수용 판정은 계속 구분한다.
로컬 Docker의 signed publish/양 role ACK·Web/App 입장·실제 Valkey 복구·TOTP 양방향 정책
전환과 새 설치/반복 upgrade 보존 시험은 PASS다. M3 전체 완료와 동일하지 않다.

Dashboard·Room 4개 주소/탭·5단계 초안 생성과 실제 운영 연결을 추가했다.
[workspace 증거](evidence/admin-workspace-summary.md): 12 browser tests,
Docker 복구/예약/보안 17 checks 및 새 설치/재시작 8 checks PASS.
이는 B4 전체 완료가 아니다. 검증 탭은 role ACK/원본 상태와 읽기 전용 URL 경로 판정을 제공한다.
[URL 판정 근거](evidence/admin-route-check-summary.md): domain/HTTP 단위·PG 역할/무변경 시험,
Docker URL 8개 시나리오를 포함한 회귀 8 checks PASS. 실제 대상 URL에 접속하지 않는다.

로컬 Docker [설치 위자드](operators/setup-wizard.md)는 실제 Control 보정, 검토한 설정 적용,
첫 관리자/TOTP 등록, 설치 결과 보존과 새 Room 기본값 연결을 구현했다.
관리자 [Traffic Lab](operators/traffic-lab.md)의 Quick 20·Smoke 1K 실행·결과 확인도 연결했다.
로컬 [복구·업그레이드](operators/recovery-upgrade.md)는 v3/v4 → v5 명시적 이행,
설치 전체 새 epoch, 업그레이드 전 자동 콜드 백업과 빈 새 설치 복원을 구현했다.
남은 우선 작업은 B0 과거 부하 503의 원인 근거와 M1~M3 전체 acceptance다.
구현된 로컬 운영 명령의 재시도·감사·역할·API·키보드/axe 회귀는 [관리자 검증](operators/admin-validation.md)으로 정리했다. Production 환경 수집·배포와 기존 계정 보정값
변경은 이 로컬 설치 흐름에 포함되지 않는다.
예약 생성/수정/중복 거부/수동 pause·resume/실제 HOLD→AUTO→DRAINING browser 시험은 PASS다.
인증 감사 PG 전체 race와 실제 Docker 새 설치/재시작/upgrade도 PASS다.
기존 v3/v4 runtime은 검사한 데이터만 v5 메타데이터로 전환하며 원본 함수와 대기표를 보존한다.
새 epoch는 별도 namespace와 설치 공통 fence를 사용하며 최소 60분 30초 안전 대기를 단축하지 않는다.
README `preview` → `beta`는 B6 판정 뒤 변경한다. 100K/HA/GA 범위를 Beta에 합치지는 않는다.

### B4 부분 Checklist

- [x] Dashboard의 실제 draft/delivery/role 응답과 Room 목록.
- [x] Room 4개 주소/탭, 직접 접속·reload·back/forward·비로그인 gate.
- [x] 5단계 Room 초안 생성, 입력 유지, 저장 후 설정 주소 이동, stale revision 입력 보존.
- [x] 실제 예약·운영·보안 화면의 주소 연결과 Docker E2E.
- [x] 저장 초안/배포 스냅샷 URL 판정, 결과 버전 표시·입력 변경 시 결과 제거·360px.
- [x] 로컬 Setup의 환경 확인·실측 calibration·검토/apply·관리자 등록 및 저장 결과 조회.
- [x] 고정 샘플 Traffic Lab 실행·결과 저장·중지·다운로드.
- [x] 로컬 3역할/3엔진 접근성 자동 검사·키보드·320px와 명령 재시도/감사 회귀.
- [x] Admin 재인증·설치 전체 generation 확인을 거친 새 epoch 명령과 영향 검토 UI.
- [x] 실제 v3/v4 데이터 이행, 이전 writer 차단, 7개 볼륨 콜드 백업과 새 프로젝트 복원.
- [ ] 실제 보조기기 사용자 acceptance 및 production 설치 wizard.

### 2026-09-09 로컬 후보 검증 범위

`make check`, 전체 PostgreSQL 인증/운영 race, Valkey/Lab integration과 3엔진 인증/설치/
Room/Traffic 브라우저 21개 회귀를 통과했다. runtime v5는 5개 seed × 300 visitor의
모델 비교, 두 Room 합계 10,000개 상한, 공유 fence와 손상된 인덱스의 입장 차단을 검증했다.
콜드 복원 후 실제 primary 재시작 안전 대기, v4 → v5 이행 후 기존 HTTP 재시도 응답과
대기 순서 보존, claim 뒤 mTLS origin 도달을 별도 Docker 환경에서 검증했다.
최종 v5 Docker HTTPS의 1K/2K/5K/10K 대기열 생성·전원 조회·각 구간 100회 exact retry,
10K 초과 용량 오류, 설정한 7개 lease의 claim과 원본 도달도 PASS다. 구형 4프로세스
`TestProcessHTTPVisitorTiers`는 동일 4개 구간 × 10회 race 반복(84.211초)을 통과했다.
인원수 경계 검사이며 10K 지속 부하 qualification을 대체하지 않는다.

이는 로컬 후보의 구현/검증 진척이다. 과거 `autonomous-final-20260906/http-visitor-tiers-3.log`의
503은 응답 problem code와 최초 실패 상태가 없어 당시 원인을 확정하지 못했다.
현재 재실행 PASS를 당시 실패의 원인 규명으로 대체하지 않는다. B0와 전체 acceptance 판정이
남아 있으므로 공개 Preview 명칭과 Beta NO-GO를 유지한다.

추가 구현한 [공개 API 제한](operators/public-api-validation.md)은 설치 공통 poll 일정,
출처별 신규 join/기존 요청 예산, 불변 함수 검증과 bounded metadata를 사용한다. 조기 poll은
queue를 읽지 않으며 재접속은 분산한다. 실제 HTTPS와 모바일 브라우저에서 429 재시도,
새로고침/탭 복귀, DRAINING의 안내 화면/JSON과 입장권을 가진 5개 HTTP 메서드를 검증했다.
기존 요청의 무제한 반복도 차단하며, 공통 대기 화면 footer의 대비를 보완했다.

다음 수용 검증은 공개 API의 나머지 상태/장애 조합, 운영 proxy/NAT별 source quota,
만료 후 재시도·key rotation 경계와 실제 보조기기 사용성을 owner sub-PRD에 대조해야 한다.
로컬 설치와 관리자 기능의 PASS를 이 미검증 항목의 PASS로 확장하지 않는다.
최종 고정 후보의 통합 재실행과 B6 판정 전에는 Beta 출시 artifact를 만들거나 공개하지 않는다.
수정한 검사기의 실제 60분 30초 새 epoch 전체 여정도 통과했다. 안전 대기 중 121회
관측, 관리자 재로그인, 검증 후 HOLD, 명시적 AUTO와 epoch 2 입장권의 실제 mTLS 원본
도달을 확인했다. 장시간 검사는 고정된 복구 코어 이미지에서, 이후 공개 API 후보의 새 epoch
ACK/차단은 별도 환경에서 검증했다. 이 두 실행을 최종 배포 artifact 전체 수용 검증으로
합치지 않는다. [복구 검증의 정확한 범위](operators/recovery-upgrade.md).

### 키 회전·재시도 후속

[2026-09-09 검증](evidence/beta-20260909-key-replay.md)에 키 회전의 실제 ACK·기존 방문자
보존·긴급 폐기와 새 v6 join 기록의 만료 후 응답 보존을 추가했다. B0의 과거 원인을
새로운 재실행 PASS로 대체하지 않으며, 전체 GA sub-PRD와 로컬 M3 검증을 구분한다.

B2~B4는 구현된 로컬 명령·Room runtime·인증/설치 화면 범위에서 완료 표시했다.
B1의 cookie 없는 최초 browser join 응답 유실/동시 최초 탭은 후속 검증으로 완료했다.
B0에는 원인을 보존하지 못한 구형 503 기록이 남는다. 반복 통과 횟수나 명칭 변경으로
이 항목을 닫지 않는다. production wizard/HA/100K는 MAIN의 M4 이후 범위다.

최종 키 회전 이미지의 전체 운영 회귀에서 Admin 새 epoch 승인 뒤 양 노드 ACK가
30초 안에 갱신되지 않은 사례를 발견했다. 공개 HTTP fixture의 통과와 구분하며
B1/B5의 미해결 항목에 추가한다. [실패 증거](evidence/beta-20260909-key-replay.md).

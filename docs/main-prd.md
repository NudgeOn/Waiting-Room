---
schema: waiting-room.main-prd/v1
id: MAIN-PRD
title: Waiting Room Product and Final Release Gate
version: 0.1
spec_status: ready_for_implementation
delivery_decision: NO-GO
document_split: GO
evidence_status: PARTIAL
m0_foundation_decision: GO
last_updated: "2026-09-10"
license: Apache-2.0
sub_prds:
  - SUB-PRD-01
  - SUB-PRD-02
  - SUB-PRD-03
  - SUB-PRD-04
  - SUB-PRD-05
  - SUB-PRD-06
  - SUB-PRD-07
  - SUB-PRD-08
---

# Waiting Room Main PRD

> 이 문서는 제품 전체 방향, 문서 간 계약과 **최종 통합 테스트·출시 GO/NO-GO**만 관리한다. 구현 세부사항과 unit test 결과는 각 `sub-prd_0x.md`의 source of truth를 따른다.

## 1. 한 문장 비전

**“WordPress처럼 켜지만, WordPress보다 앞에서 막는 Apache-2.0 셀프호스트 Waiting Room.”**

기존 웹사이트와 앱 API 앞에 설치해 갑작스러운 유입을 대기열로 흡수하고, 비개발 운영자도 관리자 화면에서 경로·입장 유량·일정·보안을 제어할 수 있게 한다.

## 2. 확정된 제품 경계

- 우선 사용자: 전담 인프라 인력이 부족한 소규모 웹팀과 이벤트 운영자
- v1 queue policy: 단일 리전 FIFO
- 연동: 브라우저 redirect/cookie와 앱 JSON API
- data plane/control: Go, Admin UI: React
- durable data: PostgreSQL, runtime queue: Valkey
- 배포: Standard 10K Compose와 High Scale 100K Helm
- 보안: Admin·Operator·Viewer, 전역 TOTP ON/OFF, 배포 강제 ON 지원
- 운영: OFF/AUTO/HOLD, 안전 종료 DRAINING, 장애 RECOVERY_HOLD, 일회성 예약
- 라이선스: Apache-2.0, 기본 외부 telemetry 없음
- v1 제외: multi-region active-active, 공식 mobile SDK, 계정 기반 중복 방지, CAPTCHA, SaaS

10K/100K는 별도 제품이 아니라 같은 production image의 qualification profile이다. 숫자는 설치 전체의 `WAITING + READY + token expiry 및 최대 30초 leeway 종료 전 ADMITTED` visitor-state hard cap이며 실제 동시 TCP 연결 수를 뜻하지 않는다.

## 3. AI 문서 읽기 규칙

1. 새 작업은 이 문서를 먼저 읽는다.
2. 다음 표에서 작업 대상 sub-PRD 하나와 그 `depends_on` 문서만 추가로 읽는다.
3. 세부 계약이 충돌하면 해당 영역의 sub-PRD가 우선하고, 영역 간 충돌은 이 문서의 결정 기록을 갱신해 해결한다.
4. 구현 후에는 대상 sub-PRD의 checklist와 unit test 실행 로그를 같은 변경에서 갱신한다.
5. 명령을 실행하지 않았거나 증거가 없으면 `PASS` 또는 `GO`로 기록하지 않는다.
6. 모든 sub-PRD가 GO가 된 뒤에만 이 문서의 최종 통합 테스트를 실행한다.

## 4. Sub-PRD 지도와 현재 판정

| ID | 파일 | Source of truth | Depends on | 현재 delivery 판정 |
|---|---|---|---|---|
| SUB-PRD-01 | [sub-prd_01.md](sub-prd_01.md) | 제품 동작·운영 모드·알고리즘 확장 | MAIN | NO-GO |
| SUB-PRD-02 | [sub-prd_02.md](sub-prd_02.md) | FIFO state·capacity/rate·복구 불변조건 | 01 | NO-GO |
| SUB-PRD-03 | [sub-prd_03.md](sub-prd_03.md) | 공개 Web·App API·cookie/token | 01, 02 | NO-GO |
| SUB-PRD-04 | [sub-prd_04.md](sub-prd_04.md) | Admin API·RBAC·TOTP·관리 UX | 01, 02 | NO-GO |
| SUB-PRD-05 | [sub-prd_05.md](sub-prd_05.md) | process·storage·key·내부 보안 | 02, 03, 04 | NO-GO |
| SUB-PRD-06 | [sub-prd_06.md](sub-prd_06.md) | 설치 wizard·10K/100K·region·비용 | 01, 02, 04, 05 | NO-GO |
| SUB-PRD-07 | [sub-prd_07.md](sub-prd_07.md) | local lab·통합/부하/장애 qualification | 02, 03, 04, 05, 06 | NO-GO |
| SUB-PRD-08 | [sub-prd_08.md](sub-prd_08.md) | OSS·repo·milestone·release artifact | 01, 03–07 | NO-GO |

M0와 기존 lab 검증에 이어 로컬 Docker Gateway/Coordinator/Control·Backoffice 운영·signed publish·복구와 보안 정책을 부분 검증했다. 로컬 복구 운영 도구는 [복구·업그레이드](operators/recovery-upgrade.md)를 따른다. 전체 UX·production 복구·qualification acceptance가 남아 모든 sub-PRD delivery는 NO-GO다. 최신 [Beta 계획](beta-plan.md), [runtime 기록](evidence/beta-runtime-progress.md)과 과거 [M0](evidence/m0-summary.md), [M1](evidence/m1-summary.md), [template](evidence/browser-template-summary.md) 증거를 구분한다.

## 5. 공통 용어와 불변조건

- `visitor state`: WAITING, READY 또는 token expiry + 최대 verifier leeway 종료 전 ADMITTED 한 건
- `active admission lease`: 실제 online user가 아니라 READY 예약 또는 token expiry + leeway까지 점유하는 입장 slot 한 건
- `strict FIFO`: 정상 단일 active-primary epoch에서 만료 ticket을 제외한 sequence 역전 0건
- `fail-closed`: queue/config 상태가 불확실할 때 신규 사용자를 origin으로 우회시키지 않음
- `profile badge`: 저장 가능성만이 아니라 정해진 join→poll→claim→proxy→expiry와 장애 시험을 같은 image digest로 반복 통과한 표식

모든 profile에서 다음 조건은 0건이어야 한다.

- capacity lease와 최근 60초 admission rate 초과
- duplicate ticket·READY claim·admission 발행
- Gateway를 경유한 비입장 요청의 origin 도달
- 정상 primary 구간의 FIFO 역전
- config rollback 수락과 외부 Waiting Room header 신뢰

## 6. 구현 순서

2026-09-06 사용자 선택: **로컬 Docker부터 Beta 완성**.
[진행 계획](beta-plan.md), [로컬 Docker 설치](local-docker.md), [현재 증거](evidence/beta-runtime-progress.md).
runtime 배포/입장/Valkey 재시작 복구/계정·TOTP 정책과 반복 upgrade 보존은 PASS했다.
예약 browser 및 인증 감사 원자성도 검증했다. Dashboard·Room 4개 주소/탭·5단계 초안 작성도
[부분 검증](evidence/admin-workspace-summary.md)했다: browser 12, Docker 통합 17 + 새 설치 8 PASS.
[읽기 전용 URL 판정](evidence/admin-route-check-summary.md)도 추가 검증했다: 단위/PG PASS,
Docker URL 8개 시나리오·Web/App·재시작 포함 8 checks PASS. 앞선 17+8 시험과 다른 이미지다.
로컬 설치 wizard의 보정·설정 적용·첫 관리자 연결은 [후속 구현](operators/setup-wizard.md)을 따른다.
Quick 20·Smoke 1K UI의 실 HTTP 실행·결과 저장은 [Traffic Lab](operators/traffic-lab.md)을 따른다. 구현된 로컬 명령 lifecycle·3역할/3엔진 운영 화면 검증은 [관리자 검증](operators/admin-validation.md)을 따른다. 로컬 v3/v4 이행·새 epoch·콜드 백업/복원은 [복구·업그레이드](operators/recovery-upgrade.md)에 구현했다. 재현된 503 수정과 최신 이미지의 로컬 M2 검증은 완료했다. 최초 진단 없는 과거 로그는 원인 미확정으로 보존하며, production 설치와 M3 사용성 acceptance는 남아 있다.
**Beta NO-GO**이며 아래 최종 테스트는 아직 실행하지 않는다.

2026-09-12 페이지 연결·대기 순번/예상시간 작업 후보에서도 같은 이미지의 10K 콜드 보존,
키/백업, 실제 60분 30초 복구 여정을 재검증했다. [Beta 계획](beta-plan.md)에 미커밋 후보의
식별과 검증 범위를 기록했으며, 운영자 사용성 결과 미확보로 B6는 NO-GO를 유지한다.

2026-09-10 고정 커밋에서 확인한 단계는 다음과 같다. [후보별 결과](evidence/beta-20260910-m2-latency.md)를
기준으로 구현·단계 수용·운영 qualification을 구분한다. VoiceOver는 사용자 요청으로 제외했다.

| 단계 | 목표 | 상태 | 주 sub-PRD | 단계 종료 조건 / 남은 항목 |
|---|---|---|---|---|
| M0 | 계약·threat model·test skeleton | ✅ 완료 | 01–08 | OpenAPI/ADR/manifest와 executable test 기반 GO |
| M1 | Valkey FIFO walking skeleton·앱/브라우저 lab | ✅ 로컬 단계 완료 | 02, 03, 05 | 5 seed 모델/Valkey command trace, 두 Gateway 혼합 Quick20, race·FIFO·재시작 replay 검증 |
| M2 | 안전한 Gateway alpha | ✅ 로컬 수용 완료 | 02, 03, 05 | 같은 이미지의 서명/양 ACK·LKG·공개 장애·Valkey 일시 중단 복구·10K/콜드 보존·60분 30초 전체 epoch·키/백업 PASS |
| M3 | 운영 가능한 Beta | 🟡 기술 검증 완료·사용성 수용 대기 | 04 | 설치/Room wizard·TOTP/RBAC·예약·감사·Traffic Lab 연결. 같은 이미지의 81개 화면/32개 API·복구 PASS. 실제 운영자 사용성·B6 최종 GO 남음 |
| M4 | Docker Compose Standard 10K RC | ⬜ qualification 미완료 | 06, 07, 08 | 설치/업그레이드/복원은 구현. 10K 인원 경계와 별도로 지속 부하 qualification 3회 필요 |
| M5 | Helm High Scale 100K RC | ⬜ 예정 | 06, 07, 08 | Helm/HA 장애와 100K qualification 3회+soak |
| M6 | v1.0 GA | ⬜ 예정 | MAIN, 08 | 모든 sub-PRD GO와 아래 final test PASS |

v1.0 GA는 Standard와 High Scale gate를 모두 통과할 때만 발행한다. 100K gate가 실패하면 v1.0을 지연하며 `High Scale 100K` 이름이나 badge를 사용하지 않는다.

### M0 기반 구현 Checklist

- [x] Apache-2.0·Git·Go/Node 개발 scaffold
- [x] Public/Admin OpenAPI skeleton과 schema 테스트
- [x] FIFO registry와 단일 스레드 queue reference model
- [x] ADR·threat/failure matrix·지원 버전 후보·부하 목표 manifest
- [x] PRD·의존성·evidence hash·부하 산술 검증기
- [x] 로컬 `make check` PASS 및 sub-PRD별 부분 결과 기록

M0 기반 구현 판정은 **GO**, M1 착수 가능이다. G0 전체 계약 승인과 G1 분산 correctness,
각 sub-PRD delivery 또는 실제 성능 검증을 대신하지 않는다.

### M1 진행 현황

- [x] 실제 Valkey 단일 Room FIFO·claim·capacity/rate 저장소
- [x] 두 Gateway의 앱 JSON Quick20 연결: 3명 입장·17명 대기
- [x] 동시 요청·실제 서버 시간·부분 model trace 검증
- [x] `calm` 내장 template·공통 browser state machine·cookie/303·탭별 복귀의 local 검증
- [x] 독립 Gateway handler 2개·동일 public host의 Web/App 혼합 20명 HTTP 여정·공유 return key (실행/한계는 아래 기록)
- [x] 별도 PID Gateway 2개·Coordinator·origin lab, 실제 Coordinator 종료·Gateway 교체 HTTP 시험 (운영 권한/네트워크 격리 아님)
- [x] unsharded lab 설치 합계 cap 및 1K/2K/5K/10K 직접 Store 검증 ([범위·증거](evidence/installation-capacity-summary.md))
- [x] lab 설치 v2의 만료 데이터 용량 유지·Room 유실 초기화 거부 ([회귀 범위](evidence/installation-retention-summary.md))
- [x] 4-PID app HTTP 1K/2K/5K/10K·교차 replay·원본 보호·10K cap·Coordinator 종료 ([HTTP 범위·증거](evidence/http-visitor-tiers-summary.md))
- [x] configtrust core/FileStore와 process lab Gateway/Coordinator signed bootstrap·cold-start·expiry 차단 ([범위·증거](evidence/signed-config-summary.md))
- [x] Backoffice template 선택·preview·Room별 저장 (M3, 로고 sanitizer 포함 후속 완료)
- [x] M1 model/Valkey command trace 5 seed × 300 visitor·공유 fencing/recovery 회귀
- [x] 혼합 Web/App Quick20과 실제 3엔진 visitor·6역할 Docker 연결
- production/HA·모든 장애 조합·지속 부하 qualification은 M2~M5의 후속 gate로 추적한다.

독립 Gateway 간 복귀 실패 수정과 혼합 여정·실제 만료·claim 응답 유실 검증은
[핵심 여정 기록](evidence/mixed-journey-summary.md)을 따른다. 동일 process의 독립 handler 시험이며
별도 process·설치 전체 cap·production Gateway 완료가 아니다.
후속 [별도 프로세스 기록](evidence/process-lab-summary.md)은 local OS process 분리와 장애 시험을 추가한다.
production role image·OS/network 격리·production 서명 설정 배포/rotation·복구 검증은 계속 남아 있다.

**2026-09-10 M1 로컬 walking skeleton 판정: GO**. M1의 종료 조건은 위 표와
SUB-PRD-08의 model trace·Quick20·race·restart persistence다. 최신 소스 CI와
[실제 통합 trace/혼합 Quick20](evidence/beta-20260910-logo-maintenance/maintenance-integration.log),
[최신 이미지 콜드 복원·기존 FIFO 입장](evidence/beta-20260910-logo-maintenance/expiry-v5-backup.log)이 이를 뒷받침한다.
이전 [M1 초기 기록](evidence/m1-summary.md)의 PARTIAL은 당시 결과로 보존한다.
이 판정은 각 sub-PRD 전체 delivery, M2/M3 Beta 승인, M4/M5 qualification의 GO가 아니다.

### M3 인증 기반 선행 구현

- [x] Admin/Operator/Viewer 중앙 action policy·세션/CSRF 검증 함수
- [x] RFC TOTP core·store interface·재사용/동시 요청 단위 검증
- [x] PostgreSQL TOTP challenge 소비·세션 발급 원자 transaction과 암호화 저장 (local fixture)
- [x] Argon2id 비밀번호→TOTP challenge/OFF session·공유 로그인 제한 (서버 service, local fixture)
- [x] 현재 DB session 조회·명시적 idle 갱신·삭제, me/logout 부분 HTTP adapter (임시 TLS 시험)
- [x] 최초 Admin bootstrap service·일회성 설치 token·TOTP ON 등록 전용 proof
- [x] TOTP 최초 등록 service·암호화 비밀키·OTP 확인·복구 hash 10개·MFA session 원자 저장
- [x] 복구 로그인 service: password proof·공유 한도·일회성 코드/challenge/session 원자 처리
- [x] 로그인/OTP/복구 HTTP·보안 cookie·Origin/JSON 경계·DB 공유 제한 (임시 TLS만 검증)
- [x] opt-in loopback bootstrap HTTP·수동 키 등록/OTP 완료·생성된 복구 코드 로그인 (임시 TLS)
- [x] React 인증/QR/복구/세션 화면·실행 가능한 disposable Admin Lab (운영 Control 아님)
- [x] 로컬 bootstrap CLI·정책/profile wizard·private/setup listener·Room 운영/설정 Backoffice (2026-09-10 후속 검증)

인증 라이브러리의 부분 결과는 [auth core 기록](evidence/admin-auth-summary.md)을 따른다.
DB adapter 결과는 [PostgreSQL 기록](evidence/admin-store-summary.md)으로 구분한다.
비밀번호 service와 calibration 도구는 [로그인 기록](evidence/password-login-summary.md)을 따른다.
세션 service와 부분 HTTP 연결은 [세션 기록](evidence/session-service-summary.md)을 따른다.
최초 관리자 생성과 등록 전용 proof는 [bootstrap 기록](evidence/bootstrap-summary.md)을 따른다.
TOTP 등록 완료 service와 복구 코드 저장은 [enrollment 기록](evidence/enrollment-summary.md)을 따른다.
복구 코드 로그인 service는 [recovery 기록](evidence/recovery-summary.md)을 따른다.
인증 HTTP와 쿠키 연결은 [auth HTTP 기록](evidence/auth-http-summary.md)을 따른다.
초기 설정·등록 HTTP 연결은 [provisioning HTTP 기록](evidence/provisioning-http-summary.md)을 따른다.
실제 React 인증 화면·로컬 실행은 [Admin UI 기록](evidence/admin-ui-summary.md)을 따른다.
이는 M1/M2 gate 통과나 M3 beta 완료를 뜻하지 않는다.

### 통합 gate 현황

설치 선행 기반: `wrctl plan`의 오프라인 10K/100K·TOTP 입력 검증과 결정적 proposal을
추가했다. [SUB-PRD-06](sub-prd_06.md)과 [실행 기록](evidence/install-plan-summary.md)을 따른다.
환경 검사·production 위자드·apply·동일 image/10K·100K qualification은 미완료다.
`wrctl preview`의 3단계 React 계획·비용 미리보기는 [위자드 부분 기록](evidence/installation-preview-summary.md)을 따른다.
DB/관리자 등록/설치 없이 loopback에서만 실행하며 production bootstrap wizard 완료가 아니다.
계획/비용 JSON 다운로드·CLI 보고서는 [보고서 부분 기록](evidence/planning-report-summary.md)을 따른다.
실제 설치 결과나 서명된 config export/backup이 아니다.
사용자 단가 기반 `wrctl estimate` 소계/비율 계산은 [비용 부분 기록](evidence/cost-estimate-summary.md)을 따른다.
Clock/store/origin·100K event checkpoint의 pure preflight 판정 기반도 추가했다.
[부분 테스트 기록](evidence/preflight-policy-summary.md)은 실제 관측 수집·runtime activation 검증과 구분한다.
`wrctl doctor-clock`의 Linux chrony collector 부분 구현은 [시계 진단 기록](evidence/clock-diagnostic-summary.md)을 따른다.
fixture/subprocess와 macOS 미지원 경계 검증이며 Linux 실제 동기화·runtime activation 성공 증거는 아니다.

| Gate | 필요한 evidence | 현재 판정 |
|---|---|---|
| G0 Contract Ready | sub-PRD 승인, ADR/OpenAPI/support matrix, requirement ownership | NO-GO |
| G1 Queue Correctness | model/property/race/Valkey persistence | BLOCKED |
| G2 Safe Data Plane | browser/app/proxy/security/failure matrix | BLOCKED |
| G3 Operability | Admin/TOTP/RBAC/Wizard/a11y | BLOCKED |
| G4 Standard 10K | Compose clean install + qualification 3회 | BLOCKED |
| G5 High Scale 100K | Helm/failover/qualification 3회 + soak | BLOCKED |
| G6 v1.0 GA | G0~G5 PASS + supply-chain + MAIN final test | BLOCKED |

## 7. Sub-PRD 공통 GO/NO-GO 규칙

각 sub-PRD는 아래 조건을 모두 만족할 때만 delivery `GO`로 바꾼다.

- [ ] 범위 내 구현 checklist가 모두 완료됨
- [ ] 해당 문서의 모든 필수 unit test가 PASS이고 실제 명령·commit·결과가 기록됨
- [ ] 관련 contract/integration test가 PASS함
- [ ] 열린 P0/P1 결함과 해결되지 않은 security-critical 항목이 없음
- [ ] 의존 sub-PRD가 모두 GO임
- [ ] reviewer와 판정 시각이 기록됨

하나라도 충족하지 않으면 NO-GO를 유지하고 이유와 다음 해소 조건을 적는다.

## 8. 최종 통합 테스트 — MAIN에서만 실행

### 8.1 실행 전제

- [ ] SUB-PRD-01~08이 모두 GO
- [ ] 시험 대상 git commit과 production image digest가 고정됨
- [ ] Standard와 High Scale이 같은 production image digest를 사용함
- [ ] SUT, load generator, deterministic origin이 분리됨
- [ ] backup recipient/KMS와 recovery runbook이 준비됨
- [ ] test manifest에 host·storage·network·clock·dependency version이 기록됨

### 8.2 Final test suite

| FT-ID | 최종 시험 | 합격 조건 | 현재 결과 | Evidence |
|---|---|---|---|---|
| FT-01 | Clean install | 새 환경에서 문서만으로 Compose와 Helm 설치 | NOT RUN | — |
| FT-02 | Browser/App E2E | join→poll→claim→origin, unsafe method, return URL 모두 PASS | NOT RUN | — |
| FT-03 | Security | TOTP/RBAC/CSRF/service auth/key rotation/SSRF/headers PASS | NOT RUN | — |
| FT-04 | Recovery | Control/Coordinator/Valkey 장애에서 fail-closed와 안전 대기 PASS | NOT RUN | — |
| FT-05 | Standard 10K | 같은 digest로 qualification 3회 연속 PASS | NOT RUN | — |
| FT-06 | High Scale 100K | qualification 3회, 2시간 soak, reconnect와 N-1 PASS | NOT RUN | — |
| FT-07 | Upgrade/restore | backup→restore, N-1 upgrade, rollback PASS | NOT RUN | — |
| FT-08 | Supply chain | amd64/arm64, SBOM, provenance, signature, license gate PASS | NOT RUN | — |
| FT-09 | External reproduction | project 외부 환경의 clean install·runbook PASS | NOT RUN | — |

`make test-final` 진입 guard는 구현됐으며 sub-PRD NO-GO 또는 최종 runner 미구현이면 실패한다. 최종 통합 suite 자체는 미구현이다. guard의 의도된 거부는 FT PASS가 아니다. 실제 실행 시 정확한 command, commit SHA, image digest, 시작/종료 시각과 artifact 경로를 아래 로그에 남긴다.

### 8.3 Final test 실행 로그

| Run | Commit | Image digest | Environment manifest | Passed/Failed | Result | Evidence |
|---|---|---|---|---:|---|---|
| — | — | — | — | — | NOT RUN | 구현 전 |

## 9. MAIN 최종 GO/NO-GO

**현재 판정: NO-GO**

사유: M1/M2 로컬 수용과 M3 핵심 기능·기술 검증은 완료했다. 실제 운영자 사용성 수용, production/HA·10K/100K 반복 qualification과 FT-01~09 및 출시 artifact 검증은 남아 있다.

최종 GO 조건:

- [ ] 모든 sub-PRD가 GO
- [ ] FT-01~FT-09 전부 PASS
- [ ] 같은 RC image digest로 10K·100K 반복 qualification 통과
- [ ] 공개 P0/P1 defect 0, Critical 취약점 0
- [ ] 출시 artifact와 운영·복구 문서 검증 완료
- [ ] 최종 reviewer, UTC 판정 시각과 evidence bundle 기록

## 10. 결정 기록

| 날짜 | 결정 | 이유 |
|---|---|---|
| 2026-09-05 | v1은 FIFO만 제공 | correctness와 운영 안전성을 먼저 검증 |
| 2026-09-05 | algorithm registry는 확장 계약으로 유지 | lottery/priority/weighted를 API 재설계 없이 후속 추가 |
| 2026-09-05 | Standard 10K와 High Scale 100K 분리 | 비용·HA·운영 복잡도가 다름 |
| 2026-09-05 | v1 single-region, multi-region은 후속 | 글로벌 합의 복잡성을 v1에서 제외 |
| 2026-09-05 | TOTP configurable+default ON | 쉬운 설치와 관리자 보안의 균형 |
| 2026-09-05 | final integration test는 MAIN에서만 판정 | 부분 PASS를 전체 출시 PASS로 오인하지 않기 위함 |

## 11. 남은 비차단 결정

- 정식 제품명·logo·visual identity: SUB-PRD-04 구현 전에 선택
- 정확한 PostgreSQL·Valkey·Kubernetes 시험 후보: [support-matrix.yaml](../support-matrix.yaml)에 고정, 실제 지원 인증은 M4/M5
- backup retention과 audit log 기본 보존 기간: threat model 뒤 고정
- multi-region Home Region 장애 복구 계약: v1 이후 별도 ADR

### 2026-09-09 후속 구현

로컬 배포 키의 선배포/ACK/활성화/폐기/긴급 폐기, 회전된 키의 콜드 복원과 v6 신규 join의
만료 후 원래 응답 보존을 [추가 검증](evidence/beta-20260909-key-replay.md)했다.
[Beta 계획](beta-plan.md)의 로컬 B2~B4 완료를 반영하되, 남은 B0/B1과 최종 Beta 판정을
GA 전체 sub-PRD 판정과 구분한다.

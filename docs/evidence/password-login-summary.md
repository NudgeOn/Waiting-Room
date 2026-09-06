# Password login — local evidence

서버 내부 password-login slice: **GO**. SUB-PRD-04·MAIN delivery: **NO-GO**.
Reviewer: Codex, 2026-09-05 UTC. HTTP 로그인/Backoffice beta 완료 판정이 아니다.

## 실행 식별

- Run: `password-login-20260905-local`
- UTC: 2026-09-05 08:03:53–08:06:39
- Source SHA-256: `d89cd73c591141e41aee62c03f0249ce8b109db40be7bdd2c0622af48767922a`
- Commit 없음, uncommitted snapshot; docs/evidence 제외 source hash의 실행 전후 불변 확인
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11, pgx 5.10.0, x/crypto 0.56.0
- 전용 loopback DB와 각 시험이 만든 임시 schema만 사용

[보고서·로그 SHA-256](password-login-20260905-local/report.json) ·
[환경](password-login-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race unit/vet/format·Node·PRD 9개·OpenAPI 2개·contract 7개 | PASS | [foundation](password-login-20260905-local/foundation.log) |
| auth core/password/vault/fingerprint 단위 | 26 PASS, 0 FAIL (기존 21 + 신규 5) | [unit](password-login-20260905-local/admin-unit.log) |
| 실제 PostgreSQL 통합 top-level 시나리오 | 13 PASS, 0 FAIL (기존 6 + password 7; 함께 실행된 unit 5 제외) | [integration](password-login-20260905-local/postgres-integration.log) |
| 기존 PostgreSQL 시나리오 10회 반복 | 60 PASS, 0 FAIL | [DB repeat](password-login-20260905-local/postgres-repeat.log) |
| password 시나리오 3회 반복 | 21 PASS, 0 FAIL | [password repeat](password-login-20260905-local/password-repeat.log) |
| password envelope parser fuzz | 5초, 401,736회, PASS | [fuzz](password-login-20260905-local/password-fuzz.log) |
| 이 Mac의 비용 측정 | 64 MiB / p=1 / t=7, 3회 중간값 306ms, 목표 구간 충족 | [calibration](password-login-20260905-local/password-calibration.log) |
| MAIN final 차단 guard | 예상 exit 2, 8개 SUB NO-GO 유지 | [guard](password-login-20260905-local/final-guard.log) |

실제 password 검증 → ON일 때 challenge → OTP 확인 → session을 검증했다.
OFF일 때는 MFA라고 표시하지 않은 session을 발급했으며, ON+미등록 계정에는
session을 발급하지 않았다. 틀린 비밀번호·없는 사용자·비활성 계정은 같은 실패 결과다.
두 pool에서 account 예산 경쟁, source/install 한도, 만료 후 재개와 bucket 정리를 검증했다.
직접적인 stale snapshot 검사뿐 아니라 **미커밋 정책 갱신과 실제 Login 호출의 경합**도
재현하여, 비밀번호 검증 후 정책 변경을 기다린 요청이 credential을 받지 못함을 확인했다.
Challenge INSERT 실패 시 기존 challenge 취소는 rollback되며 이미 차감한 시도는 유지됐다.

초기 DB 실행에서 만료된 throttle bucket 갱신이 unavailable로 실패했다. timestamp와
interval 연산의 parameter 타입을 명시한 뒤 두 실패 사례 및 전체 suite를 재검증했다.
이 bundle은 수정 후 새 소스 실행이며, 이전 실패를 PASS로 바꾼 기록이 아니다.

## 해석과 남은 범위

- PostgreSQL 설계 지침을 반영해 비밀번호 KDF를 DB 잠금 밖에서 실행하고,
  짧은 최종 transaction에서 user/password/policy snapshot을 다시 검사한다.
- 통합 시험의 hash는 t=2인 **격리 fixture**다. 별도 non-race calibration은 이 호스트의
  실행 당시 측정이며 설치 reference host 승인, 안정적 latency 보장, 설정 적용이 아니다.
  이전 탐색 실행과 선택 값이 달라질 수 있으므로 실제 배포 환경에서 다시 측정해야 한다.
- 최초 관리자/계정 provisioning, password 변경·hash upgrade, TOTP 등록/복구,
  private HTTP·cookie middleware·session 읽기/갱신/logout·React UI는 아직 미연결이다.
- source 식별은 신뢰된 peer를 받는 내부 service 계약이다. 실제 proxy trust, HTTP 수준
  enumeration/timing, OTP endpoint 전체 traffic 제한, audit/last-Admin 보호는 미완료다.
- 실제 강제 ON 배포 설정, key-file 관리, DB restart/failover, 별도 Control process,
  브라우저·인증 앱 기기 시험, 10K/100K qualification·MAIN 최종 통합은 하지 않았다.
- 원격 CI·commit·push·배포 없음. 운영 계정/비밀번호/외부 설정은 변경하지 않았다.
- 테스트가 생성한 schema만 정리했다. 전용 auth DB 컨테이너 중지를 확인했고
  named volume은 보존했다. 기존 Valkey lab 및 사용자 데이터는 변경하지 않았다.

[구현 계약](../security/password-login.md) · [재현 명령](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

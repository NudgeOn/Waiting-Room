# Recovery login — local evidence

제한된 password challenge·복구 코드 로그인 service 검증: **GO**.
SUB-PRD-04·MAIN delivery: **NO-GO**. HTTP/UI·reset·재발급 완료를 뜻하지 않는다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별

- Run: `recovery-20260905-local`
- UTC: 2026-09-05 09:28:37–09:34:09
- Source SHA-256: `f9529923aa1d9b5543faa1815d8464c5dff78e9d094b54790caca6f70f2f0c80`
- Commit 없음, uncommitted snapshot; docs/evidence 제외 source의 실행 전후 불변 확인
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11
- 전용 loopback DB·테스트별 무작위 schema·한 프로세스 안의 두 독립 connection pool

[보고서·7개 로그 hash](recovery-20260905-local/report.json) ·
[환경](recovery-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race/vet/format·Node 10개·PRD 9개·OpenAPI 2개·contract 8개 | PASS | [foundation](recovery-20260905-local/foundation.log) |
| 인증 단위 테스트 | 36 PASS, 0 FAIL (기존 34 + 신규 2; fuzz seed 별도) | [unit](recovery-20260905-local/admin-unit.log) |
| DB/TLS integration 시나리오 | 39 PASS, 0 FAIL (기존 34 + 복구 DB 5; unit/subtest 제외) | [integration](recovery-20260905-local/postgres-integration.log) |
| 복구 DB 5개 × 3회 | 15 PASS (함께 실행한 신규 unit 6 PASS 별도) | [recovery repeat](recovery-20260905-local/recovery-repeat.log) |
| TOTP/PostgreSQL 6개 × 10회 | 60 PASS | [DB repeat](recovery-20260905-local/postgres-repeat.log) |
| Session DB/TLS 9개 × 10회 | 90 PASS (projection unit 10 PASS 별도) | [session repeat](recovery-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2; SUB 8개 NO-GO 유지 | [guard](recovery-20260905-local/final-guard.log) |

확인한 사항:

- 실제 service 연결: bootstrap → enrollment/OTP → 생성된 복구 코드 → password login →
  recovery Complete → 현재 DB 세션 Me. 등록/session token을 challenge로 사용하면 거부.
- 틀린 비밀번호는 challenge를 발급하지 않음. 복구 4회 실패 후 올바른 다섯 번째 요청 성공.
- 코드 한 개만 소비, fresh challenge에서도 사용한 코드/알 수 없는 정상 형식 코드는 거부.
- 성공 후 기존 세션·나머지 코드·TOTP credential/counter 보존. 소비한 challenge의 OTP 재사용 거부.
- OTP 실패 2회 + 복구 오류 16개 경쟁에서 공유 attempts=5, 이후 정상 OTP도 거부.
- 10-slot 저장 중 하나가 없는 경우 unavailable로 실패하며 누락 슬롯을 건너뛰지 않음.
- final DB 단계의 네 가지 경쟁: 동일 code/challenge, 같은 code/다른 challenge,
  다른 code/같은 challenge, OTP/recovery. 각 두 pool 경쟁에서 세션 발급 1건만 성공.
- disabled/role/user version/policy version/OFF/credential version/선택 hash 변경 거부.
- 세션 INSERT 실패 시 recovery/challenge 소비까지 rollback, 예약 횟수는 유지.
  아직 유효한 내부 준비 단계로 재시도하면 정상 성공.
- 실제 challenge row lock 대기 중 만료 거부, code/session 무변경.
- 기본 fmt/JSON redaction·잘못된 입력, guard 실패·unknown commit은 단위 double로 검증.

## 증거의 한계와 다음 단계

- PostgreSQL 스킬의 짧은 transaction·일관된 잠금 순서를 적용했다. Argon2는 잠금 밖에서
  수행하고 검증 후 현재 DB 상태를 다시 확인한다. 기존 PK가 recovery FK/index를 충족한다.
- 경쟁 시험은 빠른 SQL fixture(사용 가능 코드 2개, 해시를 공유하는 소모된 슬롯 8개)와
  KDF 이후 final DB 단계를 사용한다. 실제 코드 10개 생성/검증은 별도 service 연결 시험이다.
- 동시 HTTP/KDF 부하, 독립 Control 프로세스, 다중 리전, 실제 네트워크 commit-loss,
  DB restart/failover, MFA 앱 기기와 브라우저는 이번 검증에 포함하지 않았다.
- MFA session은 password+backup factor 완료를 뜻하며 fresh TOTP 재인증 증명이 아니다.
  위험 action의 one-time reauth, code 재발급/TOTP reset/last-Admin 복구는 계속 후속이다.
- 복구 HTTP API/화면·초기 cookie·Origin/CSRF·private listener·source/global 요청 제한,
  audit/idempotency와 운영 calibration은 미완료다. 기존 me/logout TLS는 SQL fixture 세션이다.
- 성공 commit의 응답을 잃으면 사용한 코드는 재표시/복구되지 않는다. 비밀번호 확인 뒤
  다른 미사용 코드가 필요하며, completed-command replay는 아직 없다.
- 기존 password/bootstrap/enrollment 통합은 회귀 실행했다. 각 전용 반복/fuzz/calibration은
  이 bundle의 미지정 flag와 구분한다. 이전 enrollment 기록의 복구 소비 미구현 상태는
  이 service 기록과 UT-04-06/19로 갱신되며 HTTP/UI 미완료 경계는 유지된다.
- 10K/100K qualification·MAIN 최종 통합·원격 CI·commit/push/배포는 하지 않았다.
- 테스트 schema/임시 TLS 서버 정리, 인증 DB 컨테이너 중지 확인, named volume 보존.
  실제 운영자 계정/기존 사용자 데이터/다른 Valkey lab은 변경하지 않았다.

[구현 경계](../security/admin-recovery.md) · [로컬 명령](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

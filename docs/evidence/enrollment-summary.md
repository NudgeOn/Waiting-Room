# Enrollment — local evidence

제한된 최초 TOTP 등록 service·복구 hash 저장 검증: **GO**.
SUB-PRD-04·MAIN delivery: **NO-GO**. QR/HTTP/UI·복구 코드 로그인은 포함하지 않는다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별

- Run: `enrollment-20260905-local`
- UTC: 2026-09-05 09:00:42–09:04:31
- Source SHA-256: `d41c42707f98774d4b03d10ab6357476c0a7d4b1a29f8672b14b2fc5d6b44a1e`
- Commit 없음, uncommitted snapshot; docs/evidence 제외 source 실행 전후 불변 확인
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11
- 전용 loopback DB·테스트별 무작위 schema·한 프로세스 안의 두 독립 pool

[보고서·7개 로그 hash](enrollment-20260905-local/report.json) ·
[환경](enrollment-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race/vet/format·Node 10개·PRD 9개·OpenAPI 2개·contract 8개 | PASS | [foundation](enrollment-20260905-local/foundation.log) |
| 인증 단위 테스트 | 34 PASS, 0 FAIL (기존 31 + 신규 3; fuzz seed 별도) | [unit](enrollment-20260905-local/admin-unit.log) |
| DB/TLS integration 시나리오 | 34 PASS, 0 FAIL (기존 29 + 등록 DB 5; unit/subtest 제외) | [integration](enrollment-20260905-local/postgres-integration.log) |
| 등록 DB 5개 × 3회 | 15 PASS (함께 실행한 신규 unit 9 PASS 별도) | [enrollment repeat](enrollment-20260905-local/enrollment-repeat.log) |
| TOTP/PostgreSQL 6개 × 10회 | 60 PASS | [DB repeat](enrollment-20260905-local/postgres-repeat.log) |
| Session DB/TLS 9개 × 10회 | 90 PASS (projection unit 10 PASS 별도) | [session repeat](enrollment-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2; SUB 8개 NO-GO 유지 | [guard](enrollment-20260905-local/final-guard.log) |

확인한 사항:

- 실제 service 연결: bootstrap → 등록 proof → Begin 수동 키 → OTP Complete → MFA session Me.
- Begin 재시도 시 같은 비밀키·같은 만료, 완료 전 credential/recovery/session 각각 0개.
- 실패 4회 뒤 올바른 다섯 번째 요청은 성공. 잘못된 요청 16개 경쟁에서 예약 한도 5회 유지.
- 성공 시 active credential 1개·복구 hash 10개·MFA session 1개, pending ciphertext 제거.
- 반환 복구 코드 10개의 길이·중복 없음·각 DB hash 존재, 대표 코드 1개의 실제 Argon2 검증.
- 완료 토큰 재사용/비밀키 재표시 거부, 정상 password login은 enrollment가 아닌 TOTP challenge.
- 등록에 소비한 counter가 active credential에 저장되며 정상 TOTP 로그인에서도 replay 거부.
- 준비된 해시를 사용한 final DB 단계 16개 경쟁: 두 pool에서 성공 1개·거부 15개.
- account 비활성/user version·policy version/OFF 변경 시 거부.
- recovery/session INSERT 실패 시 credential/recovery/session 모두 rollback. 예약 횟수는
  별도 commit이므로 유지되며, 해당 시험의 아직 유효한 준비 단계로 재시도 성공.
- 실제 enrollment row lock 대기 중 만료도 잠금 획득 후 거부.
- pending/active AAD 분리·다른 token ciphertext 거부와 fmt/JSON redaction은 단위 시험.
- 등록 consumer의 늦은 만료·unknown commit은 실패 주입 단위 double에서 성공 반환 차단.

## 증거의 한계와 다음 단계

- PostgreSQL 스킬의 짧은 transaction·일관된 잠금·FK index 지침을 적용했다.
  복구 코드 KDF는 잠금 밖에서 계산하고, 입장한 검증 요청은 먼저 5회 한도를 소비한다.
- 동시성 시험은 KDF 이후 final DB 단계의 경쟁이다. 독립 Control 프로세스/다중 리전,
  모든 요청의 KDF를 포함한 16개 동시 HTTP 등록 시험이 아니다.
- 복구 코드는 생성·Argon2 hash 저장만 완료했다. 복구 로그인/원자적 1회 소비/재발급,
  TOTP reset과 실제 정책 전환은 후속이다. UT-04-06은 계속 NOT RUN이다.
- 수동 입력 키를 service로 제공하지만 QR 이미지/URI·HTTP endpoint·등록 화면·인증 앱
  기기 등록은 미구현/미검증이다. 기존 me/logout TLS session은 SQL fixture다.
- 실제 commit 응답 유실·DB restart/failover 주입은 하지 않았다. 성공 응답 유실 시
  복구 코드는 재표시되지 않으며, 등록된 OTP 로그인 후 안전한 재발급 흐름이 추가로 필요하다.
- 10개 Argon2 hash는 fixture t=2다. 설치 calibration 승인·글로벌 HTTP 제한·부하/DoS
  qualification·운영 migration/키 관리·감사 로그·command idempotency는 미완료다.
- 기존 password/bootstrap 통합은 회귀로 실행했다. 해당 전용 반복/fuzz/calibration을
  이번 bundle에서 다시 측정하지 않았으며 환경 metadata의 미지정 flag와 구분한다.
- 10K/100K qualification·MAIN 최종 통합·원격 CI·commit/push/배포는 하지 않았다.
- 테스트 schema/임시 TLS 서버 정리, 인증 DB 컨테이너 중지 확인, named volume 보존.
  실제 운영자 계정/기존 사용자 데이터/다른 Valkey lab은 변경하지 않았다.

[구현 경계](../security/admin-enrollment.md) · [로컬 명령](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

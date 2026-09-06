# Bootstrap — local evidence

제한된 최초 Admin bootstrap·등록 전용 proof service 검증: **GO**.
SUB-PRD-04·MAIN delivery: **NO-GO**. 설치/등록 전체 기능의 완료를 뜻하지 않는다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별

- Run: `bootstrap-20260905-local`
- UTC: 2026-09-05 08:42:47–08:45:05
- Source SHA-256: `49c3b88f44c0dced9c1d07cebac682ca2076b277effac2280983d5636b1e3d16`
- Commit 없음, uncommitted snapshot; docs/evidence 제외 source의 실행 전후 불변 확인
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11
- 전용 loopback DB, 테스트별 무작위 schema, 한 프로세스 안의 두 독립 connection pool

[보고서·7개 로그 hash](bootstrap-20260905-local/report.json) ·
[환경](bootstrap-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race/vet/format·Node 10개·PRD 9개·OpenAPI 2개·contract 8개 | PASS | [foundation](bootstrap-20260905-local/foundation.log) |
| 인증 단위 테스트 | 31 PASS, 0 FAIL (기존 30 + 신규 1; fuzz seed 별도) | [unit](bootstrap-20260905-local/admin-unit.log) |
| DB/TLS integration 시나리오 | 29 PASS, 0 FAIL (기존 22 + bootstrap DB 7; unit/subtest 제외) | [integration](bootstrap-20260905-local/postgres-integration.log) |
| Bootstrap DB 7개 시나리오 × 3회 | 21 PASS (함께 실행한 redaction unit 3 PASS 별도) | [bootstrap repeat](bootstrap-20260905-local/bootstrap-repeat.log) |
| 기존 TOTP/PostgreSQL 6개 시나리오 × 10회 | 60 PASS | [DB repeat](bootstrap-20260905-local/postgres-repeat.log) |
| Session DB/TLS 9개 시나리오 × 10회 | 90 PASS (projection unit 10 PASS 별도) | [session repeat](bootstrap-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2; SUB 8개 NO-GO 유지 | [guard](bootstrap-20260905-local/final-guard.log) |

확인한 사항:

- CSPRNG 설치 토큰의 SHA-256 저장, 15분 TTL, 재발급 시 이전 token/generation 무효화.
- non-loopback·zone address·잘못된/만료 token 거부. IPv6/IPv4-mapped loopback 허용.
- 실제 Argon2id 비밀번호 생성 후 두 pool 동시 bootstrap 중 정확히 1건만 성공.
- 최초 Admin/password/등록 proof 또는 OFF session/완료 tombstone을 같은 transaction에 저장.
- TOTP ON에서는 session 0개, 등록 proof만 발급. 그 proof와 설치 token을 Me 및
  CompleteTOTP에 제출하면 거부. OFF session은 실제 DB 조회에서 MFA false로 표시.
- 비밀번호 재로그인으로 등록 proof를 교체하며 이전 것은 consumed 처리.
  token hash, user/policy/session version, 5분 expiry 바인딩 확인.
- 해시 계산 후 policy 변경·token 회전/만료·기존 account 발견 시 생성 거부.
- 실제 bootstrap row lock 대기 중 만료된 token은 잠금 획득 후에도 거부.
- 등록 proof INSERT 제약 오류 시 계정·암호·완료 상태 전체 rollback, 설치 token 재시도 성공.
- 완료 후 token 재사용/재발급 거부, 테스트 계정을 삭제해도 bootstrap 재개 불가.
- 기존 account가 있는 schema에 migration 003을 적용하면 처음부터 영구 닫힘.
- 기본 fmt/JSON credential redaction 및 잘못된 peer/token의 DB/KDF 이전 거부.

## 증거의 한계와 다음 단계

- PostgreSQL 스킬 지침에 따라 비밀번호 해시는 transaction 밖에서 계산하고,
  잠금 순서를 통일하며 FK/expiry index와 짧은 timeout을 적용했다.
- Bootstrap은 service만 구현됐다. 실제 운영자용 발급 CLI, setup HTTP/CSRF/Origin,
  wizard TOTP 선택 저장, initial cookie, private listener와 화면은 아직 없다.
- 등록 proof 발급은 TOTP 등록 완료가 아니다. secret/QR/manual key·OTP 검증·복구 코드
  원자 저장·앱 기기 등록·HTTP throttle은 다음 작업이다.
- 기존 me/logout TLS 테스트는 SQL-seeded session이며 HTTP bootstrap/login 증거가 아니다.
- 새 bootstrap의 실제 commit 응답 유실/네트워크 장애 주입은 미실행이다.
  공통 TOTP consumer의 unknown-commit unit double을 bootstrap 전용 시험으로 세지 않는다.
- 비밀번호 기존 통합은 회귀 실행했지만 password 전용 3회 반복/fuzz/calibration은 이번
  bundle에 포함하지 않았다. fixture는 t=2이며 설치 calibration 승인과 구분한다.
- production migration ledger·최소 권한·key mount/rotation·감사 로그·24시간 command
  idempotency·정책 전환·전체 RBAC/Backoffice 화면은 계속 후속이다.
- 10K/100K qualification·MAIN 최종 통합·원격 CI는 미실행. commit/push/배포도 하지 않았다.
- 테스트 schema와 임시 TLS 서버는 정리됐다. 인증 DB 컨테이너 중지 확인, named volume
  보존. 실제 운영자 계정/기존 사용자 데이터/다른 Valkey lab은 변경하지 않았다.

[구현 경계](../security/admin-bootstrap.md) · [실행 방법](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

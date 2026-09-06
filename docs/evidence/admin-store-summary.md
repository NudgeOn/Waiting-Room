# PostgreSQL auth storage — local slice

부분 storage slice 판정: **GO**. SUB-PRD-04·MAIN delivery: **NO-GO**.
Reviewer: Codex, 2026-09-05 UTC. 실제 로그인 서비스 완료 판정이 아니다.

## 실행 식별

- Run: `admin-store-20260905-postgres`
- UTC: 2026-09-05 07:48:04–07:48:34
- Commit: 없음, uncommitted snapshot
- Source SHA-256: `bf5b07d99a1c5e90bdc9a3893fc6af34ac98baa988fceece00a7a5fb3ac052bd`
- Source hash는 docs/evidence를 제외한다. 실행 전후 source 불변을 검사했다.
- Go 1.26.1, Node 24.12.0, darwin/arm64
- PostgreSQL 17.11 (Debian 17.11-1.pgdg13+2), pgx 5.10.0
- Compose image: `postgres:17.11@sha256:67f41722b7a8cbdb868a44a4995c846eddfdc2973bccb291ce937dce88ad5675`

[보고서·로그 hash](admin-store-20260905-postgres/report.json) ·
[실행 환경](admin-store-20260905-postgres/environment.json)

## 결과

| 검사 | 결과 | 증거 |
|---|---|---|
| gofmt/vet·전체 Go race unit·Node test·PRD 9개·OpenAPI 2개·contract 7개 | PASS | [foundation](admin-store-20260905-postgres/foundation.log) |
| 인증 단위 테스트 (기존 17 + 신규 vault/consumer 4) | 21 PASS, 0 FAIL | [admin-unit](admin-store-20260905-postgres/admin-unit.log) |
| 실제 PostgreSQL top-level 시나리오 (unit/subtest 별도) | 6 PASS, 0 FAIL | [integration](admin-store-20260905-postgres/postgres-integration.log) |
| 동일 PostgreSQL 시나리오 10회 반복 | 60 PASS, 0 FAIL | [repeat](admin-store-20260905-postgres/postgres-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2, 8개 SUB NO-GO 유지 | [guard](admin-store-20260905-postgres/final-guard.log) |

DB 증거: 한 Go process의 두 독립 pool에서 같은 challenge 64개/서로 다른 challenge
64개가 같은 OTP로 경쟁해 각각 **세션 1건**만 발급했다. 잘못된 코드 5회, 만료,
사용자 비활성화·user/policy/credential version 변경, 암호문 변조는 실패했다.
세션 INSERT를 test-only constraint로 실패시키자 counter/challenge/session 전체가
rollback됐으며 같은 OTP로 정상 재시도할 수 있었다. pool 재연결 후에도 replay를 거부했다.
DB row lock을 기다리다 challenge가 만료되면 세션을 발급하지 않았다.

초기 기본 sandbox 실행은 loopback 접속 권한으로 실패했고 승인된 local 실행으로
재검증했다. 초기 lock-wait 시험은 같은 transaction의 통계 snapshot 때문에 대기를
관측하지 못했다. 관측 query를 별도 autocommit으로 수정한 뒤 위 10회 반복에 통과했다.
이전 실패를 PASS로 재분류하지 않았으며, 이 bundle은 수정 후 소스의 새 실행이다.

## 범위와 후속

- PostgreSQL 설계 지침의 일관된 잠금 순서, 짧은 transaction, FK index를 적용했다.
- 비밀번호 확인 challenge는 **SQL fixture**다. Argon2id/password login/등록/복구,
  source/install throttle, key-file 배포/rotation, 정책 전환·세션 읽기/갱신/로그아웃,
  private Admin API·React Backoffice는 미연결이다.
- commit 응답 유실은 unit double로만 fail-closed를 확인했다. 실제 네트워크 장애,
  별도 Control process, DB restart/failover/backup, MFA 앱 등록 시험은 하지 않았다.
- UI/Valkey integration은 이번 실행에서 재시험하지 않았다. 기존 Go unit 회귀만 포함한다.
- 10K/100K qualification·MAIN 최종 통합 시험은 **미실행**이다.
- GitHub Actions용 DB job을 추가했지만 원격 CI 실행·commit·push·배포는 하지 않았다.
- 각 테스트가 만든 임시 schema만 정리했다. 전용 auth 컨테이너는 중지 확인했고,
  named volume은 보존했다. 기존 Valkey lab/사용자 데이터는 변경하지 않았다.

[구현 계약](../security/admin-auth-postgres.md) · [재실행 방법](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 checklist](../sub-prd_04.md)

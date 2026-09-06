# Session service — local evidence

제한된 session service·me/logout adapter 검증: **GO**.
SUB-PRD-04·MAIN delivery: **NO-GO**. 전체 인증/로그아웃 계약 완료를 뜻하지 않는다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별

- Run: `sessions-20260905-local`
- UTC: 2026-09-05 08:24:46–08:26:01
- Source SHA-256: `b959d7b6011ea0bd61b29f2595a73094992c48b3146fd2aca87ced0839a7e458`
- Commit 없음, uncommitted snapshot; docs/evidence 제외 source의 실행 전후 불변 확인
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11
- 전용 loopback PostgreSQL과 임시 TLS HTTP server만 사용

[보고서·6개 로그 hash](sessions-20260905-local/report.json) ·
[환경](sessions-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race/vet/format·Node·PRD 9개·OpenAPI 2개·contract 8개 | PASS | [foundation](sessions-20260905-local/foundation.log) |
| 인증 단위 테스트 | 30 PASS, 0 FAIL (기존 26 + 신규 4) | [unit](sessions-20260905-local/admin-unit.log) |
| DB/HTTP integration 시나리오 | 22 PASS, 0 FAIL (기존 13 + session DB 8 + TLS HTTP 1; unit/subtest 제외) | [integration](sessions-20260905-local/postgres-integration.log) |
| 기존 TOTP/PostgreSQL 시나리오 10회 반복 | 60 PASS | [DB repeat](sessions-20260905-local/postgres-repeat.log) |
| session DB/TLS 시나리오 10회 반복 | 90 PASS (함께 실행한 projection unit 10 PASS 별도) | [session repeat](sessions-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2; 8개 SUB NO-GO 유지 | [guard](sessions-20260905-local/final-guard.log) |

확인한 사항:

- 조회는 idle 시간/쿠키를 연장하지 않음. 명시적 POST Touch만 lastSeenAt 갱신.
- 30분 idle·8시간 absolute·미래 timestamp·MFA·정책/user version·비활성 계정 검증.
- 최신 role로 capability 재계산, configurable OFF를 명시적으로 존중.
- 잘못된 Origin/CSRF는 갱신/삭제 모두 거부하고 세션 필드는 변경하지 않음.
- 두 pool의 32개 logout/touch 경쟁: 삭제 성공 1회, 삭제된 세션 부활 0건.
- 실제 policy lock 대기 후 stale session 거부; session lock 대기 중 만료도 거부.
- UPDATE/DELETE를 test-only SQL trigger로 실패시킨 경우 rollback, 세션 보존.
- 실제 TLS 요청→PostgreSQL: me 200, cross-origin logout 403, 정상 logout 200 및
  Secure/HttpOnly/Strict/host-only 쿠키 삭제, 이후 me/logout 재시도 401.
- 테스트 pool을 닫은 후 503 반환; 내부 DB 오류 문자열은 HTTP에 노출하지 않음.

## 증거의 한계와 다음 단계

- PostgreSQL 설계 지침의 일관된 shared/exclusive 잠금 순서와 짧은 transaction을 적용했다.
- TLS 시험의 session은 SQL fixture다. HTTP password/TOTP login·처음 쿠키 발급·
  브라우저 저장/복원·MFA 앱 기기 등록을 증명하지 않는다.
- Touch는 내부 service만 존재한다. HTTP 갱신 endpoint와 UI activity 정책은 미구현이다.
- me/logout은 production private listener에 설치되지 않았다. 공개 Gateway는 변경하지 않았다.
- logout 재시도는 현재 401이며, 24시간 command 결과 재응답/Idempotency-Key·audit·
  완전한 세션 lifecycle 계약은 미완료다. SessionView는 후속 mutation 권한 증명이 아니다.
- Bootstrap/등록/복구·정책 변경/rotation·전체 RBAC middleware·Backoffice 화면·배포 보안
  설정·rate-limit·browser/a11y는 계속 후속 작업이다.
- Pool close 시험은 DB restart/failover/network outage 증거가 아니다.
- Password 기존 통합은 회귀로 실행했지만 이번 bundle에는 password 3회 반복/fuzz/calibration을
  다시 넣지 않았다. 해당 측정은 [이전 기록](password-login-summary.md)과 구분한다.
- 10K/100K qualification·MAIN 최종 통합은 미실행. 원격 CI·commit·push·배포도 하지 않았다.
- 임시 TLS 서버와 테스트 schema는 종료·정리됐다. 전용 인증 DB 컨테이너 중지를 확인했고
  named volume은 보존했다. 기존 Valkey lab/사용자 데이터는 변경하지 않았다.

[구현 경계](../security/admin-sessions.md) · [실행 방법](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

# Authentication HTTP — local evidence

제한된 password/OTP/recovery HTTP adapter 로컬 검증: **GO**.
SUB-PRD-04·MAIN delivery: **NO-GO**. 운영 listener·Backoffice 화면 완료를 뜻하지 않는다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별

- Run: `auth-http-20260905-local`
- UTC: 2026-09-05 10:56:39–11:00:35
- Source SHA-256: `dcd41188e43d83ebd7f2ec8b0f0ef7c88576df358f7875c2dfe30ab5d92090ef`
- Commit 없음, uncommitted snapshot; docs/evidence 제외 source 실행 전후 불변·로그 hash 재확인
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11
- 전용 loopback DB, 테스트별 무작위 schema, 두 독립 connection pool, 임시 TLS 서버

[보고서·7개 로그 hash](auth-http-20260905-local/report.json) ·
[환경](auth-http-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race/vet/format·Node 10개·PRD 9개·OpenAPI 2개·contract 9개 | PASS | [foundation](auth-http-20260905-local/foundation.log) |
| 인증 단위 테스트 | 39 PASS, 0 FAIL (기존 36 + HTTP 3; fuzz seed 별도) | [unit](auth-http-20260905-local/admin-unit.log) |
| DB/TLS integration 시나리오 | 41 PASS, 0 FAIL (기존 39 + 공유 제한 1 + 인증 TLS 1; 함께 실행한 unit 18개 별도) | [integration](auth-http-20260905-local/postgres-integration.log) |
| Auth HTTP/공유 제한 2개 × 3회 | 6 PASS; TLS HTTP/1.1·HTTP/2 subflow 총 6회, unit 9 PASS 별도 | [HTTP repeat](auth-http-20260905-local/auth-http-repeat.log) |
| TOTP/PostgreSQL 6개 × 10회 | 60 PASS | [DB repeat](auth-http-20260905-local/postgres-repeat.log) |
| Session DB/TLS 9개 × 10회 | 90 PASS; projection unit 10 PASS 별도 | [session repeat](auth-http-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2; SUB 8개 NO-GO 유지. 최종 통합 테스트가 아님 | [guard](auth-http-20260905-local/final-guard.log) |

확인한 사항:

- HTTP/1.1·HTTP/2 각각 password → challenge → OTP → 실제 발급 cookie → Me → logout.
  틀린 OTP 거부, challenge expiry와 DB 값 일치, 인증 전 cookie/CSRF 미발급 확인.
- password → recovery → MFA session, explicit-OFF password-only session, ON 미등록 사용자
  enrollment-only proof, 등록 proof의 OTP 로그인 사용 거부. CookieJar가 발급 cookie를 전달한다.
- Secure/HttpOnly/SameSite=Strict/Path=/의 host-only cookie, 원시 session token의 JSON 제외,
  no-store와 일반화한 오류, DB pool unavailable 시 503·cookie 미발급.
- 단위 시험: TLS/Host/Origin/custom header, cookie/bearer 혼용, 엄격한 JSON/길이/미디어,
  실제 socket peer 사용·Forwarded 무시, deadline 미지원 거부, 동시 요청 2개 상한.
- 복구 KDF 전에 body read deadline을 해제하여 HTTP/2 stream의 5초 제한으로 오인되지 않게 처리.
  실제 slow-upload 시험은 별도로 하지 않았다.
- DB 공유 제한: 두 pool의 요청 80개에서 source 20개·설치 60개만 허용, 만료 후 갱신.
  HTTP/password bucket은 서로 다른 namespace만 정리한다. HTTP 429와 Retry-After도 확인.
- OpenAPI의 세 endpoint·요청 marker·오류·CurrentSession 응답을 현재 adapter 계약에 맞췄다.
  이 unreleased M0 변경은 기존 authenticated `user` 대신 `session`을 사용한다.

## 증거의 한계와 다음 단계

- PostgreSQL 스킬의 짧은 transaction·일관된 잠금 순서를 적용했다. HTTP 설치 bucket을
  먼저 잠그고 source bucket을 처리하며 다른 namespace 정리로 인한 잠금 역전을 방지한다.
- TLS 계정/credential은 SQL fixture다. recovery 슬롯 10개 중 1개만 미사용이며 같은 hash를
  공유하는 소비된 슬롯 9개를 포함한다. 실제 등록/코드 생성은 기존 service 회귀 시험과 구분한다.
- 환경 JSON의 password/bootstrap/enrollment/recovery 플래그는 각 전용 반복 bundle 선택 여부다.
  이번에는 `--auth-http --sessions`만 선택했다. 기본 통합에는 기존 service 회귀도 포함하지만,
  별도 password calibration/fuzz 또는 bootstrap/enrollment/recovery 반복을 실행한 것은 아니다.
- 임시 TLS/Go CookieJar 결과는 실제 browser의 SameSite·CSRF 복원 UX·기기 인증 증거가 아니다.
  enrollment HTTP/QR, bootstrap HTTP/CLI, 실제 Backoffice UI와 private production listener는 후속.
- proxy TLS 종료는 미지원이다. 운영 인증서·listener timeout 설정·full Admin middleware,
  command idempotency/audit·위험 작업 재인증·TOTP reset/복구 코드 재발급·정책 전환은 미완료다.
- 응답 유실 뒤 성공 명령 replay는 없다. recovery 코드를 소비한 경우 다른 미사용 코드가
  필요할 수 있다. DB pool 종료 시험은 DB restart/network failover 증거가 아니다.
- 10K/100K qualification, 독립 Control 프로세스 부하, 다중 리전, MAIN 최종 통합,
  원격 CI·commit/push/배포는 실행하지 않았다.
- 테스트 schema/임시 TLS 서버 정리 및 인증 DB 컨테이너 Exited (0) 확인. named volume 보존.
  실제 운영자 계정·기존 사용자 데이터·다른 Valkey lab은 변경하지 않았다.

[구현 경계](../security/admin-auth-http.md) · [로컬 명령](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

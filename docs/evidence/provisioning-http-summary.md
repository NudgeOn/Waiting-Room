# Provisioning HTTP — local evidence

제한된 bootstrap/initial enrollment HTTP adapter 로컬 검증: **GO**.
SUB-PRD-04·MAIN delivery: **NO-GO**. 운영 실행기·설치 위자드·화면 완료를 뜻하지 않는다.
Reviewer: Codex, 2026-09-05 UTC.

## 실행 식별

- Run: `provisioning-http-20260905-local`
- UTC: 2026-09-05 11:18:34–11:24:37
- Source SHA-256: `62ff7bc09ae2a851314fb7f9b5f763aa31a7424066c6ec04b15e17be6164b6d8`
- Commit 없음, uncommitted snapshot. docs/evidence 제외 source 실행 전후 불변, 로그 hash 재확인.
- Go 1.26.1, Node 24.12.0, darwin/arm64, PostgreSQL 17.11.
- 전용 loopback DB와 무작위 test schema, 서로 분리된 임시 loopback setup/admin TLS 서버.

[보고서·8개 로그 hash](provisioning-http-20260905-local/report.json) ·
[환경](provisioning-http-20260905-local/environment.json)

## 결과

| 검사 | 결과 | Evidence |
|---|---|---|
| 전체 Go race/vet/format·Node 10개·PRD 9개·OpenAPI 2개·contract 10개 | PASS | [foundation](provisioning-http-20260905-local/foundation.log) |
| 인증 단위 테스트 | 41 PASS, 0 FAIL (기존 39 + 신규 2; fuzz seed 별도) | [unit](provisioning-http-20260905-local/admin-unit.log) |
| DB/TLS integration | 42 PASS, 0 FAIL (기존 41 + provisioning TLS 1; 함께 실행한 unit 20개 별도) | [integration](provisioning-http-20260905-local/postgres-integration.log) |
| Provisioning TLS top-level 1개 × 3회 | 3 PASS; HTTP/1.1·2 × ON/OFF 총 12개 subflow, unit 6 PASS 별도 | [provisioning repeat](provisioning-http-20260905-local/provisioning-http-repeat.log) |
| 기존 Auth HTTP/DB 제한 2개 × 3회 | 6 PASS, unit 9 PASS 별도 | [auth repeat](provisioning-http-20260905-local/auth-http-repeat.log) |
| TOTP/PostgreSQL 6개 × 10회 | 60 PASS | [DB repeat](provisioning-http-20260905-local/postgres-repeat.log) |
| Session DB/TLS 9개 × 10회 | 90 PASS, projection unit 10 PASS 별도 | [session repeat](provisioning-http-20260905-local/session-repeat.log) |
| MAIN final 차단 guard | 예상 exit 2, SUB 8개 NO-GO. 실제 최종 통합 테스트가 아님 | [guard](provisioning-http-20260905-local/final-guard.log) |

확인한 사항:

- Bootstrap만 제공하는 opt-in handler, 일반 auth/enrollment에서 bootstrap 404.
  기존 New constructor는 enrollment도 노출하지 않는다. 설치 token을 발급하는 HTTP API는 없다.
- literal loopback HTTPS origin 설정, 실제 local/remote TCP loopback 검사, Origin/marker,
  중복 token·Bearer/cookie 혼용·정책 주입·다른 listener 경로·잘못된 JSON 거부.
- ON: 설치 token → 첫 Admin/password 생성 → enrollment-only proof; cookie/CSRF는 아직 없음.
  Begin 반복에서 동일 manual key와 원래 expiry, 설치 token의 등록 proof 사용은 거부.
- 틀린 OTP에서 cookie/복구 코드 없음. 실제 key로 OTP를 계산해 등록 완료하면 MFA session,
  안전한 cookie, CSRF와 서로 다른 복구 코드 10개를 응답하고 Me/logout까지 연결.
- 등록 완료 후 Begin/Complete 재사용은 401이며 비밀키·복구 코드를 다시 노출하지 않음.
  Bootstrap token 재사용도 거부. 신규 password login은 이제 정상 TOTP challenge를 요구.
- 실제 발급된 복구 코드로 HTTP 로그인 성공. fresh password challenge에서도 소비한 코드는
  거부되고 미사용 코드 9개는 보존. 완료 응답에 수동 key나 원시 session token이 없음.
- OFF: password-only Admin session과 Me/logout, bootstrap 재사용 거부, 후속 password login.
- OpenAPI에 Bootstrap 입력 제한·등록 manual-key 응답·EnrollmentResult와 공통 보안/오류를 반영.
  기존 M0의 totpEnabled 입력·필수 otpauthURI·코드-only 완료 응답을 구현 범위에 맞게 변경했다.

## 증거의 한계와 후속

- 운영 계정이 아니라 전용 test schema 계정이다. 설치 token은 test operator service로 발급하고
  DB 정책만 SQL로 ON/OFF 설정했다. account/password/credential/recovery/session은 HTTP에서 생성한다.
- 이 provisioning flow는 별도 code fixture가 아니라 10개를 실제 생성하고 각각 Argon2id로 해시한다.
  기존 auth HTTP fixture·두-pool DB 경쟁 시험과 구분한다. 동시 HTTP bootstrap 부하는 미검증이다.
- 환경 JSON의 sessionSeed는 기존 auth HTTP suite를 설명한다. 새 flow는 provisioningHTTP 필드다.
  password/bootstrap/enrollment/recovery 필드는 각 전용 flag 선택 여부로, 이번에는 별도 전용
  service 반복·password calibration/fuzz를 요청하지 않았다. 기본 service 회귀는 포함한다.
- CookieJar는 같은 loopback host의 다른 port 사이에서 cookie를 공유한다. cross-host setup
  handoff·실제 browser의 SameSite/CSRF UX·MFA 기기 증거가 아니다.
- 생성자는 listener를 시작하지 않는다. 운영 loopback bind/TLS 인증서·token CLI·setup 종료,
  migration/key 파일 관리·정책 선택 wizard·QR/수동 키/복구 UI와 browser/a11y 검증은 후속이다.
- 현재 DB 정책이 authoritative하다. 위자드 TOTP ON/OFF 구현으로 오해하면 안 된다.
  full idempotency/audit·fresh reauth·reset/복구 재발급·정책 원자 전환도 미완료다.
- 성공 commit 뒤 응답 유실은 replay되지 않는다. 등록된 authenticator로 로그인해야 하며
  복구 코드 재발급은 후속이다. unknown commit double은 실제 네트워크 장애 증거가 아니다.
- 10K/100K qualification·다중 리전·운영 listener·MAIN 최종 통합·원격 CI·commit/push/배포 미실행.
- test schema/TLS 서버 정리, 인증 DB Exited (0) 확인, named volume 보존. 기존 사용자 데이터와
  다른 Valkey lab은 변경하지 않았다. 원시 코드/key/token을 evidence 로그에 출력하지 않았다.

[구현 경계](../security/admin-provisioning-http.md) · [로컬 명령](../operators/admin-auth-lab.md) ·
[SUB-PRD-04 체크리스트](../sub-prd_04.md)

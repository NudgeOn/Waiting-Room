# 로컬 인증 DB 테스트

실제 로그인 화면이 아니라 **인증 DB 트랜잭션과 부분 HTTP adapter**를 시험한다.
TOTP-only 시험은 challenge를 SQL fixture로 준비한다. Password 시험은 fixture 계정에
실제 Argon2id 검증을 거쳐 challenge를 발급한다. 실제 운영자 계정은 만들지 않는다.

```sh
make lab-auth-db
make test-unit PRD=04
WR_TEST_AUTH_DB=local make test-auth-db
# 동일 소스의 단위/통합/10회 반복/최종 차단 guard 로그 묶음
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-auth-db-run-id
# 비밀번호 연결·3회 반복·parser fuzz·이 호스트 calibration 측정 포함
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-password-run-id --password-login
# 세션 DB 및 me/logout 임시 TLS HTTP 10회 반복 포함
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-session-run-id --sessions
# 최초 관리자 생성 service 3회 반복 포함 (실제 운영자 계정/등록 UI 아님)
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-bootstrap-run-id --bootstrap
# 등록 완료 service 3회 반복 포함 (QR/HTTP/복구 로그인 아님)
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-enrollment-run-id --enrollment
# 비밀번호→복구 로그인 및 원자 소비 DB 3회 반복 (HTTP/UI 아님)
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-recovery-run-id --recovery
# 인증 HTTP/1.1·2 TLS와 공유 DB limiter 3회 반복 (실제 브라우저/운영 listener 아님)
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-auth-http-run-id --auth-http
# 초기 설정·실제 TOTP 등록→발급 복구 코드 로그인, TLS 2종 × ON/OFF 3회 반복
WR_TEST_AUTH_DB=local node scripts/run-adminstore.mjs unique-provisioning-http-run-id --provisioning-http --auth-http --sessions
make auth-calibrate
docker compose -f deploy/compose/auth-lab.yaml stop
```

- PostgreSQL 17.11 공식 image/digest 고정, loopback `127.0.0.1:15432`만 사용한다.
- 계정/암호는 Compose에 공개한 local-only fixture다. 운영 서버에서 사용하지 않는다.
- 임의 DB URL을 받지 않고 고정된 전용 `wr_auth_lab` DB만 연결한다.
- 매 테스트는 새 무작위 schema를 만들고 **그 schema만** 정리한다.
- 종료 명령은 컨테이너만 멈추고 named volume은 남긴다. 다른 Valkey lab에는 영향이 없다.
- DB가 없거나 버전이 다르면 실패한다. skip을 PASS로 취급하지 않는다.
- 두 독립 connection pool에서 같은/다른 challenge 각각 64개 경쟁, 5회 실패,
  만료·version 변경·암호문 변조, 세션 INSERT 실패 전체 rollback을 검증한다.
- 실제 컨테이너 재시작·replica 장애·브라우저 로그인·MFA 앱 등록은 이번 시험에 없다.
- Bootstrap 시험은 새 test schema 안에서만 최초 Admin을 생성한다. 두 pool 경쟁·token 회전/
  만료·정책 변경·실패 rollback·완료 tombstone·등록 proof의 세션 사용 거부를 확인한다.
  실제 설치 token 발급 CLI/HTTP, wizard 정책 선택과 QR/등록 완료는 후속이다.
- 세션 suite는 임시 TLS 서버의 me/logout을 실제 DB와 연결한다. 세션은 SQL fixture이고
  브라우저 로그인이나 최초 cookie 발급을 증명하지 않는다. 시험 후 TLS 서버는 닫힌다.
- 비밀번호 검증은 64 MiB Argon2id여서 race DB suite는 수십 초 걸릴 수 있다.
- 등록 suite는 복구 코드 10개를 실제 Argon2id로 해시하므로 전체 DB 시험이 1분 이상
  걸릴 수 있다. 등록 경쟁 시험은 준비된 해시로 final DB phase를 두 pool에서 경쟁시킨다.
  복구 코드 소비·QR 화면·실제 MFA 앱 등록을 증명하지 않는다. [등록 계약](../security/admin-enrollment.md).
- 복구 suite는 실제 등록에서 발급된 코드로 password→recovery service를 연결한다.
  경쟁 시험은 빠른 SQL fixture와 KDF 이후 final DB 단계를 사용한다. 실제 API/화면 및
  재발급/reset은 후속이며, 10-slot Argon2 검증으로 전체 suite에 수십 초가 추가된다.
- Auth HTTP suite는 임시 TLS 서버·CookieJar로 password/OTP/recovery/OFF→cookie→me/logout을
  시험한다. 인증 계정·TOTP/recovery 자료는 SQL fixture이며 bootstrap/등록 UI를 뜻하지 않는다.
  HTTP/1.1·HTTP/2와 공유 DB 제한을 검증한다. [HTTP 계약](../security/admin-auth-http.md).
- Provisioning HTTP suite는 분리된 loopback setup/admin TLS 서버로 첫 Admin 생성부터
  수동 키·OTP 등록·실제 복구 코드 10개 발급/로그인/재사용 거부를 검증한다. 테스트가 설치
  token을 operator service로 명시 발급하며 정책만 SQL 설정한다. 계정/credential은 HTTP로 생성한다.
  HTTP/1.1·2 각각 ON/OFF를 시험하며 실제 browser·운영 listener·wizard/QR은 아니다.
  CookieJar의 같은 host·다른 port 공유는 cross-host handoff 증거가 아니다.
  [설정/등록 HTTP 계약](../security/admin-provisioning-http.md).
- calibration은 다른 테스트가 끝난 뒤 별도로 실행한다. 측정값만 출력하며 설정/계정을 변경하지 않는다.
- 로그인 제한 alpha 기본값은 계정 5·출처 20·설치 전체 60회/고정 시작 1분이다.
  production 권장값/queue 유량을 뜻하지 않는다. [비밀번호 계약](../security/password-login.md).

[구현 경계](../security/admin-auth-postgres.md) · [검증 결과](../evidence/admin-store-summary.md)

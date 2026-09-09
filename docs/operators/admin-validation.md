# 관리자 운영·보안 검증

구현된 로컬 관리자 기능의 회귀 검증 절차다. 고객 설치 환경의 DNS/TLS·원본 우회 차단, 장시간 운영과 대규모 성능, 실제 보조기기 사용자 검증은 별도다.

2026-09-09 사용자 요청으로 이번 로컬 Beta의 VoiceOver 검증은 제외했다. 아래 자동 검사와
키보드 검증은 유지하며, VoiceOver/NVDA 사용성 인증을 통과한 것으로 표시하지 않는다.

## 명령 재시도와 감사

| 명령 | 재시도 경계 | 감사·실패 처리 |
| --- | --- | --- |
| 초안 저장·서명 배포 | 같은 actor와 Idempotency-Key, 정확한 본문·If-Match로 24시간 결과 재조회 | 변경·응답 캐시·감사를 같은 DB 트랜잭션으로 저장 |
| AUTO·HOLD·safe-drain·유량 변경·즉시 OFF·새 epoch | 동일 요청 재실행 시 원래 결과·ETag와 Idempotency-Replayed 반환 | 감사 저장 실패 시 runtime 변경도 롤백; OFF와 새 epoch는 Admin 일회성 재인증 필요 |
| 일정 생성·수정·재개·취소 | runtime ETag와 정확한 요청 바이트에 바인딩 | 기존 일정과 서명 배포·감사를 함께 커밋; scheduler 전환은 별도 감사 |
| 사용자 생성·역할/활성 변경·삭제·TOTP 초기화 | 재인증이 소비되어도 커밋된 동일 명령의 결과 재조회 가능 | 대상 세션·인증 경로 폐기와 감사가 원자적; 마지막 활성 Admin 보호 |
| TOTP 정책·등록 준비 | 정책 ETag와 일회성 재인증; 등록 준비 캐시는 암호화 | ON/OFF가 세션을 교체하므로 응답 유실 시 다시 로그인하고 현재 정책 조회; 쿠키·복구 코드 재발급을 재시도하지 않음 |
| Traffic Lab 실행·취소 | 고정 preset, 명령 키 재조회; 실행은 설치당 하나 | 명령 감사와 최종 결과 감사를 각각 한 번 저장; 죽은 worker를 자동 재실행하지 않음 |
| 로그아웃 | 원래 쿠키·CSRF·같은 키로 24시간 성공 확인 | 세션 삭제·해시 영수증·감사를 원자 저장; 8개 동시 요청도 감사 1개. 키 없는 기존 클라이언트는 재요청 401 |
| 최초 설치 적용 | 검토한 plan/calibration digest가 같으면 저장된 결과 반환 | 설치 설정·보고서·감사 원자 저장; 감사 실패 시 bootstrap token 유지 |
| 로그인·TOTP·복구·재인증 | 자격 증명 발급은 일반 명령 캐시와 분리. 만료/소비된 challenge·TOTP·복구 코드는 재사용하지 않음 | 인증 성공·실패·잠금·등록·복구·재인증 감사; 비밀번호·코드·비밀키를 감사에 넣지 않음 |
| 조회·URL 판정·설치 계획/보정 | 읽기/준비 작업은 운영 명령을 실행하지 않음 | URL 판정으로 원본에 트래픽을 보내거나 운영 감사를 만들지 않음 |

명령 캐시를 읽기 전에 현재 권한·Origin·CSRF를 다시 확인한다. 다른 본문 또는 revision으로 키를 재사용하면 충돌하며, 문법이 잘못된 요청은 캐시 조회 전에 400이 될 수 있다. 5xx/응답 유실은 같은 키로 재확인하고, 412는 최신 revision을 읽어 사용자가 새 요청을 작성한다. 로그아웃 영수증은 쿠키·CSRF·키의 해시만 보관하며 다른 API 인증에 사용할 수 없다. 24시간 보관, 최대 10,000건, 만료분은 다음 keyed logout에서 정리한다.

## 역할과 API 계약

Admin만 초안/배포·사용자·보안을 변경한다. Operator는 runtime·일정·Traffic Lab을 조작한다. Viewer는 조회만 한다. 즉시 OFF와 보안 명령은 Admin 재인증이 추가로 필요하다. 설치 전체 새 epoch 복구도 Admin 재인증이 필요하다. [키 회전](key-rotation.md)은 Docker 배포 소유자의 `wrctl keys-*` 명령으로 제공한다. 긴급 폐기에는 Admin 재인증된 새 epoch가 추가로 필요하다. 복구·이행·백업은 [복구 운영 절차](recovery-upgrade.md)를 따른다.

[Admin OpenAPI](../../api/openapi/admin-v1.yaml)를 실제 HTTP 응답의 operation·status·media type·schema·캐시 헤더와 대조한다. 실패 응답은 application/problem+json이다. `/config`의 GET/PUT과 `/config/validate`는 planned 계약이며 현재 라우트에서 제공하지 않는다. `/config/draft`, `/config/publish`, `/config/delivery`를 사용한다.

## 재현

```sh
make check
WR_TEST_AUTH_DB=local make test-auth-db
PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" npm run test:admin-browser
```

전용 PostgreSQL 인증 Lab이 필요하다. 브라우저 검사는 Chromium·Firefox·WebKit으로 실행하며 로그인/등록·설치·Room·Traffic 회귀와 axe WCAG 2 A/AA, 2.1 A/AA, 2.2 AA 규칙을 확인한다. 성능 보정은 실제 현재 CPU 부하를 측정하므로 다른 부하/보정 작업과 동시에 실행하지 않는다. 보정 실패를 테스트 전용 값으로 덮어쓰지 않는다.

실제 운영 API·역할·모바일·키보드 검증은 최신 Linux arm64 `wr-control`, `wr-node`, Admin UI를 `deploy/docker/control.Dockerfile`로 `waiting-room-recovery-test:local` 이미지에 빌드한 뒤 실행한다.

```sh
WR_TEST_TRAFFIC_DOCKER=local WR_OPERATIONS_CHECK=1 WR_RECOVERY_RUNTIME=1 \
  PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" \
  node test/localbeta/traffic-runtime.mjs
```

검증기가 임의 이름의 Compose 프로젝트와 별도 키·볼륨·loopback 포트 29443/29444/30443을 만든다. 기존 설치를 선택하지 않는다. 종료 시 해당 프로젝트만 down하며 fixture 볼륨은 보존한다. 네트워크 API 응답은 실제 서버에서 받으며, 브라우저 요청은 테스트용 외부 포트를 서버의 고정 Host/Origin으로 전달한다.

대시보드·Room 목록/설정/운영/일정/검증·Traffic Lab·보안·새 Room의 9개 화면을 세 역할·세 엔진으로 검사한다. 320px 재배치, 감사 로그 50건 이후 페이지, 본문 건너뛰기, modal 포커스와 Escape 복원도 확인한다. 자동 접근성 통과는 모든 WCAG 항목 또는 실제 VoiceOver/NVDA 사용성 인증을 뜻하지 않는다. 비밀번호·등록 키·복구 코드가 표시된 화면은 캡처하지 않는다.

2026-09-09 최종 v5 로컬 후보에서 81개 화면의 자동 WCAG 위반·가로 넘침 0,
32개 실제 API operation 계약, Admin 화면의 새 epoch 실행과 양 노드 epoch 2 HOLD ACK,
사용자 생성 응답 유실 후 같은 키로 재시도하여 계정/감사 1건, 로그아웃 영수증과 세션 폐기를
통과했다. PostgreSQL/Control 재시작 뒤 Lab 보고서와 최종 감사도 보존됐다.

# 브라우저 첫 접속과 재시도

보호 경로의 첫 HTML 요청은 대기표를 만들기 전에 짧은 연결 화면을 표시한다.
같은 출처의 `POST /_wr/v1/rooms/{roomId}/browser-prepare`가 10분짜리 암호화된
HttpOnly intent 쿠키를 발급하고, 두 번째 요청이 쿠키 수신을 확인한다.
그 뒤 보호 경로를 다시 요청해 대기표를 받는다. intent 자체는 대기 순번이나 입장권이 아니다.

쿠키가 확인된 뒤 최초 join 응답 전체가 유실되더라도 intent의 같은 nonce/원래 target으로
재시도한다. v6 저장소의 보존 기간 안에서 원래 대기표 응답을 재생하며 새 순번을 만들지 않는다.
응답을 못 받은 화면은 재연결 버튼에 키보드 초점을 놓는다. 15초 안에 각 네트워크 요청이
끝나지 않으면 취소하고 재시도할 수 있게 한다. 이때 대기 순번을 자동 초기화하지 않는다.

같은 출처·Room의 여러 탭은 Web Locks로 쿠키 확인과 첫 join 응답까지 직렬화한다.
대기표는 공유하며 각 탭의 목적지는 별도로 암호화한 return 값에 보존한다.
JavaScript는 queue/admission/intent 쿠키 값을 읽지 않고 localStorage에 credential을 저장하지 않는다.
지원 범위는 Web Locks와 AbortSignal.timeout을 제공하는 최신 Chrome, Firefox, Safari다.
JavaScript 또는 쿠키가 차단됐거나 필요한 브라우저 기능이 없으면 안내를 표시한다.

만료한 대기표에서 사용자가 다시 줄서기를 선택하면 현재 queue 쿠키의 해시에 묶은 새 intent를
먼저 확인한다. 만료된 대기표를 되살리지 않으며, 새 join의 재시도 역시 한 순번으로 수렴한다.
intent 유효기간이 지난 뒤 새로 접속하는 경우까지 이전 순번 보존을 보장하지 않는다.

## 보안·계약 경계

- 실제 HTTPS는 `__Host-wri_`/`__Host-wrq_` 쿠키에 Secure, HttpOnly, SameSite=Lax,
  Path=/를 사용한다. 호스트·Room·epoch·용도에 암호학적으로 묶으며 임의의 외부 목적지를 거부한다.
- prepare는 정확한 단일 Origin과 JSON Content-Type, 엄격한 본문, 유효한 보호 경로를 요구한다.
  queue 변경이 없는 204와 쿠키 확인 실패 428 `BROWSER_STORAGE_REQUIRED`를 OpenAPI에 정의했다.
- 반환용 암호화 JSON은 HTML에 삽입하지 않는다. URL의 `&`를 여섯 바이트로 확장하지 않아
  허용된 최대 2048바이트 query target을 브라우저 쿠키 크기 안에서 보존한다.
- 키 전환에는 기존 return 키의 검증 overlap을 사용하고, 긴급 폐기/새 epoch 후 이전 intent는 거부한다.
- 실제 순서·용량·입장은 Coordinator의 기존 FIFO, quota, expiry 및 복구 HOLD 검사를 따른다.

## 검증

`test/browser/join.spec.mjs`는 세 엔진에서 첫 응답 전체 유실, 동시 5개 탭,
prepare 쿠키 유실, 키보드 회복과 360px/axe 검사를 실행한다.
`internal/lab/browser_retry_integration_test.go`는 실제 Valkey와 두 독립 Gateway에서
12개 재시도와 만료 후 새 순번을 검증한다. 실제 이미지의 Secure 쿠키·HTTPS 검증은
`test/localbeta/browser-join.mjs`를 `public-runtime.mjs`에서 호출한다.

수동 보조기기 사용자 시험, 임의의 구형/내장 브라우저, 브라우저 저장소가 지워진 뒤의
순번 복구는 이 자동 시험으로 증명하지 않는다. 출시 판정은 [Beta 계획](../beta-plan.md)을 따른다.

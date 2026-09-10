# 로컬 Web·App Lab

현재는 **Backoffice 없는 Web·App lab**이다. `calm` 브라우저 template과 앱 JSON을 제공하며 production 설치는 아직 제공하지 않는다.

## 시작

Docker Desktop을 실행하고 저장소 루트에서:

```sh
npm ci --ignore-scripts
make lab-valkey
make lab-quick
```

정상 결과는 visitors=20, admitted=3, queued=17, retryStable=true, originProtected=true다.
실행마다 새 격리 namespace를 사용하며 종료 시 HTTP server는 닫힌다.
두 Gateway는 이제 서로 다른 handler/transport를 사용하고 실행 단위의 memory-only return AEAD key를
공유한다. 같은 public host 뒤에서 Gateway가 바뀌어도 복귀 정보를 해독할 수 있다. 다른 host/port의
복귀 정보는 계속 거부하며 key mount·rotation·서로 다른 OS process의 배포는 아직 구현하지 않았다.

계속 호출하려면 `make lab`을 실행한다. Gateway는 `http://127.0.0.1:18080`,
`http://127.0.0.1:18081`이고 Ctrl-C로 종료한다.

브라우저에서 `http://127.0.0.1:18080/shop`을 열면 대기 화면 → 자동 입장 안내 → 원래 경로로 이동한다. 입장 버튼을 누를 필요 없다.
기본 template은 `calm`이다. 화면만 확인하려면 `go run ./cmd/wr-lab -hold -template calm`으로 시작한다.
`-hold`는 입장을 멈춘 새 격리 실행이며 UI mock이 아니다. 다른 lab과 함께 실행할 때는
`-gateway-port 18082`처럼 두 연속 미사용 포트를 지정한다. [template 추가 방법](../design/calm.md).
Backoffice의 template 선택 UI와 Room별 저장은 아직 미구현이다.

```sh
curl -i http://127.0.0.1:18080/shop
curl -s http://127.0.0.1:18080/_wr/v1/tickets \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: local-example-visitor-001' \
  -d '{"target":"/shop"}'
```

첫 요청은 429, join은 202다. 응답의 `ticketToken`을 `Authorization: Bearer`에 넣어
`GET /_wr/v1/rooms/abcdefghijklmnopqrst/status`를 조회한다. READY면 같은 credential로
`POST /_wr/v1/rooms/abcdefghijklmnopqrst/admissions`를 호출한다. 그 응답의
`admissionToken`을 `X-Waiting-Room-Admission`에 넣으면 `/shop`이 통과한다.
실제 token을 로그·PRD·공유 파일에 붙여 넣지 않는다.

브라우저는 HttpOnly `wr_dev_*` cookie를 사용한다. 앱 Bearer와 혼합하지 않는다.
새로고침과 이미 대기표가 있는 여러 탭은 같은 순서를 재사용하되 탭별 복귀 경로는 독립적이다.
대기표 없는 동시 첫 방문·join 응답 유실의 중복 방지는 후속이며, lab 재시작 시 이전 자격증명은 유지되지 않는다.

## 검증과 종료

```sh
make check
make test-unit PRD=03
PLAYWRIGHT_BROWSERS_PATH=$PWD/.cache/ms-playwright npx playwright install chromium
make test-browser
WR_TEST_VALKEY=127.0.0.1:16379 make test-integration
# 선택: 프로젝트 전용 Valkey를 실제 재시작하는 persistence test
WR_TEST_VALKEY=127.0.0.1:16379 \
WR_TEST_RESTART_CONTAINER=waiting-room-m1-valkey-1 make test-persistence
docker compose -f deploy/compose/lab.yaml stop
```

통합 시험은 실제 60초 window와 lease grace를 기다리므로 약 1분 이상 걸린다.
혼합 journey 시험은 browser-cookie HTTP client 10개 + app client 10개를 같은 public host와 독립 Gateway
2개로 연결한다. FIFO 3명 입장/17명 대기, commit 뒤 claim 응답을 버린 재시도, origin header/cookie 제거,
60초 token expiry 후 30초 leeway 동안 slot 유지와 다음 FIFO 3명 READY, 만료 token/Coordinator 장애 차단을
실제 Valkey와 약 90초 동안 확인한다. 브라우저 엔진 20개를 띄운 시험이나 별도 Gateway OS process/HA
qualification은 아니다. [실행 기록](../evidence/mixed-journey-summary.md).
Valkey가 없으면 실패하며 skip을 PASS로 계산하지 않는다. 재시작 시험은 다른 lab 세션을
먼저 종료한 상태에서 단독 실행한다. 이 테스트는 snapshot 후 정상 재시작이며 강제 종료·
AOF everysec 유실·replica failover 증거는 아니다.

`make test-browser`는 18082–18085에 HOLD/AUTO 전용 lab을 시작하고 종료한다.
Chromium에서 desktop 1448×1086·mobile 360×800, KR/EN·새로고침·다중 탭 claim/복귀·CSRF를 검증한다.
503/410 UI state는 응답 주입이고 실제 Coordinator 장애는 Go integration에서 별도 확인한다.
보고서 bundle은 `node scripts/run-m1.mjs unique-run-id --browser`로 생성한다. 이 명령은
전용 Valkey를 재시작하므로 다른 lab 세션을 먼저 종료한다. 10K/100K나 최종 release 시험이 아니다.

Valkey는 loopback 16379만 publish하며 **인증 없는 개발용**이다. 외부 서버나 고객 origin에
연결하지 않는다. named volume과 종료된 lab namespace는 자동 삭제하지 않는다. 반복 실행 후
데이터 정리는 별도 승인된 개발 데이터 삭제 절차로 수행한다.

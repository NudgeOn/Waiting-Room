# 로컬 공개 API 제한과 검증

최초 browser 접속·응답 유실·동시 탭은 [브라우저 접속 절차](browser-join.md)를 따른다.

이 소스 후보의 Docker Gateway/Coordinator에 적용한다. 공개 Preview 이미지와
기존 memory-only Traffic Lab은 별도다. FIFO 입장 유량과 아래 요청 제한은 목적과
카운터가 다르다. 요청 제한을 통과해도 입장권이 발급되는 것은 아니다.

Gateway는 연결 상대 주소를 설치 키로 HMAC 처리하고 15분마다 바꾼다. IPv6는 /64로
묶으며 원래 IP·bearer token을 제한 기록에 저장하지 않는다. 클라이언트의
`Forwarded`, `X-Forwarded-*`, `X-WR-Source`는 신뢰하지 않는다. 외부 프록시 뒤에서는
그 프록시가 하나의 출처로 집계되므로 고객 NAT/프록시 환경의 운영 한도 검토가 필요하다.
이 후보에는 trusted-proxy 설정이나 관리자 제한값 조정 UI가 없다.

- Room/출처별 새 join 600건/분, 기존 티켓 작업과 동일 join 재시도 합계 6,000건/분.
- Room 전체 새 join은 profile visitor cap의 6배/분, 티켓 작업은 20배/분.
- 제한은 고정된 분 경계에서 재설정한다. 엄격한 rolling admission 유량과 구분한다.
- 10분 보존 중인 동일 join key는 새 join 제한에 포함하지 않는다. 재시도도 기존 티켓 작업 한도에는 포함해 무제한 반복을 막는다. 본문 충돌 여부와
  티켓 상태는 기존 queue 함수가 다시 검증한다.
- 제한 기록이 없던 이전 설치의 join key는 처음 관측할 때 신규 요청 예산을 사용한다.
  기존 queue의 재시도 레코드는 보존하지만 제한 기록을 소급 생성하지 않는다. 운영 중
  업그레이드의 재시도 집중은 별도 수용 검증 대상이다.
- join의 최초 `pollAfterMs=3000` 응답은 기존 버전과 동일하게 재생한다. 현재 일정은
  status 응답과 `Retry-After`를 따른다.
- 최초 join 제한과 대기표 저장·응답 검증이 성공하면 보조 poll 등록이 실패해도
  확인된 202 응답을 반환한다. 없는 일정은 첫 status에서 등록하고 429로 기다리게 한다.
  선행 join 제한이나 status/claim/heartbeat 제한의 장애 차단은 유지한다.
- ticket별 poll 간격은 SHA-256 seed에 따라 3~20초다. 두 Coordinator가 같은 시점에
  조회해도 공유 함수가 한 요청만 허용한다. 조기 요청은 queue 조회·idle TTL 갱신 없이
  `429 API_RATE_LIMITED`, 올림한 초 단위 `Retry-After`를 받는다.
- poll 기록은 60초 유지한다. 기록 없는 재접속은 먼저 3~20초를 기다리며 분산한다.
- hash와 만료 index는 설치 전체 최대 `visitor cap × 4 + 2048`개다. Standard는
  42,048개다. 요청마다 만료 항목을 최대 128개 회수한다. 새 기록을 만들 수 없으면
  차단하며, 필요한 기록이 이미 있는 기존 티켓·동일 join 재시도는 계속 처리한다.
- 최대 1,000ms 시각 보정 중에는 quota/poll 기록을 변경하지 않고 429와 올림한 Retry-After로 잠깐 대기시킨다. 시각이 따라잡기 전에는 큐에 요청하지 않는다. 새로운 예산·빠른 poll을 허용하지 않으며 큐·입장권·epoch의 시계 검사는 별도로 유지한다.
- 함수 불일치, 기록 불일치, 1,000ms 초과 시계 역행, Valkey 오류는 `503 QUEUE_UNAVAILABLE`이며
  원본 우회로 전환하지 않는다.

제한 함수 `wr_public_guard_v4`는 대기열 runtime과 별도인 불변 라이브러리다. 기존 v1/v2/v3
함수는 덮어쓰지 않으며, schema 1의 출처별 카운터·poll 일정·join 기록을 그대로 사용한다.
업그레이드 시 기존 데이터 역할을 정지하고 owner가 v4를 설치한 뒤 새 역할을 시작한다.
설치/업그레이드 초기화 계정만 로드하고 Coordinator는 정확한 함수 본문을 검증한다. queue 레코드는
이 제한 함수에서 읽거나 쓰지 않는다.

브라우저는 429 뒤에도 현재 대기 상태를 유지하며 `Retry-After`와 jitter 후 재시도한다.
추가 `__Host-wrr_{roomPublicId}` 쿠키는 검증된 return envelope만 보관한다. Secure,
HttpOnly, SameSite=Lax, Path=/이며 Domain이 없다. 기존 티켓의 보호 URL 재방문은
이 envelope로 대기 화면을 다시 열고 티켓/return 만료를 연장하지 않는다. 각 탭의 최종
복귀는 계속 URL의 개별 envelope를 사용하며 이 쿠키는 원본으로 전달하지 않는다.

새 브라우저 방문의 DRAINING/일시 장애는 503 안내 화면을 사용한다. 이 화면은 외부
파일·스크립트·queue 조회 없이 내장 CSS와 정확한 CSP hash만으로 표시하며, 자동으로
join을 반복하지 않는다. App 요청은 같은 HTTP 상태의 JSON problem 계약을 유지한다.
기존 티켓은 대기 화면을 다시 열 수 있어도 장애 중 새 입장권을 발급받을 수 없다.

## 재현

```sh
WR_TEST_RUNTIME_VALKEY=127.0.0.1:16389 go test -tags=integration -race -count=1 ./internal/publicguard
go test -race ./internal/lab ./internal/runtimeplane ./internal/waiting
make check
WR_TEST_TRAFFIC_DOCKER=local \
WR_TEST_CANDIDATE_IMAGE=waiting-room-beta9-confirmed-join:local \
WR_TEST_KEYS=1 PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" \
node test/localbeta/public-runtime.mjs
```

Docker 검사는 명시한 별도 로컬 후보 이미지를 사용한다. 현재 소스로
Linux ARM64 `wr-control`/`wr-node`와 관리자 UI를 빌드한 뒤
`deploy/docker/control.Dockerfile`로 만든다. 관리자 29473, bootstrap 29474, Gateway
30473, 테스트 브라우저 CONNECT 터널 39473은 루프백 전용이다. 기존 설치를 중지하거나
요청하지 않는다. 브라우저 리다이렉트가 실제 20443 authority를 유지하도록 터널을 쓴다.
검사 후 프로젝트를 중지하고 볼륨은 보존한다.

공개 제한을 포함한 후보의 인원 경계는 다음처럼 별도로 실행한다. 기존 기본 모드는
제한 도입 전 이미지용이므로 최신 공개 API 이미지의 증거로 사용하지 않는다.

```sh
WR_TEST_TRAFFIC_DOCKER=local \
WR_TEST_PUBLIC_POPULATION=1 \
WR_TEST_COLD_POPULATION=1 \
WR_TEST_CANDIDATE_IMAGE=waiting-room-beta9-confirmed-join:local \
node test/localbeta/runtime-tiers.mjs
```

이 모드는 1K/2K/5K/10K의 실제 HTTPS join·전원 status를 확인한다. 429의 실제
`Retry-After`를 지키고 기존 대기표는 HTTP heartbeat로 유지하며, quota·시계·TTL을
변경하지 않는다. 실제 TLS 연결은 32개까지이며 poll 대기 타이머는 동시에 진행한다.
최초 일곱 방문자는 순서대로 생성해 마지막에 FIFO 입장을 확인한다. 각 구간의 최근
100개 join 응답만 보존 기간 내 exact replay로 비교한다. 10분 이전 최초 응답의 복구를
주장하지 않는다. 기록한 client 시간에는 quota 대기가 포함되므로 서버 성능 지표가 아니다.
검사는 실제 분 경계를 기다려 수십 분 걸릴 수 있으며, 10K 지속 부하 qualification과 다르다.
503·연결 장애는 자동 재시도로 숨기지 않고 실패시킨다. 오류 코드를 포함한 최초 20개
실패와 제한된 delivery/sync 진단만 남기고 credential은 출력하지 않는다.

`WR_TEST_COLD_POPULATION=1`은 10K 경계 뒤 Gateway/Coordinator와 Valkey를 실제로
정지·재시작한다. 원래 epoch와 대기표를 보존하고, 새 공통 fence 및 실제 안전 대기를
확인한다. 검증 후 HOLD에서 10K 인원을 확인한다. 최근 100개 join은 재시도 보존 기간
안이면 원래 응답과 비교한다. 긴 전원 조회·안전 대기로 보존 기간이 끝난 경우에는
서버 시각과 `QUEUE_CAPACITY_EXCEEDED`를 확인한다. heartbeat는 대기표만 유지하므로
가득 찬 큐에서 만료된 join key를 신규 요청으로 처리하는 것은 기존 대기표 손실이 아니다.
두 경우 모두 원래 100개 대기표 credential의 queued 조회를 검증한 뒤 명시적 AUTO로
최초 일곱 방문자의 FIFO 입장과 mTLS 원본 도달을 검사한다. 이 단계 전 인원 검사가
실패하면 콜드 복구는 미실행이며, 앞 단계의 PASS로 대신하지 않는다.

## 운영 모드와 서비스 중단 검사

추가 `WR_TEST_PUBLIC_FAULT_MATRIX=1`은 기존 72회 검사와 함께 네 모드, 앱의 여섯 메서드,
browser GET/HEAD, 입장권 없음/헤더/쿠키/잘못됨/모호함을 대조한다. 정상·원본 정지·
Coordinator 정지와 기존 queue cookie의 여덟 요청을 합쳐 488회다. browser intent를
먼저 확정해 준비 화면이 실제 join 장애를 가리지 않도록 한다. 320px의 실제 Coordinator
장애 안내·키보드 재시도·axe도 검사한다. 지원하는 이 조합의 결과와 임의의 다중 장애·
운영 proxy/NAT qualification을 구분한다.

`WR_TEST_CLOCK_REFRESH=1`은 실제 역할 인증서로 새 내부 복구 요청을 검사한다.
아래 probe는 비밀정보를 출력하지 않으며, fixture의 읽기 전용 role identity를 사용한다.
Gateway의 동시 8요청은 같은 승인 payload와 새 서명 bytes를 받고 감사 한 건·양쪽 ACK를
확인한다. 원본 역할, 잘못된 authority/본문, 미래 경계는 거부한다. 이 검사는 프로토콜
검증이며 실제 호스트 시계를 변경하거나 자연 발생 역행을 재현했다고 주장하지 않는다.

```sh
mkdir -p build/beta8-clock-probe
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath \
  -o build/beta8-clock-probe/probe test/localbeta/clock-probe/main.go
WR_TEST_TRAFFIC_DOCKER=local WR_TEST_THEME_LOGO=1 \
WR_TEST_PUBLIC_FAULT_MATRIX=1 WR_TEST_CLOCK_REFRESH=1 \
WR_TEST_CANDIDATE_IMAGE=waiting-room-beta9-confirmed-join:local \
PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" node test/localbeta/public-runtime.mjs
```

확장 matrix는 새 FIFO 방문자를 추가하므로 `WR_TEST_KEYS=1`과 같은 fixture에서
실행하지 않는다. 키 수명 주기는 별도 기본 public 실행으로 검증한다. 두 검사는 각각
설정된 일곱 lease 제한을 유지하며, matrix 검사에서는 기존 입장권의 300초 유효 시간을
명시적으로 설정한다. 토큰을 연장하거나 대기 순서를 건너뛰어 테스트를 통과시키지 않는다.

인원 검사에서 `WR_TEST_VALKEY_DIAGNOSTICS=1`을 쓰려면
`test/localbeta/valkey-observer/main.go`를 같은 방식으로
`build/beta8-valkey-observer/probe`에 빌드한다. 이 선택은 새 fixture Valkey의
latency threshold를 100ms로 설정하고 initializer에 `LATENCY LATEST` 읽기만 추가한다.
서비스 역할의 권한·AOF always·TTL·quota는 유지한다. 로그에는 고정된 INFO 수치와
지연 사건만 남기며 SLOWLOG/MONITOR의 명령·인자·키는 수집하지 않는다.
사건 의미는 [Valkey 공식 latency monitor](https://valkey.io/docs/topics/latency-monitor/)를 따른다.
후보별 실제 결과는 [최신 기록](../evidence/beta-20260910-m2-latency.md)을 확인한다.

`WR_TEST_VALKEY_FAULTS=1`은 새 fixture의 첫 대기표가 HOLD에 있을 때 Valkey만
일시 정지한다. join/status/claim/heartbeat의 503 차단과 보호된 원본 접근 거부를 확인한
뒤 반드시 정지를 해제한다. 같은 대기표 조회·동일 join 응답·새 ACK의 인원/epoch/fence가
복구되는지 검사한다. 중단 중 큐 쓰기를 시작하지 않는 시나리오이며, 전송 중 쓰기 유실의
불확실 쓰기 fence 검사는 별도다. API/설정 deadline·안전 대기·quota를 변경하지 않는다.

공개 runtime 검사는 고객 경로 `/shop/cart`에서 네 모드 × 입장권 세 상태 ×
GET/HEAD/POST/PUT/PATCH/DELETE의 72개 조합을 실행한다. 아래는 검사할 기대 계약이며,
실제 후보별 통과 여부는 [수용 기록](../evidence/beta-20260910-clock-recovery.md)을 따른다.

| 모드 | 입장권 없음/잘못된 입장권 | 유효한 입장권 |
|---|---|---|
| HOLD | 429 WAITING_ROOM_REQUIRED | 원본 전달 |
| AUTO | 429 WAITING_ROOM_REQUIRED | 원본 전달 |
| DRAINING | 503 QUEUE_DRAINING | 원본 전달 |
| OFF | 원본 전달 | 원본 전달 |

OFF에서도 예약된 공개 API의 메서드·인증 검사는 유지한다. 즉시 OFF는 Admin의
명령 내용에 묶인 재인증을 요구하며, 동일 요청 재시도는 감사 이벤트 하나만 만든다.
검사 후 AUTO로 돌린다.

원본을 실제 정지하면 유효한 입장권의 여섯 메서드도 503을 받는다. Coordinator를
정지하면 공개 join/status/claim/heartbeat는 503을 받고, Gateway가 자체 검증할 수
있는 유효한 입장권의 원본 접근은 유지한다. 두 장애 모두 입장권 없는 보호 경로를
열지 않는다. 재시작 후 동일 claim 응답과 원본 접근 복구까지 확인한다. 정상 모드와
이 두 서비스 중단 검사가 모든 손상·네트워크·다중 장애 조합을 증명하지는 않는다.

단위/race와 실제 두 Valkey client 시험은 조기 poll의 queue 접근 0, 32개 동시 요청 중
한 번의 허용, 출처 간 격리, 42,048개 상한에서 기존 티켓 보존, 동일 join의 6,000회
재시도 예산과 시계 역행 차단을 검증했다. 실제 Docker HTTPS에서 4개 공개 operation의
응답 schema·상태·캐시 헤더, 조기 poll 20개 동시 차단, 출처 헤더 위조 거부, 동일 claim,
보호 URL의 5개 메서드와 DRAINING을 확인했다. Chromium 360px의 대기/새로고침/탭 복귀,
503 화면 키보드 포커스와 axe WCAG 2 A/AA·2.1 AA 위반 0도 확인했다.
이 결과는 100K reconnect/지속 부하, proxy/NAT 운영 qualification, key rotation,
만료 후 exact join replay, 전체 공개 API 상태 조합 또는 보조기기 사용자 acceptance의
완료를 뜻하지 않는다. Beta 최종 판정은 [Beta 계획](../beta-plan.md)을 따른다.

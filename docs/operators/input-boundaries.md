# Public lab 입력 경계와 fuzz

```sh
make test-unit PRD=03
make test-unit PRD=05
make test-fuzz-input
make test-fuzz-config
WR_TEST_VALKEY=127.0.0.1:16379 make test-processes
node scripts/run-m1.mjs unique-run-id --processes --tiers --http-tiers --fuzz
```

## join 요청

- Content-Type은 정확한 `application/json` media type이다. 타입 대소문자와 `charset=UTF-8`은 허용한다.
  접두사만 같은 타입, 다른 charset, 알 수 없는 parameter와 중복 Content-Type은 400이다.
- body는 최대 4,096 bytes이며 JSON decode 전에 UTF-8을 검증한다.
- root object에 `target` 필드 정확히 한 개만 허용한다. unknown/case-variant/duplicate field,
  null/잘못된 타입/추가 JSON 문서를 거부한다. JSON 공백·정상 string escape는 허용한다.
- Idempotency-Key는 단일 header 한 개, 16~128 bytes다. 누락/중복은 400, 다른 target의 동일 key는 409다.
  충돌 뒤 원래 target 재시도는 기존 응답을 보존한다.
- 내부 service header는 한 개만 허용하고 누락/중복/불일치는 401이다. Gateway는 외부 internal header를
  제거한 뒤 자기 service credential 한 개를 설정한다.
- 중복 Authorization 또는 중복 admission header는 큐 read/서명 검증/원본 요청 전에 400으로 거부한다.

이 변경은 body를 저장·재생하는 기능이 아니다. body는 한 요청의 4KiB bounded parse용 메모리에서만 처리하고
원문 오류/credential/body를 로그에 남기지 않는다. 운영의 일반 URL normalizer·전체 request matrix는 후속이다.

## fuzz와 재현

`make test-fuzz-input`과 `make test-fuzz-config`는 각각 10,000회, worker 2개, GOMAXPROCS=2,
45초 timeout으로 pure parser/verifier를 검사한다. Valkey나 production URL에 트래픽을 보내지 않는다.
매 실행의 생성 입력은 Go fuzz cache에 따라 달라지며 고정 seed trace와 동일한 시험은 아니다.
서명 fuzz의 고정 private key는 test-only fixture이고 runtime/배포에서는 사용하지 않는다.

발견된 invalid UTF-8 query 입력은 `internal/lab/testdata/fuzz/FuzzJoinTarget/f432ebb00629261b`에
재현 corpus로 보존한다. JSON decoder가 replacement character로 바꾸기 전에 이제 거부한다.
20초 time-based join fuzz 재실행 한 번은 `context deadline exceeded`로 종료되어 PASS로 세지 않았다.
고정 evaluation-count 실행과 full runner 결과를 별도로 기록한다. fuzz PASS는 모든 입력의 안전성 증명이 아니다.

## config clock 동시성

`configtrust.Apply`/`Current`는 내부 mutex를 얻은 뒤 실제 시각을 측정한다. 시각을 caller에서 미리 읽고
대기하면 정상 goroutine 실행 순서 차이가 clock rollback처럼 보일 수 있기 때문이다. fake-time API는
package 내부 테스트용으로 분리했다. clock 검사와 같은 lock 안의 측정을 unit으로 확인한다.

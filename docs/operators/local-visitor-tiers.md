# 로컬 방문자 1K / 2K / 5K / 10K 시험

```sh
make lab-valkey
WR_TEST_VALKEY=127.0.0.1:16379 make test-local-tiers
# 실제 별도 PID Gateway/Coordinator HTTP 경로
WR_TEST_VALKEY=127.0.0.1:16379 make test-http-tiers
# 소스·로그 digest를 고정한 전체 회귀 증거. 실행마다 새 ID 사용.
node scripts/run-m1.mjs unique-run-id --processes --tiers --http-tiers
```

## 무엇을 검증하는가

4개 Room을 같은 설치 namespace에 만들고, 32개 작업자로 1,000 → 2,000 → 5,000 →
10,000개 논리 방문자 상태를 등록한다. 각 단계는 독립 namespace이며 앞 단계 실패 시 다음 단계로 가지 않는다.
각 설치의 visitor cap은 해당 단계 인원, idempotency cap은 인원의 2배다.

- 전원 등록과 read-only status 조회, Room별 sequence 중복/누락 검사
- 10% 동일 join 재시도 결과 보존, 새 방문자 128건의 예상 용량 거부
- Room별 선두 3명 promotion/claim 및 claim 재시도, lease 초과 promotion 0건
- 설치 합계·Room 실제 데이터 수 일치, 80% 경고, 신규만 거부
- Valkey 메모리·eviction·예상/비예상 server error 수와 join/status p95·p99 기록

직접 Store → Valkey protocol 시험이다. HTTP Gateway/Coordinator, 실제 브라우저, 모바일 기기,
10,000개 동시 TCP 연결, 지속 TPS/초당 유입 성능, 전체 인원의 입장 완료를 증명하지 않는다.
각 단계 입장은 12명(4 Room × 3명)이며 나머지는 대기 상태를 유지한다.
latency는 race detector와 load generator가 같은 개발 머신에 있는 짧은 관측값이고 성능 보장 기준이 아니다.

## 안전 범위

고정 loopback `127.0.0.1:16379`만 받는다. 전용 Compose Valkey를 사용하고 다른 테스트·재시작과
동시에 실행하지 않는다. 시작 전 `docker info`, 컨테이너 scope label, Valkey memory를 확인한다.
각 단계는 고정 fixture 기준 `현재 used_memory + 인원 × 8 KiB < maxmemory × 70%`를 사전 검사하고,
끝에서 used_memory 70% 미만·eviction 증가 0·예상 capacity 오류 128건 이외 오류 0을 검사한다.
이 메모리 여유 추정은 실제 최대 8KiB replay payload나 운영 트래픽의 sizing 근거가 아니다.
단계별 context 120초, suite 10분 상한. 시험이 만든 정확한 key만 정리하며 기존 namespace·volume·library는 보존한다.

## HTTP 단계별 시험

`test-http-tiers`는 실제 OS child PID 4개(Gateway 2, Coordinator 1, origin 1)를 사용한다.
각 단계는 별도 설치 namespace이고 Standard 10K cap을 유지한 채 1K/2K/5K/10K를 등록한다.
최대 32개 동시 요청으로 전원 join/status, 10%의 교차 Gateway join replay, FIFO 선두 3명의
claim/교차 retry/origin 전달을 검사한다. 각 단계 unsafe POST 128건은 429이고 origin에 도달하지 않는다.
10K 단계에서만 추가 방문 128건을 503 `QUEUE_CAPACITY_EXCEEDED`로 거부한다.
이후 Coordinator를 실제 SIGKILL해 신규 join 503, 기존 admission 3건의 origin 통과를 확인한다.

각 단계 context 55초, HTTP timeout 4초, worker 32개다. 60초 admission TTL 안에 마치는 짧은 시나리오이며
시간 예산을 넘으면 실패하고 다음 단계로 가지 않는다. 종료 시 child를 기다리고 그 namespace의
알려진 key 11개만 삭제한다. admission/ticket/credential/요청 본문은 결과 로그에 기록하지 않는다.
메모리 사전 한도와 종료 임계는 위 Store fixture와 동일하다.

`make test-local-tiers`/`test-http-tiers`의 generator와 process lab child는 `GOMAXPROCS=2`로 Go 실행 병렬도를
고정한다. CPU 하드 quota나 production sizing이 아니며 32개 HTTP 요청의 동시성은 그대로다.
`run-m1.mjs --http-tiers`는 같은 소스에서 HTTP 네 단계를 3회 반복한다. local 재현성 확인용이고
SUB-PRD-07 Standard qualification 3회와는 다른 시험이다. phase 실패 시 후속 phase를 진행하지 않는다.

HTTP native-app JSON 프로토콜 시험이지 실제 모바일 기기나 렌더링 브라우저 수천 개 시험이 아니다.
인원 전체의 입장/만료 완료, 지속 status rate·jitter·early-poll, 정상 재개/HA, 분리된 load generator의
CPU/FD/네트워크 임계, 운영 SLO는 검증하지 않는다. 별도 PID도 동일 사용자/호스트라 production 격리가 아니다.

join 실패는 최대 8개 고정 오류 category와 총 실패 수만 출력한다. 실패 시 fixture 정리 전에
schema/mode/dirty/clock/합계 counter만 읽어 기록한다. 원문 응답·config·credential·ticket은 출력하지 않는다.
2026-09-06 최종 반복 중 5K에서 간헐 503이 관측되어 원인 미확정 blocker로 남아 있다.
후속 PASS가 나오더라도 이전 실패 원인을 해결했다는 뜻은 아니다.

## 설치 합계 저장소

`OpenRoom`은 기존 `Open` 단일 Room 경로와 다른 immutable library `wr_queue_install_v2`/schema 2를 사용한다.
기존 설치 v1 library/source/data는 보존하며 자동 migration 또는 FUNCTION REPLACE는 하지 않는다.
v1 namespace를 v2로 열면 schema 오류로 거부한다. 새 lab namespace에서 검증한다.
unsharded single-primary만 허용하며 여러 Room과 공유 visitor/idempotency 만료 인덱스의 쓰기를
한 Function에서 처리한다. 예상치 못한 부분 쓰기 오류는 설치 공유 dirty marker를 남겨 다른 Room과
새 Store handle도 fail-closed한다. 인덱스 cardinality 불일치도 거부하고 자동 복원하지 않는다.
예약 Room ID `installation`은 공유 key와 충돌하므로 거부한다.

만료된 visitor/idempotency도 소유 Room의 실제 hash record와 함께 삭제될 때까지 설치 용량을 점유한다.
다른 Room의 sweep으로 공유 인덱스만 제거해 용량을 반환하지 않는다. cleanup은 각 종류 128건씩 제한한다.
Capacity의 visitors/idempotency는 유효 상태 수, retainedVisitors/retainedIdempotency는 미수거 만료 상태를
포함한 점유 수다. cap과 80% 경고는 점유 수 기준이다. 비활성 Room도 sweep하지 않으면 용량을 계속 점유한다.
운영용 전체 Room sweep scheduler/복구는 아직 미완료다.

새 namespace만 신규 설치로 취급한다. 기존 Room이 남았는데 설치 metadata가 없으면 거부한다.
설치 metadata의 Room registry에 등록된 Room이 유실되면 재초기화하지 않아 sequence 재사용을 차단한다.
전체 metadata/인덱스 유실을 포함한 durable installation registry, fencing, primary recovery,
production config 배포·복구와 HA는 미완료다. 동일 cardinality의 악의적 데이터 치환까지 검증하는 무결성 장치는 아니다.
80% 경고는 내부 Capacity snapshot이며 아직 Backoffice 알림에 연결하지 않았다.
별도 PID process lab은 이 경로를 사용하고 기존 단일-process lab은 이전 경로를 보존한다.

Standard/High qualification 및 MAIN final test는 별도 gate다. 이 시험만으로 GO/profile badge를 발행하지 않는다.
v2 변경 및 최종 회귀는 [retention 기록](../evidence/installation-retention-summary.md)을 따른다.

# 실제 HTTP 방문자 단계별 검증

2026-09-06 local uncommitted snapshot. `WR_TEST_VALKEY=127.0.0.1:16379 make test-http-tiers` 네 단계 PASS.
고정 source/log digest 전체 회귀 bundle `http-visitor-tiers-20260906-local`의 10K HTTP 재실행은
55초 deadline 도달로 FAIL했다. 단독 PASS와 반복 실행 실패를 구분한다. 실패 artifact는 보존한다.
첫 실패는 status 마지막 구간이며 이후 취소된 context로 replay를 계속 요청해 연쇄 오류가 기록됐다.
resource 관측에서 eviction/메모리 부족은 확인되지 않았지만 host VM/Go 프로세스의 CPU 사용과 AOF delayed fsync가
관측됐다. 원인으로 단정하지 않으며 local generator/child의 CPU 예산 고정과 phase 실패 즉시 중단을 검증한다.

후속 수정: local generator/child `GOMAXPROCS=2`, phase 실패 즉시 중단, timeout/HTTP/응답 차이의
redacted 진단 분리. 인원·32 workers·55초 deadline·기능 합격 조건은 유지했다.
이 설정의 단독 재실행은 네 단계 PASS, 10K 시나리오 약 4.04초였다. 3회 반복 전체 bundle로 재확인한다.

## 최종 고정 소스 재검증

- `node scripts/run-m1.mjs http-visitor-tiers-20260906-bounded --processes --tiers --http-tiers`
- **12 checks PASS**: foundation/public unit, HTTP 네 단계 3회, Store 네 단계, process 통합/Quick20,
  기존 통합, 정상 재시작, Quick20, 의도된 final NO-GO guard.
- UTC 2026-09-06 01:16:15.116~01:18:41.741, source unchanged.
- source SHA-256: `b92e540b2ea063b4a211d9538ef8117db50cdc28486341bda0a5f4baf5166602`.
- [보고서](http-visitor-tiers-20260906-bounded/report.json), [환경](http-visitor-tiers-20260906-bounded/environment.json),
  [반복 1](http-visitor-tiers-20260906-bounded/http-visitor-tiers-1.log),
  [반복 2](http-visitor-tiers-20260906-bounded/http-visitor-tiers-2.log),
  [반복 3](http-visitor-tiers-20260906-bounded/http-visitor-tiers-3.log).

| 10K 반복 | 시나리오 초 | join p95 / p99 ms | status p95 / p99 ms | 결과 |
|---:|---:|---:|---:|---|
| 1 | 2.875 | 6.401 / 7.486 | 5.262 / 6.163 | PASS |
| 2 | 2.888 | 6.446 / 7.800 | 5.159 / 5.952 | PASS |
| 3 | 2.844 | 6.366 / 7.598 | 5.243 / 6.103 | PASS |

1K/2K/5K도 매 반복 모두 PASS. 동일 머신·32 workers·GOMAXPROCS=2·짧은 local app HTTP 관측값이며
1만 동시 TCP 연결이나 지속 부하/Standard qualification으로 해석하지 않는다.
최초 [실패 보고서](http-visitor-tiers-20260906-local/report.json)도 source unchanged 상태로 보존한다.

- 4 child PID: Gateway 2개 → Coordinator → Valkey, 샘플 origin 1개.
- 1K/2K/5K/10K 각각 전원 join/status, 10% join replay, 선두 3명 claim/retry/origin PASS.
- 각 단계 미입장 unsafe POST 128건 429, origin 도달 0.
- 10K 설치에서 신규 초과 128건 503. Coordinator SIGKILL 후 신규 join 503, 유효 입장권 3건 계속 통과.
- 중복 ticket, FIFO inversion, 잘못된 origin 전달, eviction 0. 모든 owned child 종료 및 fixture key 정리.
- Store 직접 시험과 달리 HTTP 프로세스 경로를 통과한다. 실제 모바일/브라우저 엔진 시험은 아니다.

단계별 최대 32 workers, 55초 deadline, 짧은 local correctness fixture다. 전체 방문자의 입장/만료,
지속 유량, generator/SUT 분리, N-1 무중단, production origin ACL/권한 격리와 qualification은 미검증이다.
각 owner sub-PRD 및 MAIN delivery NO-GO 유지. final guard 거부는 FT PASS가 아니다.

# 설치 합계 용량 및 단계별 로컬 방문자 검증

2026-09-06, uncommitted local snapshot. 전체 회귀 bundle 9 checks PASS.

- 실행: `node scripts/run-m1.mjs installation-capacity-20260906-local --processes --tiers`
- UTC: 2026-09-06 01:00:57.614~01:03:05.449
- source SHA-256: `7618daae028ec7b0de134a1abad88a4d8f3a2dec543e4af5e5e790e973908e3b` (실행 중 변경 없음)
- [보고서](installation-capacity-20260906-local/report.json), [환경](installation-capacity-20260906-local/environment.json),
  [통합](installation-capacity-20260906-local/integration.log), [단계별](installation-capacity-20260906-local/visitor-tiers.log).

- 설치 visitor/idempotency 공유 cap, 80% snapshot 경고, immutable profile/config, 예약 Room ID 차단 구현.
- 부분 Function 쓰기 오류·인덱스 유실은 다른 Room과 새 Store handle까지 fail-closed.
- process lab Coordinator를 `OpenRoom` 경로에 연결. 기존 단일 Room library와 데이터 보존.
- 로컬 1K/2K/5K/10K 직접 Store 시험 모두 PASS. 전원 join/status, 10% replay,
  각 128건 초과 거부, 12명 FIFO claim, 중복/용량 초과/eviction 0.
- 테스트 개발 중 ZSCORE의 RESP3 float 응답을 integer로 읽어 1건 실패했고 test decoder를 수정했다.

새 unit 2개와 설치 전용 integration 6개 PASS. 기존 `make check`, public unit, process integration/Quick20,
단일 Room/혼합 HTTP integration, 정상 재시작 persistence, Quick20, NO-GO final guard를 함께 통과했다.
기존 restart 시험은 v1 경로이며 설치 공유 registry의 HA 복구를 증명하지 않는다.

| 방문 상태 | join p95 / p99 ms | status p95 / p99 ms | Valkey used bytes | 결과 |
|---:|---:|---:|---:|---|
| 1,000 | 4.661 / 6.779 | 3.601 / 7.612 | 4,138,136 | PASS |
| 2,000 | 3.779 / 7.639 | 3.088 / 3.902 | 5,555,480 | PASS |
| 5,000 | 3.792 / 5.159 | 3.129 / 5.536 | 9,329,040 | PASS |
| 10,000 | 3.935 / 5.162 | 3.430 / 5.339 | 16,038,096 | PASS |

각 단계는 32 workers·4 Rooms·직접 Store 호출·race detector를 사용한 약 0.22~1.59초의 짧은 fixture다.
latency와 메모리는 관측값일 뿐 지속 TPS/HTTP/API SLO 또는 최대 payload sizing의 보장 수치가 아니다.
테스트 소유 key만 삭제했으며 기존 데이터/library/volume은 보존했다. 다음 승인된 개발 시험을 위해 Valkey는 실행 상태다.
HTTP 단계별 부하, sustained load, HA/recovery, signed config, source quota, production qualification은 NOT RUN/미완료.
SUB-PRD-02/07 및 MAIN delivery NO-GO 유지. `make test-final` guard의 의도된 거부는 final test PASS가 아니다.

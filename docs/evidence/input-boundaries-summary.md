# 입력 검증·fuzz 및 clock 동시성 보강

2026-09-06 local uncommitted snapshot. 고정 source 전체 회귀 실행 전 기록.

- 수정 전 unit에서 JSON media type/중복·case-variant 필드/중복 protocol header의 경계 실패 12개를 확인.
  일부는 queue read/write까지 도달했고, 일부는 기대와 다른 HTTP 상태였다. origin 우회가 입증된 것은 아니다.
- strict media type, UTF-8/4KiB body, token-level 단일 target field, 단일 credential/idempotency header 검사로 수정.
- fuzz가 invalid UTF-8 query의 silent replacement를 발견했다. corpus `f432ebb00629261b` 보존 및 decode 전 검사 추가.
- 최초 20초 join fuzz 재검증은 525,109 executions 뒤 context deadline error로 끝나 PASS로 세지 않았다.
  이후 고정 10,000 evaluations join/config fuzz 각각 PASS. config 20초 fuzz도 PASS(179,604 executions).
- core clock의 caller-side 시각 측정은 정상 goroutine 재정렬을 rollback으로 오판할 가능성이 있어
  runtime clock sampling을 lock 내부로 옮겼다. 이는 코드 검토로 찾은 경계이며 운영 incident 재현 주장이 아니다.
- 신규 입력 unit 3개(16 body cases +4 duplicate-header cases +admission duplicate), clock serialization unit 1개,
  pure fuzz target 2개. actual public HTTP input + 정상 Quick20 + idempotency conflict/replay 통합 1개.

[실행과 정확한 범위](../operators/input-boundaries.md). production normalization·전체 HTTP matrix/qualification 미완료,
MAIN/SUB delivery NO-GO 유지. 최종 고정 bundle 결과를 아래에 기록한다.

## 고정 source 회귀

- `node scripts/run-m1.mjs input-boundaries-20260906-local --processes --tiers --http-tiers --fuzz`
- **15 checks PASS**, UTC 2026-09-06 01:57:21.694~02:02:10.615, source unchanged.
- source SHA-256: `953b1bb4ca03a1c1d5a37c3307ef31d99590b7d275be6df798e8a34d7a29d379`.
- [보고서](input-boundaries-20260906-local/report.json), [환경](input-boundaries-20260906-local/environment.json),
  [public unit](input-boundaries-20260906-local/public-lab-unit.log),
  [입력 fuzz](input-boundaries-20260906-local/fuzz-input.log), [서명 fuzz](input-boundaries-20260906-local/fuzz-config.log),
  [HTTP process](input-boundaries-20260906-local/process-integration.log).
- HTTP 네 인원 단계 3회, Store 단계, signed config, 입력/기존 process, 실제 만료/정상 재시작도 함께 PASS.

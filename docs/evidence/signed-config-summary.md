# Signed config / cold-start 검증

2026-09-06 uncommitted local snapshot. 고정 source 전체 회귀 **13 checks PASS**.

- configtrust core unit 10개, role binding unit 2개 구현.
- 실제 process integration 3개 PASS: Gateway cold trust 4변형·실제 expiry,
  Coordinator unsigned 쓰기 0건·expiry 후 API 503 및 promotion clock 불변.
- 최초 단위 검증 과정에서 이동한 model import 1건을 제거해 compile 오류 수정.
- [실행·보안 경계](../operators/signed-config-lab.md). processlab MemoryStore와 별도 FileStore restart 시험을 구분한다.

production Control/전체 config schema·deployment trust·refresh/ACK/key rotation·generation fencing·HA는 미완료.
MAIN 및 모든 sub-PRD delivery NO-GO 유지. 전체 회귀 결과/digest는 아래에 추가한다.

## 고정 source 검증

- `node scripts/run-m1.mjs signed-config-20260906-local --processes --tiers --http-tiers`
- UTC 2026-09-06 01:36:31.211~01:39:15.481, source unchanged.
- source SHA-256: `986b0a4b79f71c1dd52133637529fcf598e1c5250253ede44ee035d56209f428`.
- [보고서](signed-config-20260906-local/report.json), [환경](signed-config-20260906-local/environment.json),
  [서명/role unit](signed-config-20260906-local/config-trust-unit.log),
  [실제 프로세스·만료](signed-config-20260906-local/process-integration.log).
- core unit 10개 + 신규 role binding unit 2개 + 기존 IPC unit 4개 PASS.
- 신규 process integration 3개와 기존 process integration 2개 PASS. 모든 정상 child의 성공 종료도 검사.
- HTTP 1K/2K/5K/10K 3회, Store tiers, 기존 단위/통합/정상 재시작·Quick20·final guard 모두 PASS.

결과는 local trust/파일 원자 저장/프로세스 ingress 차단의 증거다. 실제 Linux power-loss durability,
production trust 배포·5분 refresh·전체 payload schema·handler swap·ACK/rotation·queue generation fencing은 미검증이다.

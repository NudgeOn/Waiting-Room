# M1 로컬 앱 실행 구현 — 2026-09-05

이 문서는 앱 전용 단계의 역사적 snapshot이다. 후속 browser/template 및 최신 전체
회귀 결과는 [Calm template 기록](browser-template-summary.md)을 따른다.

최종 로컬 bundle **PASS**. [report.json](m1-20260905-app-runtime-final/report.json),
[환경 기록](m1-20260905-app-runtime-final/environment.json).

- 실행: 2026-09-05 05:34:58~05:36:09 UTC, macOS arm64, Valkey 8.1.6
- source SHA-256: `8b7a915ce9f62c21ae6ae30311da11a7d9353a63b73e99338e4af89bff78ed1d`
- 상위 테스트 40개 PASS (Go 24 + JS 16, 중복 실행 제외), randomized 하위 시나리오 45개 PASS
- format/vet·두 OpenAPI lint·9개 PRD 검사 PASS
- foundation/integration/restart/Quick20/final guard 5개 단계 PASS; source와 log digest 확인 PASS
- 최종 guard PASS는 8개 sub-PRD NO-GO 때문에 진입을 거부했다는 뜻이며 최종 통합 시험 자체는 NOT RUN

원본 로그: [foundation](m1-20260905-app-runtime-final/foundation.log),
[integration](m1-20260905-app-runtime-final/integration.log),
[restart](m1-20260905-app-runtime-final/restart.log),
[Quick20](m1-20260905-app-runtime-final/quick20.log),
[final guard](m1-20260905-app-runtime-final/final-guard.log).

**앱 walking-skeleton slice GO**, **M1 전체와 출시 NO-GO**. 검토: Codex, 최종 로컬
source/log digest 검사 후 판정. 별도 사람/외부 보안 reviewer 승인을 의미하지 않는다.

판정 범위: **앱 walking-skeleton 부분 구현**. M1 전체, 각 sub-PRD delivery,
Backoffice 완성, production 및 10K/100K qualification은 **NO-GO**다.

## 구현

- 실제 Valkey 8.1.6 Function: FIFO·READY/claim·rate·expiry·idempotency budget·HOLD/DRAINING
- ticket hash와 encrypted join token replay, deterministic Ed25519 admission
- 두 loopback Gateway → 내부 credential을 검사하는 Coordinator → Valkey
- Gateway의 local 입장권 검증 → deterministic origin
- `make lab-valkey`, `make lab`, `make lab-quick`, 명시적 integration/persistence target

## 시험 범위

| 시험 | 관측 | 주의 |
|---|---|---|
| Quick20 | 20명 중 FIFO 3명 입장·17명 대기, 교차 Gateway retry 동일 | 앱 JSON만; 브라우저 대기 화면 아님 |
| 동시성 | 두 Store client의 40개 동일 join/claim, 20개 promotion 경쟁 | 두 OS Coordinator process/HA 아님 |
| Oracle | 5 seeds × 100 joins와 promotion/claim trace의 순번·서버 시간 비교 | 전체 명령·장애 trace는 후속 |
| 시간 | 실제 60초 rate window와 1초 추가 lease grace | 테스트 설정; lab Gateway는 30초 유예 사용 |
| TTL | read-only status와 heartbeat·만료 후 재생 금지 | 실제 서버 시간; fake clock 없음 |
| 재시작 | 별도 전용 Compose 정상 restart 시험 | 강제 종료·replica failover·자동 복구 아님 |
| 보호 | 미입장/unsafe origin 차단, internal auth, header 제거, Coordinator down 503 | signed config·SSRF 일반 정책·browser·mTLS는 미구현 |

초기 실행에서는 테스트 fixture ID 충돌과 저장소 key index 오지정이 발견돼 수정했다.
read-only snapshot의 조회 오류도 무시하지 않도록 바꿨다. 초기 실패를 최종 PASS로 숨기지 않으며,
최종 결과는 수정된 소스의 별도 실행 bundle에 연결한다.

첫 bundle `m1-20260905-app-runtime`은 restart 단계의 readiness 판정으로 FAIL했다.
Docker restart 반환 뒤 Valkey가 LOADING일 수 있어, 신규 입장 차단을 유지한 채 PONG을
기다린 후 persisted hold를 검사하도록 fixture를 수정했다. 기존 FAIL bundle은 보존한다.

남은 경계는 [ADR-0003](../adr/0003-m1-local-runtime.md), 실행 방법은
[local-lab.md](../operators/local-lab.md)를 따른다. source/log hash는 로컬 증거이며
commit·원격 CI·컨테이너 release·취약점 인증이 아니다.

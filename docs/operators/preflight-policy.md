# 설치 전 검사 판정 라이브러리

`internal/installplan/preflight`는 신뢰된 collector가 수집할 관측값을 판정하는 **pure Go 기반**이다.
현재는 unit fixture로 검증하며 NTP/Kubernetes/DB/origin을 직접 조회하지 않는다.
pure library 자체에는 CLI/UI가 없다. 후속 [로컬 시계 진단](clock-diagnostic.md)은
Linux chrony 관측을 이 정책에 전달하며 기존 `wrctl plan`의 출력/exit 의미는 바꾸지 않는다.

```sh
make test-unit PRD=06
node scripts/run-installplan.mjs my-unique-preflight-run
```

## 판정 계약

`Evaluate(planInput, planDigest, now, observations)`는 호출자가 명시한 시각에 판정한다.
`installplan.Digest`는 `Build` 결과를 compact JSON(개행 없음)으로 직렬화한 SHA-256이다.
다른 리전·TOTP·profile·유량 계획의 관측을 재사용할 수 없도록 입력 계획과 digest 일치를 검사한다.
hash는 서명이 아니며 관측의 진위/collector 신뢰를 증명하지 않는다.

각 관측은 plan digest, sanitized artifact digest와 observedAt을 가진다. 값이 없거나
미래/60초보다 오래된 관측은 `UNVERIFIED`다. **60초는 이 정책 기반의 보수적 초깃값**이며
실제 수집 주기·장기 성능 qualification의 승인값은 아니다. 원본 endpoint/credential/로그를
결과에 넣지 않으며 clock method는 허용 enum, source는 별도 정제 기록의 digest로 남긴다.

| 검사 | PASS | WARN | 차단 또는 미검증 |
|---|---|---|---|
| clock | 동기화됨, 절대 offset ≤2초 | 2초 초과~5초 이하 | >5초/비동기화 FAIL; 관측·source·method 누락 UNVERIFIED |
| store | auth/role/read-write; High는 TLS/connection switch까지 | 없음 | 하나라도 FAIL이면 실패 우선; 미실시·알 수 없는 값 UNVERIFIED |
| origin | 유효한 active probe PASS | 없음 | probe FAIL 또는 미수행/불가 UNVERIFIED |
| High HA record | PASS를 만들지 않음 | 운영자 acknowledgement만 있음 | acknowledgement도 없으면 UNVERIFIED |
| High T−10분 | Gateway 최소 8 Ready, Coordinator 최소 4 Ready | 없음 | 목표 미달 FAIL, 관측 없음 UNVERIFIED |
| High T−5분 | 모든 desired pod Ready, Valkey p95 RTT >0 및 <2ms, sample 존재 | 없음 | NotReady/≥2ms FAIL, RTT/sample 없음 UNVERIFIED |

Standard에는 High 전용 HA/event 검사를 `NOT_APPLICABLE`로 표시한다. Standard store 검사는
isolated-network/auth 요구의 일부일 뿐 TLS 지원 여부나 안전한 topology 전체를 증명하지 않는다.
High profile에는 예약 event context가 없으면 준비 상태를 추측하지 않고 `UNVERIFIED`를 반환한다.

## 예약 event의 시각 경계

T−10분/T−5분 각각 `[deadline − 60초, deadline]`에 기록한 별도 snapshot을 요구한다.
마감 전에는 `NOT_DUE`이며 승인할 수 없다. 마감 뒤 늦게 찍힌 정상 snapshot은 해당 마감의
성공으로 대체할 수 없다. T−10분에도 desired 숫자만 늘린 상태가 아니라 최소 Ready 수를 요구한다.
T−5분에는 Gateway 8~12, Coordinator 4~8, Control 2의 모든 desired pod가 Ready여야 한다.
snapshot은 동일 event digest에 묶이며 전체 rollout을 대표해야 한다. RTT sample 수가 양수인
것만 검사하므로 표본의 통계적 적절성·부하 환경은 collector/qualification에서 추가 검증해야 한다.

이 함수는 과거 checkpoint 관측을 평가할 뿐, 실패 결정을 저장하거나 scheduler를 정지하지 않는다.
후속 production adapter는 revision/event binding, checkpoint 성공/실패의 영속화, 수동 변경 경쟁,
활성화 직전 현재 readiness 재검사와 관측 collector의 인증을 구현해야 한다.

## 제품 GO와 분리

결과는 `scope: offline-preflight-policy-only`, `activationAllowed:false`, `qualification:NOT_RUN`이다.
`hasBlockingChecks:false`도 **이번 함수가 다룬 일부 관측**에 차단 항목이 없다는 뜻일 뿐이다.
실제 clock 동기화, 연결 검사, topology/backup/RPO/RTO 기록 검증, 서명 설정, 전체 cap,
origin 보호, Quick20, production activation와 UI 연결은 아직 완료하지 않았다.

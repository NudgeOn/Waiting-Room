# 로컬 시계 진단 — 부분 구현

`wrctl doctor-clock`은 계획 JSON을 먼저 검증한 뒤 Linux 로컬 chrony를 읽어 기존 preflight
정책에 연결한다. 시계 변경, 외부 NTP 직접 질의, 설치·활성화, DB/브라우저 접근은 하지 않는다.
현재 macOS/Windows는 대체 결과를 추측하지 않고 `UNVERIFIED`다.

```sh
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/go-mod"
go build -o build/wrctl ./cmd/wrctl
./build/wrctl doctor-clock < test/installplan/standard.json
make test-unit PRD=06
node scripts/run-installplan.mjs my-unique-clock-run --clock-local
```

## 실행 경계

- 고정 명령: `/usr/bin/chronyc -n -h 127.0.0.1 tracking`
- shell/PATH 검색/사용자 지정 host·실행 파일·명령 인수 없음. `LC_ALL=C`, `LANG=C`를 사용한다.
- Linux 호스트의 신뢰된 `/usr/bin/chronyc`와 로컬 daemon이 전제다. 자동 설치·sudo·설정 수정 없음.
- 실행 3초 timeout, 종료 후 pipe wait 100ms, stdout 16 KiB 상한. stderr와 실패 원문은 출력하지 않는다.
- 명령 없음·접근 거부·실패·취소·timeout·지원하지 않는 형식은 `UNVERIFIED`다.
- 명시적 CLI 전용이며 preview HTTP에 연결하지 않는다. 원격 호스트는 해당 호스트에서 실행해야 한다.

chrony의 `tracking`은 읽기 전용 관측 명령이며 `System time`과 `Last offset`은 서로 다른 값이다.
현재 system clock 오차에는 `System time`을 사용한다. [chronyc 공식 문서](https://chrony-project.org/doc/4.8/chronyc.html)

## 판정

기존 정책의 절대 offset ≤2초 PASS, >2~5초 WARN, >5초 또는 비동기화 FAIL을 유지한다.
부호는 system clock이 빠르면 양수, 느리면 음수다. 파서는 1ns 단위로 계산하며 float를 사용하지 않는다.

추가 collector 안전 경계:

- local reference mode는 외부 동기화 증거가 아니므로 `UNVERIFIED`.
- leap insert/delete는 이 collector에서 미지원이며 `UNVERIFIED`.
- 마지막 reference가 5분보다 오래됐거나 미래 5초를 넘으면 `UNVERIFIED` (보고된 offset 보정 허용).
- `|offset| + root dispersion + root delay/2`가 5초를 넘으면 불확실성을 이유로 `UNVERIFIED`.
- 명백한 비동기화·>5초 offset FAIL은 오래된 reference보다 우선한다. local/leap 미지원은 계속 미검증이다.
- reference freshness와 uncertainty 제한은 보수적인 제품 초깃값이며 chrony 기본 설정이나 qualification 기준 승인이 아니다.
- 13개 필수 tracking label은 중복/누락/추가를 거부한다. 판정에 사용하지 않는 Last/RMS offset,
  frequency/skew/update interval은 비어 있지 않은 ASCII 값인지까지만 검사하며 결과에는 보관하지 않는다.

## 출력·exit 계약

| Exit | 의미 |
|---|---|
| 0 | clock 항목만 PASS/WARN; 전체 설치 승인 아님 |
| 3 | clock FAIL/UNVERIFIED; JSON 판정은 출력됨 |
| 2 | 잘못된 명령/입력; subprocess 실행 전 거부 |
| 1 | 출력 실패 |

보고서는 `scope: local-clock-diagnostic-only`이고 nested policy는 언제나
`activationAllowed:false`, `qualification:NOT_RUN`이다. 미실시 store/origin 검사는 계속 차단한다.
`go run`은 자식 exit 3을 자체 exit 1로 감쌀 수 있으므로 자동화에서는 빌드된 실행 파일을 사용한다.

관측 artifact에는 method, 고정 source 분류, plan digest, UTC 관측/reference 시각, stratum,
offset, delay/dispersion, 동기화 flag만 남긴다. peer IP/Reference ID/명령 원문은 남기지 않는다.
`artifactDigest`와 `clockSourceDigest`는 이 동일한 정제 artifact를 compact Go JSON으로 직렬화한
SHA-256이며 서명이 아니다. 기존 계획/비용 `wrctl report`와는 별도 출력으로 저장·합성은 아직 없다.
로컬 호스트/daemon 침해, 독립 UTC 정확도, NTP 인증/서버 신뢰성은 이 진단으로 증명할 수 없다.

## 검증 범위

fixture → collector → preflight 및 실제 자식 process의 timeout/출력 상한/실패 비반사를 시험한다.
`--clock-local` 결과의 `boundaryTest: PASS`는 host clock의 PASS와 다르다.
Linux 실제 daemon, 배포 target·다중 node 수집, wizard/report 저장·runtime activation 연동은 후속이다.
[검증 기록](../evidence/clock-diagnostic-summary.md), [pure policy 계약](preflight-policy.md).

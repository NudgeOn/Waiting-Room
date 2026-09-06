# 10K/100K 비용 소계 비교

`wrctl estimate`는 사용자가 제공한 단가로 두 reference profile의 **서버 + 디스크 소계**를
비교한다. 클라우드 견적, 총 운영비, 성능 인증이 아니다. 네트워크/API 호출이나 설치는 없다.

```sh
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/go-mod"
go run ./cmd/wrctl estimate < test/installplan/cost-example.json
make test-unit PRD=06
```

[예제 입력](../../test/installplan/cost-example.json)의 단가는 **테스트용 가상 숫자**이며
실제 provider 가격이 아니다. 예제 출력은 Standard `76.00`, High `888.00`, 포함 소계 배율
`11.6842`다. 소계에 외부 HA 저장소/LB/트래픽 등이 빠져 있으므로 이를 실제 월 견적으로 쓰지 않는다.

## 입력 및 산식

[JSON schema](../../api/schema/cost-input-v1.json)의 모든 필드가 필수다.

- `provider`, `regionId`: 같은 단가 묶음의 소문자 ASCII slug. 실제 provider/리전 존재를 검증하지 않는다.
- `currency`: 대문자 3자 식별자. ISO 목록/환율 조회 없음. 서로 다른 통화/리전의 가격을 섞지 않는다.
- `asOf`: 단가 확인 기준일 `YYYY-MM-DD` (2000~9999). 실제 날짜는 검사하지만 가격의 최신성은 보증하지 않는다.
- `fractionDigits`: 운영자가 명시한 표시 소수 자릿수 0~4. 통화에 맞는 값을 입력한다.
- `monthlyHours`: 컴퓨팅 비용 가정 1~744시간. 기준일의 달 길이를 자동 적용하지 않는다.
- `standardHostHourly`: 4 vCPU/8GiB 서버 **1대**의 시간당 CPU·메모리 묶음 단가.
- `highWorkerHourly`: 8 vCPU/16GiB 워커 **1대**의 시간당 CPU·메모리 묶음 단가.
- `volumeGiBMonthly`: 같은 저장소 등급의 GiB당 월 단가. CPU·메모리 묶음 단가에 디스크 비용을 중복 포함하지 않는다.
- `highWorkerVolumeGiB`: High 워커 1대당 디스크 비용 가정 1~65,536GiB. PRD가 인증한 최소/권장 용량이 아니다.
- `expectedEgressGiB`: 예상 outbound 용량 0~1,000,000,000GiB. 기록만 하고 비용에서 제외한다.

단가는 음수가 아닌 **문자열 소수** `"0.10"`처럼 입력한다. 소수점 앞 최대 9자리, 뒤 최대 6자리.
지수·부호·NaN·Infinity·과도한 precision·숫자형 JSON 단가는 거부한다. `"0"`은 명시적 단가로
허용하지만 필드 누락을 무료로 처리하지 않는다. 숫자형 설정은 지수/소수점 없는 정수 표기를 쓴다.

| Profile | Compute | Volume |
|---|---|---|
| Standard | 1 × monthlyHours × standardHostHourly | 1 × 50GiB × volumeGiBMonthly |
| High | 3 × monthlyHours × highWorkerHourly | 3 × highWorkerVolumeGiB × volumeGiBMonthly |

Standard는 host headroom과 bundled PostgreSQL/Valkey를 포함한 서버 전체 비용이다.
pod CPU/memory planning share를 다시 더하지 않는다. High는 PRD reference 워커 3대만 계산하며
Gateway/Coordinator pod 수 증가를 워커 추가로 오해하지 않는다. 실제 추가 워커는 별도 비용이다.
Volume은 한 달 전체 금액으로 계산하고 `monthlyHours`에 비례해 줄이지 않는다.

## 정밀도와 provenance

Go `big.Int`로 10⁻⁶ 통화 단위를 정확히 계산한다. 각 줄은 `exactAmount`에 6자리 소수로 남기고,
반올림 전 합계를 더한 뒤 최종 소계를 `fractionDigits`로 **half-up** 반올림한다.
비용 배율은 반올림 전 High/Standard 소계로 계산하고 소수 4자리 half-up을 적용한다.
Standard 소계가 0이면 배율은 `null`이며 이유를 출력한다. 0으로 나누거나 임의의 배율을 만들지 않는다.

결과 JSON에 입력 전체·통화·기준일·자원 수량·단가·산식·정확한 금액·반올림 방식·제외 항목을 남긴다.
같은 입력은 같은 bytes를 만든다. 현재 stdout 출력이며 install report의 DB 저장/위자드 표시와는
연결되지 않았다. 정상 입력은 그대로 출력되므로 정상 필드에도 비밀값을 넣지 않는다.
stdin 최대 16KiB, 추가/중복/대소문자가 다른 키·null·배열·trailing document는 거부한다.
오류는 원래 값이나 parser/I/O 오류를 반사하지 않는다. `estimate` 외 인수/비밀을 받지 않는다.

## 제외 항목과 남은 작업

CDN/LB, egress, backup object storage, Prometheus retention, 운영 인력, 세금, IOPS/추가 provider fees는
기본 제외다. High는 external HA stores(저장소/Sentinel 포함), Kubernetes control plane,
워커 3대 초과 비용도 제외한다. 제외 금액을 0으로 추정하지 않는다. 단가에 포함된 세금·할인 등은
자동으로 분해하지 않으므로 동일한 가격 기준으로 입력해야 한다.

계산 결과는 `user-priced-reference-subtotals-only`, `qualification:NOT_RUN`이다.
실제 위자드 연결, provider별 과금 규칙·storage class 비교, 운영 환경 관측, 설치 report 저장,
production manifest/10K·100K qualification은 남아 있다.

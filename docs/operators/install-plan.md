# 설치 전 오프라인 계획

`wrctl plan`은 설치 위자드의 **부분 기반**이다. 표준 입력 JSON을 검사하고 동일 입력에
byte-stable JSON proposal을 출력한다. Docker/Kubernetes/DB/네트워크에 접근하지 않는다.
`setup`, `apply`, 실제 manifest 생성, image digest 확정, 활성화는 아직 없다.
가격 입력을 받는 별도 `wrctl estimate`의 [비용 소계 계산](cost-estimate.md)은 사용할 수 있다.

```sh
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/go-mod"
go run ./cmd/wrctl plan < test/installplan/standard.json
go run ./cmd/wrctl plan < test/installplan/high-scale.json
make test-unit PRD=06
node scripts/run-installplan.mjs my-unique-install-plan-run
```

입력 계약은 [JSON schema](../../api/schema/install-plan-input-v1.json)이다. 이것은 production
config 전체 schema가 아니다. 두 예제는 reference 계획일 뿐 해당 규모 인증이 아니다.
`regionId`는 운영자 지정 ASCII slug 하나이며 provider 리전 존재/지원 여부를 검사하지 않는다.
FIFO만 허용한다. `expectedPeakVisitors`는 한 이벤트의 예상 설치 전체 visitor state이고,
`maxActiveAdmissionLeases`는 READY + 아직 만료/leeway가 끝나지 않은 ADMITTED 한도다.
실제 Room/설치 전체 counter 검사와 activation guard는 별도 미구현이다.

TOTP는 `{"mode":"configurable","enabled":true}`가 권장 예제다. 사용자가 OFF를 선택할 때
`enabled:false`를 명시한다. `forced_on + false`는 거부한다. 누락 시 조용히 OFF로 내리지 않는다.
계획의 정책은 실행 중인 Admin Lab/DB의 정책을 변경하지 않는다.

입력은 최대 16 KiB, 모든 필드 필수, 추가/중복/대소문자가 다른 키·null·복수 document·배열을
거부한다. 정수는 소수점/지수 없는 JSON 정수 표기를 쓴다(일반 JSON Schema의 수학적 integer보다
CLI가 엄격함). 문자열·숫자 값이나 원래 parser/I/O 오류를 stderr에 반사하지 않는다.
유효 입력은 plan에 그대로 포함되므로 **region 등 정상 필드에도 비밀값을 넣지 않는다**.
password, token, TOTP secret, credential URL과 secret reference는 이 단계의 입력 대상이 아니다.
CLI 계산 명령은 `plan`, `estimate`, `report` 명령명 외 인수를 받지 않는다. 비밀을 인수로 전달한 뒤 거부되더라도 OS process list나
shell history 노출을 되돌릴 수 없으므로 절대 입력하지 않는다.

출력은 언제나 `executable:false`, `activationAllowed:false`, `qualification:NOT_RUN`이다.
성공 exit 0은 입력/계획 생성 성공만 의미한다. 입력/명령 오류는 2, 출력 오류는 1이다.
`requiredChecksNotRun`은 검사할 목록이지 관측 결과가 아니다. High Scale 외부 HA stores는
운영자 제공이며 연결 성공도 HA/backup/RPO/RTO 증명이 될 수 없다.
비용은 `NOT_CALCULATED`와 기본 제외 항목만 표시한다. 월 금액·상대 비용·무료라는 추정을 하지 않는다.

별도 [preview UI](installation-preview.md)와 [계획/비용 보고서](planning-report.md)는 로컬 부분 구현됐다.
다음 범위: 환경/secret reference schema, 전체 preflight·pre-scale gates,
production 위자드, 명시적 승인 apply, production Compose/Helm와 동일 image qualification.

후속 부분 기반으로 [preflight 판정 라이브러리](preflight-policy.md)가 있다. 관측 fixture 판정만
구현됐으며 위 `wrctl plan` 명령이 실제 환경 검사를 수행하는 것은 아니다.
명시적으로 실행하는 별도 `wrctl doctor-clock`은 [Linux 로컬 chrony 진단](clock-diagnostic.md)을 제공한다.

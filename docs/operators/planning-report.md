# 계획 보고서 JSON 내보내기

보고서는 **설치 전 계획 기록**이다. 설치 완료 보고서, config backup, 서명된 manifest나
activation 허가가 아니며 파일을 import/apply하는 기능은 제공하지 않는다.

## UI 및 CLI

`make preview` 후 계획 확인 화면에서 **계획 JSON 다운로드**, 비용 계산 후에는
**계획·비용 JSON 다운로드**를 누른다. 파일명은 `waiting-room-planning-report.json`이다.
입력을 변경하면 이전 결과와 내보내기 버튼을 제거하며, 다시 검증/계산해야 새 보고서를 받을 수 있다.
파일 저장 위치/중복 이름 처리는 브라우저 설정에 따른다. UI의 다운로드 요청 문구는 사용자가
OS 저장 대화상자를 완료했다는 보증이 아니다.

```sh
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/go-mod"
go run ./cmd/wrctl report < test/installplan/report-example.json
```

CLI는 stdout만 사용한다. 예제의 provider·단가는 가상 값이며 실제 견적이 아니다.
입력 schema는 [report-input-v1](../../api/schema/report-input-v1.json), 중첩 schema는
[계획 입력](../../api/schema/install-plan-input-v1.json)과 [비용 입력](../../api/schema/cost-input-v1.json)이다.
루트에 `schemaVersion:1`, `plan`이 필수이며 `cost`는 생략 가능하다. `cost:null`은 받지 않는다.
계획·비용의 `regionId`는 같아야 한다. JSON Schema의 정적 `$ref` 외에 Go에서 동등성을 검사한다.

POST `/preview/report` 또는 CLI는 **입력만** 받는다. 클라이언트가 계산한 소계, 미리 만들어진
보고서, 성공/활성화 주장, 추가/중복/대소문자가 다른 키, null, secret 필드는 거부한다.
서버는 기존 계획·비용 validator와 calculator를 다시 실행한 뒤 보고서를 만든다.
HTTP의 Host/Origin/loopback/credential 거부와 16KiB body 경계는 기존 미리보기와 동일하다.

## 결과 계약

- `schemaVersion:1`, `payload`, `sha256`, `hashEncoding`
- payload에 계획 전체와 `planDigest`, 선택적 비용 계산 전체(입력·산식·정확한 소계·반올림·제외 항목)를 보관
- 비용 생략 시 `cost:null`, `costStatus:NOT_REQUESTED`; 0원/무료로 추정하지 않음
- 비용 포함 시 `costStatus:REFERENCE_SUBTOTALS_ONLY`
- 항상 `installation:NOT_RUN`, `activationAllowed:false`, `qualification:NOT_RUN`
- 계획 안에 `requiredChecksNotRun`을 보관하며 실제 환경 관측이 있었다고 표현하지 않음
- 같은 검증 입력에는 byte-stable 결과. 암묵적 현재 시각·random ID는 넣지 않음

`sha256`은 Go `encoding/json`의 compact payload bytes(Go struct 출력 필드 순서, HTML escaping ON,
UTF-8, 마지막 개행 없음)의 SHA-256이다. RFC 8785 canonical JSON은 아니다. pretty-printed 파일
전체의 hash와 다르다. 다른 언어에서 검증하려면 필드 순서를 유지하고 `<`, `>`, `&`, U+2028,
U+2029를 Go와 동일하게 escape해야 한다. 변경 확인용으로만 사용하며 **전자서명·진위 증명·권한이 아니다**.

정상 입력값은 보고서에 남으므로 region/provider 등 정상 필드에도 비밀을 넣지 않는다.
서버/DB/browser storage에 자동 저장하지 않지만 **다운로드한 파일은 사용자가 보관하는 로컬 파일**이다.
UI는 Blob URL과 고정 파일명을 사용하고 다운로드 요청 뒤 임시 URL을 해제한다.
운영자 report persistence, 실제 설치 결과/이미지 identity/관측 서명과 재개·복원은 후속 작업이다.

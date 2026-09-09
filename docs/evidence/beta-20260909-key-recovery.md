# 키 회전 복구 대기 — 2026-09-09

로컬 Docker Beta 후속 검증이다. 공개 Beta GO 판정은 별도다.

## 보존된 실패의 원인 근거

[이전 실행](beta-20260909-browser-join/latest-key-ack-failure.log)은 Coordinator가
`stage=apply code=unavailable generation=0`을 기록한 뒤 키 ACK 대기에서 종료됐다.
해당 fixture `waiting-room-traffic-test-75c9aebf`의 원본 7개 볼륨을 보존한 상태에서,
Queue 볼륨을 읽기 전용으로 복사했다. 네트워크 없는 별도 Valkey에서 recovery 호출 없이
허용한 메타데이터 필드만 읽었다. 자격 증명과 대기표 내용은 증거에 포함하지 않았다.

[원본 메타데이터](beta-20260909-key-recovery/preserved-failure-metadata.json):

| 필드 | 관측 |
| --- | --- |
| mode / reason | RECOVERY_HOLD / uncertain_write |
| 안전 대기 시작 | 2026-09-09 05:07:20.463 UTC |
| 안전 대기 종료 | 2026-09-09 05:09:50.463 UTC |
| 발급 수명 최댓값 / 여유 | 120초 / 30초 |
| 첫 apply 실패 로그 | 2026-09-09 05:07:35 UTC |
| fence / epoch / validationError | 2 / 1 / 비어 있음 |

실제 공유 상태가 150초 안전 대기였다. `runtimeCall`은 이 상태의 Configure를 거부하며,
Coordinator는 적용 전 새 키 ACK를 보내지 않는다. 기존 `wrctl`의 30초 ACK 제한과
회귀 검사기의 짧은 polling은 이 정상 복구 기간보다 짧았다. `uncertain_write` 기록은
확인했지만 결과가 유실된 개별 쓰기는 기록에 없다. 종료 시 context 취소가 유발했다는
설명은 코드에 근거한 가설이며 개별 명령의 캡처 증거로 확대하지 않는다.

## 수정과 실제 재시도

`wrctl keys-*`는 기존 기동 준비 상태 검사를 먼저 수행하고, 준비 완료 뒤 30초 키 ACK
제한을 시작한다. Coordinator의 최대 복구 대기, 다른 역할의 180초 기동 제한, 종료 상태
거부와 취소 동작을 재사용한다. 안전 시간·대기열 함수·입장 허용 조건은 변경하지 않았다.
Traffic/HTTPS/백업 회귀 검사기도 역할 준비와 키 ACK를 별도로 확인한다.
키 상태 조회 subprocess에도 30초 context를 전달해 조회 자체가 멈춰도 대기 예산을 지킨다.

- [수정 전 회귀](beta-20260909-key-recovery/before-fix-regression.log): Git HEAD의 기존
  `rotation.go`를 Go overlay로 사용하면 150초 복구 시나리오에서 실패한다. 실제 작업 파일을
  되돌리거나 안전 시계를 변경하지 않았다.
- [수정 후 단위/race](beta-20260909-key-recovery/key-rotation-unit.log): 정상 회전·부족한 ACK·
  기존 signer·폐기 TTL과 150초 복구·대기 취소·종료된 Coordinator·준비 후 키 ACK 부재·조회 정체를 검증했다.
  단위 테스트의 시간은 `testing/synctest`를 사용하며 실제 Docker 대기와 구분한다.
- [실패 복사본 실제 재시도](beta-20260909-key-recovery/preserved-copy-retry.log): 7개 볼륨을
  원본 읽기 전용으로 복사한 `waiting-room-key-recovery-test-0b68d6fa`에서 수정된 installer
  엔진을 실행했다. 기존 staged generation 1과 digest가 그대로이며 **161.5초 후 양 역할 ACK**다.
  복사된 Valkey의 새 primary로 다시 안전 대기를 거쳤다. 원본 시계를 재현한 결과는 아니다.
- [Coordinator 로그](beta-20260909-key-recovery/preserved-copy-node.log): 06:23:55 apply pending,
  06:26:26 generation 6 recovered. 외부 수동 재시작 없이 같은 실행에서 회복했다.
- 전체 `make check` PASS: Go race/vet, 운영 도구·UI 단위, OpenAPI 및 설치 스키마/문서 검사.

복사본 런타임은 [고정 Go 1.26.8 이미지](beta-20260909-browser-join/patched-identity.json)를
사용했다. 이번 제품 코드 변경은 호스트 `wrctl`의 installer뿐이다. 이미지의 Control/Node/UI와
대기열 복구 코드는 변경하지 않았다. 시험 종료 후 이번 전용 컨테이너·네트워크만 정리하고
원본과 복사본 볼륨을 모두 보존했다.

## 별도 실행 경계

신규 Traffic 회귀의 첫 기동은 Docker 주소 풀 고갈로 애플리케이션 시작 전 실패했다.
[진단](beta-20260909-key-recovery/traffic-startup-address-pool.log)을 보존했다. 완료된 이번 작업의
전용 네트워크를 정리했으며 기존 사용자 설치, 전역 Docker 설정과 데이터 볼륨은 변경하지 않았다.

주소 풀 정리 후 [신규 설치 재검사](beta-20260909-key-recovery/traffic-keys.log)는 PASS다.
실제 calibration/apply·설정 재시작 보존, Control 중단 후 새 generation ACK, Quick 20/Smoke 1K,
Room 검증 탭의 실제 실행, key stage/activate 양 ACK, PostgreSQL/Control 재시작 후 결과·감사를
확인했다. 최초 Docker 환경 실패를 제품 검증 PASS로 바꾸지 않았다.

[최신 Go 1.26.8 이미지의 전체 epoch](beta-20260909-key-recovery/patched-epoch-full.log)도 PASS다.
`waiting-room-epoch-test-977f678a`에서 실제 안전 기한 07:10:56.636 UTC까지 60분 30초와
121회 관측을 완료했다. bounded validation → HOLD → 명시적 AUTO → epoch 2 claim →
실제 mTLS 원본 서버 도달을 확인했다. [이미지 식별 정보](beta-20260909-browser-join/patched-identity.json)에
고정 image ID, 두 바이너리 hash와 해당 실행 결과를 연결했다. [전체 검사 로그](beta-20260909-key-recovery/full-check.log)도 보존한다.

VoiceOver 제어 도구는 시간 초과로 기능 검증을 완료하지 못했다. 이후 2026-09-09 사용자가
"voice over는 넘어가"라고 지시해 **이번 Beta의 VoiceOver 항목은 제외**했다. 검증 통과나
모든 보조기기의 사용성 인증으로 기록하지 않는다. 이 항목은 더 이상 이번 Beta의 미완료 gate가 아니다.

남은 근거 부족은 과거 v2 5K HTTP 503의 최초 problem code/상태와 이전 Admin epoch ACK 지연의
개별 원인이다. 현재 재실행 PASS 또는 이번 별도 키 회전 원인으로 같은 문제라고 단정하지 않는다.

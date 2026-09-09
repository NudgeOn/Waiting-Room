# 로컬 Docker 키 회전

현재 소스의 `wrctl`과 같은 소스로 빌드한 런타임에 적용한다. 공개 Preview 바이너리에는
이 명령이 없다. `keys-*`는 Docker 배포 소유자의 명령이며 Admin API의 사용자 역할을
추가하지 않는다. 관리되는 단일 Control·Coordinator·Gateway 설치만 지원한다.

## 정상 회전

```sh
wrctl keys-stage --directory /private/install
wrctl keys-status --directory /private/install
wrctl keys-activate --directory /private/install
# keys-status의 retireAfter 이후
wrctl keys-retire --directory /private/install
```

`keys-stage`는 새로운 Config/Admission Ed25519, 공개 재시도/복귀 AES-256-GCM 키를
생성하고 역할별 파일에 배포한다. Control에는 Config 개인 키만, Coordinator에는
Admission 개인 키와 재시도 키만, Gateway에는 복귀 키만 전달한다. DB에는 키 값이
없으며 공개 키·세대·단계의 digest와 mTLS로 확인된 두 노드의 ACK만 기록한다.

각 변경은 세 application role을 잠시 정지한다. stage ACK 두 건이 확인돼야 activate가
가능하다. 이전 signer가 정지한 뒤 DB 시간과 30초 여유로 전환 시점을 기록한다.
Coordinator는 티켓의 변경되지 않는 승격 시각으로 서명 키를 선택하므로 회전 전 claim의
재시도도 같은 token bytes다. Gateway는 새 키와 이전 키로 서명·복귀 정보를 검증한다.
새 암호문은 key ID, Room, epoch와 목적을 인증한다. 기존 v1 암호문은 overlap 동안 읽는다.

종료 중 결과를 확인하지 못한 대기열 쓰기가 있으면 기존 데이터의 `RECOVERY_HOLD`가
재기동 후 설정 적용과 새 키 ACK를 보류한다. 예를 들어 발급 가능한 lease/READY 수명의
최댓값이 120초이면 30초 여유를 더한 150초를 실제로 기다린다. `wrctl`은 먼저 모든
서비스의 준비 상태를 확인하고, 그 뒤 최대 30초 동안 두 키 ACK를 확인한다. Coordinator의
복구 준비 대기는 최대 약 62분이며 다른 서비스의 기동 제한은 180초다. 종료된 서비스는
즉시 실패한다. 이 대기는 안전 시간을 줄이거나 실패한 데이터 검증을 건너뛰지 않는다.

Ctrl-C는 대기를 취소하며 기록된 키 변경과 데이터를 보존한다. `status`와 `keys-status`로
상태를 확인하고 같은 명령을 재시도한다. ACK가 부족한 상태에서 다음 단계로 진행하지 않는다.
[실패 데이터 복사본 재시도 기록](../evidence/beta-20260909-key-recovery.md)은 30초 조기 실패를
재현하고 수정 후 실제 161.5초 대기와 동일 키 세대의 두 ACK를 확인했다.

정상 폐기는 전환 시점으로부터 **24시간 30초**와 현재 세대의 두 ACK를 모두 요구한다.
가장 긴 24시간 ticket/config TTL을 보존하는 보수적인 공통 대기다. 시계를 앞당기거나
키 파일을 직접 편집해 이 조건을 건너뛰지 않는다. 폐기 후 이전 키의 암호문은 거부된다.

`keys-status`는 `phase`, `generation`, `digest`, `acknowledged`, `activateAt`,
`retireAfter`, `emergencyReady`만 반환한다. 시간은 Unix milliseconds다. 세대별
`keys.stage`, `keys.activate`, `keys.retire` 감사는 한 건만 기록한다. 파일 배포나 commit
응답이 중단됐으면 같은 명령을 재시도한다. private journal이 먼저 보존되므로 새 키나
전환 시점을 다시 만들지 않는다. 실패 후 서비스가 정지된 경우에도 같은 명령으로 복구한다.

## 긴급 폐기

정상 stage/activate가 완료된 상태에서 Admin이 관리자 화면에서 설치 전체 **새 epoch**를
실행한다. 기존 action-bound 재인증·generation·revision·감사 검증을 통과해야 한다.
두 노드가 새 epoch를 ACK하고 `keys-status`의 `emergencyReady`가 true이면 다음을 실행한다.

```sh
wrctl keys-revoke --directory /private/install
```

이 명령은 활성화 이후 최근 5분 안에 기록된 Admin의 새 epoch 감사와 두 ACK를 DB에서
다시 확인하고 이전 키를 폐기한다. 단순히 Docker 소유자라는 이유로 안전 대기를 생략하지
않는다. 이미 새 epoch가 무효화한 방문자는 다시 줄을 서야 하며 **RECOVERY_HOLD의 실제
최소 60분 30초는 유지**된다. 키 ACK 완료는 대기열 복구 완료와 다른 상태다.

이 명령은 방문자·설정 키의 회전이다. TOTP 저장 master, 비밀번호 fingerprint 키, TLS CA와
DB/Valkey 비밀번호 교체를 의미하지 않는다. 다중 replica/HA 회전과 외부 secret manager는
후속 배포 범위다. 최초 구형 설치의 파생 키는 첫 회전 후 임의 생성 키로 대체된다.

## 백업·복원

키 회전 journal은 serving role이 mount하지 않는 identities 볼륨 루트의 0600 파일이다.
[7개 볼륨 콜드 백업](recovery-upgrade.md)에 역할별 키·journal·DB ACK가 함께 포함된다.
복원할 때도 기록된 키 세대와 원래 `retireAfter`를 사용하며 overlap을 새로 시작하지 않는다.
이전 백업은 이전 개인 키를 포함할 수 있으므로 현재 serving keyring에서의 폐기와 백업
매체에서의 삭제는 구분한다. 서로 다른 복원본을 동시에 운영하지 않는다.

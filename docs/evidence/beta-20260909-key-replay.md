# 2026-09-09 로컬 Beta 후보: 키 회전과 원래 join 응답

대상은 로컬 Docker, 단일 리전 FIFO, 고정 한 개씩의 Control/Coordinator/Gateway다.
MAIN의 M4 10K qualification, M5 HA/100K, M6 GA 판정과 구분한다.

## 구현

- `wrctl keys-stage/status/activate/retire/revoke`: 임의 생성 역할별 키, 실제 mTLS ACK와
  세대 digest, 정지된 signer의 전환 시점, 기존 입장권의 동일 claim, 24시간 30초 overlap.
- 긴급 폐기는 최근 action-bound Admin 새 epoch 감사와 양 노드 ACK를 요구한다.
  키 폐기 때문에 epoch 복구 대기를 줄이지 않는다. [운영 절차](../operators/key-rotation.md).
- 새 **함수 ABI v6**는 저장 schema 5를 사용한다. 공개된 v5 Lua는 변경하지 않는다.
  기존 key/index를 그대로 읽고 새 idempotency row에 최초 ticket metadata와 idle TTL을
  원자 보존한다. 응답의 token은 계속 AEAD로 암호화한다. 만료 뒤에도 보존 기간 안에서는
  원래 응답을 돌려주고, 실제 ticket/status/claim을 되살리지 않는다.
- 이전 v3/v4/v5가 이미 지운 ticket에 대해 최초 응답 metadata가 없는 경우는 복구해서
  만들어내지 않는다. 그 구형 row는 기존 410 경계를 유지한다. 새 v6 row의 전체 retention
  재생 및 살아 있는 구형 ticket의 이행은 별도로 검증한다.
- 관리자 브라우저 CI 작업에 빠졌던 전용 Valkey를 추가했다. 이전 CI의 실패는 세 엔진
  모두 Traffic Lab fixture가 시작하지 못한 것으로, DB·Valkey 서비스가 있는 로컬 회귀는 통과했다.
- cross-Room bounded sweep 시험은 400건 작성 중 500ms TTL이 먼저 만료될 수 있었다.
  30초 setup TTL과 마지막 Valkey deadline까지의 실제 대기로 바꿨다. 만료·capacity·128건
  sweep 조건을 완화하지 않았다.

## 확인된 검증

- 관리자 Chromium/Firefox/WebKit 21개: PASS, 2.4분.
- PostgreSQL 전체 인증/운영 race: pgstore 259.782초, sessionhttp 5.236초,
  authhttp 75.429초 PASS. 새 키 ACK·오래된 ACK 거부·서명 키 즉시 refresh 포함.
- Valkey 전체 store 회귀: 수정된 fixture로 108.001초 PASS. 별도 Lab의 실제 60초 token
  만료 + 30초 여유 여정과 public guard 회귀도 통과했다.
- 실제 Docker의 stage/activate·동일 명령 재시도·감사 1건·이전/새 키 입장·join/claim/복귀
  보존·조기 retire 거부·Admin 새 epoch 뒤 revoke·이전 return 거부·epoch HOLD 유지: PASS.
- 과거 v2 process-lab의 5K 구간을 50회 race 반복: PASS, 163.467초. 250,000 join,
  250,000 status, 25,000 join retry다. 당시 503의 원인을 확인한 결과는 아니며
  현재 v6 배포 이미지의 10K 성능 qualification으로 사용하지 않는다.

## 고정 이미지와 복원 결과

검증 이미지 `waiting-room-keys-beta-final:local`의 ID:
`sha256:2f86f7ad8884451269bb7f80c1137a8eb9211b371a4b6753c302156eae4ab520`.

- [공개 HTTP·키 회전·긴급 폐기](beta-20260909-key-replay/public-runtime.log): PASS.
- [세 차례 콜드 백업/새 설치 복원](beta-20260909-key-replay/backup-runtime.log): PASS.
  구형 v4 데이터 이행 뒤 회전된 키와 새/이전 재시도 응답, FIFO 및 origin 도달을
  빈 새 볼륨에서 확인했다. 실제 primary 안전 대기를 거쳤으며 회전 deadline과 양 ACK를 보존했다.
- [만료 후 실제 HTTP 재시도](beta-20260909-key-replay/expired-http.log),
  [전체 Valkey store](beta-20260909-key-replay/valkey.log),
  [PostgreSQL 키 ACK](beta-20260909-key-replay/key-ack-postgres.log),
  [회전 단위 회귀](beta-20260909-key-replay/rotation-unit.log): PASS.
- 최종 Go 변경을 포함한 `make check`: PASS.

## 운영 회귀에서 발견한 미해결 적용 지연

같은 이미지의 별도 전체 운영 시험에서 Setup/실제 Quick 20·Smoke 1K/역할별 API/
Chromium Admin 9개 화면/320px/axe/응답 유실 후 사용자 생성 재시도는 통과했다.
이어 Admin UI 새 epoch는 DB에서 `accepted`였지만 30초 안에 양 노드 ACK가 갱신되지
않았다. 당시 delivery 55에 대해 Gateway/Coordinator는 모두 54, epoch 1에 머물렀다.
재시작 후 56/epoch 2/RECOVERY_HOLD를 확인했으나 재시작을 원인 해결로 보지 않는다.
[최초 실패 기록](beta-20260909-key-replay/operations-first-failure.log)을 보존했다.
새 fixture `waiting-room-traffic-test-a2491b18` 반복은 전체 PASS였다:
[81개 실제 화면·32개 API 계약·새 epoch·재시작](beta-20260909-key-replay/operations-repeat.log).
반복 PASS는 최초 실패의 원인 규명을 대체하지 않는다.

후속 진단은 config fetch/verify/current/apply/metrics/ACK 단계와 안전한 고정 오류 코드,
노드/세대만 출력한다. 동일 실패는 분당 한 번, 회복은 한 번 기록하며 원본 오류/URL/
credential/서명 payload는 남기지 않는다. 이 진단 추가는 위 고정 이미지 다음 소스 변경이다.
회귀 fixture는 실패 시 해당 진단만 추출해 컨테이너 삭제 전 보존한다.
구현 커밋 `199d52e`는 origin/main으로 푸시했으며
[CI](https://github.com/NudgeOn/Waiting-Room/actions/runs/34307033392)의 foundation과 PostgreSQL/브라우저는 PASS였다.
Valkey는 stale-fence 시험이 고정한 `wr_r5_command`가 새 CI 환경에 없어 실패했다.
로컬에는 이전 v5 함수가 남아 있어서 이 오류를 가렸다. 시험이 선택한 ABI 함수를
호출하도록 수정했고, 빈 Valkey에서 stale fence/손상 복구 차단과 원래 응답 재생을
재검증했다: [빈 환경 결과](beta-20260909-key-replay/clean-abi.log).
수정 후 원격 CI는 다음 커밋에서 다시 실행한다.

## 판정

**Beta NO-GO 유지.** 새 epoch ACK 지연, 쿠키 없는 최초 browser 응답 유실/동시 최초 탭,
원인 근거가 없는 과거 503 및 전체 acceptance를 닫기 전 공개 Beta로 표시하지 않는다.
이번 커밋은 기능과 재현/진단 증거를 보존하는 개발 커밋이다. 공개 release/tag는 만들지 않는다.

## 마지막 진단 이미지 회귀

진단만 추가한 `waiting-room-keys-beta-diagnostics:local` 이미지 ID는
`sha256:3085a23825a642f0f2f135ce084ef5cf1dff5e4045c60e294383b83d0af1b261`이다.
[새 fixture 전체 운영 회귀](beta-20260909-key-replay/operations-diagnostics.log)는 PASS:
실제 Setup, Quick 20/Smoke 1K, 키 stage/activate, Admin 새 epoch와 두 ACK,
3개 엔진 × 3개 역할 × 9개 화면, 320px reflow, axe, 키보드/재인증 응답 유실 재시도,
32개 API 계약, DB/Control 재시작 뒤 결과와 감사 보존을 확인했다.
이 소스의 `make check`와 runtimeplane race 1.556초도 PASS다.
콜드 복원은 앞서 명시한 코어 이미지 결과이며, 진단 이미지에서 재실행했다고 주장하지 않는다.
첫 새 epoch ACK 실패는 재현되지 않아 원인 미확정 상태를 유지한다.

# 읽기 연결 복구와 공개 제한 함수 업그레이드 — 2026-09-09

로컬 Docker M1~M3 후보의 후속 수정이다. 검증 시간은 2026-09-09 14:19 UTC부터다.
VoiceOver는 사용자 요청으로 제외했다. 실제 보조기기 검증 PASS를 뜻하지 않는다.

## 재현한 읽기 장애

기존 `Store.call`은 `INFO server` 또는 `FCALL_RO`의 응답 유실에도 로컬 실패 상태를
영구 설정했다. v6 `runtimeCall`은 같은 읽기 장애를 불확실한 쓰기로 처리해 설치 전체에
`RECOVERY_HOLD`를 만들었다. 한 방문자의 실패가 이후 정상 요청까지 차단할 수 있었다.

시험은 실제 TCP 연결에서 지정한 명령을 Valkey에 전달하고 실제 응답을 받은 뒤 소켓을
닫는다. 결과 객체, 서버 시각, 대기열 기록이나 fence는 대체하지 않았다.

- [수정 전](beta-20260909-read-recovery/read-before.log): legacy primary/status 및
  v6 status/metrics의 네 경로가 모두 실패했다. 쓰기 응답 유실의 안전 대기는 유지됐다.
- [수정 후 10회](beta-20260909-read-recovery/read-after-repeat.log): 읽기 요청 자체는
  실패하지만 같은 client가 재접속해 기존 대기표와 다음 FIFO 순서를 유지한다.
- [안전 경계 3회](beta-20260909-read-recovery/read-safety.log): 실제 legacy/v6 쓰기
  응답 유실은 계속 입장을 차단한다. 잘못된 서버 자료형에 대한 읽기 오류도 계속 차단한다.
  transport 오류만 예외이며 schema, invariant, malformed response와 fencing 검사는 유지한다.
- 실제 Gateway → Coordinator → Valkey HTTP 여정도
  [수정 전 지속 503](beta-20260909-read-recovery/http-before.log)과
  [수정 후 재시도](beta-20260909-read-recovery/http-after.log)를 비교했다.
  `QUEUE_UNAVAILABLE` 뒤 3초 후 같은 프로세스에서 재접속하고, 원래 join 응답·FIFO를
  보존해 claim 및 보호된 원본 접속까지 통과했다. 이 단위 환경의 HTTP는 로컬 평문이며
  아래 Docker HTTPS/mTLS 검증과 구분한다.

수정 커밋은 `41f70ed`다. 읽기 transport 실패를 제한된 오류 타입으로 구분한다.
실제 쓰기의 결과가 불확실할 때는 기존 공유 fence와 안전 대기를 그대로 적용한다.

## 재현한 함수 버전 충돌

[첫 전체 검사](beta-20260909-read-recovery/integration-first.log)에서 대기열과 HTTP Lab은
통과했지만 public guard 다섯 시험은 함수 설치 단계에서 실패했다. 전용 시험 Valkey의
`wr_public_guard_v1`과 추적된 소스가 같은 이름으로 다른 코드를 가지고 있었다.
[해시와 차이](beta-20260909-read-recovery/guard-mismatch.json)를 보존했다. 저장된 버전은
기존 join 재시도에 요청 카운터를 적용하지 않았고, 추적된 버전은 기존 요청 예산을 적용한다.

기존 함수를 삭제하거나 `FUNCTION LOAD REPLACE`하지 않았다. `f320f91`은 현재 제한
동작을 새 ABI `wr_public_guard_v2`/`wr_pg2_check`로 설치하고 새 Coordinator가 그 본문만
검증하도록 한다. 저장 schema 1과 key 형식은 유지한다. 기존 v1 함수와 데이터는 남긴다.

[실제 업그레이드 검사](beta-20260909-read-recovery/guard-upgrade.log)는 이전 함수 본문과
세 key의 직렬화 바이트가 두 번의 설치 및 Open 후에도 같은지 확인한다. 이후 v2에서
기존 poll deadline과 join 기록을 재사용하고 출처 예산·메타데이터 상한·재접속 분산을
검증했다. 기존 FIFO 대기열 함수의 공개된 ABI는 수정하지 않았다.

## 고정 후보와 검증

이미지 `waiting-room-beta6-read-recovery:local`은 소스
`f320f911f7ee6cbee7104d2ae70fc2bfc5e007c1`에서 빌드했다.
이미지 ID는 `sha256:16cb62abdd79e5021e21261b7822cd0e0dcbb2781ff1916bc1469ebc34b8d7cc`다.
[실제 Linux/arm64 바이너리](beta-20260909-read-recovery/image-identity.json)는 Go 1.26.8,
동일 vcs revision 및 `vcs.modified=false`이며 각각 SHA-256을 기록했다.

- [전체 소스 검사](beta-20260909-read-recovery/full-check.log): `make check` PASS.
- [구형 HTTP 4프로세스 경계](beta-20260909-read-recovery/http-tiers.log): 1K/2K/5K/10K
  join·status·retry PASS, 9.697초. 지속 부하 qualification 결과는 아니다.
- [전체 integration](beta-20260909-read-recovery/integration-final.log): Valkey 100.966초,
  HTTP Lab 99.730초, public guard 9.536초, Traffic 엔진 3.847초 PASS.
- [고정 커밋 CI](https://github.com/NudgeOn/Waiting-Room/actions/runs/34364985197):
  foundation, dependency-security, Valkey integration, PostgreSQL/브라우저 모두 PASS.
- [실제 운영 회귀](beta-20260909-read-recovery/traffic-verified.log): Quick 20/Smoke 1K,
  Control 6초 중단 및 재시작 없는 새 설정 ACK, 키 stage/activate 및 양 ACK,
  Admin 새 epoch와 안전 대기, 세 엔진 × 세 역할 × 9개 화면, 320px/axe/키보드,
  32개 API 계약, PostgreSQL/Control 재시작 후 보고서·명령·감사 보존 PASS.
- 실제 이미지에서 추출한 두 실행 파일도 빌드 SHA-256과 일치한다.
  [Control 검사](beta-20260909-read-recovery/control-vulnerability.log)와
  [Node 상세 검사](beta-20260909-read-recovery/node-vulnerability-verbose.log)는 영향 있는
  symbol/package가 0건이다. 모듈 수준 `GO-2026-5932`는 사용하지 않는 `x/crypto/openpgp`
  경고이며 원문 결과를 보존했다. 모듈 경고까지 0건이라고 표시하지 않는다.
- 동일 고정 이미지의 실제 60분 30초 epoch와 백업 복원은 별도 실행 중이다.

## 검증 도구와 환경의 실패 보존

Traffic 시험의 두 초기 실행은 Compose 기동에서 실패했다.
[첫 실행](beta-20260909-read-recovery/traffic-startup-first.log)과
[두 번째](beta-20260909-read-recovery/traffic-startup-second.log)는 앱 검증 PASS가 아니다.
stderr가 숨겨져 있어 이 기록만으로 정확한 기동 오류를 확정하지 않는다. 로컬 Docker에
사흘 전 종료된 시험 프로젝트의 빈 네트워크가 남아 있어
[연결 0개 확인](beta-20260909-read-recovery/unused-networks.json) 후 여섯 개만 제거했다.
중지 컨테이너와 모든 볼륨, 실행 중 사용자 설치와 별도 epoch 환경은 보존했다.
그 다음 새 fixture는 같은 후보로 기동했다.

그 실행의 Firefox 두 번째 역할은 화면/키보드 검사를 끝낸 뒤 정리 단계에서 3분 이상
멈췄다. [미완료 로그](beta-20260909-read-recovery/traffic-teardown-incomplete.log)와
[정리 범위](beta-20260909-read-recovery/traffic-teardown.json)를 보존하고 전체 PASS로
처리하지 않았다. Playwright의 `unrouteAll({behavior:'wait'})`는 진행 중 라우트 완료를
무기한 기다릴 수 있다. 검사 도구는 모든 화면 오류를 먼저 검증한 뒤 정리 중인 콜백만
`ignoreErrors`로 종료하고 context를 닫도록 바꿨다. 제품 API/화면 검증은 제거하지 않았다.
동일 고정 이미지의 새 fixture `waiting-room-traffic-test-82a72253`은 아홉 context를 모두
종료했고 위 전체 운영 회귀를 통과했다. 제품의 VoiceOver 검증과는 무관하다.

## 판정의 경계

### 추가 종료 수정

정상 종료 중 HTTP 서버가 active handler보다 먼저 반환하는 결함을
[실제 TLS 시험](beta-20260909-read-recovery/http-shutdown-before.log)으로 재현했다.
백그라운드 승격도 종료 취소가 이미 전달된 쓰기 응답을 버려 불필요한 안전 대기를 만들었다.
[실제 pipeline 수정 전](beta-20260909-read-recovery/promotion-shutdown-before.log)과
[수정 후 3회](beta-20260909-read-recovery/shutdown-after.log)를 비교했다.
정상 종료는 완료 응답을 기다리지만 실제 1초 쓰기 deadline은 계속 uncertain-write hold다.

Serve는 최대 12초 HTTP drain을 기다린다. Node는 새 작업 예약을 멈추고 진행 중인 sync는
12초, recovery/promote는 기존 1초 예산 내에서 끝낸다. 새 Compose와 기존 설치의 CLI
stop 명령은 20초 유예를 사용한다. 저장된 이전 Compose 파일의 checksum은 변경하지 않는다.
종료 시 입장권 유효 시간·공유 fence·unsafeUntil·검증 조건을 완화하지 않았다.
이 후속의 [전체 검사](beta-20260909-read-recovery/shutdown-full-check.log) 및
[최종 runtime/installer/CLI race](beta-20260909-read-recovery/shutdown-final-unit.log)는 PASS다.
실제 pipeline 종료 회귀는 표준 `make test-integration`과 CI에도 추가했다.
앞의 이미지 결과와 후속 종료 수정의 고정 이미지 결과는 구분한다.

현재 재현한 읽기 연결 유실 및 불변 함수 이름 충돌은 원인과 수정 전후 증거가 있다.
과거 `autonomous-final-20260906/http-visitor-tiers-3.log`의 최초 실패에는 같은 원인인지
확인할 상태가 남아 있지 않다. 당시 5K 503과 이전 Admin epoch ACK 지연의 정확한
원인을 이번 수정으로 입증했다고 주장하지 않는다. **Beta NO-GO를 유지**하며, 남은
수용 판정은 [Beta 계획](../beta-plan.md)을 따른다. 공개 release/tag는 만들지 않았다.

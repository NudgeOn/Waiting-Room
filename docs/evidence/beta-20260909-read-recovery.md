# 읽기 연결 복구와 공개 제한 함수 업그레이드 — 2026-09-09

로컬 Docker M1~M3 후보의 후속 수정이다. 검증 시간은 2026-09-09 14:19 UTC부터다.
VoiceOver는 사용자 요청으로 제외했다. 실제 보조기기 검증 PASS를 뜻하지 않는다.

**현재 판정: Beta NO-GO.** 읽기 복구·함수 업그레이드·정상 종료를 수정하고 여러 운영
회귀를 통과했지만, 8,600 대기표 및 별도 epoch 환경에서 새 스냅샷 차단을 보존했다.
아래의 단기 PASS와 이 미해결 실패를 구분한다.

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

## 첫 고정 후보와 검증

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
- 이 첫 이미지의 장시간 epoch는 아래 후속 수정으로 중지했다. 첫 백업은
  마지막 복원에서 retention 검사가 실패했으므로 전체 PASS가 아니다.

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

## 추가 종료 수정

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

## 고정 시험 이미지

종료 수정을 포함한 소스는 `533150eaf7145954a6447bf9a667a7d12e63f456`이며,
이미지는 `waiting-room-beta6-graceful:local`, ID는
`sha256:51a8655836c06c90f24f167d8d1b1c418cbbad9df417ea2984501fe72bbd9252`다.
[소스·빌드·이미지에서 추출한 바이너리](beta-20260909-read-recovery/graceful-image-identity.json)를
연결했다. Go 1.26.8/Linux arm64, vcs clean이다.

[고정 소스 CI](https://github.com/NudgeOn/Waiting-Room/actions/runs/34369036699)의
foundation·dependency-security·Valkey integration·PostgreSQL/브라우저 네 작업도
모두 PASS다. [조회 결과](beta-20260909-read-recovery/graceful-source-ci.json)에
같은 `headSha`와 완료 시각을 보존했다.

[최종 운영 회귀](beta-20260909-read-recovery/graceful-traffic.log)는 전체 PASS다.
키 stage와 activate는 각각 12,879ms/12,455ms에 양 ACK를 받았고, 정상 종료 전후의
recovery fence가 같은지 추가로 확인했다. 81개 화면·32개 API·Control 중단 회복·
Admin 새 epoch·재시작 보존도 같은 이미지에서 확인했다. 시간은 해당 두 실행의 관측이며
운영 성능 보장은 아니다. [Control](beta-20260909-read-recovery/graceful-control-vulnerability.log)과
[Node](beta-20260909-read-recovery/graceful-node-vulnerability.log)의 binary scan은 영향 있는
호출 경로 0건이다. 사용하지 않는 openpgp 모듈 경고는 앞의 설명과 같다.

앞 이미지의 장시간 epoch는 코드 변경 때문에
[완료 전 중지](beta-20260909-read-recovery/superseded-epoch.log)했으며 전체 PASS가 아니다.
최종 이미지의 안전 시간은 새 fixture에서 2026-09-09 16:18:06.914 UTC까지이며,
실제 전 구간 검증 결과는 완료 후 기록한다. 아래 15:58 스냅샷 차단 때문에 현재 전체 PASS가 아니다.

첫 백업 시험은 키 회전의 안전 대기가 반복되면서 초기 응답의 10분 retention을 넘었다.
[토큰을 제외한 실패 기록](beta-20260909-read-recovery/backup-first-incomplete.log)에 새 join의
만료 시각과 최초 응답 만료 시각 차이를 남겼다. 원래 대기표·idempotency를 시계 편집으로
살리지 않는다. 검사기는 유지 가능한 시점에 실제 HTTP heartbeat를 보내고, 최종 종료
코드에서 다시 실행한다. heartbeat는 idempotency 보존 기간을 연장하지 않는다.

[최종 v4 백업·이행·키 교체 후 복원](beta-20260909-read-recovery/graceful-backup-v4.log)은
전체 PASS다. 7개 중지 볼륨 49,912,832바이트를 검증한 후 새 볼륨으로 복원하고,
실제 schema 4 → 5 이행, 기존 ACL·계정·세션·초안·join 응답 보존을 확인했다.
stage/activate 뒤 세 번째 콜드 복원도 두 역할 ACK, 동일 키 digest와 원래 retirement
deadline, 기존/신규 암호화된 재시도 응답을 보존했다. 마지막에는 첫 대기표가 FIFO 순서대로
입장해 실제 mTLS 원본에 도달했다. 안전 대기나 설정된 TTL을 줄이거나 늘리지 않았다.

[이전 Beta → 최종 이미지](beta-20260909-read-recovery/graceful-backup-v5.log)도 PASS다.
원본 `waiting-room-beta4-patched:local`은 schema 5/public guard v1이며, 50,102,784바이트의
7개 볼륨을 새 프로젝트로 복원한 뒤 최종 이미지로 업그레이드했다. 이미 schema 5인 큐는
`state=current`, `from=5`, `to=5`를 유지하고 계정·세션·초안·exact join 응답·FIFO와 mTLS
원본 도달을 보존했다. v4 이행과 schema 5에서 함수 ABI만 추가하는 업그레이드를 별도로 검증했다.

[최종 공개 HTTPS·브라우저·키 경계](beta-20260909-read-recovery/graceful-public.log)도
전체 PASS다. 최대 2048바이트 query, 모든 cookie와 함께 최초 응답 유실, 키보드 재시도,
동시 최초 5개 탭과 모바일 axe를 실제 Chromium에서 확인했다. 5개 공개 operation의
계약, early poll/출처 quota, 5개 보호 HTTP 메서드, DRAINING 안내도 확인했다.
키 stage/activate의 양 ACK·재시도 감사 한 번·기존 join/admission/return 보존과 새 키의
원본 도달, 24시간 30초 이전 retire 거부, Admin 새 epoch 이후 emergency revoke와
계속되는 RECOVERY_HOLD까지 같은 이미지에서 통과했다. 이 실행은 긴급 폐기 후의
전체 60분 대기를 포함하지 않으며 별도 epoch 실행과 구분한다.

실제 360px [대기 화면](beta-20260909-read-recovery/graceful-waiting-mobile.png)과
[schema 5 복원 후 epoch 검토창](beta-20260909-read-recovery/graceful-epoch-review-mobile.png)을
직접 확인했다. 이번 fixture가 각각 15:41:03/15:39:45 UTC에 생성한 파일이며, 대기 상태,
영향받는 두 대기표·한 Room, 60분 30초 안내와 빈 재인증 입력을 포함한다.

## 이번 실행의 수용 범위

이 표는 `533150e` 고정 시험 이미지 기준이다. 이후 `5941844` 진단 이미지의 범위는 아래에 별도로 기록한다.

| 사용자 요청 | 이 최종 이미지에서 확인한 결과 | 남는 경계 |
|---|---|---|
| 설치 위자드 | 실제 Linux 보정, 검토 후 apply, 설정 기본값·서명 배포·양 ACK, DB/Control 재시작 보존 | 외부 production 설치·환경별 qualification은 별도 |
| Traffic Lab | Quick 20·Smoke 1K의 실제 HTTP, 격리 ACL, UI 실행·저장 결과·감사·재시작 보존 | 운영 원본에 부하를 가한 결과가 아님 |
| 운영·보안 | 3엔진 × 3역할 × 9개 화면, 32개 API, 재시도·감사·320px·키보드·axe | 검사한 상태 조합의 결과이며 VoiceOver는 사용자 요청으로 제외 |
| 백업·업그레이드 | v4 → v5, 기존 schema 5 → 최종 이미지, 교체된 키를 포함한 콜드 복원, 기존 FIFO 원본 도달 | 공개 release digest를 쓰는 배포 CLI 설치/복원은 후속 |
| 읽기 장애·종료 | 소켓/TLS 수정 전 실패·수정 후 회복, 정상 key stop 전후 동일 fence | 과거 로그만 남은 별도 503/ACK 지연의 정확한 원인은 미확정 |
| 새 epoch | 양 ACK 1,024ms 뒤 실제 안전 대기 중 15:58 스냅샷 차단 발생 | 전체 PASS 아님; 마지막 후속 판정은 종료 후 기록 |
| 공개 인원 경계 | 제한을 유지한 1K/2K/5K PASS; 8,600 대기표에서 CONFIG_UNAVAILABLE 503 | 10K FAIL; 지속 부하 qualification과도 구분 |

검사기와 운영 문서 후속은 `fd3f98e`로 커밋·푸시했다. 서빙 코드는 `533150e`와 같으며
위 이미지 태그를 다시 빌드하거나 실행 중인 사용자 설치를 갱신하지 않았다.

PRD 대조 과정에서 `make test-unit PRD=01`이 M0 policy 두 테스트만 선택하던 진입점을
현재 control/waiting 단위/race까지 포함하도록 고쳤다. [실제 실행](beta-20260909-read-recovery/product-policy-unit.log)은
세 package 모두 PASS다. SUB-PRD-01의 route·revision·예약·DRAINING 검증을 고정 소스의
단위/PG/Valkey CI에 연결하고, 이미지 업로드/decode/re-encode 미구현과 비개발 운영자
수용 검증은 미완료로 명시했다. [bearer 경계 ADR](../adr/0004-bearer-identity-boundary.md)은
MAIN의 기존 v1 비범위를 기록하며 계정별 중복 방지를 구현했다고 주장하지 않는다.

로컬 macOS/arm64 CLI는 clean `7dde8dc`에서 `build/beta6/wrctl`로 빌드했다.
[SHA-256·Go build 정보](beta-20260909-read-recovery/local-cli.json),
[실제 help](beta-20260909-read-recovery/local-cli-help.log),
[바이너리 보안 검사](beta-20260909-read-recovery/local-cli-vulnerability.log)를 보존했다.
표시 버전은 `wrctl source`이며 공식 Beta release가 아니다. 실제 실행 가능한 바이너리와
백업·복원·키 명령의 노출을 확인했지만 사용자 설치에 설치하거나 명령을 적용하지 않았다.
영향 있는 호출 경로는 0건이고 미사용 모듈 경고 한 건은 앞의 이미지 검사와 같다.

## 새로 보존한 스냅샷 차단 실패

[공개 인원 검사 원문](beta-20260909-read-recovery/graceful-public-tiers-failed.log)은
1K/2K/5K 전원 status 및 구간별 최근 100개 exact join retry까지 PASS다. 실제 quota를
지키고 26,800회 heartbeat를 성공한 뒤 10K join 구간에서 `CONFIG_UNAVAILABLE` 503으로
실패했다. 실패 진단 시 waiting 8,600, epoch 1, recovery fence 1, mode HOLD였으며
데이터 복구 hold는 없었다. 10K 완료나 최초 일곱 방문자의 입장은 PASS로 처리하지 않는다.

인원 환경의 양 노드는 15:58:35 UTC에 `current/snapshot_unavailable`을 기록했다.
별도 장시간 epoch 환경도 15:58:34 UTC부터 같은 차단을 기록했다. 그 환경에 저장된
[서명 설정 메타데이터](beta-20260909-read-recovery/snapshot-failure-metadata.json)는
generation 13, issued 15:57:38 UTC, expires 다음 날 15:57:38 UTC다. 따라서 관측 시각의
단순 만료로 설명되지 않는다. 기존 진단은 clock rollback과 uncertain persistence 뒤
`Current()` 오류를 모두 `snapshot_unavailable`로 덮어써 최초 원인을 구분하지 못했다.
동시 발생만으로 Docker 시계 역행을 확정하지 않는다. 원본 볼륨과 위 실패 기록은 보존했다.

후속 소스는 Gate의 최초 차단 이유를 고정 코드 `snapshot_clock_rollback`,
`snapshot_persistence`, `snapshot_invalid`로 보존하고 Node 진단에 반영한다.
[configtrust/runtime race 회귀](beta-20260909-read-recovery/trust-diagnostics.log)는 PASS다.
원인 조회나 새 서명 설정이 실패 gate를 다시 열지 않고, 잘못된 수신 서명이 유효한 LKG를
차단하지 않는지도 확인했다. 서명·시계·영속성 보호를 완화하지 않았다.
epoch 검사기도 안전 대기 중 `QUEUE_UNAVAILABLE`을 확인해 관계없는 `CONFIG_UNAVAILABLE`
503을 안전 대기의 성공 관측으로 세지 않도록 보완했다. 기존 실행에 새 검사나 새 진단을
소급 적용하지 않는다. 이 진단 후속은 위 `533150e` 이미지의 시험 결과에 포함되지 않는다.

공개 대기/장애 화면의 자동 검사에 WCAG 2.2 AA `target-size`도 추가했다.
[같은 고정 이미지 재실행](beta-20260909-read-recovery/graceful-public-wcag22.log)은 PASS다.
관리자 81개 화면은 기존부터 WCAG 2.2 AA 태그를 포함했다. 이 공개 재실행은 키 옵션을
생략했으며, 키 경계는 앞의 별도 전체 공개 실행 증거를 따른다.

## 진단 후속 이미지의 별도 검증

소스 `5941844f6cd4509350b7384378f7194b8198ef4f`의 별도 이미지는
`waiting-room-beta6-diagnostics:local`,
`sha256:9e21be80e9e79a01f9f0e490d4ab4c84024cf28655f859ef39e9bd1ca5514b75`다.
[빌드 로그](beta-20260909-read-recovery/diagnostics-image-build.log)와
[Go 1.26.8/Linux arm64·clean revision·바이너리 SHA](beta-20260909-read-recovery/diagnostics-image-identity.json)를 보존했다.

[실제 공개 HTTPS·키 교체·긴급 폐기·WCAG 2.2 AA](beta-20260909-read-recovery/diagnostics-public.log)는
새 fixture `waiting-room-public-test-ada2d3ba`에서 모두 PASS다.
[Control](beta-20260909-read-recovery/diagnostics-control-vulnerability.log)과
[Node](beta-20260909-read-recovery/diagnostics-node-vulnerability.log)의 영향 있는 호출 경로는
0건이며, 미사용 openpgp 모듈 경고 한 건은 앞과 같다.
[후속 전체 source check](beta-20260909-read-recovery/trust-final-check.log)도 PASS다.
이 이미지에서 10K 또는 60분 30초 전체 epoch를 다시 통과한 것은 아니다.
공개 검사도 실패 시 정리 전에 제한된 `runtime_sync` 로그를 보존하도록 보완했다.

앞선 검사 진입점 커밋 `7dde8dc`의
[CI 4개 작업](https://github.com/NudgeOn/Waiting-Room/actions/runs/34372868295)은 모두 PASS이며
[조회 결과](beta-20260909-read-recovery/policy-source-ci.json)를 보존했다.
진단 소스의 CI 상태와 장시간 시험 최종 결과는 종료 시점에 별도로 기록한다.

## 판정의 경계

현재 재현한 읽기 연결 유실 및 불변 함수 이름 충돌은 원인과 수정 전후 증거가 있다.
과거 `autonomous-final-20260906/http-visitor-tiers-3.log`의 최초 실패에는 같은 원인인지
확인할 상태가 남아 있지 않다. 당시 5K 503과 이전 Admin epoch ACK 지연의 정확한
원인을 이번 수정으로 입증했다고 주장하지 않는다. **Beta NO-GO를 유지**하며, 남은
수용 판정은 [Beta 계획](../beta-plan.md)을 따른다. 공개 release/tag는 만들지 않았다.
이번에 직접 보존한 스냅샷 차단도 현재의 차단 항목이다. 진단 보완을 이 장애의 원인 규명이나
복구 완료로 표시하지 않는다.

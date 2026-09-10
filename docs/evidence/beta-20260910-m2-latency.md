# 2026-09-10 — M2 로컬 수용 완료와 503 수정 증거

판정: **M2 로컬 Docker 수용 GO**. 검토자 Codex, 2026-09-10 10:00 UTC.
같은 서비스 이미지 e2cb0b9에서 공개 장애·10K 인원·콜드 복원·전체 epoch·운영·키 검사를
완료했다. **Beta 최종 B6는 NO-GO**다. 비개발 운영자의 실제 사용성 수용은 미실행이며,
이 기술 검증으로 대체하지 않는다. M4/M5 지속 부하·HA와 GA 판정도 별도다.

기준 소스 dc0b7a1 이후 남은 공개 guard deadline을 추가 진단하고 동일 설정의 쓰기 잠금과
확인된 join을 덮던 보조 등록 오류를 수정했다. 수정 전 실패·수정 후 10회 race 회귀와
전체 소스/CI 검사를 확보했다. 기존 설치·실패 볼륨·deadline·quota·AOF always·안전 대기는 보존했다.

| 같은 서비스 이미지의 검증 | 결과 |
| --- | --- |
| 488개 확장 공개 장애 조합 + 72개 기본 조합 | PASS |
| Valkey 일시 중단의 네 API 차단과 기존 대기표 복귀 | PASS, 재시작/epoch 변경 없이 2,594ms |
| 1K/2K/5K/10K 전원 join/status·구간별 exact replay | PASS |
| 실제 콜드 복구와 10K 보존·100개 동일 응답·FIFO 입장 | PASS, 실제 안전 대기 162,541ms |
| 전체 새 epoch 안전 대기·재로그인·HOLD→AUTO→원본 | PASS, 121회 관측 |
| 81개 역할/브라우저 화면·32개 API·Quick 20/Smoke 1K | PASS |
| Control 중단 LKG·키 stage/activate/긴급 폐기 | PASS |
| 기존 schema 5 업그레이드·세 차례 콜드 복원·원래 FIFO | PASS |
| 서비스 커밋과 검사기 커밋의 CI | 각각 네 작업 PASS |

## 단독 진단 재현

진단 이미지 `waiting-room-beta9-guard-diagnostic:local`,
ID `sha256:d9afea21da32d2637d6bec3864d6a1afba7559992cda1cfe7b92676afc8d43d8`는
41e67e1에 제한기 타이밍 계측만 더한 **미커밋 진단 빌드**다. 최종 배포 후보가 아니다.
[빌드](beta-20260910-m2-latency/diagnostic-image-build.log),
[실패 실행](beta-20260910-m2-latency/diagnostic-tiers-failed.log).

- 1K와 2K의 join·전원 status·100개 exact replay가 PASS였다.
- 08:34:40 UTC 최초 guard 실패는 `register`, RPC 353ms, 남은 예산 320ms,
  호출 시작 시 동시 실행 9건이다. join 완료 뒤의 poll 등록에서 발생했다.
- 08:34:41의 join 쓰기는 남은 예산 691ms 중 200ms 뒤 상위 요청이 취소됐고
  불확실 쓰기 안전 차단으로 전환됐다. 최종 waiting 2,391/fence 2,
  generation 5 pending(Coordinator 4/Gateway 5)이다. 5K/10K·콜드 인원은 미통과다.
- 같은 시각 INFO 왕복 1,905.806ms에서 VM CPU 대기 누적 약 962ms,
  I/O some 약 372ms가 증가했다. TCP 재전송은 없었고 major fault/swap-in은 각 1개다.
  이 카운터는 VM 전체와 관측기 TCP 범위이며 특정 syscall 원인으로 단정하지 않는다.
- Valkey PID의 [추가 scheduler 관측](beta-20260910-m2-latency/diagnostic-valkey-scheduler.jsonl)은
  최초 실패 이후에 시작했으므로 최초 실패의 CPU 원인을 증명하지 않는다.

관측기는 숫자 커널 통계와 제한된 INFO/LATENCY 항목만 읽는다. 명령·키·값·자격증명은
출력하지 않는다. 측정 해석은 [Valkey 지연 문서](https://valkey.io/docs/topics/latency-monitor/)와
[Linux scheduler 통계](https://docs.kernel.org/scheduler/sched-stats.html)를 따른다.

## 동일 설정 갱신 잠금 수정

[ADR-0009](../adr/0009-unchanged-config-read-lock.md).
매초 같은 설정을 확인하면서 쓰기 잠금을 요청하는 경로가 새 요청을 막는 문제를
[RED](beta-20260910-m2-latency/refresh-red.log)로 재현했다.
같은 generation은 읽기 잠금만 사용하고 실제 변경은 기존 배타 적용을 유지한다.
동일 설정·잘못된 payload·실제 변경 수명 검사가
[10회 race GREEN](beta-20260910-m2-latency/refresh-green-10x.log)이다.
[전체 make check](beta-20260910-m2-latency/source-check.log)도 PASS다.

최종 고정 이미지의 실제 결과는 아래에 별도 기록한다.

## 확인된 join 응답 보존

[ADR-0010](../adr/0010-confirmed-join-poll-hint.md)은 이미 저장이 확인된 대기표 응답을
보조 조회 일정 등록의 실패로 덮던 문제를 수정한다. 최초 quota 검사와 실제
status/claim/heartbeat 제한은 유지한다. 없는 일정은 공통 status가 분산 등록 후
429로 지연하므로 큐 읽기나 입장을 우회하지 않는다.
[등록 장애/제한의 RED](beta-20260910-m2-latency/registration-red.log),
[10회 race GREEN 및 필수 제한 유지](beta-20260910-m2-latency/registration-green-10x.log).

[등록 응답 수정 후 전체 make check](beta-20260910-m2-latency/registration-source-check.log) PASS.

## 고정 후보와 후속 검증

최종 서비스 소스는 `e2cb0b9cd4b4a0af33b6cf0c9d2d35a6f8ff957a`, 이미지는
`waiting-room-beta9-confirmed-join:local`, ID는
`sha256:9e871ba5a6b48ff57d4be4ce19cdb382455adc8ed4731133636c1ca4b42e1eb4`다.
[빌드](beta-20260910-m2-latency/confirmed-image-build.log)와
[실제 이미지 바이너리 식별](beta-20260910-m2-latency/confirmed-image-identity.json)은
Go 1.26.8/Linux arm64, 정확한 커밋, `vcs.modified=false`, 두 바이너리 SHA 일치를 확인한다.

[커밋 e2cb0b9의 CI 34457413795](https://github.com/NudgeOn/Waiting-Room/actions/runs/34457413795)는
foundation, dependency-security, Valkey integration, PostgreSQL/브라우저 네 작업 모두 PASS다.
[CI 응답 보존](beta-20260910-m2-latency/confirmed-ci.json).
브라우저는 관리자 21개·공개 방문 흐름 33개 PASS다. govulncheck의 호출 코드/가져온
패키지는 0건, 요구 모듈 수준에서 호출되지 않는 항목은 1건이며 npm audit는 0건이다.
모든 의존 모듈에 취약점이 없다고 확대하지 않는다.
[총계와 보안 검사 문맥](beta-20260910-m2-latency/confirmed-ci-totals.log).

- [공개 장애 검사](beta-20260910-m2-latency/confirmed-public-matrix.log): PASS.
  488개 확장 모드/장애 조합과 기존 72개, origin/Coordinator 중단과 재개,
  실제 mTLS clock-refresh 요청·권한·정확한 재시도·감사 한 건, source quota,
  320px/키보드·axe 0건, 로고 서명 배포를 확인했다.
- 전체 epoch는 별도 실행했다. [첫 시도의 브라우저 경로 누락](beta-20260910-m2-latency/epoch-browser-path-failed.log)은
  epoch 발행 전에 종료된 실행 환경 오류다. 기존 `.cache/ms-playwright`를 명시한 재실행에서
양 ACK 1,024ms와 실제 안전 종료 시각 2026-09-10 09:59:34.397 UTC를 확인했다.
  [전체 여정](beta-20260910-m2-latency/confirmed-full-epoch.log)은 exit 0이다.
  실제 안전 대기 동안 121회 `QUEUE_UNAVAILABLE`을 확인하고, 관리자 재로그인·검증 후
  HOLD·명시적 AUTO·epoch 2 claim·mTLS 원본 도달까지 통과했다.

### 최종 서비스 이미지의 인원·콜드 복구 PASS

[e2cb0b9 인원 실행](beta-20260910-m2-latency/confirmed-population-cold.log)은 exit 0이다.
1K/2K/5K/10K의 전원 join/status와 구간별 최근 100개 exact replay를 통과했다.
구간 시간은 67.1/134.9/409.3/813.8초다. 실제 `Retry-After`를 지킨 429는 27,791건,
성공한 heartbeat는 48,349건이다. 일반 503·연결 실패를 재시도로 숨기지 않았다.

실제 Gateway/Coordinator·Valkey 정지/재시작 뒤 준비까지 162,541ms를 기다렸다.
전체 10,000개 대기표, 같은 epoch, 새 fence, HOLD를 확인했고 최근 join 100개는
모두 보존 기간 안의 **202·동일 응답**이었다. 이 실행에서 만료 예외 분기는 0건이다.
원래 100개 대기표 credential의 queued 조회, 초과 신규 요청의 정확한 용량 거부,
보호 GET/POST 차단, 명시적 AUTO 후 최초 일곱 명의 FIFO claim·mTLS 원본 도달까지 PASS다.
10K 지속 부하 qualification은 별도이며 이 실행에서는 NOT_RUN이다.

숫자 INFO 표본 56개의 최대 왕복은 2.415ms였다.
[Valkey scheduler](beta-20260910-m2-latency/confirmed-valkey-scheduler.jsonl)와
[VM 시각/스케줄 기록](beta-20260910-m2-latency/vm-clock-schedule.jsonl)을 별도로 보존한다.
긴 환경 지연이 영구히 사라졌다고 주장하지 않는다.

### 최종 이미지의 저장소 일시 중단·키 수명 주기 PASS

[별도 public/keys 실행](beta-20260910-m2-latency/confirmed-public-keys.log)은 exit 0이다.
실제 Valkey pause에서 join/status/claim/heartbeat 모두 `503 QUEUE_UNAVAILABLE`,
보호된 원본 접근 거부를 확인했다. unpause 후 조회 일정 대기 포함 2,594ms에 같은
대기표·정확한 join 응답을 확인했고, 새 ACK의 epoch/fence/대기표 수가 그대로였다.
재시작·새 epoch·안전 대기 단축은 하지 않았다. 확인되지 않은 큐 쓰기까지 이 경로로
복구한다고 확대하지 않으며 실제 쓰기 유실 fence는 별도 회귀로 검증한다.

같은 실행에서 정상 72개 모드 조합과 원본/Coordinator 중단·복구,
키 stage/activate 양 ACK, 재시도 감사 중복 0, 기존 admission/join/return 보존,
새 키 입장권의 원본 도달, 24시간 30초 이전 폐기 거부, 새 epoch 재인증 이후 긴급 폐기와
기존 return 거부를 확인했다. 24시간 30초 전체 실제 대기를 수행한 검사는 아니다.

### 최종 이미지의 운영·Traffic Lab·LKG PASS

[운영 실행](beta-20260910-m2-latency/confirmed-operations.log)은 exit 0이다.
Admin/Operator/Viewer × Chromium/Firefox/WebKit × 9개 화면, 총 81개 실제 화면에서
320px 가로 넘침·자동 WCAG 위반 0건과 키보드 포커스를 확인했다. 관측된 API 계약은
32개이며 응답 유실 뒤 keyed logout 재시도·세션 폐기도 확인했다. VoiceOver는 제외했다.

Control을 실제로 멈춰 fetch deadline을 넘긴 동안 기존 서명 설정으로 HOLD join이
성공했다. 재개 후 재시작 없이 새 generation을 양 노드가 ACK했다.
Quick 20은 123요청, Smoke 1K는 3,013요청으로 실제 Control/Coordinator mTLS·격리된
HTTP 테스트 큐를 사용했다. 운영 큐 키 접근은 거부됐고, 실행 결과와 Lab별 최종 감사 한 건,
발행된 Room과 초안은 PostgreSQL/Control 재시작 뒤에도 보존됐다.

### 최종 이미지의 기존 설치 업그레이드·백업 PASS

[백업/복원 실행](beta-20260910-m2-latency/confirmed-upgrade-backup.log)은 exit 0이다.
원본은 별도의 기존 schema 5 이미지 `waiting-room-beta4-patched:local`이며
[그 이미지 ID](beta-20260910-m2-latency/backup-source-image.json)를 보존했다.
7개 정지 볼륨 50,028,544바이트의 백업을 검증하고 빈 새 볼륨으로 복원했다.
기존 계정·세션·초안·mTLS 식별 키·서명 캐시·원래 join 응답을 확인했다.

e2cb0b9 업그레이드는 schema 5를 재해석하거나 초기화하지 않았으며 원래 ACL을 유지했다.
키 stage/activate 이후 세 번째 콜드 복원에서 교체된 키·private journal·원래 폐기 시각·
양 ACK·이전/새 암호화 응답·서명된 로고를 보존했다. 실제 primary 복구 안전 대기를
거쳐 원래 대기표의 FIFO claim·mTLS 원본 도달까지 PASS다. 관리자 복구 검토창은
360px/키보드 Escape로 확인했으며 새 reset은 실행하지 않았다.

전체 epoch와 백업 실행은 모두 exit 0이고 각 시험 프로젝트를 종료했다. 마지막
[읽기 전용 시각 관측](beta-20260910-m2-latency/vm-clock-schedule-final.jsonl)은
09:42:55~09:59:55 UTC에 1분 표본을 남겼고 그 구간 역행 관측은 0회다.
완료 후 이 관측기만 수동 종료했다. 실패·원본·복원 볼륨은 보존했다.

## 판정 범위

M2의 로컬 안전 기능·장애 차단·복구 수용은 같은 고정 서비스 이미지에서 완료했다.
이전 후보의 FAIL과 최초 진단 없는 과거 사례는 원래 상태로 남긴다. 이번 PASS로 그때의
미관측 원인을 만들어 내거나 환경 지연이 사라졌다고 주장하지 않는다.

M3의 기능·기술 회귀는 PASS지만 [실제 운영자 사용성 수용](../operators/beta-operator-acceptance.md)은
미실행이다. B6/공개 Beta 명칭 변경·출시 artifact 발행은 하지 않았다.
VoiceOver 제외는 사용자 요청에 따른 것이며 보조기기 실사용 PASS가 아니다.
10K/100K 지속 부하, Helm/HA, GA 최종 검사는 후속 단계다.

앞선 `12ef71f` 이미지는 동일 설정 잠금만 수정한 중간 후보다.
[빌드](beta-20260910-m2-latency/refresh-image-build.log),
[식별](beta-20260910-m2-latency/refresh-image-identity.json),
[CI](beta-20260910-m2-latency/refresh-ci.json)를 보존하며 최신 이미지 결과와 합치지 않는다.

### 중간 후보의 10K 통과와 콜드 검사 실패

12ef71f는 1K/2K/5K/10K 전원 join/status와 구간별 100개 exact replay를 통과했다.
각 구간은 74.6/134.0/415.4/890.3초였으며 실제 heartbeat 56,200건을 확인했다.
[전체 로그](beta-20260910-m2-latency/refresh-population-cold-failed.log).
이는 기존 8,600명을 넘은 실제 인원 검사이며 지속 부하 qualification은 아니다.

콜드 재시작 이후 generation 9/양 ACK, epoch 1, fence 2, HOLD, waiting 10,000은
확인됐다. 그러나 최근 join 재시도 첫 응답에서 검사기가 202 대신 503을 받아 전체 실행은
FAIL이다. 이 실행은 해당 응답의 problem code를 남기지 않아 그 503의 정확한 원인을
확정할 수 없다. 10분 join 재시도 보존과 heartbeat의 대기표 보존은 별개이며,
10K 구간의 긴 조회와 안전 대기가 이 경계를 넘을 수 있다.

중지된 실패 볼륨을 네트워크 없는 관측기에 읽기 전용으로 연결한
[AOF 숫자 확인](beta-20260910-m2-latency/refresh-aof-times.json)에서 최신 `JoinedAt`은
09:03:00.266 UTC였다. 서로 다른 대기표 10,000개와 최대 순번 10,000을 포함하는 집계다.
이 후보의 기본 `IdempotencyTTL=600000ms`를 적용하면 마지막
재시도 보존 종료도 09:13:00.266 UTC다. 실제 복구 완료 09:13:07보다 이르므로 콜드
재시도 단계의 모든 원래 join key는 보존 기간이 지난 상태다. 이로써 무조건 202를 기대한
검사기의 전제가 잘못됐음을 확인했다. 기록되지 않은 당시 problem code 자체를
추정으로 채우지는 않는다. [읽기 전용 관측기 소스](beta-20260910-m2-latency/aof-times-probe.go.txt).
증분 AOF에서 설정은 관측되지 않았으며 JSON의 `configPresent=false`와 0은 미관측을
뜻한다. TTL 근거는 해당 후보의 `model.DefaultConfig`와 변경하지 않은 fixture 설정이다.

후속 검사기는 새 응답의 code/서버 Date를 확인하고 기간 내 응답 비교와 만료 후 가득 찬
큐의 용량 거부를 구분한다. 원래 credential로 100개 대기표를 다시 조회하고 전체 인원과
FIFO 입장을 확인한다. `QUEUE_UNAVAILABLE`/`CONFIG_UNAVAILABLE`/기간 전 용량 실패는
통과시키지 않는다. [검사기 회귀](beta-20260910-m2-latency/cold-retention-checker.log).
서비스의 TTL·시계·데이터·제한값은 바꾸지 않는다.
[도구 전체 테스트 22개](beta-20260910-m2-latency/harness-tests.log) PASS.
검사기 커밋 `294cba7`의 [CI 34460083805](https://github.com/NudgeOn/Waiting-Room/actions/runs/34460083805)도
네 작업 모두 PASS다. [CI 보존](beta-20260910-m2-latency/harness-ci.json).
서비스·관리자 UI·배포 설정·의존성은 e2cb0b9 이미지 소스와
[동일함을 확인](beta-20260910-m2-latency/service-source-equivalence.json)했다.

중간 후보의 숫자 INFO 표본 57개 최대 왕복은 3.747ms였다.
[Valkey PID scheduler](beta-20260910-m2-latency/refresh-valkey-scheduler.jsonl)는
08:45:14~09:10:05 UTC 범위에서 관측 간격 최대 114.249ms, 한 구간 실행 대기 증가
105.987ms였다. Valkey를 콜드 정지하면서 같은 PID namespace 관측기도 종료됐다.
이 실행에서 긴 지연이 없었다는 관측이며 환경 지연이 영구히 제거됐다는 증거는 아니다.

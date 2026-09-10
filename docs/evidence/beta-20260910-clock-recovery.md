# 2026-09-10 — 시계 보정 후 설정 복구와 전송 전 취소

현재 판정: **Beta NO-GO**. 공개/운영 회귀는 PASS이며, 10K 인원 검사는 실패했다. 전체 epoch와 백업 통합은 최종 결과를 아래에 기록한다. VoiceOver는 사용자 요청으로 제외했다.
기존 설치를 변경하지 않고 별도 Docker 프로젝트에서 검사한다. 실패 이미지와 볼륨은 보존한다.

## 실제 장애 근거와 수정

[호스트 시간 근거](beta-20260910-clock-recovery/incident-host-clock.json)는 Docker Desktop의
`GET /time` 응답에서 2026-09-09 15:58:06 → 15:58:34 UTC 사이 monotonic 경과
30.005468초, wall 경과 28.002722초를 확인한다. 약 **-2.002746초의 호스트 시계 보정**이다.
이전 두 독립 프로젝트의 Gateway/Coordinator가 처음 설정 차단을 기록한 시각은
15:58:34/35 UTC이며, 저장된 서명 설정은 아직 24시간 유효 범위 안이었다.
원래 노드의 진단은 최초 Gate 이유를 보존하지 않았으므로 호스트 관측과 노드 트리거의
추론을 구분한다. 더 오래된 원인 미상 5K 장애의 직접 원인까지 이 기록으로 확정하지 않는다.

기존 Gate는 시계 역행 뒤 더 최신의 정상 서명 설정도 영구 거부했다. `fb70fcb`는 역행
중 차단을 유지하면서, 시간 high-water 회복 + 해당 시각 이후 발행된 더 높은 generation +
전체 서명/유효기간 검증 + 정상 영속 저장을 모두 충족해야 복구한다. 시간 경과·같은 설정
재생·오래된 발행·손상 저장소로는 열리지 않는다. [ADR-0005](../adr/0005-config-clock-quarantine.md).

[수정 전](beta-20260910-clock-recovery/clock-recovery-red.log) 복구 4개 검사가 실패했다.
[수정 후](beta-20260910-clock-recovery/clock-recovery-green.log) 신규 5개와 기존 clock/
저장소 경계를 race로 10회 반복했다. [설정·runtime 단위](beta-20260910-clock-recovery/config-runtime-unit.log)와
[실제 PG publication/refresh](beta-20260910-clock-recovery/publication-integration.log)도 PASS다.
단위의 clock 주입과 실제 Docker 장시간 결과는 별도 증거다.

이번 진단 이미지의 [인원 검사 실패](beta-20260910-clock-recovery/diagnostic-tiers-failed.log)는
1K PASS 뒤 1,200개에서 `QUEUE_UNAVAILABLE`, `uncertain_write`, fence 2였다.
`CONFIG_UNAVAILABLE`과 다른 실패다. 이 실행의 최초 쓰기 오류/전송 단계까지는 보존되지 않았다.

코드 추적에서 복구 mutex 대기 중 취소된 요청을 실제 전송 전에 재확인하지 않는 결함을
발견했다. [전용 Valkey 수정 전](beta-20260910-clock-recovery/cancel-wait-red.log)은 전송하지
않은 취소 요청이 uncertainty와 fence 2/RECOVERY_HOLD를 만들었다. `be060a8`는 대기
직후 취소를 확인한다. [수정 후 10회](beta-20260910-clock-recovery/cancel-wait-green.log)는
다음 방문자의 sequence 1/기존 fence 보존, 읽기 응답 유실 복구, 실제 쓰기 유실 차단을
검증했다. 이 재현으로 진단 인원 실행의 원인까지 유일하게 확정하지 않는다.

## 중간 고정 후보 be060a8

- source `be060a8f290092216c4cc3c945d8445cca682638`, Go 1.26.8, Linux/arm64, vcs.modified=false.
- image `waiting-room-beta7-recovery:local`.
- ID `sha256:0730ee1a03c5b161fb584bf23407cd51643137fe375a08ff823dbd000667ec2a`.
- [바이너리/빌드 정보](beta-20260910-clock-recovery/image-identity.json), [Docker 빌드](beta-20260910-clock-recovery/image-build.log).
- [make check](beta-20260910-clock-recovery/make-check.log): Go race, UI, 계약, 문서, 설치 schema PASS.
- [별도 process trust](beta-20260910-clock-recovery/process-trust.log): 실제 cold signature/key/binding 차단과 5초 lab envelope 만료 후 차단 PASS. production TTL/host clock을 바꾼 검사가 아니다.
- [Control](beta-20260910-clock-recovery/control-vulnerability.log)/[Node](beta-20260910-clock-recovery/node-vulnerability.log) 고정 바이너리: 영향 호출 경로/사용 package 0, 사용하지 않는 module advisory 1. 전체 module advisory 0이라는 뜻은 아니다.
- 로컬 `npm audit`는 의존성 메타데이터 외부 전송 위험으로 자동 승인 검토에서 실행 전 차단됐다. [이미 완료된 동일 커밋 CI 보안 기록](beta-20260910-clock-recovery/existing-ci-security.log)의 npm audit 0건을 읽어 확인했다. 새 audit/워크플로를 우회 실행하지 않았다.

| 실제 후보 검사 | 상태 | 판정 범위 |
|---|---|---|
| 공개 quota 유지 1K/2K/5K/10K | FAIL | 1K/2K/5K PASS 뒤 일부 QUEUE_UNAVAILABLE, waiting 9795/fence 1; [보존 로그](beta-20260910-clock-recovery/intermediate-tiers-failed.log) |
| 설치 전체 새 epoch | SUPERSEDED | 양 ACK 1,018ms/안전 대기 진입, 최신 제한 함수 이미지로 교체; 전체 PASS 아님 |
| 공개 HTTPS/키 교체/72개 모드 조합 | NOT_RUN | 고정 후보에서 후속 실행 |
| 관리자 3역할/3엔진/Traffic Lab | NOT_RUN | 고정 후보에서 후속 실행 |
| v4 이행/백업/키 복원 | NOT_RUN | 고정 후보에서 후속 실행 |
| 원격 CI | PASS | [네 작업](beta-20260910-clock-recovery/source-ci.json), serving source be060a8 |

중간 진단 epoch와 clock-only 후보 epoch는 최종 두 수정 이미지로 교체하기 위해 종료했다.
각각 초기 ACK/안전 대기 진입만 확인했으며 전체 성공으로 집계하지 않는다.

10K 인원 경계는 지속 처리량 qualification과 다르다. host 시계/NTP, TTL, quota,
`unsafeUntil`, 운영 모드의 안전 조건을 테스트 통과 목적으로 변경하지 않는다.
이 수정은 이미지 업로드 sanitizer, 비개발 운영자 수용, 모든 장애/API 상태 조합,
100K/HA/production 배포 검증의 완료를 뜻하지 않는다.

## 공개 제한 시각의 후속

실제 VM 관측에서 49.6ms/9.7ms/1.1ms의 역행을 기록했다. 중간 인원 실행의 정확한 각 503 원인은 로그에 없어 시각 보정만으로 확정하지 않는다.
기존 v2 제한 함수는 1ms 역행에도 오류를 반환했다. [전용 metadata 재현](beta-20260910-clock-recovery/guard-clock-red.log)에서 900ms 보정 상태가 오류를 반환함을 확인했다.
v3는 최대 1000ms 보정 동안 이전 관측 시각을 유지한다. quota와 poll 시각을 되감거나 새 예산을 주지 않으며, 1000ms 초과 역행과 schema 오류는 계속 차단한다. queue/admission/epoch의 시간 검사는 변경하지 않는다.
[전체 제한기 회귀](beta-20260910-clock-recovery/guard-green.log)와 [기존 v1/v2 보존·시계 경계 3회](beta-20260910-clock-recovery/guard-targeted.log)는 PASS다. 신규 함수 이름을 사용하고 기존 함수/세 key는 교체하지 않는다.
후속부터 제한기 장애는 비밀정보 없는 고정 code로 분당 최대 한 번 기록하며 실패 인원 검사에 최초 응답 시각을 남긴다. 최신 이미지의 10K/콜드 복구/전체 epoch 결과는 후속 기록으로 갱신한다.

후속 검토에서 v3의 보정 구간에 queue 요청이 도달할 수 있는 경로를 더 보수적으로 변경했다. 최종 v4는 1000ms 이하 역행에서 기록을 변경하지 않고 429/Retry-After를 반환한다. 실제 clock이 high-water를 따라잡아야 기존 quota 처리와 queue 호출을 재개한다. v1/v2/v3 라이브러리를 모두 보존한다. [v4 경계 검사](beta-20260910-clock-recovery/guard4-targeted.log)는 PASS다. v3 이미지는 중간 검증용으로 보존하고 최종 장시간 후보로 집계하지 않는다.

## 최종 고정 후보 4f582f8

`waiting-room-beta7-clock-defer:local`, source `4f582f8470deafd62f5d6196c4216ae66fb3588b`,
ID `sha256:01b428a713d6423787c2c5edb5bddc4b409af947390d202ead1f1d08e1105b9b`.
[최종 바이너리 정보](beta-20260910-clock-recovery/defer-image-identity.json),
[빌드](beta-20260910-clock-recovery/defer-image-build.log),
[전체 v4 guard 회귀](beta-20260910-clock-recovery/defer-guard-all.log)는 PASS다.

| 검사 | 상태 | 범위 |
|---|---|---|
| 10K 공개 인원 + 실제 콜드 재시작 | FAIL / 콜드 NOT_RUN | 두 번째 실행 10K 생성 후 상태 조회 중 guard deadline/연결 종료; 1K/2K/5K PASS |
| 전체 epoch 복구 여정 | PASS / 관측 공백 있음 | [95회 실제 관측](beta-20260910-clock-recovery/defer-epoch.log), deadline 이후 HOLD → AUTO → 실제 원본; 호스트 sleep 포함 |
| 공개 모드·장애·키 교체 | PASS | [실제 HTTPS·브라우저](beta-20260910-clock-recovery/defer-public.log), 72개 조합·두 서비스 중단·키 교체/폐기 |
| 관리자/Traffic Lab | PASS | [3엔진/3역할/81화면/32API](beta-20260910-clock-recovery/defer-operations.log), 실제 Quick20/Smoke1K·Control 중단 복구 |
| v4 백업 이행·키 복원 | PASS | [7개 볼륨·세 번의 콜드 복원](beta-20260910-clock-recovery/defer-backup.log), 기존/새 키 응답·FIFO·원본 보존 |
| schema 5 업그레이드 | NOT_RUN | 기존 라이브러리·세션·대기표 보존 |

중간 be060a8 epoch의 [초기 ACK/안전 대기 기록](beta-20260910-clock-recovery/intermediate-epoch-superseded.log)은 전체 PASS로 집계하지 않는다.

최종 후보 첫 인원 실행도 [실패 로그](beta-20260910-clock-recovery/defer-tiers-first-failed.log)를
보존한다. 04:45:03.711 UTC부터 `QUEUE_UNAVAILABLE`, 696 waiting/fence 2/uncertain_write,
같은 초에 제한기 `code=deadline`이 관측됐다. 설정 generation 4는 적용된 상태였다.
로컬 `make check`는 04:44:28.271 UTC에 종료되어 약 35초 앞선다. 검사 동시 실행이
직접 원인이라는 초기 추론은 철회한다. 다른 호스트 작업의 영향과 Valkey fsync 지연은
가설이며 현재 최초 큐 쓰기의 지연/전송 원인을 입증하는 자료가 없다.
동일 이미지 재검사에는 `appendfsync always`, 2초 제한, quota, 안전 시간을 그대로 사용한다.
재검사가 성공하더라도 첫 실패를 삭제하거나 지속 처리량 통과로 바꾸지 않는다.

최종 후보의 [전체 로컬 검사](beta-20260910-clock-recovery/defer-make-check.log)는 PASS이며,
실제 epoch 컨테이너에서 복사한 두 바이너리 SHA도 빌드 산출물과 일치한다.
원격 source 4f582f8 CI는 네 작업 중 Valkey 통합이 FAIL이었다. 04:47:00에 quota의
정상 분 만료를 무시한 새 테스트의 검사 조건이 실패했다. 서비스 코드를 변경하지 않고
보정 중 quota 보존과 보정 후 살아 있는 분/만료된 분을 구분하도록 테스트를 수정한다.

최종 서비스 커밋 4f582f8의 [이미 완료된 CI 보안 기록](beta-20260910-clock-recovery/defer-existing-ci-security.log)은
실제 호출 취약점/사용 package 0건, 호출하지 않는 module advisory 1건, npm audit 0건이다.
최종 이미지에 대한 새 로컬 `govulncheck -mode=binary`는 의존성·버전 메타데이터의 외부 전송을
이유로 자동 승인 검토에서 실행 전 차단됐다. 우회 실행하지 않았으며 이 이미지의 별도
바이너리 취약점 스캔은 NOT_RUN이다. 바이너리 SHA/Go buildinfo 일치와 CI 소스 검사는
관측한 범위 그대로 기록한다. 앞선 be060a8 바이너리 스캔을 이 이미지의 스캔으로 바꾸지 않는다.

[새 시간 초과 구간의 호스트 시각](beta-20260910-clock-recovery/defer-timeout-host-clock.json)은
04:43~04:46 UTC의 약 30초 표본에서 큰 역행을 보이지 않았다. 이 표본만으로 짧은 VM
보정이나 RPC 지연 원인을 배제할 수는 없다. 큐의 `appendfsync always`와 noeviction은
계속 유지한다.

테스트만 수정한 `596094f27d7f9fbee8bedc464685da73c09f0c18`의
[후속 원격 CI](beta-20260910-clock-recovery/source-ci-after-minute-fix.json)는 foundation,
Valkey integration, Admin/PostgreSQL integration, dependency-security **네 작업 모두 PASS**다.
실제 서비스 바이너리는 계속 4f582f8 고정 이미지이며 이 후속의 서비스 코드 변경은 없다.
분 경계 수정은 [로컬 10회 반복](beta-20260910-clock-recovery/guard-minute-green.log)도 PASS다.

최종 후보의 [두 번째 인원 실행](beta-20260910-clock-recovery/defer-tiers-second-failed.log)은
1K/2K/5K PASS 뒤 10,000개를 생성했지만 전체 상태 조회에서 제한기 deadline과 연결
종료로 FAIL했다. 종료 당시 generation 7/applied/HOLD/10,000 waiting/fence 1이었고,
설정 영구 차단이나 새 recovery fence는 없었다. 429를 18,088번 따랐고 실제 heartbeat
29,398개가 성공했다. 이 성공 개수는 10K 전체 수용 PASS가 아니다. 콜드 복구 단계에는
도달하지 못했다. 동일 조건을 다시 반복해 실패를 덮지 않는다.

인원 실행 중 두 번의 [Valkey 읽기 전용 통계](beta-20260910-clock-recovery/defer-valkey-info.jsonl)는
AOF 쓰기 상태 정상, eviction 0, 메모리 약 13~15MB/256MB, INFO 왕복 0.265/0.520ms였다.
[수집 코드](beta-20260910-clock-recovery/valkey-info-probe.go.txt)는 고정 숫자·상태만 출력했다.
이 표본은 05:06:26 장애 순간의 디스크/스케줄링 지연 증거가 아니며 원인을 확정하지 않는다.

공개 runtime `waiting-room-public-test-77524301`은 정상 종료했다. 설치 실제 보정/적용,
첫 browser 응답과 cookie 전체 유실 후 동일 대기표 복구, 동시 다섯 탭, 공개 API 계약,
320/360px·키보드·axe, 72개 모드 조합, 실제 origin/Coordinator 정지·복구,
키 stage/activate의 기존 입장권·join·return 보존과 새 키 입장, 조기 retire 차단,
Admin 새 epoch에 묶인 긴급 폐기와 동일 generation 재시도를 통과했다.

10K 콜드 검사 보조 함수는 readiness가 이미 안전 대기를 포함하는데 이후 RECOVERY_HOLD를
다시 찾던 검사 순서도 바로잡았다. 현재 함수는 실제 150초 readiness 대기, 이후 HOLD와
새 fence/기존 epoch/10K/최근 100개 응답 보존을 검사한다. 연속 안전 차단 probe와는
범위를 구분하며, 이번 인원 실패 때문에 해당 10K 콜드 단계는 여전히 NOT_RUN이다.

다음 인원 검사에는 transport 오류의 허용된 code, 발생 시각, 작업 종류, 소켓 재사용,
소켓 배정 대기와 전체 경과 시간을 추가했다. URL/헤더/응답 본문/원래 오류 메시지는
출력하지 않는다. 이번 연결 종료는 그 진단이 없었던 실행이므로 원인을 소급 단정하지
않는다. 503/transport 오류를 성공할 때까지 재시도하는 변경은 하지 않았다.

관리자 runtime `waiting-room-traffic-test-a399357e`도 정상 종료했다. 실제 Quick20
123건/Smoke1K 3,013건 요청, wr_traffic ACL 격리, Control fetch 제한을 넘는 중단과
서명 LKG 처리/새 generation 양 ACK, 실제 키 stage/activate, 세 브라우저 × 세 역할 ×
아홉 페이지, 320px/키보드/axe WCAG 2.2 AA 포함 위반 0, 32개 API 계약과 명령 재시도,
로그아웃 응답 유실 재시도, 감사·저장 보고서·PG 재시작 보존을 통과했다.

후속 [macOS 전원 기록](beta-20260910-clock-recovery/host-power-events.json)에서 두 번째 실행의
14:06:22 KST `Software Sleep`과 이후 DarkWake/Maintenance Sleep을 확인했다.
제한기 deadline은 14:06:26 KST였다. 별도 VM probe의 wall-minus-monotonic 차이는
05:05:30 UTC의 약 -14ms에서 05:06:41의 +11.6초, 05:23:25의 +835.4초로 증가했다.
따라서 두 번째 실행에는 실제 호스트/VM 중단 조건이 있었다. 이를 정상 지속 가동
10K qualification으로 판정할 수 없다. 최초 요청의 전송 단계까지 기록된 것은 아니므로
개별 모든 오류의 유일한 원인이라고 확장하지 않는다. 첫 번째 13:45 KST 실패 구간에는
같은 Sleep/Wake 기록이 없어 그 deadline 원인은 미확정이다.

장시간 epoch도 같은 호스트를 사용했으므로 안전 대기 전후 복구 결과와 연속 서비스
관측을 구분한다. 호스트 중단 중 성공 probe를 가정하지 않으며 실제 관측 수만 기록한다.

## 남은 Beta 조건

1. 첫 최종 후보의 guard deadline/불확실 쓰기 원인을 요청 전송·저장소 지연 근거로 규명한다.
   두 번째 실행의 호스트 sleep 기록을 첫 번째 실행의 원인으로 대체하지 않는다.
2. 호스트가 중단되지 않는 검증 환경에서 같은 후보의 10K 전원 조회·최근 재시도·
   실제 콜드 10K 보존/FIFO를 끝까지 통과한다. quota/TTL/fsync/deadline을 완화해 통과시키지 않는다.
3. 이미지 업로드의 크기·pixel 제한/decode/re-encode sanitizer와 비개발 운영자 수용 검증,
   아직 실행하지 않은 API·장애 조합의 owner acceptance를 완료한다.
4. 최신 후보의 schema 5 기존 설치 업그레이드 실물 회귀를 별도로 기록하고 B6를 재판정한다.

공개 Preview를 Beta로 재명명하거나 출시 태그를 만들지 않았다. 원본 설치와 실패 볼륨은
보존했다. 소스 수정과 전용 로컬 이미지 검증을 기존 설치 적용/운영 배포로 표시하지 않는다.

최종 epoch `waiting-room-epoch-test-eeb9ed47`은 **95회** 실제 관측 후 정상 종료했다.
2026-09-10T05:43:45.917Z의 실제 안전 deadline 이후 관리자 재로그인, 검증 완료 HOLD,
명시적 AUTO, epoch 2의 새 claim과 실제 mTLS 원본 HTTP 200까지 통과했다. 이전
8,600개 후보처럼 대기 후 설정 차단이 계속되는 실패는 이 실행에서 발생하지 않았다.
호스트 sleep으로 생긴 관측 공백 때문에 121회 연속 관측/무중단 서비스라고 표시하지 않는다.

별도 [VM clock 관측 전체](beta-20260910-clock-recovery/vm-clock-observations.jsonl)와
[요약](beta-20260910-clock-recovery/vm-clock-summary.json),
[수집 코드](beta-20260910-clock-recovery/vm-clock-probe.go.txt)를 보존했다. 10ms 표본에서
작은 역행 9회를 기록했고, 종료 시 wall/monotonic 경과 차이는 약 835.4초였다.
호스트 시각/NTP/TTL을 변경하지 않았으며 작업용 관측 컨테이너만 명시적으로 종료했다.

최종 백업 검사는 정상 종료했다. 기존 schema 4의 7개 정지 볼륨을 새 볼륨에 복원한 뒤
실제 안전 대기, schema 5 이행과 기존 응답 보존을 확인했다. 키 stage/activate 후 세 번째
콜드 복원도 양 ACK, 원래 retire deadline, 기존/새 encrypted join 응답을 보존했다.
360px 복구 검토와 Escape, 이전 대기표의 FIFO claim 및 실제 mTLS 원본 도달까지 PASS다.
이 시험의 기존 schema 4 원본, 이행 직전 백업과 교체 키 백업은 모두 보존했다.

## 전달 상태

서비스 source는 4f582f8 고정 이미지다. 후속 변경은 테스트와 증거/문서다.
마지막 테스트 커밋 594c65d의 [CI 상태](beta-20260910-clock-recovery/final-test-ci.json)는
아래 기록 시각의 결과를 보존한다. 서비스 코드가 같은 596094f의 네 작업은 모두 PASS다.

594c65d CI: completed / success; foundation: success, admin-postgres-integration: success, dependency-security: success, valkey-integration: success.

Git branch `main`, origin `git@github.com-personal:NudgeOn/Waiting-Room.git`, author/committer `marvinkim-photo <marvinkim82dev@gmail.com>`. 사용자 요청으로 선택한 변경만 커밋·푸시하며, 최종 커밋 SHA와 원격 일치는 전달 응답에서 확인한다.

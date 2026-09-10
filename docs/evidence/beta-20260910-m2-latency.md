# 2026-09-10 — M2 제한기 지연과 동일 설정 잠금

판정: M2 최종 검증 진행 중, Beta NO-GO. 기준 소스 dc0b7a1의 잠금 수정 이후에도
공개 guard deadline이 남아 있어 추가 진단과 수정 전후 회귀를 수행했다.
기존 설치·실패 볼륨·원래 deadline·quota·AOF always·안전 대기를 보존한다.

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

최종 고정 이미지의 인원·공개 장애·콜드 복원·전체 epoch 결과는 후속 실행으로 별도 기록한다.

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
- 전체 epoch는 별도 실행한다. [첫 시도의 브라우저 경로 누락](beta-20260910-m2-latency/epoch-browser-path-failed.log)은
  epoch 발행 전에 종료된 실행 환경 오류다. 기존 `.cache/ms-playwright`를 명시한 재실행에서
  양 ACK 1,024ms와 실제 안전 종료 시각 2026-09-10 09:59:34.397 UTC를 확인했다.
  대기 종료 이후 입장까지 마쳐야 전체 PASS다.

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

후속 검사기는 새 응답의 code/서버 Date를 확인하고 기간 내 응답 비교와 만료 후 가득 찬
큐의 용량 거부를 구분한다. 원래 credential로 100개 대기표를 다시 조회하고 전체 인원과
FIFO 입장을 확인한다. `QUEUE_UNAVAILABLE`/`CONFIG_UNAVAILABLE`/기간 전 용량 실패는
통과시키지 않는다. [검사기 회귀](beta-20260910-m2-latency/cold-retention-checker.log).
서비스의 TTL·시계·데이터·제한값은 바꾸지 않는다.
[도구 전체 테스트 22개](beta-20260910-m2-latency/harness-tests.log) PASS.

중간 후보의 숫자 INFO 표본 57개 최대 왕복은 3.747ms였다.
[Valkey PID scheduler](beta-20260910-m2-latency/refresh-valkey-scheduler.jsonl)는
08:45:14~09:10:05 UTC 범위에서 관측 간격 최대 114.249ms, 한 구간 실행 대기 증가
105.987ms였다. Valkey를 콜드 정지하면서 같은 PID namespace 관측기도 종료됐다.
이 실행에서 긴 지연이 없었다는 관측이며 환경 지연이 영구히 제거됐다는 증거는 아니다.

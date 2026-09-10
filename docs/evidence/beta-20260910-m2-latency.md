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

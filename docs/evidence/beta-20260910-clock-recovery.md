# 2026-09-10 — 시계 보정 후 설정 복구와 전송 전 취소

현재 판정: **Beta NO-GO / 최종 통합 검사 진행 중**. VoiceOver는 사용자 요청으로 제외했다.
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

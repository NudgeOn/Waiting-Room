# 설치 v2 — 실제 삭제와 용량 반환의 일치

2026-09-06 local uncommitted snapshot. production/HA qualification이 아니다.

## 수정과 재현

- v1에서 다른 Room이 전역 만료 인덱스만 수거하면 owner hash가 남아 있어도 새 방문자를 수용했다.
  `TestInstallationExpiredOwnerRetainsBudget`가 수정 전 FAIL, v2에서 PASS.
- 설치 metadata가 남고 Room의 8개 key가 유실된 경우 Room 재초기화를 허용했다.
  `TestInstallationMissingRoomCannotResetSequence`가 수정 전 FAIL, v2에서 PASS.
- `wr_queue_install_v2`/schema 2를 추가했다. 기존 v1 source/library/data를 보존하며 자동 이관하지 않는다.
- 만료 record와 공유 reservation을 소유 Room의 bounded cleanup에서 함께 제거한다.
  유효 수와 미수거 만료를 포함한 점유 수를 구분하고 cap/80% 경고는 점유 수를 사용한다.
- Room registry가 있는 기존 Room의 metadata 유실을 시작 단계에서 거부한다.
- 400건 cross-Room 회귀는 다른 Room sweep 후에도 점유 400 유지/신규 거부,
  owner 128건 bounded sweep 이후 점유 0, 다른 Room 신규 상태 보존을 검증한다.

## 경계

비활성 Room의 예약 용량은 owner sweep 전까지 반환하지 않는 보수적 동작이다.
운영용 전체 Room scheduler, durable registry, 전체 metadata 유실, fencing/HA/자동 복구,
악의적 same-cardinality 변조, 운영 100K/장시간 SLO는 미완료다.
기존 v1 evidence는 당시 버전의 기록이며 현재 v2 증거와 구분한다.
MAIN과 모든 SUB delivery는 NO-GO 유지한다.

## 최종 고정 source 회귀

- `autonomous-final-20260906 --processes --tiers --http-tiers --fuzz`: **14 PASS / 1 FAIL**.
- UTC 02:09:07.799~02:12:31.989, source unchanged,
  SHA-256 `2a317edc27db46a618cdcd90b1a7ae9c277f3937c98888b839d00e10c7d8ef19`.
- [보고서](autonomous-final-20260906/report.json), [환경](autonomous-final-20260906/environment.json).
- 설치 v2 통합 8개, Store 1K/2K/5K/10K, signed config/입력/fuzz/정상 재시작 PASS.
  HTTP 첫 두 반복은 네 인원 단계 모두 PASS. 세 번째 5K join에서 예상치 못한 503, 10K는 미실행.
- 실패 원인 미확정이다. 이전 PASS를 근거로 덮지 않으며 재현성 blocker로 유지한다.
  [실패 로그](autonomous-final-20260906/http-visitor-tiers-3.log) 보존.
- 후속 변경은 test-only bounded 오류 category/실패 시 metadata 진단과 문서 보강이다.
  production fail-closed 조건이나 인원/worker/deadline/합격 기준은 완화하지 않았다.
  `autonomous-diagnostic-20260906` 전체 회귀 **15 checks PASS**.
- 후속 UTC 02:18:03.600~02:21:43.448, source unchanged,
  SHA-256 `917769ca947e78aa4f28303eb9bb4294ded9f259ad6ba262f1dc37188debf253`.
  [최종 보고서](autonomous-diagnostic-20260906/report.json), [환경](autonomous-diagnostic-20260906/environment.json).
  HTTP 네 단계 3회 모두 PASS. 간헐 503은 원인 미확정으로 계속 추적한다.

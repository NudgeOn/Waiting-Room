# 2026-09-10 — 로고 배포·복구·가용성 검증

판정: **Beta NO-GO, 후속 검증 진행 중**. 기존 실패 자료를 보존하고 새 기능·수정의
증거를 구분한다. VoiceOver는 사용자 요청으로 제외했다.

## 로고 구현

`706fe86`은 PNG/JPEG 업로드를 Room 생성·설정에 연결한다. 입력 16 KiB, 최대
256×256px를 확인하고 서버가 PNG로 다시 인코딩한다. 출력도 16 KiB 이하이며,
메타데이터·추가 내용·SVG·외부 URL을 배포하지 않는다. 저장 전에 전체 64 KiB 서명
봉투와 향후 runtime 필드의 공간을 확인한다. [운영 절차](../operators/theme-logo.md).

- [Go 로고/크기 경계](beta-20260910-logo-maintenance/logo-unit.log)와
  [PostgreSQL 저장·제거·재시도·거부·감사·권한](beta-20260910-logo-maintenance/logo-postgres.log) PASS.
- [관리자 단위 48개](beta-20260910-logo-maintenance/logo-admin-unit.log),
  [Chromium/Firefox/WebKit 실제 업로드·SVG 거부·reload·키보드 제거·360px](beta-20260910-logo-maintenance/logo-browser.log) 3/3 PASS.
- 신규 axe 검사에서 기존 template 설명의 대비가 실패했다. [원래 실패](beta-20260910-logo-maintenance/logo-browser-contrast-failed.log)를 보존하고 수정 후 3엔진 PASS를 확인했다.

## 첫 고정 후보: fab5409

이미지 `waiting-room-beta8-logo-diagnostics:local`, ID
`sha256:a46cfc23b2e07c11c3cd943ecbf6d1ca1c34fc18cea0c00f9784605ae4ce4c37`.
Go 1.26.8, linux/arm64, `vcs.modified=false`이며 실제 이미지의 두 바이너리 SHA는
로컬 빌드와 일치했다. [동일성](beta-20260910-logo-maintenance/image-identity.json),
[빌드](beta-20260910-logo-maintenance/image-build.log),
[CI 네 작업 PASS](beta-20260910-logo-maintenance/initial-ci.json).

| 실제 실행 | 결과 | 증거 |
| --- | --- | --- |
| 로고 서명 배포·Gateway PNG·320px/axe·키 교체 | PASS | [public.log](beta-20260910-logo-maintenance/public.log), 정확한 PNG bytes·GET/HEAD·POST 거부·실제 이미지 디코딩 확인 |
| 공개 API·72개 모드 조합·서비스 중단·긴급 키 폐기 | PASS | 같은 public.log |
| 기존 schema 5 → 5 업그레이드·콜드 복원·교체 키·이전 FIFO 입장 | PASS | [v5-backup.log](beta-20260910-logo-maintenance/v5-backup.log); 원본은 실제 구형 `waiting-room-beta4-patched:local` |
| 로고 추가 후 교체 키와 세 번째 콜드 복원 | PASS | 같은 v5-backup.log; 복원된 초안·서명 설정·Gateway 자산 보존 |
| 공개 quota 유지 인원 경계 | FAIL | [최초 큐 진단 포함](beta-20260910-logo-maintenance/tiers-diagnostic-failed.log); 1K/2K 뒤 2,000개에서 안전 복구 차단 |
| 10K 콜드 보존 | NOT_RUN | 인원 단계가 실패하여 도달하지 않음 |

그 전에 기존 `4f582f8` 이미지를 다시 실행한 결과도 4,800개에서 실패했다.
[기존 후보 실패](beta-20260910-logo-maintenance/tiers-4800-failed.log)를 보존한다.

## 최초 차단 경로와 수정

06:18:37 UTC에 `promote`, `read=false`, `deadline`, `elapsed_ms=1011`,
`lock_wait_ms=0`, `budget_ms=999`를 기록했다.
[최초 로그](beta-20260910-logo-maintenance/first-queue-failure.log).
HOLD에서 필요 없는 백그라운드 쓰기의 응답이 제한을 넘으며 `uncertain_write` fence 2로
이어졌다. 같은 범위의 [호스트 전원 로그](beta-20260910-logo-maintenance/power-events.json)에는
sleep/wake가 없었다. 06:20:47의 [Valkey 표본](beta-20260910-logo-maintenance/failure-valkey-sample.json)은
메모리 약 6.5 MB/256 MiB, eviction 0, AOF write `ok`, INFO 왕복 0.307ms였다.
이는 실패 순간의 fsync·VM 스케줄링 원인을 확정하는 표본은 아니다.

새 유지보수 읽기 함수는 만료 정리·입장 처리가 필요할 때만 기존 원자 쓰기를 호출한다.
기존 runtime 함수·fence·TTL·quota·fsync·RPC 제한은 유지한다.
[결정과 안전 경계](../adr/0006-idle-queue-maintenance.md).
직전 커밋의 소스 복사본에서 같은 유휴 회귀가 [RED](beta-20260910-logo-maintenance/maintenance-red.log),
수정 후 읽기 응답 유실·기존 대기표 보존·실제 만료/승격·쓰기 유실 차단이
[5회 GREEN](beta-20260910-logo-maintenance/maintenance-green-5x.log)이다.
최종 이미지의 인원·콜드 복구·장시간 epoch 결과는 다음 실행으로 별도 확인한다.

## 41c3713에서 직접 측정한 시계 역행

`waiting-room-beta8-idle-read:local`, ID
`sha256:9b978a70a95d5cfd5156927033217e84aab7cddf285a8f2208aa1aa42b8666af`.
[네 CI 작업 PASS](beta-20260910-logo-maintenance/idle-ci.json).
실제 이미지 실행 파일은 Go 1.26.8, linux/arm64, `vcs.modified=false`였고 로컬 빌드와 같았다.

[동일 Docker VM의 독립 관측](beta-20260910-logo-maintenance/vm-clock-initial-failure.jsonl)은
06:43:27.305585305 → 06:43:25.289092083 UTC, wall **-2.016493222초**,
monotonic **+9.7525ms**를 기록했다. 호스트 시계나 TTL을 주입하지 않았다.
두 독립 환경의 Gateway/Coordinator 모두 06:43:25에 `snapshot_clock_rollback`을 기록했다.
[인원 실행](beta-20260910-logo-maintenance/idle-tiers-failed.log)은 1K/2K 후 4,200개에서
`CONFIG_UNAVAILABLE`로 실패했고 fence 1/HOLD였다. [epoch 실행](beta-20260910-logo-maintenance/idle-epoch-failed.log)도
안전 대기 중 같은 설정 오류를 발견해 중단했다. 이 두 실행은 전체 PASS가 아니다.
검사기는 5분 갱신 전에 종료했으므로 이 후보가 영구히 복구 불가능했다고 확대하지 않는다.

## 빠른 서명 복구와 브라우저 안내

`320f0ae`는 Coordinator 전송 실패 시 browser GET/HEAD에도 503 안내·키보드 재시도를
제공한다. 앱 JSON은 유지한다. 새 방문/기존 티켓 GET/HEAD의 [수정 전 실패](beta-20260910-logo-maintenance/browser-failure-red.log),
[5회 race 통과](beta-20260910-logo-maintenance/browser-failure-green.log),
[Gateway/대기 화면/runtime 패키지](beta-20260910-logo-maintenance/browser-failure-packages.log)를 보존한다.

`a6847d2`는 시계 격리 노드가 자신의 generation/digest와 관측 시각 경계를 mTLS
내부 요청으로 보내도록 연결했다. Control은 현재 승인된 설정만 새로 서명한다.
Control 시계가 경계에 도달하지 않았거나 영속 저장/감사가 실패하면 열리지 않는다.
동시 요청은 이미 발급된 적격 서명을 재사용하며, 새 운영자 설정을 되돌리지 않는다.
[ADR-0005](../adr/0005-config-clock-quarantine.md).

[PostgreSQL 4개 시나리오 × 3회](beta-20260910-logo-maintenance/fast-clock-pg.log)는
동시 12요청·단일 감사/서명·정확한 재시도·미배포 초안 제외·미래 시각/권한/서명 충돌 거부·
감사 실패 rollback·뒤늦은 노드의 최신 운영자 상태 보존을 확인했다.
[전체 make check](beta-20260910-logo-maintenance/fast-clock-check.log),
[관리자 단위 48개](beta-20260910-logo-maintenance/fast-clock-admin.log),
[최종 소스 CI 네 작업](beta-20260910-logo-maintenance/fast-ci.json) PASS.

최종 이미지 `waiting-room-beta8-fast-clock:local`, ID
`sha256:c255619a4ad4a1524af1b74a141c8ff31b6b64b23dd7a7a69e7ba3951f32e4f4`,
소스 `a6847d2c0a6dba876948f8fe7ce6979ece99beaf`.
Go 1.26.8, linux/arm64, `vcs.modified=false`, 두 실제 이미지 바이너리와 빌드 일치.
[동일성](beta-20260910-logo-maintenance/fast-image-identity.json), [빌드](beta-20260910-logo-maintenance/fast-image-build.log).

최종 후보의 [첫 인원 실패](beta-20260910-logo-maintenance/fast-tiers-heartbeat-failed.log)는
06:56:58의 `heartbeat` 쓰기 deadline(2,009ms, mutex 대기 0, 남은 예산 1,974ms)이다.
1K 통과 후 1,800개에서 fence 2/uncertain_write가 됐다. 당시 관측된 새 시계 역행은 없다.
이는 설정 시계 복구나 유휴 쓰기 제거와 구분되는 현재 미해결 가용성 항목이다.

[진단 도구 초기 실행](beta-20260910-logo-maintenance/observer-unready-tiers.log)은
2,000개 상태에서 public guard deadline을 기록했으나, 종료된 observer의 정리 오류가
원래 exception 출력까지 덮었다. [다음 실행](beta-20260910-logo-maintenance/observer-setup-failed.log)은
observer 시작 검증에서 중단되어 인원 요청 0건이다. 두 도구 실패도 보존한다.
관측기는 전용 fixture에만 100ms latency monitor와 initializer의 `LATENCY LATEST`
읽기를 추가한다. Coordinator/공개 역할, AOF always, quota, TTL은 변경하지 않는다.

최종 후보의 실제 epoch·인원·공개/운영/백업 결과는 아래 후속 실행 기록으로 판정한다.

## a6847d2 이미지의 후속 실행 (만료 배치 수정 이전)

- [관측기를 포함한 인원 실행](beta-20260910-logo-maintenance/observed-tiers-failed.log):
  1K/2K/5K PASS. 07:19:01.060 UTC에 join index 6,219 한 건이
  `QUEUE_UNAVAILABLE` 503. 최종 관측은 waiting 9,991, generation 7 applied,
  fence 1/HOLD이며 새 uncertain-write 차단은 없었다. 10K/콜드 인원 검사는 FAIL/NOT_RUN.
  11,429회 429와 28,357회 heartbeat를 기록했다. 재시도로 503을 숨기지 않았다.
- Valkey 관측은 memory 약 16.8MB/256MiB, eviction 0, AOF write ok였다. 내부 100ms
  latency event는 없었으나 INFO 왕복은 최대 656.184ms였다. 이것은 syscall의 지연과
  같지 않다. 별도 VM 표본에는 07:18:52의 384ms 실행 공백과 07:18:57의 작은 역행이
  있지만 07:19:01 join 실패 순간의 2초 공백은 없다. 최초 내부 오류는 미확정이다.
- [공개 matrix 별도 실행](beta-20260910-logo-maintenance/fast-matrix.log):
  488개 browser/app/mode/서비스 장애 조합과 기존 72개 검사, 320px·키보드·axe 0,
  실제 mTLS 역할 경계·동시 8요청의 동일 재서명·감사 한 건·양 ACK PASS.
  로고, 600개 신규 출처 quota, 새 epoch의 초기 차단도 PASS다.
- [운영](beta-20260910-logo-maintenance/fast-operations.log): 3역할×3엔진×9화면=81개,
  API 계약 32개, Quick20 123요청/Smoke1K 3,013요청, Control 중단 LKG/회복,
  키 stage/activate, 재시도·감사·PG/Control 재시작 PASS.
- [처음 결합한 공개 검사](beta-20260910-logo-maintenance/fast-public-combined-failed.log)는
  matrix 이후 새 키 방문자의 claim 대기에서 실패했다. 추가 matrix 방문자가 실제 일곱
  FIFO lease를 사용한 테스트 구성 문제다. 각각의 실제 한도를 유지하도록 두 독립
  시나리오로 분리했다. 이 실행을 전체 PASS로 표시하지 않는다.

## 추가 만료 정리 수정과 앱 예제

[ADR-0007](../adr/0007-bounded-expiry-retry.md)의 별도 결함은 실제 300개 재시도 기록
만료 뒤 join이 `ErrSweep`로 실패하는 [RED](beta-20260910-logo-maintenance/sweep-red.log)로
재현했다. 확인된 정리 완료 응답에만 동일 인자를 최대 여덟 번 재개하며 원래 deadline,
함수·TTL·fence를 유지한다. 실제 쓰기 응답 유실은 재시도하지 않고 차단한다.
[배치/FIFO/응답 유실 3종 × 3회](beta-20260910-logo-maintenance/sweep-bounds.log),
[정리 직후 취소 × 3회](beta-20260910-logo-maintenance/sweep-cancel.log) PASS다.
이 소스 변경은 위 a6847d2 이미지에 포함되지 않는다.

[앱 참조 예제](../../examples/app-client/README.md)는 join intent의 생성 시각/key/target을
함께 보존하고 만료 후 새 방문자를 자동 발급하지 않는다. 가짜 HTTP·시계의
[실패/재시도 회귀 8개](beta-20260910-logo-maintenance/app-client.log)가 PASS다.
실제 native 기기·background·보안 저장소 검증을 대신하지 않는다.

[분리한 실제 키 수명 주기](beta-20260910-logo-maintenance/fast-keys.log)는 이전 admission/join/return, 새 키 claim·mTLS 원본, 조기 retirement 거부, 긴급 폐기의 실제 새 epoch 차단까지 PASS다.

[CI 보안 발췌](beta-20260910-logo-maintenance/fast-ci-security.log): Go 호출 경로 영향 0개, import package 0개, 미호출 module 수준 1개; npm audit 0개. 실제 최종 바이너리 재스캔은 미실행이다.

## 만료 배치 수정 후보 1173fbf

서비스 소스 `1173fbf45994e9788de93785cb0e1597b7944844`, 이미지 `waiting-room-beta8-expiry-retry:local`,
ID `sha256:a6071a225a0f807932e81df2cb69647f7624398ddfbfa6bcf41188afbc799dca`.
Go 1.26.8 / Linux arm64 / `vcs.modified=false`, 실제 이미지의 두 실행 파일은 로컬 빌드와
일치한다. [이미지 동일성](beta-20260910-logo-maintenance/expiry-image-identity.json),
[빌드 로그](beta-20260910-logo-maintenance/expiry-image-build.log).

[기존 schema 5 업그레이드와 세 차례 콜드 복원](beta-20260910-logo-maintenance/expiry-v5-backup.log)은
PASS다. 구형 schema 5 이미지의 계정·세션·초안·ACL·서명 cache·기존 join/replay/FIFO를
보존하고 업그레이드·키 stage/activate·재복원 후 실제 mTLS 원본에 도달했다. 각 150초
안전 대기를 실제로 기다렸으며 TTL을 수정하지 않았다. [CI 네 작업](beta-20260910-logo-maintenance/expiry-ci.json)도 PASS다.

[공개 인원 검사](beta-20260910-logo-maintenance/expiry-tiers-failed.log)는 07:52:03 UTC에
실패했다. 최초 join index 625, 최종 waiting 632, generation 4 applied,
fence 2/uncertain_write다. join 진단은 mutex 대기 1,103ms, 남은 예산 761ms,
RPC 경과 811ms였다. 같은 순간 INFO 왕복은 1,362.19ms, memory 3.4MB,
eviction 0, AOF write ok, 내부 100ms 이상 latency event는 없었다.
복구 기한 07:54:35.555 이전에 검사를 중단했으므로 장시간 복구 실패를 뜻하지 않는다.
1K/10K·콜드 인원 수용은 FAIL/NOT_RUN이다.

a6847d2의 [60분 30초 epoch 전체 여정](beta-20260910-logo-maintenance/fast-epoch.log)은
121회 실시간 관측, 세션 만료 후 재로그인, bounded validation → HOLD → 명시적 AUTO,
epoch 2 claim → 실제 mTLS 원본까지 PASS다. 최초 양 ACK는 512ms였다.
이전 이미지의 공개·운영·장시간 epoch 검사를 1173fbf의 전체 수용 PASS로 합치지 않는다.

## 복구 관측 잠금 수정

[ADR-0008](../adr/0008-recovery-snapshot.md)은 네트워크 작업 중 복구 mutex가 일반 요청의
예산까지 소비하던 경로를 제거한다. 승인된 불변 스냅샷을 읽고 실제 Valkey fence 검사는
유지한다. [수정 전 RED](beta-20260910-logo-maintenance/snapshot-red.log),
[독립 join·불변 상태·공유 HOLD/fence·취소·쓰기 유실 5회 반복](beta-20260910-logo-maintenance/snapshot-green-5x.log),
[전체 make check](beta-20260910-logo-maintenance/snapshot-source-check.log)를 보존한다.

확장 회귀에서 [기존 테스트의 호스트/VM 시각 차이](beta-20260910-logo-maintenance/snapshot-fixture-clock-failed.log)가
드러났다. 모의 안전 대기 종료는 테스트가 이미 받은 Valkey 시각을 기준으로 바꿨다.
실제 Docker epoch·콜드 복원은 계속 실제 안전 대기를 사용한다.
별도 [유휴 큐 DUMP 바이트 비교 실패](beta-20260910-logo-maintenance/snapshot-fixture-dump-failed.log)는
최초 변경 키를 기록하지 않아 원인을 확정하지 않았다. 동일 검사의 [20회 재실행](beta-20260910-logo-maintenance/snapshot-idle-investigation.log)은
통과했다. 이후 검사는 모든 hash field/value를 순서 독립적으로 비교하고 실제 쓰기
FCALL 수가 증가하지 않는지도 확인한다. 직렬화 순서 가설을 실제 서비스 결함 해결로
표시하지 않는다. 최종 통합 회귀와 이미지 결과는 아래 후속 기록을 따른다.

[수정 후 통합 회귀](beta-20260910-logo-maintenance/snapshot-runtime-final.log)는 race 모드의
최상위 27개 PASS, 별도 primary 재시작 옵션을 요구하는 1개 SKIP다. 5개 seed의
실제 모델 trace, v3/v4 이행과 공유 복구·손상 차단, 만료 정리·응답 유실도 포함한다.
이 테스트의 모의 복구 대기와 실제 Docker 안전 대기 증거는 구분한다.

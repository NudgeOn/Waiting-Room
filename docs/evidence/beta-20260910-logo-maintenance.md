# 2026-09-10 — 로고 배포와 유휴 유지보수

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

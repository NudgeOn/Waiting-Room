# 2026-09-09 최초 browser 접속 복구와 통합 검증

검토 범위는 로컬 Docker, 단일 리전 FIFO의 M1~M3다. HA, 10K/100K 지속 부하
qualification과 공개 release는 포함하지 않는다. 최종 수용 판정은 아래 결과를 구분한다.

## 변경

- 최초 queue 쓰기 전에 암호화한 HttpOnly intent를 발급하고 쿠키 수신을 확인한다.
  join 응답 전체 유실 뒤에도 같은 nonce와 원래 target으로 같은 응답을 재생한다.
- 같은 출처/Room의 Web Locks가 prepare와 최초 응답까지 직렬화한다. 탭들은 대기표를
  공유하고 각 목적지는 별도 return 값으로 보존한다. 만료 후 명시적 재참여도 새 순번 한 건으로 수렴한다.
- 쿠키 차단·지원하지 않는 브라우저·네트워크 실패에는 안내와 키보드 재시도를 제공한다.
  요청별 15초 제한, 최대 query target의 쿠키 크기, prepare OpenAPI 계약을 보완했다.
- Traffic Lab의 HTTP 방문자도 최초 연결 화면과 두 번의 쿠키 확인을 수행한다.
  Quick 20은 123회 요청, Smoke 1K는 3013회 요청이며 기존 FIFO/입장/원본 보호 검사는 유지한다.
- CI의 backend 통합 검사에 Traffic Lab을 추가하고 public browser 33개를 세 엔진에서 실행한다.
- 후보 빌드 명령은 관리자 UI와 두 실행 파일을 함께 빌드해 별도 local tag에 저장한다.
  [실제 빌드](beta-20260909-browser-join/candidate-build.log)는 테스트용 rebuild tag만 만들었다.
  이후 최신 browser 후보도 같은 명령으로 별도 tag에 빌드했다. 실행 중인 설치는 변경하지 않았다.

운영 조건과 제한은 [브라우저 접속](../operators/browser-join.md)을 따른다.

## 고정 통합 이미지

`waiting-room-beta4-integrated:local`:
`sha256:c44911ac0a688a36bd3b7c48a1e46e8a8f269145d00b048a40f744441496622d`.
[identity](beta-20260909-browser-join/identity.json)에 실행 파일과 runtime 소스 hash를 기록했다.
소스는 `fa9b7aa`로 커밋했다. 직전 working tree에서 빌드했으므로 바이너리의 VCS 표시는
부모 `c30c8da`와 modified=true다. 공개 GHCR digest나 공식 배포 artifact로 표현하지 않는다.

최대 query의 Coordinator 전달 문제를 수정한 최신 browser 이미지는
`waiting-room-beta4-browser-final:local` /
`sha256:263f6c9c8179dd600dcc81ba2daa89907676b453fa7952da64db93efb50e5a91`다.
통합 이미지 이후 runtime 변경은 `internal/lab/browser.go`의 JSON 인코더 호출 한 줄이며,
복구 코어는 동일하다. 그래도 아래 통합 이미지의 전체 안전 대기 결과를 이 새 이미지의
전체 안전 대기 PASS로 표현하지 않는다. [최신 빌드](beta-20260909-browser-join/latest-candidate-build.log).

보안 패치 후보 `waiting-room-beta4-patched:local`은
`sha256:31d66395d7bb4bd43c62fc9591b4afe7927586c791d53aa191980018a4c146da`다.
[식별 정보](beta-20260909-browser-join/patched-identity.json)와 [빌드](beta-20260909-browser-join/patched-build.log)에
Go 1.26.8, 커밋 `ddff347`, modified=false 및 두 실행 파일 hash를 기록했다.
이 패치 이미지의 전체 60분 30초 시험은 아직 실행하지 않았다.
[실제 HTTPS](beta-20260909-browser-join/patched-public-runtime.log)는 최대 query·응답 유실·동시 탭,
모바일/키보드/axe, API 제한·claim·키 stage/activate/긴급 폐기, 새 epoch 양 ACK와 HOLD까지 PASS다.
release Dockerfile의 [공식 베이스 manifest](beta-20260909-browser-join/go1268-release-base.log)도 조회했다.
[동일 패치 이미지 운영](beta-20260909-browser-join/patched-operations.log)도 PASS다: 실제 설치 보정,
Control 6초 중단 회복, Quick 20/Smoke 1K, 키 stage/activate 양 ACK, Admin 새 epoch UI,
세 엔진 × 세 역할 × 아홉 화면, 320px/axe/키보드, 32 API 계약 및 재시작 후 결과·감사 보존.
이 실행의 보정은 첫 측정에서 반복 8회/중앙값 287ms로 통과했다.

## 완료한 검사

| 검사 | 결과 | 정확한 범위 |
|---|---|---|
| 전체 소스 | [PASS](beta-20260909-browser-join/source-check.log) | Go vet/race, API, 문서, 관리자 UI, 설치 계약 |
| 공개 browser | [33 PASS](beta-20260909-browser-join/public-browser.log) | Chromium/Firefox/WebKit 대기·claim·첫 응답 유실·동시 탭·최대 2048바이트 query·15초 timeout 뒤 잠금 해제와 재시도 |
| 최종 연결 안내 | [9 PASS](beta-20260909-browser-join/join-browser-final.log) | 모바일 문구 최종 수정, 세 엔진의 360px/axe/키보드 재연결 |
| 관리자 browser | [21 PASS](beta-20260909-browser-join/admin-browser.log) | 실측 calibration/apply/TOTP, Room, Traffic Lab |
| Valkey/HTTP | [PASS](beta-20260909-browser-join/runtime-integration.log) | FIFO/복구/반복 응답·두 Gateway·public guard; 패키지 직렬 실행 |
| Traffic Lab | [PASS](beta-20260909-browser-join/traffic-handshake.log) | 수정된 backend의 실제 Quick 20/Smoke 1K |
| 통합 이미지 운영 | [PASS](beta-20260909-browser-join/operations.log) | 81개 실제 화면·32개 API 계약·역할·재시도·감사·키 회전·Admin 새 epoch ACK |
| 통합 이미지 HTTPS | [PASS](beta-20260909-browser-join/public-runtime.log) | 최초 응답 전체 유실, 동시 5탭, Secure/HttpOnly, API quota/claim/5개 메서드, 키 회전·긴급 폐기 |
| 통합 이미지 백업/이행 | [PASS](beta-20260909-browser-join/backup-runtime.log) | 독립 콜드 복원 세 번, v4 → v5 메타데이터 이행, 원래 대기표 FIFO·claim·원본 도달, 키 overlap/deadline·양 ACK 보존 |
| 최신 HTTP | [PASS](beta-20260909-browser-join/latest-http-integration.log) | 실제 Valkey의 browser HTTP와 Traffic Lab, race 검사 |
| 최신 이미지 HTTPS | [PASS](beta-20260909-browser-join/latest-public-runtime.log) | 최대 2048바이트 query의 응답 전체 유실·동일 표 복구·새로고침, 동시 5탭, 키 회전·quota·긴급 폐기·새 epoch ACK와 HOLD |

운영 시험은 Control을 6초간 pause해 실제 fetch timeout을 만들었다. 유효한 서명 설정으로
HOLD join을 유지했고, unpause 후 새 generation을 양 노드가 재시작 없이 ACK했다.
정제된 fetch deadline/recovered 진단도 기록했다. 과거 원인 미상 ACK 지연과 같은 원인이라고 단정하지 않는다.

Browser plugin not available; 기존 Playwright를 사용했다. 360px 대기/연결 오류 화면과
Quick 20 결과, 360px 새 epoch 검토창을 실제 캡처해 확인했다. 빈 화면·framework overlay·
수평 넘침·자동 WCAG 위반이 없었으며, 주입한 연결 오류 외 예상 밖 console 오류는 없었다.
실제 VoiceOver/NVDA 사용자 acceptance와 임의의 구형/내장 브라우저는 미검증이다.

## 실패와 보완

첫 CI `34310521942`는 [Traffic Lab 방문자 시나리오 실패](beta-20260909-browser-join/ci-first-failure.log)를
잡았다. 첫 응답을 303으로 가정한 시나리오가 새 연결 화면 200을 받았다. 이를 쿠키 확인
절차로 수정한 커밋이 `fa9b7aa`다. HTTP 상태 검사를 느슨하게 바꾸지 않았다.

그 다음 CI의 [남은 실패](beta-20260909-browser-join/ci-portable-path-failure.log)는 Linux에 없는
macOS 전용 `/private/tmp` 캡처 경로였다. 관리자 21개와 공개 24개는 통과했고,
세 엔진의 연결 오류 화면 캡처에서만 실패했다. `os.tmpdir()`로 환경별 임시 경로를 사용하도록
수정했으며, Docker 시험의 같은 하드코딩도 정리했다.

[최대 query 회귀 재현](beta-20260909-browser-join/maximum-query-before.log)은 `&`가 많은
유효한 2048바이트 목적지가 Coordinator 전달 중 JSON escape로 늘어나 본문 제한을 넘는
문제였다. 이 내부 전달에도 compact JSON을 사용했다. 세 엔진 33개와 최신 이미지의 실제
HTTPS 최대 query·응답 유실·새로고침이 수정 후 통과했다. API 본문 제한을 늘리지 않았다.

한 설치 측정에서 password calibration 후보가 206ms 다음 801ms를 기록했다.
목표 범위 미달은 설치를 차단했고, 이후 별도 설치의 실제 재측정이 통과했다. 보정 목표를 낮추지 않았다.
기존 전용 Valkey의 오래된 guard 함수 소스는 불변 함수 검증에서 거부됐고,
빈 16389 fixture에서 통과했다. 기존 함수를 REPLACE하거나 사용자 데이터를 지우지 않았다.

이전 이미지의 [20 epoch/800 명령 스트레스](beta-20260909-browser-join/prior-epoch-stress.log)는
양 ACK 최대 1384ms로 통과했다. 이후 수정 이미지를 검증하기 위해 그 실행을 종료했으므로
그 로그를 전체 60분 30초 안전 대기 PASS로 계산하지 않는다.

## 의존성 보안 후속

2026-09-09 05:08 UTC의 [govulncheck v1.8.0](beta-20260909-browser-join/go-vulnerability-before.log)는
Go 1.26.1 표준 라이브러리에서 호출 경로가 있는 취약점 22건을 보고했다. 실제 악용의
증거는 아니다. [npm 전체 의존성 검사](beta-20260909-browser-join/npm-audit.json)는 0건이다.
[Go 공식 릴리스 기록](https://go.dev/doc/devel/release)에서 확인한 같은 계열의 패치
1.26.8로 go.mod, release Dockerfile 및 소스 설치 안내를 함께 갱신했다.
이후 빌드에는 패치 도구체인이 필요하다. 앞서 실행한 Go 1.26.1 이미지들에는 이 수정이
소급 적용되지 않는다. `make check-security`와 별도 CI job은 Go 배포 소스의 호출 경로와
npm lockfile 전체를 현재 공개 advisory DB로 검사한다. 기존 소스 검사와 별도로 실행한다.
패치 후 [전체 소스 검사](beta-20260909-browser-join/go1268-source-check.log)는 PASS다.
[같은 도구 재검사](beta-20260909-browser-join/go1268-vulnerabilities.log)는 호출 경로 0건,
import한 package 0건이다. require한 module 중 사용하지 않는 경로 1건은 별도로 남는다.
패치 후보 재빌드와 실제 HTTPS는 별도 검증했으며 기존 이미지를 덮어쓰지 않았다.
실제 Linux 바이너리 [Control](beta-20260909-browser-join/patched-control-vulnerabilities.log)과
[Node](beta-20260909-browser-join/patched-node-vulnerabilities.log)의 바이너리 모드 검사도
호출 경로 0건이다. 사용하지 않는 module 경로 1건을 포함해 "모든 의존성 취약점 0건"으로
표현하지 않는다.

## 후속 운영 시험의 실패 보존

최신 browser 이미지의 첫 운영 재검사는 Chromium Viewer 마지막 화면 이후 정체되어
[미완료로 보존](beta-20260909-browser-join/latest-operations-incomplete.log)했다. 강제 종료 전에
서비스 컨테이너는 모두 healthy였다. SIGTERM 뒤 남은 검사 프로세스가 setup 포트를
점유해 [다음 실행은 포트 충돌](beta-20260909-browser-join/latest-operations-port-failure.log)로
실패했다. 해당 PID를 확인해 종료했으며 사용자 프로세스나 실제 안전 대기 검사는 건드리지 않았다.

그 다음 별도 fixture는 설치·Control 중단 회복·Quick 20/Smoke 1K까지 통과했지만
[키 전환 후 ACK 대기](beta-20260909-browser-join/latest-key-ack-failure.log)에서 실패했다.
Coordinator가 `stage=apply code=unavailable generation=0`을 기록했다. 재시작과 반복 PASS로
해결된 것으로 처리하지 않는다. 이전 Admin 새 epoch ACK 지연과 동일 원인인지도 미확정이다.
브라우저 단계별 keyboard/teardown 진단과 key stage/activate별 ACK 상태를 추가했다.
이 진단 로그는 제품 변경이 아니다.

패치 이미지 첫 운영 실행은 [실제 calibration 목표 미달](beta-20260909-browser-join/patched-calibration-failure.log)로
설정을 적용하지 않고 종료했다. 후속 검사기는 위자드의 기존 다시 측정 동작을 최대 세 번
수행하고 모든 후보 중앙값을 남긴다. 250~500ms·64 MiB·반복 횟수 제한은 바꾸지 않았으며
목표를 충족한 검토 결과만 적용한다.

## 최종 검증과 출시 판정

패치 이미지의 [콜드 복원·이행 전체](beta-20260909-browser-join/patched-backup-runtime.log)는 PASS다.
독립 복원 세 번, v4 → v5 메타데이터 이행, 원래 대기표의 FIFO/응답/claim/원본 도달,
회전 키·retirement deadline·양 ACK 보존을 확인했다. patch image의 runtime 소스 hash는
빌드 이후 변경되지 않았다. 사용자 설치와 기존 볼륨은 변경하지 않았고, 이번 전용 실행
컨테이너 및 임시 16389 Valkey는 종료했다. 전용 백업·fixture 볼륨은 보존했다.

통합 코어 이미지 c44911ac의 [전체 epoch 여정](beta-20260909-browser-join/integrated-epoch-full.log)도 PASS다.
2026-09-09 05:33:49.531 UTC까지 실제 60분 30초와 121회 안전 관측을 거쳤다.
bounded validation → HOLD → 명시적 AUTO → epoch 2 claim → 실제 mTLS 원본 입장을 확인했다.
이 이미지는 Go 1.26.1이다. Go 1.26.8 패치 이미지에서 같은 전체 시간을 다시 검증한 결과는 아니다.

Go 패치 전 `9df9373`의 [Linux CI](https://github.com/NudgeOn/Waiting-Room/actions/runs/34313083764)는 PASS다.
패치 커밋 `ddff347`의 [원격 CI](https://github.com/NudgeOn/Waiting-Room/actions/runs/34314076305)도
네 job 모두 PASS다: foundation, Valkey, PostgreSQL/3엔진 browser, dependency-security.
[CI 식별 정보](beta-20260909-browser-join/patched-ci.json)에 소스 SHA와 run ID를 남겼다.
이후 변경은 검사기의 진단/재측정과 문서·증거이며 배포 runtime 소스는 동일하다.

2026-09-09 [키 회전 후속](beta-20260909-key-recovery.md)에서 보존 데이터의 150초 안전 대기와
조기 ACK 실패를 확인하고 수정했다. 원본 복사본의 실제 161.5초 후 같은 키 세대 양 ACK,
최신 Go 1.26.8 이미지의 전체 60분 30초/121회 epoch 검사도 PASS다. 위 실패 기록은 보존한다.
VoiceOver는 사용자 요청으로 이번 Beta 범위에서 제외하며 검증 PASS를 의미하지 않는다.

**Beta NO-GO 유지.** 남은 과거 원인 미상 기록을 반복 PASS나 명칭 변경으로 닫지 않는다.

| Gate | 남은 조건 |
|---|---|
| B0 | 과거 5K 503의 problem code와 최초 상태가 없어 원인 미확정 |
| B1 | 이전 Admin epoch ACK 지연의 개별 원인 미확정. 이번 키 전환의 조기 ACK 실패는 보존 데이터 복사본으로 원인 확인·수정·회귀 PASS |
| B5 | Go 1.26.8 최종 이미지 전체 60분 30초 PASS. VoiceOver는 사용자 요청으로 이번 Beta에서 제외 |
| B6 | 위 조건을 포함한 M1~M3 전체 수용 판정; 공개 Beta release는 만들지 않음 |

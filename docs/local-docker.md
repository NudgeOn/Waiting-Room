# 로컬 Docker 설치 — Waiting Room Beta 개발 후보

**현재 Beta NO-GO: 출시 검증 진행 중.** 영속 Control, Gateway, Coordinator, Valkey와
보호된 demo origin을 함께 실행한다. 저장 초안과 실제 배포 상태는 구분한다.
10K/100K 성능 인증 또는 공개 인터넷 배포용 구성이 아니다.
최종 판정은 [MAIN PRD](main-prd.md), 진행 기준은 [Beta 계획](beta-plan.md)이다.

## 1. 설치

저장소 루트에서 실행한다. Docker Compose, go.mod의 Go 버전, Node 22.12 이상,
시스템 공개 PEM CA bundle이 필요하다. 현재 배포 형태는 소스 빌드다.
Docker 데몬의 CPU 아키텍처에 맞춰 Linux 바이너리를 빌드한다.

```sh
npm ci --ignore-scripts
node scripts/local-beta.mjs build
node scripts/local-beta.mjs init on
node scripts/local-beta.mjs up
node scripts/local-beta.mjs bootstrap
node scripts/local-beta.mjs token
node scripts/local-beta.mjs setup
```

최초 TOTP를 끄려면 `init off`를 선택한다. `init` 재실행으로 정책을 바꿀 수 없다.
이미 설치한 경우 아래 업그레이드 절차를 사용한다.

`token`은 로컬 터미널에 최초 설정 토큰을 표시한다. 화면 공유·로그 수집을 끄고 사용한다.
`bootstrap`은 이전 토큰을 폐기하고 15분짜리 토큰을 발급하며, 첫 관리자가 있으면 거부된다.
일반 시작 시 계정·비밀번호·bootstrap 토큰을 자동으로 만들지 않는다.

- 최초 설정: `https://127.0.0.1:19444/setup` — setup 터널 실행 중에만 접근.
- 관리자: `https://127.0.0.1:19443`
- 고객 Gateway: `https://127.0.0.1:20443`

TLS는 로컬 자체서명이다. 해당 로컬 주소의 인증서 경고만 확인하고,
시스템 전체 인증서 검증을 끄지 않는다. 인증서 유효기간은 1년이다.
TOTP ON 설치는 등록과 복구 코드 보관을 마친 뒤 설정 포트에서 로그아웃하고 관리자 포트로 로그인한다.
설정 후 setup 터미널에서 Ctrl-C로 터널을 닫는다.

## 2. 첫 대기열 운영

관리자 → **Room 초안 관리** → **새 Room 초안**에서 로컬 demo 연결을 입력한다.

| 항목 | 로컬 시험값 |
|---|---|
| Room ID / 표시 이름 | `my_sale` / 원하는 이름 |
| 고객 호스트 | `127.0.0.1` |
| 원본 HTTPS 주소 | `https://demo-origin:20445` |
| 원본 상태 확인 URL | `https://demo-origin:20445/health` |
| 보호 경로 | `/shop` |
| 최대 활성 입장권 / 분당 입장 | `3` / `60` |
| 입장권 유효 시간 | `60`초 |

보호 활성화 체크 후 **초안 저장** → **실시간 운영** → **저장 초안 배포**를 누른다.
Gateway와 Coordinator가 같은 generation을 확인하면 “두 서비스 적용 확인”이 표시된다.
첫 활성화는 HOLD다. 새 시크릿 창으로 `https://127.0.0.1:20443/shop`에 들어가
대기 화면을 확인한 다음, 관리자에서 해당 Room의 **AUTO 시작**을 누른다.

- HOLD: 신규 입장 일시정지. 이미 발급된 유효 입장권은 원래 만료 시각까지 사용 가능.
- AUTO: FIFO와 설정한 분당 유량·활성 입장권 상한 안에서 입장.
- 안전 종료: 신규 대기는 막고 기존 대기자를 처리한다. 원본 상태·5분 유입 관측 등
  조건이 충족돼야 OFF가 된다. 즉시 종료 버튼이 아니다.
- 재인증 후 즉시 OFF: 보호를 해제하므로 대기자도 원본으로 접근할 수 있다.
- 예약: 표시된 시간대의 일정을 UTC로 저장한다. 생성·수정·취소·재개를 지원한다.
  수동 모드 변경은 예약을 일시정지하며 자동으로 다시 시작하지 않는다. 취소도 현재 모드를 바꾸지 않는다.
  입력은 미래 366일 이내 사전 대기 → 입장 → 안전 종료 순서다. 편집 중 revision이 바뀌면
  입력을 보존하고 충돌을 안내한다. 최신 기준으로 다시 편집하려면 입력 초기화/수정 취소를 선택한다.
- 유량 편집 중 revision이 바뀌면 최신 값을 다시 불러오거나 충돌을 확인한다.

`demo-origin`은 이 로컬 설치에만 있는 고정 mTLS 시험 원본이다.
일반 origin은 유효한 공개 HTTPS 인증서와 허용된 공개 DNS 주소가 필요하다.
loopback·사설 IP·metadata·다른 host redirect는 일반 원본으로 허용하지 않는다.
원본 직접 접근 차단은 고객의 방화벽/mTLS 설정까지 따로 검증해야 한다.

앱은 같은 Gateway의 JSON `join → status → admissions` API를 사용한다.
대기 응답의 `pollAfterMs`와 `heartbeatAfterMs`를 따르고, 503일 때 새 대기표를 자동 생성하지 않는다.
[Public API](../api/openapi/public-v1.yaml)를 참고한다.

## 3. 사용자와 TOTP

Room 초안 관리 → **사용자 · 보안 설정**에서 계정 생성·역할 변경·비활성화·삭제·TOTP 초기화를 한다.
민감한 변경은 현재 비밀번호 및 정책에 따른 TOTP 재인증이 필요하다.
마지막 활성 Admin은 삭제·비활성화·강등할 수 없다.

TOTP OFF 설치에서도 **ON 전 현재 Admin TOTP 등록**으로 먼저 본인의 키를 등록할 수 있다.
등록만으로 설치 전체 정책이 ON이 되지는 않는다. ON/OFF 변경은 새 TOTP를 요구한다.
실제 정책 변경은 다른 세션을 모두 폐기하고 현재 Admin의 세션과 CSRF를 교체한다.
응답을 받지 못했다면 다시 로그인해 현재 정책부터 확인한다.
복구 코드는 한 번만 표시하므로 안전하게 보관한다. QR/키/코드를 공유하지 않는다.

## 4. 정지와 업그레이드

```sh
node scripts/local-beta.mjs status
node scripts/local-beta.mjs stop
node scripts/local-beta.mjs up
```

업그레이드는 운영 중인 로컬 서비스를 정지한다. 먼저 일정을 확인하고,
같은 설치의 DB·키·secret 세트를 안전하게 보관한 후 실행한다.

```sh
node scripts/local-beta.mjs build
node scripts/local-beta.mjs upgrade
node scripts/local-beta.mjs up
```

`upgrade`는 기존 키 binding과 migration checksum을 검증하고, 현재 TOTP 정책·계정·초안을 유지한다.
알려진 이전 생성 ACL만 명시적으로 교체한다. 임의 수정된 ACL/키를 덮어쓰지 않는다.
실패 시 볼륨을 삭제하지 않고 서비스를 정지 상태로 유지한다.

**이전 v3 runtime queue가 이미 있는 설치는 v4로 자동 변환하지 않는다.**
해당 queue 상태의 안전한 migration 절차는 아직 출시 차단 항목이다.
데이터를 삭제하거나 schema 번호를 직접 바꿔 시작하지 않는다.
초안만 저장한 이전 Control 설치와 이미 실행한 queue의 업그레이드는 다른 경우다.

## 5. 장애 복구 표시

Valkey primary 변경·불확실한 쓰기·상태 불일치 시 RECOVERY_HOLD로 신규 입장을 금지한다.
공유 안전 대기는 최소 90초이며, 이전에 설정된 더 긴 입장/READY TTL이 있으면 더 길어진다.
안전 시각이 지나도 ticket·lease·인덱스·rate 불변조건 검증을 통과해야 재개한다.
원래 WAITING 순서는 보존하고, 기존 입장권 만료는 연장하지 않는다.

검증 실패는 자동 재구성으로 숨기지 않는다. 새 epoch 운영 복구는 아직 미완료다.
감사 로그는 “안전 대기 시작 / 검증 후 복구 / 복구 검증 실패”를 구분한다.

## 6. 데이터와 보안 경계

- 프로젝트: `waiting-room-local-beta`. 기존 lab과 다른 네트워크/볼륨이다.
- PostgreSQL database 볼륨: 계정·세션·암호화 TOTP·초안/배포·예약·명령 재시도·감사.
- state/identities 볼륨: 설치 키·인증서·role별 최소 키와 자격증명.
- queue-data: Valkey AOF runtime. gateway-data/coordinator-data: 서명된 LKG.
- `.cache/local-beta/secrets/`: owner/runtime DB 비밀번호. Git과 이미지에서 제외된다.
- runtime은 non-root UID 65532, 읽기 전용 루트, capability 제거. Gateway에
  DB/Valkey 비밀번호와 signing private key를 주지 않는다.
- DB/Valkey는 호스트 포트를 게시하지 않는다. Admin/Gateway는 호스트 127.0.0.1에만 게시한다.
- setup은 실제 컨테이너 loopback에 Docker exec로 TLS 바이트만 전달한다.
- Valkey는 noeviction 및 AOF `appendfsync always`다. 처리량은 별도 측정해야 한다.
- Valkey 8.1.6의 [AOF/ACL 오류](https://github.com/valkey-io/valkey/issues/3983)를 피하기 위해
  기본 사용자를 `off resetpass`로 유지하면서 내부 AOF 재생 권한을 부여한다.
  기본 사용자 네트워크 로그인은 허용되지 않는다.
- PostgreSQL 연결은 격리된 로컬 네트워크에서만 평문이다. 공용 Docker 호스트나
  클라우드/공유 네트워크에 이 Compose 구성을 그대로 사용하지 않는다.
- Docker 권한이 있는 사용자는 설치 관리자 권한을 가진다.

정지/재생성/업그레이드는 볼륨을 삭제하지 않는다. reset/uninstall 명령은 제공하지 않는다.
DB와 키/secret을 잃으면 TOTP 등을 복원할 수 없다.
암호화된 백업/복원 도구·키/인증서 갱신은 아직 별도 출시 검증 항목이다.

## 7. 로컬 검증

다음 테스트는 사용자 설치가 아닌 임시 프로젝트를 만든다.
19443/20443 포트 충돌을 피하려면 사용자용 설치를 먼저 정지한다.
시험 컨테이너와 네트워크는 종료 시 해제하고, 진단용 볼륨과 secrets는 보존한다.

```sh
node scripts/local-beta.mjs stop
WR_TEST_LOCAL_BETA=local node test/localbeta/run.mjs
WR_TEST_LOCAL_BETA=local WR_TEST_LOCAL_SECURITY=1 WR_TEST_LOCAL_RECOVERY=1 node test/localbeta/runtime-quick.mjs
```

Playwright Chromium이 이미 설치되어 있어야 한다. 저장소 전용 브라우저 캐시를 쓰면
`PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright"`를 함께 설정한다.
브라우저/앱 입장·실제 재시작·보안 정책·복구 대기를 구분해 기록한다.
1K/2K/5K/10K 부하 시험은 별도 테스트이며 위 기능 E2E 통과가 해당 인원 성능 인증은 아니다.

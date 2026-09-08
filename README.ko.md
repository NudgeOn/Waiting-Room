# NudgeOn Waiting Room

<p align="center">
  <a href="https://github.com/NudgeOn/Waiting-Room/actions/workflows/m0.yml"><img src="https://github.com/NudgeOn/Waiting-Room/actions/workflows/m0.yml/badge.svg" alt="CI" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License: Apache-2.0" /></a>
  <img src="https://img.shields.io/badge/status-preview-orange.svg" alt="status: preview" />
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8.svg?logo=go&logoColor=white" alt="Go 1.26" />
</p>

<p align="center">
  <img src="docs/releases/social-preview.png" alt="NudgeOn Waiting Room 설치와 운영" width="960" />
</p>

<p align="center">
  <a href="README.md">English</a> · <b>한국어</b>
</p>

**NudgeOn Waiting Room은 웹사이트와 앱을 위한 오픈소스 셀프호스트 대기열입니다.**

기존 사이트나 API 앞에 설치합니다. 한정 판매, 티켓 오픈, 회원 신청처럼 접속이 몰리는 순간에 방문자를 선착순 대기열로 받아 두고, 운영자가 정한 속도로 입장시킵니다. 원본 서비스는 감당할 수 있는 만큼만 요청을 받고, 운영자는 터미널이 아닌 관리자 화면에서 대기열을 제어합니다. Apache-2.0이며 외부 telemetry가 없습니다.

> ⚠️ **Beta가 아닌 Preview입니다.** 대기열 코어, 입장 토큰, 관리자 인증(TOTP·복구 코드·RBAC), Docker 재시작에도 유지되는 로컬 Control은 구현되어 있고 테스트를 통과합니다. 로컬 Gateway·운영 Dashboard·prebuilt 설치 CLI를 제공하며, 현재 소스 후보에는 설치 적용 위자드·Traffic Lab·명령 재시도/감사·복구 도구를 추가했습니다. Production 설치, 과거 간헐 503 원인 규명과 전체 acceptance, 10K/100K qualification은 남아 있습니다. [Main PRD](docs/main-prd.md)의 모든 delivery gate는 아직 **NO-GO**입니다. 실제 트래픽 앞에 두지 마세요.

## 지금 할 수 있는 것

- **엄격한 FIFO 대기열**: capacity lease 상한과 최근 60초 입장 rate를 Valkey Function 하나 안에서 원자적으로 판정합니다.
- **브라우저와 앱 연동**: 웹은 cookie + 303 redirect, 앱은 JSON `join → poll → claim` API. 두 경로가 복귀 key를 공유합니다.
- **서명된 입장 토큰**(Ed25519): Gateway가 로컬에서 검증하며 raw ticket은 저장하지 않습니다.
- **기본 fail-closed**: 서명 설정이 없거나 primary가 없거나 저장소가 불확실하면 우회가 아니라 대기입니다.
- **관리자 인증**: Argon2id 비밀번호, RFC 6238 TOTP, 일회성 복구 코드, `__Host-` 세션 쿠키, Origin 고정 CSRF, Admin/Operator/Viewer 역할.
- **영속 로컬 Control**: 최초 관리자 bootstrap, Room 초안, 서명 publish, AUTO/HOLD/안전 종료, 일회성 예약, 재시작 후에도 남는 감사 로그.
- **설치 계획 CLI**(`wrctl plan`, `wrctl estimate`, `wrctl preview`): 오프라인 10K/100K profile 검증과 비용 비교.
- **`calm` 대기 화면**: 새로고침해도 순서가 유지되는 내장 한국어/영어 대기 페이지. 도입 고객 브랜드를 앞세울 수 있습니다.

관리자 **Traffic Lab**에서 Quick 20·Smoke 1K를 실행하고 결과·방문자 순번·JSON을 확인합니다. [사용법과 범위](docs/operators/traffic-lab.md): 고정 샘플 원본의 기능 검사이며 production URL 시험이나 처리 용량 인증을 대신하지 않습니다.

<p align="center">
  <img src="docs/design/calm-concept.png" alt="calm 대기 화면: 순서를 기다리고 있어요, 입장 상태 대기 중, 예상 대기 시간 계산 중" width="640" />
</p>

![Local operator dashboard demo](docs/releases/demo.gif)

실제 로컬 Docker 실행에서 캡처한 HOLD → AUTO → ADMITTED 흐름입니다. 소규모 기능 데모이며 부하 성능 검증은 아닙니다.

## 동작 구조

![Waiting Room 런타임 아키텍처: 방문자 → Gateway → Coordinator → Valkey, Gateway가 입장 요청을 원본으로 proxy, Control과 PostgreSQL이 서명 설정을 배포](docs/architecture/runtime-architecture.svg)

- **주 경로**: 브라우저·앱 → Gateway → Coordinator → Valkey. 방문자는 `WAITING → READY → ADMITTED`로 이동하고, Coordinator는 100ms마다 lease cap과 최근 60초 rate 안에서 대기열 선두를 promote합니다. 이 판정은 Valkey Function 하나에서 원자적으로 끝납니다.
- **입장 요청**은 Ed25519 토큰을 가지고 오며, Gateway가 로컬에서 검증한 뒤 Valkey를 거치지 않고 원본으로 proxy합니다. 클라이언트가 보낸 Waiting Room 헤더와 `__Host-wr` 쿠키는 먼저 제거되므로 원본은 유효한 입장을 가진 요청만 받습니다.
- **신뢰 경계**: 외부 방문자 / 공개 에지(Gateway) / 내부 평면(Coordinator, Valkey, Control, PostgreSQL. Docker 내부 네트워크와 역할별 mTLS identity) / 고객 원본 / loopback 전용 관리자.
- **제어 평면**: 관리자 화면은 세션 쿠키, TOTP, CSRF 토큰으로 Control과 HTTPS 통신합니다. Control은 계정·세션·설정 revision·감사 로그를 단일 transaction으로 PostgreSQL에 쓰고, runtime 설정은 Ed25519로 서명한 snapshot으로만 배포합니다. Gateway와 Coordinator는 서명 없이는 서비스하지 않습니다.
- **운영 모드**는 `OFF`(통과), `AUTO`(cap과 rate 안에서 입장), `HOLD`(기존 입장은 통과, 신규 promotion 중지)와 내부 `DRAINING`, `RECOVERY_HOLD`입니다. 10K/100K는 설치 전체의 `WAITING + READY + ADMITTED` 합에 대한 hard cap이며 동시 TCP 연결 수가 아닙니다.
- **인터랙티브 버전**: [`docs/architecture/runtime-architecture.html`](docs/architecture/runtime-architecture.html)을 브라우저에서 열면 검색, 경로 추적, 코드 소스 링크, 다크 모드, PNG/SVG 내보내기를 쓸 수 있습니다. 다이어그램은 [`runtime-architecture.json`](docs/architecture/runtime-architecture.json)에서 생성되며, 소스 참조는 렌더링 시 저장소와 대조해 검증됩니다.

## 빠르게 시작하기

[v0.1.0-preview.1](https://github.com/NudgeOn/Waiting-Room/releases/tag/v0.1.0-preview.1)에서 OS와 CPU에 맞는 CLI를 받고 `SHA256SUMS`를 확인한 뒤 압축을 풉니다. **Docker와 Compose만 있으면 됩니다.** Go·Node.js 설치나 저장소 복제가 필요하지 않습니다.

```sh
./wrctl install         # 고정된 GHCR 이미지 다운로드 + 로컬 실행
./wrctl setup           # 일회용 관리자 등록 토큰 + 설정 터널
```

새 위자드가 포함된 runtime에서는 [설치 설정·서버 보정·계획 적용](docs/operators/setup-wizard.md)을 먼저 완료합니다. 첫 관리자와 인증 앱 등록을 마친 뒤 `https://127.0.0.1:19443`에서 콘솔을 엽니다. Gateway는 `https://127.0.0.1:20443`입니다. 자체서명 TLS를 사용하며, 이번 Preview는 loopback에만 열리는 로컬 데모 구성입니다.

```sh
./wrctl status
./wrctl stop
./wrctl up
# 다음 릴리스 CLI를 받은 뒤 설치 상태를 백업하고:
./wrctl upgrade
```

업그레이드는 서비스 중단을 수반하며 기존 볼륨·키·계정·Room 설정을 보존합니다. migration 실패 시 동일 업그레이드를 재시도합니다. 공개 Preview의 자동 rollback은 제공하지 않습니다. 현재 소스 후보는 검증된 콜드 백업과 v3/v4 → v5 명시적 이행을 추가했습니다. 버전 조건과 빈 새 설치 복원은 [복구·업그레이드](docs/operators/recovery-upgrade.md)를 따릅니다. macOS CLI는 서명·공증되지 않았습니다. [CLI 시작 안내](docs/releases/quick-start.md), [소스 빌드 및 로컬 Docker 안내](docs/local-docker.md)를 참고하세요.

Dashboard에서는 **WAITING / READY / ADMITTED, 최근 60초 입장 배정, 원본 상태, Gateway HTTP 5xx 오류, HOLD/AUTO**를 확인하고 제어합니다. 오래되거나 누락된 관측을 정상 또는 0으로 표시하지 않습니다.

설치 계획은 규모 → 환경 → 유량 → 보안 → 검토 → 선택적 비용 비교의 6단계, Room 생성은 연결 → 경로 → 유량 → 대기 화면 → 검토의 5단계로 안내합니다.

## 저장소 구조

```
cmd/
  wr-control/      영속 로컬 Control(관리자 API, 서명 publish)
  wr-node/         Docker runtime의 Gateway / Coordinator / demo-origin 역할
  wr-lab/          단일 프로세스 대기열 lab
  wr-process-lab/  다중 프로세스 Gateway + Coordinator lab
  wr-admin-lab/    loopback TLS 위의 일회용 관리자 UI lab
  wrctl/           설치 계획, 비용 추정, 시계 진단
internal/
  queue/model      단일 스레드 reference model과 랜덤 불변조건 테스트
  queue/valkeystore Valkey Functions 저장소(runtime_v3.lua)
  admission/       Ed25519 입장 토큰
  waiting/         template registry와 calm 대기 화면
  adminauth/       Argon2id, TOTP, 복구, 세션, CSRF, RBAC, PostgreSQL store
  localcontrol/    설치 상태, migration, identity, 서명 설정
  runtimeplane/    Gateway와 Coordinator HTTP 역할
  configtrust/     서명 설정 검증
  installplan/     10K/100K profile 검증, preflight, preview
apps/admin/        React 관리자 UI
api/openapi/       공개·관리자 API 계약
deploy/            Docker Compose 파일과 Control Dockerfile
docs/              PRD, ADR, threat model, 운영 가이드, evidence 번들
test/              contract, 브라우저, schema 테스트
```

## 로드맵

| 단계 | 목표 | 상태 |
|---|---|---|
| M0 | 계약, threat model, test skeleton | ✅ 완료 |
| M1 | Valkey 위 FIFO walking skeleton, 앱·브라우저 lab | 🟡 부분 |
| M2 | 안전한 Gateway alpha: fail-closed, last-known-good 설정 | 🟡 부분 |
| M3 | 운영 가능한 Beta: 관리자 UX, TOTP/RBAC, 예약, 감사 | 🟡 진행 중 |
| M4 | Docker Compose 기반 Standard 10K RC | ⬜ |
| M5 | Helm 기반 High Scale 100K RC | ⬜ |
| M6 | v1.0 GA | ⬜ |

v1은 단일 리전 FIFO만 제공합니다. lottery·priority·weighted 정책은 API 재설계 없이 추가할 수 있도록 algorithm registry에 자리를 잡아 두었습니다. multi-region, 공식 mobile SDK, CAPTCHA, 관리형 SaaS는 v1 범위 밖입니다.

## 문서

- [Main PRD와 출시 gate](docs/main-prd.md) — 여기서 시작합니다. 영역별 세부는 `docs/sub-prd_0x.md`가 source of truth입니다.
- [Beta 계획](docs/beta-plan.md)과 [개발 기록](docs/evidence/beta-development.md)
- [Threat model](docs/security/threat-model.md)과 [failure matrix](docs/security/failure-matrix.md)
- [아키텍처 결정 기록](docs/adr/)
- [운영 가이드](docs/operators/) — lab, 설치 계획, 비용 추정, 시계 진단
- [대기 화면 template 가이드](docs/design/calm.md)
- [Evidence 번들](docs/evidence/) — PRD의 모든 PASS는 여기의 원본 로그를 가리킵니다. unit test 통과는 production 자격이 아닙니다.

## NudgeOn 제품군

NudgeOn Waiting Room은 [NudgeOn](https://github.com/NudgeOn/nudgeon-platform) 제품군의 독립 제품입니다. 고객 행동 메시징 플랫폼 NudgeOn과 브랜드, 그리고 "위자드로 쉽게 시작하고 우리 인프라에서 직접 운영한다"는 약속을 공유하지만 설치와 실행은 독립적입니다. 대기열을 쓰기 위해 메시징 플랫폼을 설치할 필요는 없습니다.

## 기여와 보안

[CONTRIBUTING.md](CONTRIBUTING.md)와 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)를 참고하세요. 취약점은 [SECURITY.md](SECURITY.md)의 절차로 알려 주시고, 공개 이슈로 올리지 말아 주세요.

## 라이선스

[Apache-2.0](LICENSE). 서드파티 고지는 [NOTICE](NOTICE)에 있습니다.

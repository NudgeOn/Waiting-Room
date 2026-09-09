# 미리 빌드한 이미지로 로컬 Preview 설치

릴리스의 `wrctl` 바이너리와 Docker만 있으면 설치할 수 있다. 사용자 컴퓨터에서 저장소를
복제하거나 Node·Go로 앱을 빌드하지 않는다. 바이너리에 Compose 정의와 해당 릴리스의
GHCR 이미지 digest가 포함된다. 공개된 릴리스 artifact가 전제이며, 소스 빌드의
`wrctl`에는 기본 이미지가 없다. 로컬 개발 Compose 및 데이터와는 별도 설치다.

이 명령은 **로컬 Preview**를 실행한다. 공개 인터넷 배포, Standard/High Scale production
installer 또는 10K/100K 성능 인증으로 취급하지 않는다. 현재 Gateway·Coordinator·Control,
PostgreSQL·Valkey 및 보호된 demo origin을 실행한다.

## 설치와 첫 관리자

Linux 또는 macOS에서 로컬 Linux Docker daemon과 Docker Compose 2.35 이상을 준비한다.
Docker 엔진은 amd64 또는 arm64여야 한다. 원격 TCP/SSH Docker context는 지원하지 않는다.
공개된 릴리스에서 OS/CPU에 맞는 `wrctl`과 checksum을 내려받아 확인한 다음 실행한다.

```sh
wrctl --version
wrctl install
wrctl setup
```

`install`은 고정 digest의 이미지를 pull하고 source·revision·runtime 계약·OS/CPU를 검사한다.
private 설치 파일과 비밀번호를 만들고 DB/queue를 초기화한 뒤 서비스 healthcheck가 모두
통과할 때까지 기다린다. 관리자나 bootstrap token은 자동 생성하지 않는다.

초기 관리자 보안은 TOTP ON이 기본이다. 최초 설치에서 끄려면 `wrctl install --totp off`를
명시한다. 기존 설치의 정책은 `install` 재실행으로 바꾸지 않으며 관리자 화면에서 변경한다.

`setup`은 첫 관리자 등록을 위해 15분짜리 일회용 token을 발급하고 터미널에 한 번 표시한다.
이전 bootstrap token은 폐기된다. 터미널을 공유하거나 로그로 수집하지 않는다.
`https://127.0.0.1:19444/setup`에서 token을 입력하고 [설치 위자드](setup-wizard.md)의
설정·Control 서버 보정·계획 검토·적용을 마친 뒤 관리자를 등록한다. 이 흐름이 포함된
runtime 이미지가 필요하며 이전 Preview 이미지는 관리자 등록 화면만 제공한다. 첫 Admin이 이미 있으면
backend가 재발급을 거부한다. 설정 후 Ctrl-C로 터널을 닫고
`https://127.0.0.1:19443`에 로그인한다. TLS는 로컬 자체서명이며 시스템 전체 인증서
검증을 끄지 않는다.

필요한 경우 분리된 `wrctl bootstrap`과 `wrctl token`도 사용할 수 있다. 일반 `up`이나
`upgrade`는 token을 발급·출력하지 않는다. setup port는 Docker에 게시하지 않으며,
명시적으로 실행한 터널만 container loopback으로 TLS 바이트를 전달한다.

## 실행, 정지, 업그레이드

```sh
wrctl status
wrctl status --json
wrctl stop
wrctl up
```

`up`은 기존 이미지와 비밀번호를 유지한다. 로컬에 보관된 이미지가 있으면 이를 검사하며,
없는 이미지만 같은 고정 digest로 받는다. `status --json`에는 `schemaVersion`, `version`,
`project`, `image`, `phase`, 선택적 `pendingImage`, `services[].service/state/health`만 담긴다.
비밀번호·token·Docker 명령 원문은 넣지 않는다.

업그레이드 전에 같은 설치의 Docker volume과 private 설치 디렉터리를 함께 안전하게
백업한다. 현재 소스의 `wrctl upgrade`는 콜드 백업을 검증한 뒤 이행하지만, 공개된
`v0.1.0-preview.1` 바이너리에는 이 기능이 없다. 사용할 바이너리/이미지의 기능을 확인한 뒤:

```sh
wrctl upgrade
```

새 이미지를 pull/검사한 후 application과 Valkey를 정지하고, 기존 identity binding·migration
checksum·queue schema를 검증한다. 새 버전이 건강하게 시작한 뒤에만 현재 이미지 기록을
바꾼다. 설치 프로젝트·volume·비밀번호·현재 TOTP·계정·초안을 보존한다. 명령은 volume을
삭제하거나 기존 자격증명을 재발급하지 않는다. 업그레이드에는 중단 시간이 있다.

pull 또는 이미지 metadata 검사 실패는 실행 중인 서비스를 정지하지 않는다. migration이나
healthcheck 실패 뒤에는 `phase: upgrading`과 목표 `pendingImage`가 남는다. 이때 `up`으로
이전 이미지를 실행하지 못하게 막고, **동일한 `wrctl upgrade`를 재시도**하도록 한다.
자동 rollback은 제공하지 않는다. 현재 소스는 명시적 `queue-upgrade`로 검사한 v3/v4
데이터를 schema 5로 이행한다. 이전 공개 이미지의 거부 동작과 구분한다.
실패를 없애려고 queue schema를 편집하거나 volume을 지우지 않는다.

## 경로와 이미지 선택

기본 설치 위치는 Go의 사용자 설정 디렉터리 아래 `waiting-room`이다. 일반적으로 Linux는
`~/.config/waiting-room`, macOS는 `~/Library/Application Support/waiting-room`이다.
사용자 지정 위치는 모든 명령에 같은 `--directory`로 전달한다.

```sh
wrctl install --directory ./waiting-room-data
wrctl setup --directory ./waiting-room-data
wrctl status --json --directory ./waiting-room-data
wrctl upgrade --directory ./waiting-room-data
```

기존 비어 있는 디렉터리를 쓸 때는 mode 0700이어야 한다. 비어 있지 않은 unmanaged
디렉터리, symlink, 공개된 secret 파일, 사라진 비밀번호, 변경된 생성 Compose는 거부한다.
단일 설치의 동시 lifecycle 명령은 OS lock으로 막는다. 설치가 중단됐을 때 `install`은 기존
이미지·초기 TOTP·비밀번호를 그대로 사용해 재시도한다.

특정 공개된 runtime을 선택할 때만 다음처럼 **실제 공개된 digest**를 전달한다.

```text
wrctl install --image ghcr.io/nudgeon/waiting-room@sha256:<published-64-character-digest>
wrctl upgrade --image ghcr.io/nudgeon/waiting-room@sha256:<published-64-character-digest>
```

floating tag, 다른 저장소, 임의 명령·secret 인수는 받지 않는다. 이미지 pin과 OCI label
확인은 선택한 artifact와 runtime 호환성을 확인하는 절차다. 이미지 서명 검증이나
HA·성능 qualification의 증거는 아니다. 릴리스 빌드는 `DefaultImage`에 발행된 digest,
`Version`에 릴리스 버전을 주입한다.

## 유지되는 격리와 검증 범위

prebuilt Compose는 [기존 로컬 runtime](../local-docker.md)의 non-root/read-only 실행,
role별 identity volume, 내부 DB/queue 네트워크, loopback 공개 포트, pinned PostgreSQL·Valkey를
유지한다. 설치마다 랜덤 Compose 프로젝트를 사용하므로 기존 `waiting-room-local-beta`의
volume을 재사용하거나 자동으로 가져오지 않는다. 두 설치의 기본 포트는 같으므로 동시에
실행하려면 하나를 먼저 정지해야 한다.

설치 디렉터리의 `installation.json`은 이미지/진행 기록, `compose.yaml`은 생성된 실행 정의,
`secrets/`는 private DB 자격증명이다. 이 디렉터리와 Docker volume은 한 세트로 보관한다.
`COMPOSE_*`, `WR_RUNTIME_IMAGE`, 작업 디렉터리의 `.env`로 다른 정의나 이미지를 주입할 수
없도록 CLI가 실행 환경을 제한한다. source checkout의 `.env`나 Node runtime은 사용하지 않는다.

```sh
make test-unit PRD=06
```

installer 단위 시험은 fake Docker와 실제 제한된 fake subprocess로 설치/재개, 이미지 거부,
중단 전 pull, 실패 후 upgrade journal, 자격증명 보존, 파일 변조, 출력 비반사와 취소를 확인한다.
실제 Docker 설치와 공개 GHCR digest에 대한 검증은 별도 release smoke의 결과로 판단한다.


백업·빈 새 설치로의 복원, v3/v4 이행과 새 epoch 절차는 [복구·업그레이드](recovery-upgrade.md)를 따른다. 새 소스의 `wrctl upgrade`는 확인된 콜드 백업 이후에만 이행한다. 아직 출시되지 않은 명령을 이전 Preview 바이너리가 제공한다고 가정하지 않는다.

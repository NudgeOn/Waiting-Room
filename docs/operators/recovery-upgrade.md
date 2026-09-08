# 로컬 설치의 백업·복원과 대기열 복구

이 절차는 이 소스 후보의 `wrctl`과 archive helper가 포함된 새 런타임 이미지에 적용한다.
기존 공개 Preview 바이너리에는 이 명령이 없다. 이전 v3/v4 설치는 새 이미지로 `upgrade`할 때
그 새 이미지의 helper로 이행 전 백업하며, 이전 이미지 자체에는 archive helper가 없을 수 있다.
단일 리전·단일 Valkey의 로컬 Docker 설치를 대상으로 한다. 실행 중인 두 설치가
같은 복원본의 서명 키와 설치 ID를 사용하도록 운영하지 않는다. 다른 서버의 HA/클러스터
복구, 운영 트래픽에서 오래된 Gateway 제거 증명, 자동 이미지 롤백은 포함하지 않는다.

## 백업

```sh
wrctl backup --directory /private/install --backup-directory /private/backups/before-upgrade
```

새 백업 경로를 지정한다. 기존 파일은 덮어쓰지 않는다. 모든 설치 컨테이너를 중지하고
PostgreSQL, Valkey, 설치 상태, 노드 식별 키, Gateway/Coordinator의 서명 설정 캐시,
Valkey ACL의 7개 볼륨을 함께 보관한다. 설치 레코드·Compose 파일·호스트 비밀번호도
같은 백업에 포함한다. 백업 디렉터리는 0700, 아카이브와 manifest는 0600이다.

백업은 개인 키·계정 데이터·토큰을 포함한다. 비공개 저장소에서 관리한다. 명령은
값을 출력하지 않고 체크섬과 구조를 검증한 결과만 안내한다. 중단하거나 실패한
백업에는 완료 manifest가 없으므로 복원할 수 없다. 새 경로로 다시 백업한다.

완료 후에도 원본 설치는 중지된 상태다. 원본을 계속 운영하려면 `wrctl up`을 실행한다.
원본의 복구 안전 대기와 검증이 끝난 후 입장 처리가 가능하다.

## 복원

```sh
wrctl restore --directory /private/restored-install --backup-directory /private/backups/before-upgrade
wrctl up --directory /private/restored-install
```

복원 명령은 먼저 모든 파일의 SHA-256, 아카이브 크기·파일 수·경로·타입을 검사한다.
심볼릭 링크, 장치, 경로 이탈, 중복 엔트리, 변경된 manifest는 거부한다. 현재 지원 한도는
파일 데이터 16 GiB, 엔트리 100만 개이며 외부 PostgreSQL tablespace 링크는 지원하지 않는다.

빈 새 설치 디렉터리와 새 Docker 프로젝트/볼륨에만 복원한다. 원본의 기록된 프로젝트가
이 Docker 호스트에서 실행 중이면 거부한다. 원본 볼륨은 덮어쓰거나 삭제하지 않는다.
백업에 기록된 정확한 공식 이미지 digest와 동일 아키텍처를 검증한다. 복원이 완료되어도
서비스는 자동으로 시작하지 않는다. 원본이 중지되어 있음을 확인한 뒤 복원본만 시작한다.
업그레이드 전 백업은 이전 서비스 이미지와 아카이브 생성에 쓴 새 helper 이미지의 digest를
각각 기록한다. 복원은 검증된 새 helper로 추출한 뒤 이전 서비스 이미지를 그대로 유지한다.

Valkey 재시작은 저장된 primary와 달라지므로 RECOVERY_HOLD에 들어간다. 기존 최대
입장권/READY TTL과 30초 여유를 지난 뒤 각 Room의 대기표·예약·인덱스·전체 예산을
128개씩 검사한다. 누락된 인덱스를 추정해 만들거나 기존 입장권 수명을 늘리지 않는다.
복원이 중단되면 해당 새 대상은 불완전한 상태로 보존된다. 다른 빈 경로로 재시도한다.

## v3/v4 → v5 업그레이드

```sh
wrctl upgrade --directory /private/install --image ghcr.io/nudgeon/waiting-room@sha256:DIGEST
```

이미지를 확인한 다음 `backups/시간`에 콜드 백업을 생성하고 검증한 후에만 마이그레이션을
시작한다. 백업 생성 실패 시 마이그레이션하지 않는다. 이미 이행을 시작한 설치는 기록된
pending image로 재시도한다. 다운그레이드를 위해 이전 이미지만 실행하지 않는다.
이전 상태가 필요하면 업그레이드 전의 전체 백업을 빈 새 설치로 복원한다.

명시적인 `queue-upgrade`는 v3/v4의 등록 Room과 카디널리티, 대기표 epoch, 유지된 최대
입장권 만료를 조사한다. 검토 시점의 digest가 달라졌거나 레코드/인덱스가 불일치하면
쓰기 전에 거부한다. 대기표·암호화된 재시도 응답·FIFO 인덱스는 그대로 보존하고 메타데이터를
v5로 전환한 뒤 RECOVERY_HOLD와 새 fence를 기록한다. 이전 v3/v4 writer는 거부된다.
최소 90초이며 과거 TTL·시간·유지 중인 입장권 만료에 따라 더 오래 기다릴 수 있다.

함수 라이브러리는 버전마다 불변이며 자동 REPLACE를 사용하지 않는다. 유지보수 함수는
별도 maintenance key 권한이 필요하므로 Coordinator와 Traffic Lab 계정으로 이행할 수 없다.
원본 v3/v4 함수를 덮어쓰지 않는다. 소스 개발 도구의 `local-beta.mjs upgrade` 사용자는
이 호출 전에 별도로 콜드 백업을 검증해야 한다. 배포된 `wrctl upgrade`는 자동 백업한다.

## 새 epoch 복구

Room 운영 화면의 **재인증 후 설치 전체 새 epoch 복구**를 사용한다. 검토창은 영향받는
Room 수, 최근 관측된 대기표/예약 수 또는 미확인 상태, 무효화 범위와 안전 대기를 표시한다.
Admin 재인증은 정확한 요청 본문·대상·revision·전체 배포 generation에 결합되며 응답을 잃어도 같은 요청은 한 번만
적용된다. 모든 Room의 epoch를 함께 증가시키고 활성 Room을 HOLD로, 예약을 일시정지한다.

공유 epoch 레코드는 이전 Coordinator가 추가 입장권을 발급하지 못하도록 차단한다.
이전 namespace는 검사와 백업을 위해 보존된다. 새 Gateway는 이전 epoch의 토큰을 거부한다.
오래된 Gateway가 남을 수 있으므로 최대 지원 입장권 TTL 3600초와 30초 여유를 실제 Valkey
적용 시점부터 기다린다. 이전에 관측한 더 빠른 시계나 더 늦은 보류 시각은 대기를 연장한다.
즉시 입장 재개나 임의의 안전 대기 단축 기능은 제공하지 않는다.

안전 대기 후에도 검증을 통과해야 하며 기본 모드는 HOLD다. 운영자가 AUTO를 명령한다.
이전 namespace는 자동 삭제하지 않아 반복 복구 시 디스크·메모리를 사용한다. 물리 메모리
부족은 `noeviction`으로 실패 차단되며, epoch 변경으로 물리 메모리 예산을 우회할 수 없다.

## 재현 가능한 검사

```sh
WR_TEST_RUNTIME_VALKEY=127.0.0.1:16389 go test -tags=integration -race -count=1 \
  -run 'TestRecoveryEpochNamespace|TestRuntimeMigration' ./internal/queue/valkeystore
WR_TEST_AUTH_DB=local go test -tags=integration -race -count=1 \
  -run 'TestEpochRecovery|TestCommandRetryAuditMatrix' ./internal/adminauth/pgstore
go test -race ./internal/recoveryarchive ./internal/installer
WR_TEST_TRAFFIC_DOCKER=local WR_TEST_PUBLIC_CANDIDATE=1 node test/localbeta/backup-runtime.mjs
WR_TEST_TRAFFIC_DOCKER=local node test/localbeta/epoch-runtime.mjs
WR_TEST_TRAFFIC_DOCKER=local node test/localbeta/runtime-tiers.mjs
```

Docker 검사는 별도 프로젝트·시험 이미지·대체 루프백 포트를 사용한다. v4 원본 fixture는
`waiting-room-operations-test:local`이 필요하다. `WR_TEST_PUBLIC_CANDIDATE=1`은 최신
공개 요청 제한 후보 `waiting-room-public-beta-test:local`을 helper/업그레이드 대상으로 쓴다.
생략하면 이전 복구 검증 이미지 `waiting-room-recovery-test:local`을 사용한다.
`epoch-runtime.mjs`는 실제 60분 30초를 기다리며 시계를 조작하지 않는다. 단위 경계 시험의
테스트 전용 시각 조정과 이 실제 대기 검증을 구분한다. 이 결과는 공개 이미지 출시나
외부 고객 설치, 전체 10K/100K qualification 완료를 뜻하지 않는다.

2026-09-09 로컬 검증에서 7개 볼륨 49,896,960바이트·1,433엔트리 백업을 새 프로젝트로
복원하고 실제 primary 안전 대기 뒤 계정·초안·서명 키·이전 HTTP 대기표 재시도를 확인했다.
v4 → v5 이행 후에도 같은 대기표 순서와 claim의 mTLS 원본 도달을 확인했다.
이후 공개 요청 제한을 포함한 최종 로컬 후보에서도 이 전체 백업/복원/이행 여정을 다시
통과했으며, 변경 전의 join 응답과 계정/초안·ACL 자격 증명이 그대로 유지됐다.
최종 검증기는 이 실제 백업을 읽기 전용으로 다시 검사했다. 손상·경로 이탈·잘못된 디렉터리
계층은 대상 파일 생성 전에 거부한다. CPU 아키텍처 불일치와 이전 서비스/새 helper의
버전 분리는 installer 회귀로 검사했다. 공개 digest를 사용하는 다운로드 CLI의 새 명령
설치/복원 검증은 새 release artifact 이후 별도로 필요하다.

수정한 장시간 검사기는 실제 안전 종료 시각 `2026-09-08T23:19:43.624Z`까지 121회
관측하고, 대기 후 관리자 재로그인 → bounded validation 후 HOLD → 명시적 AUTO →
epoch 2 입장권 → 실제 mTLS 원본 도달을 연속으로 통과했다. 초기 검사기의 30분 유휴
세션 만료 문제는 재로그인으로 처리했으며 세션 수명이나 안전 대기는 줄이지 않았다.

이 장시간 검사는 고정 복구 코어 이미지
`sha256:f1c70a3fdca30fd7429203860065c0e50ce9316e96f732f788be91c79950b69e`에서 실행했다.
이후 공개 요청 제한/안내 화면 후보
`sha256:a6c6d2b586ed9e709c44af0a36f43db1f03cbb49e36f63fe0661957c8938c79a`에서는
별도 환경의 새 epoch 재시도·양 노드 ACK·입장 차단, v4 백업/복원/이행과 기존 티켓의
원본 도달을 검증했다. 서로 다른 실행을 하나의 공개 release artifact 수용 결과로
간주하지 않는다. 운영 중 복구가 관리자 유휴 세션보다 오래 걸리면 다시 로그인하여
상태를 확인하고 명시적으로 AUTO를 실행한다.

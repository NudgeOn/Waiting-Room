# 로컬 Docker 설치 — 영속 Control 개발판

**Beta NO-GO.** 계정·TOTP·Room 초안·감사 로그를 보존하는 설치다.
초안 저장은 실제 Gateway/Coordinator에 배포되지 않는다.
전체 Waiting Room Beta, 10K 운영 성능, 공개 인터넷 배포를 보증하지 않는다.
진행 기준은 [Beta 계획](beta-plan.md), 최종 판정은 [MAIN PRD](main-prd.md)다.

## 준비와 실행

Docker Compose, Go(go.mod 버전), Node(package.json 버전), `npm ci`가 필요하다.
현재 스크립트는 소스 체크아웃에서 실행한다. 배포용 사전 빌드 이미지/원클릭 설치는 미완료다.

```sh
node scripts/local-beta.mjs build
node scripts/local-beta.mjs init on
node scripts/local-beta.mjs up
node scripts/local-beta.mjs bootstrap
node scripts/local-beta.mjs token
node scripts/local-beta.mjs setup
```

`init on`의 `on`은 최초 TOTP ON 선택이다. 최초 설치 때만 `init off`로 선택할 수 있다.
재실행으로 기존 TOTP 정책을 변경할 수 없다. 실행 중 정책 전환 UI는 후속 구현이다.
`token`은 로컬 터미널에 설치 토큰을 표시하므로 화면 공유·로그 수집을 끄고 사용한다.
`bootstrap`은 이전 토큰을 폐기하고 15분짜리 토큰을 새로 발급한다.
첫 관리자가 존재하면 발급과 최초 관리자 생성이 모두 거부된다.

- 최초 설정: `https://127.0.0.1:19444/setup` — `setup` 터널 실행 중에만 접속.
- 관리자: `https://127.0.0.1:19443` — 로그인, Room 초안 저장, 최근 감사 로그.
- TLS는 로컬 자체서명이다. 브라우저의 해당 로컬 주소에 대한 경고만 확인한다.
  시스템 전체 인증서 검증을 끄지 않는다. 인증서는 1년이며 자동 갱신은 미구현이다.
- 처음 만든 계정으로 TOTP 등록·복구 코드 보관을 마친 후 로그아웃한다.
  관리자 포트로 이동해 로그인한다. 포트별 탭 CSRF가 달라 인증한 포트에서 로그아웃해야 한다.
- 설치가 끝나면 `setup` 터미널에서 Ctrl-C로 터널을 닫는다.

```sh
node scripts/local-beta.mjs status
node scripts/local-beta.mjs stop
node scripts/local-beta.mjs up
```

`stop`/`up`, 컨테이너 재생성은 볼륨을 삭제하지 않는다.
스크립트에는 reset/uninstall/volume-delete 명령이 없다.

## 영속 데이터와 경계

- Compose 프로젝트: `waiting-room-local-beta` (기존 Lab과 별도).
- `waiting-room-local-beta_database`: PostgreSQL 계정·세션·암호화 TOTP·초안·감사·명령 재시도 결과.
- `waiting-room-local-beta_state`: 암호화 키·로그인 fingerprint·자체서명 TLS·runtime DB 비밀번호.
- `.cache/local-beta/secrets/`: 생성된 owner/runtime DB 비밀번호. Git과 Docker 이미지에서 제외.
- DB와 state, 로컬 secrets를 **같은 시점의 세트로 보관**한다. 키가 없으면 TOTP를 복호화할 수 없다.
  완성된 백업/복원 운영 명령과 키 회전 절차는 후속 gate다.
- 컨테이너 runtime은 UID 65532, 읽기 전용 루트와 state, capability 없음.
  DB owner 비밀번호는 runtime 컨테이너에 제공하지 않는다.
- 명시적 `init`만 migration을 수행한다. SQL checksum/키 binding을 검증한다.
  `serve`는 읽기 전용 검증만 수행하고, 파일·schema가 없거나 불일치하면 시작하지 않는다.
- runtime DB role은 schema DDL과 기존 감사 로그 UPDATE/DELETE가 불가하다.
  INSERT 권한까지 가진 runtime 자체가 침해되었을 때 감사 진위를 보장하는 외부 저장소는 아니다.
- PostgreSQL 포트는 호스트에 게시하지 않는다. Admin 19443은 호스트 `127.0.0.1`만 게시한다.
- DB는 internal network에만 연결한다. Control은 호스트 포트 연결을 위한 별도 management
  bridge에도 연결한다. 이는 Control의 외부 송신까지 차단하는 firewall 설정은 아니다.
- setup 19444는 컨테이너의 실제 loopback listener다. 호스트 loopback TCP →
  로컬 운영자가 실행한 `docker exec` stdio → 컨테이너 loopback으로 TLS 바이트를 전달한다.
  Forwarded/X-Forwarded-For로 로컬 사용자라고 주장할 수 없다.
- Docker 권한이 있는 사용자는 로컬 설치 관리자 권한을 가진다. 공용 Docker 호스트에 배포하지 않는다.
- DB 통신은 격리된 로컬 Docker network에서만 평문이다. 클라우드/공유 네트워크에는 이 설정을 사용하지 않는다.

## 알려진 미완료

Room runtime publish/ACK와 모드·유량 적용, 예약, 완성된 위자드·Dashboard,
TOTP 정책 변경/재인증·사용자 관리, 일반 origin 연결 검증, 운영 백업/복원과 인증서 갱신,
완전한 Beta E2E 및 1K/2K/5K/10K 부하 회귀는 별도 gate다.
브라우저 새로고침, 프로세스 재시작, 컨테이너/DB 재시작 증거는 구분해서 기록한다.

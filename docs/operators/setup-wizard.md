# 로컬 Docker 설치 위자드

`wrctl install`로 실행한 로컬 Docker 구성에서 `wrctl setup`을 실행하고,
표시된 설치 토큰으로 `https://127.0.0.1:19444/setup`을 연다. 위자드 구현이
포함된 runtime 이미지가 필요하며, 이전 Preview 이미지에는 새 화면이 없다.

1. **설치 확인**: 토큰과 PostgreSQL 연결을 확인한다. 적용 후 새로고침했다면
   저장된 결과를 불러와 첫 관리자 생성부터 이어진다.
2. **설정**: Standard 10K 프로필의 리전, 예상 방문자, 새 Room 유량 시작값과
   TOTP 정책을 선택한다. `wrctl plan` 또는 계획 미리보기에서 내려받은 JSON도
   불러올 수 있다. 입력은 서버가 다시 검증하며 파일 자체를 실행하지 않는다.
3. **환경 보정**: 실제 Control 프로세스에서 Argon2id를 측정한다. 메모리 64 MiB,
   병렬도 1, 반복 2~10회 범위에서 후보별 warmup 뒤 세 번의 중앙값을 사용한다.
   목표 250~500ms에 들어오는 값을 선택한다. 목표를 찾지 못하면 적용을 차단하고
   CPU·메모리 할당 및 서버 부하를 확인한 뒤 다시 측정하도록 안내한다.
4. **검토·적용**: 검토한 입력과 보정 결과의 digest를 서버 기록과 비교한 후
   리전·프로필, 새 Room 시작값, 초기 TOTP 정책, 비밀번호 보정값과 설치 결과,
   감사 로그를 하나의 PostgreSQL transaction에 저장한다. 실패하면 모두 rollback한다.
5. **첫 관리자**: 적용 결과 JSON을 내려받고 첫 관리자를 생성한다. TOTP ON이면
   인증 앱 등록과 복구 코드 보관을 완료한다. **설정 완료 · 관리자 콘솔로 이동**을
   눌러 설정 포트에서 로그아웃한 뒤 관리자 포트에서 다시 로그인한다.

새 Room 만들기는 설치 시 저장한 유량을 기본값으로 사용한다. Room 초안 저장과
서명 배포는 기존 운영 흐름을 따른다. 위자드는 기존 Room을 수정하거나 자동 활성화하지 않는다.
인증된 `GET /api/admin/v1/installation`으로 저장된 결과를 다시 조회할 수 있다.

## 재시도와 보존

- 설치·보정·계획 API는 게시되지 않은 loopback TLS 설정 포트에서만 제공한다.
  실제 loopback peer/listener, 정확한 Host/Origin, `X-WR-Auth`, 설치 토큰을 검증하고
  Cookie·Authorization·프록시 주소 주장을 거부한다.
- 토큰과 측정값은 15분 유효하다. 토큰 교체·측정 만료 후에는 다시 보정하고 검토한다.
- 동일한 적용 요청은 같은 결과를 반환한다. 감사 기록과 설정 revision을 늘리지 않는다.
  이미 적용한 입력을 다른 계획으로 덮어쓸 수 없다.
- 첫 관리자 생성은 적용 완료 후에만 가능하다. 완료 tombstone이 모든 설치 API와
  추가 토큰 발급을 영구 차단한다. 이후 계정·정책은 인증된 관리자 경로로 관리한다.
- migration 011은 이미 존재하는 계정의 `wrp1 / t=2` 설정을 보존한다. 재시작·upgrade는
  보정값을 자동 변경하지 않는다. 기존 계정의 일괄 재해싱·보정값 변경은 지원하지 않는다.
- Queue 재시작 후 Coordinator의 보호 대기는 기존 최대 티켓 수명 + 30초까지 필요하다.
  `wrctl up/upgrade`는 이 동안 준비 완료를 선언하지 않고 실제 6개 서비스가 모두
  healthy가 될 때까지 기다린다. Coordinator는 최대 약 62분, 다른 서비스는 180초로
  제한하며 취소·실패 시 앱 역할을 정지하고 데이터를 보존한다.
- 설치 결과에는 계획, 측정 samples·시각, 적용 시각, Control OS/아키텍처·CPU 병렬도와
  PostgreSQL 시각 차이가 남는다. 비밀번호·토큰·TOTP 키·복구 코드는 포함하지 않는다.

## 검증 범위

이 경로는 **현재 로컬 Compose 구성의 초기 설정 적용**이다. 공개 DNS/TLS·실제 host NTP
검사, production 원본 우회 차단, 10K/100K qualification과 Helm은
별도 작업이다. PostgreSQL과 Control 시각 비교를 NTP 검증으로 표시하지 않는다.
기존 계정·Room이 있는 설치를 위자드로 재구성하지 않으며, Beta 전체 판정은 계속 NO-GO다.

재현 명령:

```sh
make check
WR_TEST_AUTH_DB=local go test -tags integration -race ./internal/adminauth/pgstore -run '^TestSetup' -count=1
npm run test:admin-browser -- test/admin-browser/setup.spec.mjs
# source Linux binaries and Admin UI를 먼저 빌드한 전용 이미지:
docker build -f deploy/docker/control.Dockerfile -t waiting-room-setup-test:local .
WR_TEST_SETUP_DOCKER=local node test/localbeta/setup-runtime.mjs
```

브라우저 시험은 일회용 PostgreSQL 스키마와 18571~18574 loopback 포트를 사용한다.
Docker 시험은 별도 프로젝트와 29443·29444·30443 포트를 사용하며 기존 설치를 정지하지 않는다.
새 fixture 컨테이너·네트워크는 종료하고 DB·키 볼륨과 private 디렉터리는 보존한다.

관리자 메뉴·Room 검증 탭의 [Traffic Lab](traffic-lab.md)에서 Quick 20·Smoke 1K를 실행할 수 있다. 이 결과는 고정 샘플의 기능 검사이며 고객 Room 연결/production 활성화 검사를 대체하지 않는다.

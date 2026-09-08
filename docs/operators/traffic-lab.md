# 관리자 Traffic Lab

로컬 Docker runtime에 로그인하고 **Traffic Lab** 메뉴 또는 **Room → 검증** 탭을 연다.
Admin·Operator는 실행/중지하고 Viewer는 저장된 결과를 조회·다운로드한다.
이 기능을 포함한 이미지와 migration `012_traffic_lab.sql`, `wr_traffic` ACL이 필요하다.
기존 설치는 [업그레이드 절차](prebuilt-install.md)를 따라 명시적으로 업그레이드한다.

1. **Quick 20 실행**: 브라우저 쿠키 방식 10명과 앱 방식 10명의 순서·새로고침·여러 탭·재시도를 확인한다.
2. 결과에서 **3명 입장·17명 대기**, 7개 판정 근거와 방문자별 기대/실제를 확인한다.
3. **Smoke 1K 실행**: 8 workers가 1,000명과 중복 요청을 생성한다. **3명 입장·997명 대기**와 예상 밖 오류 0을 확인한다.
4. **결과 JSON 다운로드**로 현재 결과를 저장한다. 페이지를 나가거나 새로고침해도 서버의 기록은 유지된다.
5. 실행 중에는 **시험 중지**를 누를 수 있다. 서버가 시험 환경을 종료하면 `중지됨`이 된다. 실패·중단은 전체 통과로 표시하지 않는다.

두 preset은 실제 HTTP와 설치에 포함된 Valkey runtime-v4를 사용한다. Quick은 정상 완료 시
93회, Smoke는 3,013회 요청하며 의도한 거절 429·409·503 각 1회를 오류와 분리한다.
판정은 join 재시도, FIFO, 입장 한도, claim 재시도, 조기 claim 거절, 원본의 인증값 제거,
샘플 Coordinator 종료 후 신규 요청 차단/기존 입장권 사용이다. 타임라인은 실제 큐 순번으로
정렬하며 Smoke도 첫 20명만 표시한다. p95는 이 짧은 시험의 Gateway HTTP 응답 시간이다.

## 범위와 실행 제한

- 고객 URL·Room을 입력받지 않고 고정 샘플 상점에만 요청한다. 현재 Room 초안·배포·유량을 바꾸지 않는다.
- Control은 PostgreSQL 작업을 기존 mTLS로 Coordinator에 전달한다. Control에 Valkey 접근을 추가하지 않는다.
- Coordinator 안의 임시 loopback Gateway 2개·Coordinator·origin과 전용 `wr_traffic` 키 범위를 쓴다.
  운영 키 조회가 ACL에서 거절되는지 확인한 뒤 실행한다. CPU/메모리·Valkey 서버는 설치와 공유한다.
- 한 번에 1건, 최대 실행 90초. 중지 요청은 실행기 취소와 정리를 기다린다.
  Control 비정상 종료 후 남은 작업은 최대 120초 lease가 끝나면 `실행 끊김`으로 기록한다. 자동 재실행하지 않는다.
- 전용 fixture의 11개 키만 시작/종료 시 삭제하며 10분 TTL도 둔다. 운영 큐의 키나 epoch를 초기화하지 않는다.
- 최근 20건을 화면에 표시하고 서버는 최대 500건을 보관한다. 새 실행 시 24시간 지난 종료 기록을 정리한다.
- 시작·중지는 기존 CSRF/Idempotency-Key/RBAC 규칙, 최종 결과는 원자적 감사 기록을 따른다.
  리포트에는 입장권·세션·쿠키·비밀번호·원본 URL을 넣지 않는다.

`실패`는 기대 결과 불일치이며 판정 근거/오류 수를 확인한다. `실행 끊김`은 서버·연결·실행 제한
등으로 전체 결과가 확정되지 않은 상태다. 서비스 상태와 감사 기록을 확인한 후 새 시험을 실행한다.
설치 업그레이드가 누락되어 실행기가 연결되지 않아도 통과로 처리하지 않는다.

이 통과는 고객 Room 연결이나 production 원본 우회 차단, 모든 수동 OFF/AUTO/이벤트 시나리오,
Valkey failover, 별도 PID 간 네트워크 성능, Standard 10K/High Scale 100K qualification을 보장하지 않는다.
[SUB-PRD-07](../sub-prd_07.md)의 전체 검증 및 Beta 판정은 별도로 유지한다.

## 개발 검증

전용 로컬 시험 인스턴스만 사용한다. 기존 설치를 지정하는 인자는 제공하지 않는다.

```bash
make lab-valkey
make lab-auth-db
WR_TEST_TRAFFIC_VALKEY=127.0.0.1:16379 go test -tags=integration -race ./internal/trafficlab
WR_TEST_AUTH_DB=local go test -tags=integration -race ./internal/adminauth/pgstore -run '^TestTrafficLab'
npm run build:admin
WR_TEST_AUTH_DB=local PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" npm run test:admin-browser -- traffic-lab.spec.mjs
```

`test/localbeta/traffic-runtime.mjs`는 `waiting-room-traffic-test:local` 이미지를 사용해 별도 Compose
프로젝트·비밀값·볼륨·대체 loopback 포트 29443/29444/30443을 만든다.
`WR_TEST_TRAFFIC_DOCKER=local PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" node test/localbeta/traffic-runtime.mjs`로 실제 6개 역할에서
설치·mTLS 실행·ACL 격리·설정 불변·PostgreSQL/Control 재시작 후 결과 보존을 확인한다.
기존 설치는 선택하지 않으며 종료 시 이 fixture의 컨테이너만 내리고 전용 볼륨은 보존한다.
Chromium이 Room 검증 탭에서 Quick 20을 실행하는 동안, 대체 공개 포트의 요청은 고정 논리 Host/Origin으로 실제 서버에 전달한다. API 데이터를 만들어 응답하지 않는다.
스크린샷은 OS 임시 디렉터리 또는 `WR_TRAFFIC_SCREEN_DIR`에 둔다.

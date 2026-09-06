# 설치 계획 위자드 미리보기

```sh
make preview
```

저장소 루트에서 실행하고 `http://127.0.0.1:18770/install-preview`를 연다. Go/Node 의존성을
준비해야 하며 UI build 뒤 `wrctl preview`가 시작된다. 종료는 Ctrl-C다. port 18770 사용 중이면
기존 process를 덮어쓰지 않고 실패한다. 외부 bind나 임의 port 옵션은 없다.

## 제공하는 화면

1. 규모 선택: Standard 10K / High Scale 100K의 서버 구성·설치 책임·한도를 비교한다.
2. 운영 환경: 단일 Home Region과 프로필별 준비 항목을 확인한다. 실제 연결 검사는 하지 않는다.
3. 유량 설정: 예상 방문 상태·lease·rate·TTL을 입력한다. 필드별 한도 오류가 다음 진행을 차단한다.
4. 관리자 보안: TOTP ON/OFF 또는 `forced_on`을 선택하고 계획을 생성한다.
5. 계획 확인: Go `installplan.Decode/Build`로 검증한 전체 설정을 검토한다. 단계별 수정, BOM, 미실행 검사 목록, JSON 다운로드를 제공한다.
6. 비용 비교(선택): provider·통화·기준일·단가를 입력하면 Go `Estimate`가 서버·디스크 소계와 산식을 계산한다.

완료한 단계는 직접 돌아갈 수 있고 단계 이동 시 입력을 유지한다. 설정 변경 시 이전 계획/비용을 무효화하고 가격 변경 시 이전 소계를 지운다. 검증된 계획 또는 계획+비용을 [JSON 보고서](planning-report.md)로 다운로드한다. 가격 오류는 해당 필드에서 수정하도록 안내한다.

FIFO만 표시하고 미구현 알고리즘·multi-region 선택지는 제공하지 않는다. 큰 profile에서 작은
profile로 변경해도 방문 상태/유량을 몰래 낮추지 않는다. 한도를 초과하면 수정 전 제출을 막으며
직접 HTTP 요청도 서버 validator가 거부한다. 금액 계산의 범위·산식·제외 항목은
[비용 계약](cost-estimate.md)을 따른다. UI의 가격 기본값은 비어 있고 예제 시세를 자동 채우지 않는다.

## 안전 경계

**실제 installer나 Admin bootstrap이 아니다.** 별도 stateless loopback HTTP process이며
DB·Docker·Kubernetes·외부 endpoint·shell command 실행과 연결되지 않는다.
TOTP를 바꿔도 현재 Admin Lab/운영 서버의 정책이 바뀌지 않는다. 관리자 계정/secret도 받지 않는다.
입력은 브라우저 메모리에 유지하고 새로고침하면 초기화한다. 버튼으로 다운로드한 JSON은 로컬 파일로 남는다.
정상 필드에도 secret을 넣지 않는다.

- exact Host `127.0.0.1:18770` 및 loopback peer 요구
- `/preview/plan`, `/preview/estimate`, `/preview/report`만 POST JSON 지원
- POST는 exact Origin, `X-WR-Preview: 1`, JSON을 요구하고 cookie/authorization 요청 거부
- credential 전송 없이 same-origin fetch, no-store, redirect 거부, 10초 client timeout
- CSP/anti-frame/referrer/no-store, body 최대 16KiB, read 5초/write 10초, 계산 동시 처리 8개 제한
- raw request/secret/error 로그 없음. 잘못된 입력은 generic 오류로 응답
- `/setup`, Admin/bootstrap/API, apply, filesystem directory listing은 제공하지 않음

인증 없는 plain HTTP이므로 **비밀 없는 로컬 계산 미리보기 전용**이다. reverse proxy로 외부에
공개하거나 production setup listener로 사용하면 안 된다. 향후 실제 설치는 인증된 one-time
bootstrap/secret handling/명시적 apply 승인을 별도로 구현해야 한다.

## 검증

```sh
make check
make test-unit PRD=06
GOCACHE="$PWD/.cache/go-build" GOMODCACHE="$PWD/.cache/go-mod" \
  PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/ms-playwright" npm run test:preview-browser
node scripts/run-installplan.mjs my-unique-preview-run --preview-ui
```

마지막 두 명령은 설치된 Chromium과 loopback listener 실행 권한이 필요하다. Playwright는
자신이 시작한 preview server를 종료한다. 기존 서버는 재사용하지 않는다. 스크린샷이 필요하면
`WR_PREVIEW_SCREEN_DIR`에 repository 밖 출력 디렉터리를 지정한다. 가격 fixture는 가상 값이다.
DB 로그인·관리자 bootstrap·실제 환경 검사/설치·10K/100K 부하 qualification을 대신하지 않는다.

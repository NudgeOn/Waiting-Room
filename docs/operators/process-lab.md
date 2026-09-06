# 별도 프로세스 Web/App Lab

`wr-process-lab`은 Gateway 2개, Coordinator, 샘플 origin을 각각 별도 OS 프로세스로 실행한다.
기존 단일-process `wr-lab`은 유지한다. 두 실행기 모두 lab-only이며 production binary/image가 아니다.

```sh
make lab-valkey
make process-lab-quick
# 계속 실행: summary의 gateway URL + /shop에 접속. Ctrl-C로 전체 종료.
make process-lab
# 실제 child process fault/replacement 시험
WR_TEST_VALKEY=127.0.0.1:16379 make test-processes
make test-unit PRD=05
# unique-run-id는 매번 새 값. 전용 Valkey 정상 재시작 포함.
node scripts/run-m1.mjs unique-run-id --processes
```

## 구성과 수명

- 고정 개발용 Valkey: `127.0.0.1:16379`, 실행마다 `wr:lab:process-<random>` namespace.
- 서버 listen은 모두 `127.0.0.1:0`; 실제 할당된 두 Gateway URL과 child PID만 summary에 출력.
- parent가 같은 실행 파일을 `child origin|coordinator|gateway`로 시작한다. PATH 실행 파일 검색 없음.
- 초기 설정은 stdin pipe의 최대 16 KiB JSON 한 줄, ready 응답은 stdout pipe의 최대 4 KiB 한 줄.
- Coordinator가 admission private key·idempotency replay key를 생성·보유한다. parent/Gateway로 내보내지 않는다.
- parent는 Coordinator의 service credential/public key를 private ready pipe에서 받아 Gateway에 전달한다.
- parent가 별도 ephemeral config 서명을 생성하고 role 설정 전체에 binding한다. Gateway/Coordinator는
  signed bootstrap을 검증하며 expiry 뒤 트래픽/신규 입장 작업을 중단한다. [서명 설정 범위](signed-config-lab.md).
- Gateway 설정에는 Valkey 주소/credential 및 admission private key 필드가 없다. return key는 parent가
  생성해 두 Gateway의 stdin pipe로만 전달한다. 키/credential은 디스크·args·환경 변수·사용자 로그에 저장하지 않는다.
- child mode는 stdin/stdout이 pipe가 아니면 거부한다. role별 설정 외 필드는 거부한다. 이 IPC는
  신뢰된 parent 전용이며 외부용 bootstrap API나 악성 로컬 사용자에 대한 인증 경계가 아니다.
- child 환경은 고정 PATH/LANG/race 종료 설정과 `GOMAXPROCS=2`만 포함한다. parent DB/password/proxy 환경을 상속하지 않는다.
- startup 8초 제한; 실패 시 이미 시작한 child를 정리한다. parent의 pipe EOF/추가 control byte는 종료 요청이다.
- 정상 종료 시 모든 pipe를 닫고 child를 기다린다. 4초 내 종료되지 않는 자신이 시작한 child만 kill한다.
- child 서버의 body/header deadline과 크기 제한을 적용하고 stderr/원문 오류는 사용자 로그에 내보내지 않는다.

## 검증 범위

프로세스 시험은 distinct PID, app Quick20(3명 입장/17명 대기), Coordinator 실제 SIGKILL 뒤 join 503/
unsafe 429/origin count 불변을 검사한다. 다른 시험에서는 한 public authority 뒤에서 브라우저 HTTP
요청을 Gateway 사이로 전환하고, Gateway 한 개를 같은 설정으로 종료·교체한 뒤 ticket/return/claim/origin
흐름을 확인한다. parent pipe EOF 뒤 child 종료도 관측한다. 렌더링 브라우저 20개 시험은 아니다.

Quick20의 프로필은 lease 3, rate 6/min, token 60초, 최대 verifier leeway 30초다.
전체 실행기 재시작은 새 key/namespace를 만들며 기존 대기표를 복구하지 않는다. Gateway 교체는
현재 parent가 보유한 동일 키로 하는 시험이며 production key persistence/rotation이 아니다.

## 남은 보안·출시 경계

별도 PID는 별도 OS 사용자, 컨테이너, mTLS, firewall/NetworkPolicy 격리와 다르다.
개발 Valkey는 인증 없이 loopback에 있으므로 침해된 동일 사용자 프로세스의 직접 접근을 방지하지 않는다.
샘플 origin과 그 테스트용 counter는 local fixture이며 운영 origin ACL 증거가 아니다.
현재 Coordinator는 [설치 합계 cap의 lab 경로](local-visitor-tiers.md)를 사용한다.
production role image, secret mount, production signed config 배포/refresh/ACK, production cap 연결, Coordinator 재시작 복구,
HA/fencing, 10K/100K qualification은 여전히 미완료다. secret-redacted errors는 진단 상세를 제한한다.

실행 종료는 Valkey 데이터/namespace/volume을 삭제하지 않는다. 필요할 때
`docker compose -f deploy/compose/lab.yaml stop`으로 전용 Valkey를 중지한다.
[증거·출시 차단 항목](../evidence/process-lab-summary.md).

# 서명 설정·cold-start 로컬 검증

```sh
make test-unit PRD=05
make lab-valkey
WR_TEST_VALKEY=127.0.0.1:16379 make test-processes
# 새 run ID, HTTP 네 단계 3회 및 기존 회귀 포함
node scripts/run-m1.mjs unique-run-id --processes --tiers --http-tiers
```

## 신뢰 경계

`internal/configtrust`는 Ed25519 서명 envelope를 검증한다. snapshot은 schemaVersion,
installation, monotonic generation/revision, issuedAt/expiresAt(Unix seconds), signer kid,
전체 payload를 포함한다. `waiting-room-config/v1` domain과 NUL 뒤 deterministic JSON을 서명한다.
Go struct field order/JSON encoding을 고정한 전용 wire format이지 RFC 8785/JCS 구현은 아니다.
envelope/snapshot의 unknown field·중복 field·비정규 bytes, 64KiB 초과, 다른 installation/key,
유효기간 24시간 초과와 future/expired snapshot을 거부한다. 시간 leeway는 없다.

payload의 **전체 schema·semantic validation·정규 bytes 검사**는 필수 callback이다. 서명만 유효하다고
임의 JSON을 설정으로 활성화하지 않는다. 반환 snapshot/key/input은 별도 복사해 caller mutation을 막는다.
낮은 generation/revision과 동일 generation의 다른 bytes를 거부하며 exact replay만 허용한다.
잘못된 refresh는 아직 유효한 LKG를 대체하지 않는다. clock 역행이나 저장 결과 불확실은 fail-closed latch다.

## 파일 LKG

`FileStore`는 caller가 소유·관리하는 절대 경로 0700 디렉터리, 0600 `snapshot.json`을 사용한다.
`os.Root`에 디렉터리를 고정하고 새 임시 파일 write/fsync → atomic rename → directory fsync 후
memory snapshot을 바꾼다. signature를 다시 검증한 파일의 generation을 restart high-water로 복원한다.
만료된 파일도 high-water는 유지하지만 트래픽을 통과시키지는 않는다. corrupt file을 자동 덮어써 복구하지 않는다.
symlink/non-regular/잘못된 mode/oversized file을 거부하며 정상 저장 뒤 temporary file을 정리한다.

role별 전용 디렉터리와 **한 Gate writer**를 전제로 한다. cross-process writer locking, root/same-user
디스크 롤백·삭제, VM snapshot rollback, 재시작을 넘는 clock 역행 방어, 외부 durable generation authority는
미구현이다. 파일이 사라지면 cold-start 상태이며 외부에서 고정한 minimum generation보다 낮은 값은 받지 않는다.
실제 power-loss/fsync fault injection·배포 secret owner/권한 검증은 후속이다.

## process lab 연결

trusted lab supervisor가 role별 ephemeral config signer 역할을 한다. private signing key를 child에 전달하지 않는다.
bootstrap trust public key와 signed snapshot은 기존 private stdin pipe로 전달된다.

- Gateway snapshot: 실제 Coordinator/origin URL, Room/audience/template/admission key,
  service credential·return key의 SHA-256 fingerprint 전체와 binding. raw secret은 snapshot에 없다.
- Coordinator snapshot: 실제 Valkey 주소/namespace/Room, 전체 queue config와 설치 cap에 binding.
- Gateway에 snapshot이 없거나 변조/불일치/만료이면 GET `/livez` 외 모든 path/method가 503
  `CONFIG_UNAVAILABLE`이다. customer traffic·queue API·asset도 예외가 아니다.
- Coordinator는 유효한 bootstrap snapshot 없이는 Valkey 초기 쓰기 전에 시작을 거부한다.
  실행 중 expiry 뒤 요청 처리를 503으로 차단하고 background promotion도 중지한다.
- 매 request/작업 시작 시 검사한다. 이미 시작한 in-flight 작업의 원자 취소나 queue generation fencing은 후속이다.

lab 유효기간은 24시간, 설정 교체 channel은 아직 없으므로 전체 lab 재실행이 필요하다.
process lab은 **MemoryStore**를 쓰며 Gateway 교체 시험은 parent가 보유한 동일 signed bootstrap을 전달한다.
FileStore restart 시험과 processlab restart는 다른 증거다. production Control, durable deployment-pinned root,
전체 Site/Room config schema, 원자 handler 교체, 5분 refresh/ACK, key rotation, HA/fencing은 완료되지 않았다.
기존 단일-process `wr-lab`은 이전 unsigned lab 경로를 보존한다.

## 검증 범위

- core unit 11개: deterministic/tamper/schema/key/time/rollback/LKG/clock/concurrent generation/
  persistence failure/cold HTTP/file restart/expired high-water/torn file/mode/symlink/directory rename.
- 실제 시각은 Gate lock 내부에서 측정한다. caller의 지연된 시각으로 정상 병렬 요청을 역행으로 오판하지 않는다.
- role binding unit 2개, 실제 process test 3개: cold trust 4개 변형, Gateway real expiry,
  Coordinator unsigned 쓰기 0건·real expiry 뒤 API/자동 promotion 중단.
- 기존 process Quick20·Gateway 교체·4단계 HTTP 반복도 같이 검증한다.

검증은 local이며 production trust chain·Linux crash durability·전체 release qualification을 대신하지 않는다.

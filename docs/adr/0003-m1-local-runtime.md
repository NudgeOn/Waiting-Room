# ADR-0003 — M1 단일 Room·앱 실행 경계

상태: 로컬 구현에 적용. M1 전체/production 승인 아님.

- 단일 Room의 Valkey 8.1.6 Function으로 FIFO·READY·claim·rolling rate·expiry·idempotency budget을 원자 처리한다. 순번과 시간은 실제 Valkey primary에서 발급한다.
- key 8개를 FCALL에 명시한다. cleanup은 128건씩 수행하고 backlog가 남으면 신규 쓰기를 보수적으로 거부한다. full queue scan을 admission 경로에서 하지 않는다.
- library source와 config를 시작 시 비교하며 자동 REPLACE나 config overwrite를 금지한다. 함수는 오류 이전 쓰기를 rollback하지 않으므로 알 수 없는 write 오류는 해당 Store를 fail-closed latch한다.
- primary process ID를 매 요청 전 확인하고 변경/연결 오류를 발견한 Store는 재개하지 않는다. 재시작 뒤 새 Coordinator도 저장된 primary ID가 다르면 RECOVERY_HOLD한다. 이 두 관측 사이의 failover race, replica fencing과 공유 recovery latch는 아직 미구현이며 HA 인증 대상이 아니다.
- `wr:lab:*` namespace만 허용한다. 지금의 cap은 하나의 Room에만 해당한다. 여러 Room의 설치 전체 합산은 M1 후속 구현이 필요하다.
- `wr-lab`은 loopback HTTP 두 Gateway·Coordinator·deterministic origin을 실행한다. Gateway는 Valkey credential/private key를 가지지 않고 내부 service credential로 Coordinator를 호출한다. 모든 role은 현재 한 lab process에 있어 production process isolation 증거가 아니다.
- opaque queue token은 random 256-bit다. Valkey ticket ID에는 hash만 저장하고 join replay token은 Coordinator의 AES-GCM으로 암호화한다. claim은 고정 Ed25519 claims로 동일 bytes를 반환한다.
- signing/replay key는 lab process lifetime에만 존재한다. durable secret mount·rotation·서명 config·복구 프로토콜은 후속이다. 재시작 시 이전 lab 자격증명 유지 기능을 주장하지 않는다.
- 앱 JSON에 이어 local browser redirect/cookie/return AEAD와 `calm` template을 연결했다. [template 계약](../design/calm.md). adaptive polling의 server-side early limit/abuse quota, Backoffice UI/TOTP/DB는 미구현이다. encoded route는 M2 normalizer 전까지 보수적으로 거부한다.
- browser return은 tab별 target을 host+port/Room/ticket hash/issued/absolute expiry와 AES-GCM으로 봉인한다. 단일 process의 두 Gateway가 key를 공유하며 독립 process key distribution/rotation은 없다. browser join 응답 유실 및 동시에 시작하는 cookie 없는 여러 탭의 중복 최초 join은 미해결이므로 full join-or-resume GO가 아니다.
- Quick20은 20명 중 FIFO 3명 입장·17명 대기와 교차 Gateway 재시도를 검증한다. 이는 실제 성능 10K/100K, 브라우저 Quick20 또는 완전한 M1 완료가 아니다.
- join의 original queued 응답은 ticket이 남아 있는 동안 재생한다. 만료 후 idempotency record만 남은 경우 410을 반환하는 M1 제한이 있다. 전체 retention 동안 exact HTTP replay 계약은 후속 구현 전 delivery NO-GO다.

공식 구현 참고: [Valkey Functions](https://valkey.io/topics/functions-intro/),
[Valkey Go client](https://github.com/valkey-io/valkey-go).

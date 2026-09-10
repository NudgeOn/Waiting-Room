# 앱 연동 참조 클라이언트

Node.js 22.12+의 작은 실행 예제다. iOS/Android SDK나 브라우저 cookie 흐름을 대체하지
않는다. native 앱에서는 같은 상태 전이를 플랫폼 HTTP·보안 저장소·수명 주기에 옮긴다.
[공개 API 계약](../../api/openapi/public-v1.yaml)이 기준이며 서버가 최종 입장을 결정한다.

```js
import {newJoinIntent, waitForAdmission, QueueError} from './client.mjs';

const intent = newJoinIntent('/checkout?item=123');
// 요청 전 intent의 target/key/생성 시각을 함께 보존한다.
// 응답 유실 뒤에는 같은 intent를 사용한다. 실제 앱은 안전한 저장소를 사용한다.
try {
  const result = await waitForAdmission({
    origin: 'https://waiting.example.test',
    ...intent,
    signal: AbortSignal.timeout(30 * 60 * 1000),
    onState: ({state, usersAhead}) => updateWaitingUI(state, usersAhead),
  });
  if (result.state === 'admitted') {
    // 다음 사용자가 요청한 고객 API 호출의 헤더에만 추가한다.
    // 기존 OAuth Authorization과는 별도이며 토큰을 URL이나 로그에 넣지 않는다.
    const admissionHeaders = {'X-Waiting-Room-Admission': result.admissionToken};
    showContinueAction(admissionHeaders);
  } else {
    showContinueAction({}); // 서버 pass. 다음 실제 요청도 Gateway 검사를 거친다.
  }
} catch (error) {
  if (error instanceof QueueError) showQueueError(error.code, error.retryAfterMs);
  else showCancelled();
}
```

`updateWaitingUI`, `showContinueAction`, `showQueueError`, `showCancelled`는 호출 앱이 제공한다.
예제는 고객 POST/결제 요청을 저장하거나 자동 재생하지 않는다. `onState`에는 대기표·입장권을
전달하지 않는다. 응답의 외부 URL은 사용하지 않고 검증한 origin과 Room ID로 API 경로를 만든다.
TLS 인증서 검증과 redirect 차단을 유지한다. 테스트 전용 self-signed 설정을 운영 앱에 복사하지 않는다.

- join 응답 유실과 429/503은 같은 key/body로 재시도한다. 8회 연속 실패를 넘으면 중단한다.
  원래 key 생성 시각부터 9분이 지나면 10분 replay 경계를 넘지 않도록 `JOIN_RETRY_WINDOW`로
  멈춘다. 이 경우 또는 410에서는 사용자가 상황을 확인하기 전 새 key를 자동 생성하지 않는다.
- status는 `pollAfterMs`, 429/503은 `Retry-After`와 작은 jitter를 따른다. heartbeat 일정은
  status 성공으로 미뤄지지 않는다. 서버의 `ready` 또는 `admitted` metadata 뒤에 같은 ticket으로
  claim한다. claim 응답 유실도 같은 ticket으로 재시도한다.
- 토큰은 이 호출 동안 메모리에만 남는다. background/프로세스 종료 후 기존 ticket의 재개 저장소,
  OS background 실행, UI 연결은 예제 범위 밖이다. 앱 재개 시 새 방문자를 조용히 발급하지 않는다.
- 사용자가 취소하거나 앱 화면을 떠나면 `AbortSignal`을 취소한다. 서버 410은 만료로 표시한다.
  클라이언트가 서버 시각·TTL·입장권을 수정하거나 입장을 자체 승인하지 않는다.

검증: `node --test scripts/app-client.test.mjs`. 응답 유실·동일 재시도·독립 heartbeat·서버
간격·외부 URL 차단·410·취소·오래된 intent를 가짜 HTTP/시계로 검증한다. 실제 iOS/Android
기기 수용 검증을 수행한 결과는 아니다.

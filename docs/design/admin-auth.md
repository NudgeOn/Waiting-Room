# Admin authentication visual specification

Concept: [admin-login-concept.png](admin-login-concept.png), built-in Image Gen, 1586×992.
Working implementation reference chosen for this task; no separate user design approval claimed.
Brief: Korean login, text-only Waiting Room wordmark, pure white, forest-green headline/button,
open two-column layout, no cards, metrics, illustrations, gradients or decorative navigation.

## Tokens and component inventory

- Native desktop: header 100px, horizontal padding 50px, content 1260px, left/right columns
  550/565px, 145px gap; form starts near y210, heading area y280; footer min112px.
- White #fff; text #11171b; heading #093c31; button #105641; muted #60646a; border #85888b.
- System Korean sans: heading 60px/1.55 bold, form title 42px/1.3, copy/labels 23px/1.6,
  field/button 25px; footer 20px. Scale down responsively; minimum 16px input on mobile.
- Inputs/buttons 70px high, 8px radius, 1px outline; labels and forms use consistent vertical gaps.
- No graphic/icon assets. Wordmark and all controls are real text, not the concept bitmap.
- Shared shell, Field, Submit, error/status region. Separate login, setup, factor, enrollment,
  recovery-code acknowledgement and current-session components. Requests only in event handlers.

## Copy lock and necessary downstream states

Primary login: Waiting Room; 관리자 콘솔; 흐름은 차분하게, / 운영은 간편하게.;
사이트와 앱의 대기열을 / 한곳에서 안전하게 관리하세요.; Apache-2.0 · Self-hosted;
관리자 로그인; 설치 시 만든 계정으로 로그인하세요.; 사용자 이름; admin; 비밀번호;
로그인; TOTP가 켜져 있으면 다음 단계에서 인증합니다.; 로그인 정보는 이 서버에서만 처리됩니다.

Functional extensions in the same form system: setup token/first Admin, six-digit factor,
recovery code alternative, manual key/local QR, one-time recovery-code save acknowledgement,
current server session/capabilities and logout. These are required auth workflow states, not
new Room metrics or dashboard claims. Setup explicitly labels temporary local lab and policy.
The authenticated screen states that Room operations and installation remain unconnected.

Mobile: stack headline and form with 24px gutters, no horizontal overflow, footer wraps.
Focus rings, labelled inputs, aria-live errors, disabled pending submit, reduced-motion support.
Do not screenshot real credentials, QR/manual keys or recovery codes. Keep credentials in memory
only, except a session-bound CSRF proof in versioned per-tab sessionStorage for reload/logout.
No login credentials in storage, external fonts/CDN/QR endpoint, request logging or telemetry.
New tabs without that CSRF proof are read-only until logout in the original tab or session expiry.

Evidence and concept/render comparison belong in the local Backoffice evidence summary.

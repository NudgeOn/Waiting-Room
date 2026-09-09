// SPDX-License-Identifier: Apache-2.0
package waiting

import (
	"html/template"
	"io"
)

var joinPage = template.Must(template.New("join").Parse(`<!doctype html>
<html lang="ko"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Waiting Room</title><link rel="icon" href="data:,"><link rel="stylesheet" href="/_wr/assets/calm.css"><script src="/_wr/assets/join.js" defer></script></head>
<body data-bootstrap="true" data-restart="{{.Restart}}" data-room="{{.Room}}" data-prepare-url="{{.Endpoint}}" data-target="{{.Target}}"><header class="site-header"><span class="wordmark">Waiting Room</span></header><main><section class="intro" aria-labelledby="heading"><h1 id="heading">대기 연결을 준비해요</h1><p class="description">잠시만 기다려 주세요. 열린 탭에서도 같은 대기 순서를 사용할 수 있도록 연결하고 있어요.</p></section><section class="status-panel" aria-label="연결 상태"><p id="join-notice" role="status" aria-live="polite">연결 확인 중</p><button id="join-retry" class="primary" type="button" hidden>다시 연결하기</button></section><noscript><p>대기 연결에는 JavaScript와 쿠키가 필요합니다. Enable JavaScript and cookies to join.</p></noscript></main><footer>Powered by Waiting Room</footer></body></html>`))

func JoinPage(w io.Writer, room, endpoint, target string, restart bool) {
	_ = joinPage.Execute(w, struct {
		Room, Endpoint, Target string
		Restart                bool
	}{room, endpoint, target, restart})
}

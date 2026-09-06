// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"net/http"
	"slices"
	"sync"
	"time"

	"waiting-room/internal/control"
)

// httpErrorWindow counts final HTTP 5xx responses, once per request, with
// one-second buckets. It contains no request URLs, headers, or visitor data.
// A new/rolled-back window is explicitly incomplete until 60 seconds elapse.
type httpErrorWindow struct {
	mu            sync.Mutex
	started, last int64
	counts        [60]int64
	seconds       [60]int64
}

func httpErrorsFor(old *roomHandler, room control.Room, now time.Time) *httpErrorWindow {
	if old != nil && old.httpErrors != nil && old.room.Origin == room.Origin && old.room.Hostname == room.Hostname && slices.Equal(old.room.ProtectPrefixes, room.ProtectPrefixes) && slices.Equal(old.room.ExcludePrefixes, room.ExcludePrefixes) {
		return old.httpErrors
	}
	return &httpErrorWindow{started: now.Unix(), last: now.Unix()}
}

func (a *httpErrorWindow) advance(now time.Time) int64 {
	sec := now.Unix()
	if sec < a.last || a.started == 0 {
		a.started = sec
		a.counts = [60]int64{}
		a.seconds = [60]int64{}
	}
	a.last = sec
	return sec
}

func (a *httpErrorWindow) observe(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sec := a.advance(now)
	i := (sec%60 + 60) % 60
	if a.seconds[i] != sec {
		a.seconds[i] = sec
		a.counts[i] = 0
	}
	a.counts[i]++
}

func (a *httpErrorWindow) snapshot(now time.Time) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sec := a.advance(now)
	var total int64
	for i, s := range a.seconds {
		if s > sec-60 && s <= sec {
			total += a.counts[i]
		}
	}
	return total, sec-a.started >= 60
}

type httpErrorWriter struct {
	http.ResponseWriter
	window *httpErrorWindow
	final  bool
}

// Unwrap preserves ResponseController support (flush, hijack, and deadlines)
// for streamed and upgraded origin responses.
func (w *httpErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *httpErrorWriter) WriteHeader(status int) {
	if !w.final && (status >= 200 || status == http.StatusSwitchingProtocols) {
		w.final = true
		if status >= 500 && status <= 599 && w.window != nil {
			w.window.observe(time.Now())
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *httpErrorWriter) Write(p []byte) (int, error) {
	if !w.final {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *httpErrorWriter) FlushError() error {
	if !w.final {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *httpErrorWriter) Flush() { _ = w.FlushError() }

func serveMeasuredRoom(w http.ResponseWriter, r *http.Request, handler http.Handler, window *httpErrorWindow) {
	handler.ServeHTTP(&httpErrorWriter{ResponseWriter: w, window: window}, r)
}

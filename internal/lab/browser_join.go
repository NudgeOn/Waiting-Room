// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"waiting-room/internal/admission"
	"waiting-room/internal/keyring"
	"waiting-room/internal/queue/valkeystore"
	"waiting-room/internal/waiting"
)

// A browser establishes this HttpOnly intent before the first queue mutation.
// The queue credential may be lost with the join response; this earlier cookie
// still supplies the same idempotency key and original target on every retry.
type browserIntent struct {
	Host, Nonce, Target, Prior string
	Issued, Expires            int64
}

func (b *browserGateway) intentCookie() string {
	if b.secure {
		return "__Host-wri_" + b.binding.Room
	}
	return "wr_dev_i_" + b.binding.Room
}
func (b *browserGateway) intentAAD() []byte {
	return []byte("wr-browser-join/v1/" + b.binding.Room + "/" + strconv.FormatUint(b.binding.Epoch, 10))
}
func (b *browserGateway) sealIntent(d browserIntent) (string, error) {
	plain, err := browserCookieJSON(d)
	if err != nil {
		return "", err
	}
	if b.binding.Keys != nil {
		return keyring.Seal(b.binding.Keys.Current, "browser-join", plain, b.intentAAD())
	}
	nonce := make([]byte, b.seal.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b.seal.Seal(nonce, nonce, plain, b.intentAAD())), nil
}
func (b *browserGateway) intent(r *http.Request) (browserIntent, error) {
	var d browserIntent
	sealed, err := uniqueCookie(r, b.intentCookie())
	if err != nil || len(sealed) == 0 || len(sealed) > 4096 {
		return d, admission.ErrInvalid
	}
	var plain []byte
	if b.binding.Keys != nil {
		plain, err = b.binding.Keys.Open("browser-join", sealed, b.intentAAD(), b.intentAAD())
	} else {
		var data []byte
		data, err = base64.RawURLEncoding.DecodeString(sealed)
		if err == nil && len(data) >= b.seal.NonceSize()+b.seal.Overhead() {
			plain, err = b.seal.Open(nil, data[:b.seal.NonceSize()], data[b.seal.NonceSize():], b.intentAAD())
		} else {
			err = admission.ErrInvalid
		}
	}
	now := time.Now().UnixMilli()
	if err != nil || json.Unmarshal(plain, &d) != nil || d.Host != r.Host || len(d.Nonce) != 43 || !b.binding.ValidTarget(d.Target) || d.Issued > now || d.Expires <= now || d.Expires-d.Issued != 600000 {
		return browserIntent{}, admission.ErrInvalid
	}
	return d, nil
}

func (b *browserGateway) preparePage(w http.ResponseWriter, r *http.Request) {
	browserHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		q, _ := uniqueCookie(r, b.queueCookie())
		waiting.JoinPage(w, b.binding.Room, b.binding.base()+"/browser-prepare", r.URL.RequestURI(), q != "")
	}
}

// prepare creates no ticket and consumes no admission capacity. Confirmation
// requires a second request carrying the cookie, so blocked/lost cookies cannot
// cause a redirect loop or an unresumable anonymous queue mutation.
func (b *browserGateway) prepare(w http.ResponseWriter, r *http.Request) {
	browserHeaders(w)
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		problem(w, 405, "INVALID_REQUEST")
		return
	}
	if !b.hostCheck(r) || len(r.Header.Values("Origin")) != 1 || len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Origin") != b.scheme()+"://"+r.Host || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/json" || r.URL.RawQuery != "" {
		problem(w, 403, "INVALID_REQUEST")
		return
	}
	var input struct {
		Target  string `json:"target"`
		Confirm bool   `json:"confirm"`
		Restart bool   `json:"restart"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF || !b.binding.ValidTarget(input.Target) {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	q, err := uniqueCookie(r, b.queueCookie())
	if err != nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	if !input.Restart && q != "" {
		sealed, cookieErr := uniqueCookie(r, b.returnCookie())
		if cookieErr == nil {
			if _, openErr := b.openReturn(sealed, r.Host, q, time.Now()); openErr == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}
	intent, err := b.intent(r)
	if err != nil || intent.Prior != valkeystore.Hash(q) {
		if input.Confirm {
			problem(w, 428, "BROWSER_STORAGE_REQUIRED")
			return
		}
		now := time.Now().UnixMilli()
		intent = browserIntent{Host: r.Host, Nonce: randomToken(), Target: input.Target, Prior: valkeystore.Hash(q), Issued: now, Expires: now + 600000}
		sealed, err := b.sealIntent(intent)
		if err != nil {
			problem(w, 503, "QUEUE_UNAVAILABLE")
			return
		}
		b.setCookie(w, b.intentCookie(), sealed, time.UnixMilli(intent.Expires))
	}
	w.WriteHeader(http.StatusNoContent)
}

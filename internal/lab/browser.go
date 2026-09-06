// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"waiting-room/internal/admission"
	"waiting-room/internal/queue/valkeystore"
	"waiting-room/internal/waiting"
)

const queueCookie = "wr_dev_q_" + Room
const admissionCookie = "wr_dev_a_" + Room
const waitPath = "/_wr/wait/" + Room
const absoluteHeader = "X-WR-Absolute-Expires"

type returnData struct {
	Host    string `json:"host"`
	Ticket  string `json:"ticket"`
	Target  string `json:"target"`
	Issued  int64  `json:"issued"`
	Expires int64  `json:"expires"`
}
type browserGateway struct {
	coordinator string
	service     string
	public      ed25519.PublicKey
	client      *http.Client
	seal        cipher.AEAD
	renderer    *waiting.Renderer
	binding     Binding
	hostCheck   func(*http.Request) bool
	secure      bool
}

func newBrowserGateway(coordinator, service string, public ed25519.PublicKey, transport http.RoundTripper, templateID string) (*browserGateway, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return newBrowserGatewayWithKey(coordinator, service, public, transport, templateID, key)
}

func newBrowserGatewayWithKey(coordinator, service string, public ed25519.PublicKey, transport http.RoundTripper, templateID string, key []byte) (*browserGateway, error) {
	if len(key) != 32 || bytes.Equal(key, make([]byte, 32)) {
		return nil, errors.New("a nonzero 256-bit Gateway return key is required")
	}
	renderer, err := waiting.NewRenderer(templateID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	seal, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &browserGateway{coordinator: coordinator, service: service, public: public, seal: seal, renderer: renderer, binding: labBinding(), hostCheck: browserHost,
		client: &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (b *browserGateway) sealReturn(d returnData) (string, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, b.seal.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b.seal.Seal(nonce, nonce, data, []byte("wr-return/v1/"+b.binding.Kid+"/"+b.binding.Room))), nil
}
func (b *browserGateway) openReturn(sealed, host, ticket string, now time.Time) (returnData, error) {
	var d returnData
	if len(sealed) > 4096 {
		return d, admission.ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(data) < b.seal.NonceSize() {
		return d, admission.ErrInvalid
	}
	plain, err := b.seal.Open(nil, data[:b.seal.NonceSize()], data[b.seal.NonceSize():], []byte("wr-return/v1/"+b.binding.Kid+"/"+b.binding.Room))
	if err != nil || json.Unmarshal(plain, &d) != nil {
		return d, admission.ErrInvalid
	}
	if d.Host != host || d.Ticket != valkeystore.Hash(ticket) || !b.binding.ValidTarget(d.Target) || d.Issued > now.UnixMilli() || d.Expires <= now.UnixMilli() || d.Expires <= d.Issued || d.Expires-d.Issued > int64(24*time.Hour/time.Millisecond) {
		return d, admission.ErrInvalid
	}
	return d, nil
}

// Local adapter accepts literal loopback authorities only, never client Forwarded headers.
func browserHost(r *http.Request) bool {
	host, port, err := net.SplitHostPort(r.Host)
	n, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	return err == nil && portErr == nil && n > 0 && n <= 65535 && ip != nil && ip.IsLoopback()
}
func navigation(r *http.Request) bool {
	return (r.Method == "GET" || r.Method == "HEAD") && (r.Header.Get("Sec-Fetch-Dest") == "document" || strings.Contains(r.Header.Get("Accept"), "text/html"))
}
func uniqueCookie(r *http.Request, name string) (string, error) {
	value, count := "", 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value, count = c.Value, count+1
		}
	}
	if count > 1 {
		return "", admission.ErrInvalid
	}
	return value, nil
}
func setLabCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires})
}
func browserHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	// Keep a real Origin on form POST while never sending the sealed query as Referer.
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

type internalResult struct {
	status int
	header http.Header
	body   []byte
}

func (b *browserGateway) call(r *http.Request, method, path, ticket string, payload []byte) (internalResult, error) {
	req, err := http.NewRequestWithContext(r.Context(), method, b.coordinator+path, bytes.NewReader(payload))
	if err != nil {
		return internalResult{}, err
	}
	req.Header.Set("X-WR-Service", b.service)
	req.Header.Set("X-WR-Room", b.binding.Room)
	if ticket != "" {
		req.Header.Set("Authorization", "Bearer "+ticket)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", randomToken())
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return internalResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(data) > 16384 {
		return internalResult{}, errors.New("invalid internal response")
	}
	return internalResult{resp.StatusCode, resp.Header, data}, nil
}
func forwardResult(w http.ResponseWriter, result internalResult) {
	for _, name := range []string{"Content-Type", "Retry-After"} {
		if v := result.header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(result.status)
	_, _ = w.Write(result.body)
}
func (b *browserGateway) join(w http.ResponseWriter, r *http.Request) {
	if !b.hostCheck(r) {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	ticket, err := uniqueCookie(r, b.queueCookie())
	if err != nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	var result internalResult
	if ticket != "" {
		result, err = b.call(r, "GET", b.binding.base()+"/status", ticket, nil)
		if err != nil {
			problem(w, 503, "QUEUE_UNAVAILABLE")
			return
		}
		// Unknown state is never permission to allocate a new ticket.
		if result.status == 410 {
			ticket = ""
		} else if result.status != 200 && result.status != 202 {
			forwardResult(w, result)
			return
		}
	}
	if ticket == "" {
		payload, _ := json.Marshal(map[string]string{"target": r.URL.RequestURI()})
		result, err = b.call(r, "POST", "/_wr/v1/tickets", "", payload)
		if err != nil {
			problem(w, 503, "QUEUE_UNAVAILABLE")
			return
		}
		if result.status != 202 {
			forwardResult(w, result)
			return
		}
		var output struct {
			TicketToken string `json:"ticketToken"`
		}
		if json.Unmarshal(result.body, &output) != nil || output.TicketToken == "" {
			problem(w, 503, "QUEUE_UNAVAILABLE")
			return
		}
		ticket = output.TicketToken
	}
	expires, err := strconv.ParseInt(result.header.Get(absoluteHeader), 10, 64)
	if err != nil || expires <= time.Now().UnixMilli() {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return
	}
	sealed, err := b.sealReturn(returnData{Host: r.Host, Ticket: valkeystore.Hash(ticket), Target: r.URL.RequestURI(), Issued: time.Now().UnixMilli(), Expires: expires})
	if err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return
	}
	browserHeaders(w)
	b.setCookie(w, b.queueCookie(), ticket, time.UnixMilli(expires))
	http.Redirect(w, r, b.waitPath()+"?return="+sealed, http.StatusSeeOther)
}
func (b *browserGateway) page(w http.ResponseWriter, r *http.Request) {
	browserHeaders(w)
	if !b.hostCheck(r) || (r.Method != "GET" && r.Method != "HEAD") {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	ticket, err := uniqueCookie(r, b.queueCookie())
	if err != nil || ticket == "" || r.Header.Get("Authorization") != "" || len(r.URL.Query()["return"]) != 1 {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	sealed := r.URL.Query().Get("return")
	d, err := b.openReturn(sealed, r.Host, ticket, time.Now())
	if err != nil {
		problem(w, 400, "INVALID_REQUEST")
		return
	}
	var out bytes.Buffer
	if err = b.renderer.Render(&out, waiting.Page{StatusURL: b.binding.base() + "/status", HeartbeatURL: b.binding.base() + "/heartbeat", ClaimURL: b.binding.base() + "/admissions?return=" + url.QueryEscape(sealed), Return: sealed, Target: d.Target}); err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		_, _ = w.Write(out.Bytes())
	}
}

// cookieAPI returns false only for a pure app Bearer request.
func (b *browserGateway) cookieAPI(w http.ResponseWriter, r *http.Request) bool {
	ticket, err := uniqueCookie(r, b.queueCookie())
	if ticket == "" && err == nil {
		return false
	}
	browserHeaders(w)
	if err != nil || !b.hostCheck(r) || r.Header.Get("Authorization") != "" {
		problem(w, 400, "INVALID_REQUEST")
		return true
	}
	path := r.URL.Path
	if (path != b.binding.base()+"/status" || r.Method != "GET") && (path != b.binding.base()+"/admissions" && path != b.binding.base()+"/heartbeat" || r.Method != "POST") {
		problem(w, 400, "INVALID_REQUEST")
		return true
	}
	var target, sealed string
	if r.Method == "POST" {
		if r.Header.Get("Origin") != b.scheme()+"://"+r.Host {
			problem(w, 403, "INVALID_REQUEST")
			return true
		}
		sealed = r.Header.Get("X-Waiting-Room-CSRF")
		if path == b.binding.base()+"/admissions" {
			if len(r.URL.Query()["return"]) != 1 {
				problem(w, 403, "INVALID_REQUEST")
				return true
			}
			sealed = r.URL.Query().Get("return")
		}
		d, err := b.openReturn(sealed, r.Host, ticket, time.Now())
		if err != nil {
			problem(w, 403, "INVALID_REQUEST")
			return true
		}
		target = d.Target
	}
	result, err := b.call(r, r.Method, path, ticket, nil)
	if err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return true
	}
	if path != b.binding.base()+"/admissions" {
		forwardResult(w, result)
		return true
	}
	if result.status != 200 {
		http.Redirect(w, r, b.waitPath()+"?return="+url.QueryEscape(sealed), http.StatusSeeOther)
		return true
	}
	var output struct {
		AdmissionToken string `json:"admissionToken"`
	}
	if json.Unmarshal(result.body, &output) != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return true
	}
	claims, err := admission.Verify(b.public, output.AdmissionToken, b.binding.Kid, b.binding.Room, b.binding.Audience, b.binding.Epoch, time.Now(), 30*time.Second)
	if err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return true
	}
	b.setCookie(w, b.admissionCookie(), output.AdmissionToken, time.Unix(claims.Expires, 0))
	http.Redirect(w, r, target, http.StatusSeeOther)
	return true
}

// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"waiting-room/internal/admission"
	"waiting-room/internal/keyring"
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
	theme       waiting.Page
	color       string
	sourceKey   []byte
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
	return &browserGateway{coordinator: coordinator, service: service, public: public, seal: seal, renderer: renderer, binding: labBinding(), hostCheck: browserHost, sourceKey: append([]byte(nil), key...),
		client: &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (b *browserGateway) sealReturn(d returnData) (string, error) {
	data, err := browserCookieJSON(d)
	if err != nil {
		return "", err
	}
	if b.binding.Keys != nil {
		return keyring.Seal(b.binding.Keys.Current, "return", data, []byte(b.binding.Room+":"+strconv.FormatUint(b.binding.Epoch, 10)))
	}
	nonce := make([]byte, b.seal.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b.seal.Seal(nonce, nonce, data, []byte("wr-return/v1/"+b.binding.Kid+"/"+b.binding.Room))), nil
}

// This browser protocol JSON is never embedded in HTML. Escaping
// every query separator as six bytes can exceed browser cookie limits for an
// otherwise valid 2048-byte target.
func browserCookieJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}
func (b *browserGateway) openReturn(sealed, host, ticket string, now time.Time) (returnData, error) {
	var d returnData
	if len(sealed) > 4096 {
		return d, admission.ErrInvalid
	}
	var plain []byte
	var err error
	if b.binding.Keys != nil {
		plain, err = b.binding.Keys.Open("return", sealed, []byte(b.binding.Room+":"+strconv.FormatUint(b.binding.Epoch, 10)), []byte("wr-return/v1/admission-v1/"+b.binding.Room))
	} else {
		var data []byte
		data, err = base64.RawURLEncoding.DecodeString(sealed)
		if err != nil || len(data) < b.seal.NonceSize() {
			return d, admission.ErrInvalid
		}
		plain, err = b.seal.Open(nil, data[:b.seal.NonceSize()], data[b.seal.NonceSize():], []byte("wr-return/v1/"+b.binding.Kid+"/"+b.binding.Room))
	}
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
	return b.callKeyed(r, method, path, ticket, payload, "")
}
func (b *browserGateway) callKeyed(r *http.Request, method, path, ticket string, payload []byte, key string) (internalResult, error) {
	req, err := http.NewRequestWithContext(r.Context(), method, b.coordinator+path, bytes.NewReader(payload))
	if err != nil {
		return internalResult{}, err
	}
	req.Header.Set("X-WR-Service", b.service)
	req.Header.Set("X-WR-Room", b.binding.Room)
	req.Header.Set("X-WR-Source", b.sourceFingerprint(r))
	if ticket != "" {
		req.Header.Set("Authorization", "Bearer "+ticket)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		if key == "" {
			key = randomToken()
		}
		req.Header.Set("Idempotency-Key", key)
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
func navigationProblem(w http.ResponseWriter, r *http.Request, status int, code string) {
	if navigation(r) && (status == 429 || status == 503) {
		waiting.Problem(w, r, status, code)
		return
	}
	problem(w, status, code)
}
func forwardNavigationResult(w http.ResponseWriter, r *http.Request, result internalResult) {
	if navigation(r) && (result.status == 429 || result.status == 503) {
		var p struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(result.body, &p)
		w.Header().Set("Retry-After", result.header.Get("Retry-After"))
		waiting.Problem(w, r, result.status, p.Code)
		return
	}
	forwardResult(w, result)
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
	intent, intentErr := b.intent(r)
	prior := valkeystore.Hash(ticket)
	var result internalResult
	if ticket != "" {
		// A sealed, host/ticket-bound return cookie resumes navigation without
		// consuming a status poll or extending either the ticket or envelope TTL.
		sealed, cookieErr := uniqueCookie(r, b.returnCookie())
		if cookieErr != nil {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		if d, openErr := b.openReturn(sealed, r.Host, ticket, time.Now()); openErr == nil && (intentErr != nil || intent.Prior != prior) {
			b.redirectWaiting(w, r, ticket, d.Expires, false)
			return
		}
		result, err = b.call(r, "GET", b.binding.base()+"/status", ticket, nil)
		if err != nil {
			problem(w, 503, "QUEUE_UNAVAILABLE")
			return
		}
		// Unknown state is never permission to allocate a new ticket.
		if result.status == 410 {
			ticket = ""
		} else if result.status != 200 && result.status != 202 {
			forwardNavigationResult(w, r, result)
			return
		}
	}
	if ticket == "" {
		if intentErr != nil || intent.Prior != prior {
			b.preparePage(w, r)
			return
		}
		payload, _ := browserCookieJSON(map[string]string{"target": intent.Target})
		result, err = b.callKeyed(r, "POST", "/_wr/v1/tickets", "", payload, intent.Nonce)
		if err != nil {
			problem(w, 503, "QUEUE_UNAVAILABLE")
			return
		}
		if result.status != 202 {
			forwardNavigationResult(w, r, result)
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
	b.redirectWaiting(w, r, ticket, expires, true)
}

func (b *browserGateway) redirectWaiting(w http.ResponseWriter, r *http.Request, ticket string, expires int64, setTicket bool) {
	issued := time.Now().UnixMilli()
	// Queue expiry uses Valkey TIME, which can be slightly ahead of this host.
	// Bound the return envelope by both clocks without extending the ticket or
	// weakening openReturn's 24-hour limit.
	returnExpires := min(expires, issued+int64(24*time.Hour/time.Millisecond))
	sealed, err := b.sealReturn(returnData{Host: r.Host, Ticket: valkeystore.Hash(ticket), Target: r.URL.RequestURI(), Issued: issued, Expires: returnExpires})
	if err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return
	}
	browserHeaders(w)
	if setTicket {
		b.setCookie(w, b.queueCookie(), ticket, time.UnixMilli(expires))
	}
	b.setCookie(w, b.returnCookie(), sealed, time.UnixMilli(returnExpires))
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
	page := b.theme
	page.StatusURL = b.binding.base() + "/status"
	page.HeartbeatURL = b.binding.base() + "/heartbeat"
	page.ClaimURL = b.binding.base() + "/admissions?return=" + url.QueryEscape(sealed)
	page.Return = sealed
	page.Target = d.Target
	page.Room = b.binding.Room
	page.PrepareURL = b.binding.base() + "/browser-prepare"
	if err = b.renderer.Render(&out, page); err != nil {
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
	method := map[string]string{b.binding.base() + "/status": "GET", b.binding.base() + "/admissions": "POST", b.binding.base() + "/heartbeat": "POST"}[path]
	if method == "" {
		problem(w, 404, "NOT_FOUND")
		return true
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		problem(w, 405, "METHOD_NOT_ALLOWED")
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
	claims, err := b.verifyAdmission(output.AdmissionToken, time.Now())
	if err != nil {
		problem(w, 503, "QUEUE_UNAVAILABLE")
		return true
	}
	b.setCookie(w, b.admissionCookie(), output.AdmissionToken, time.Unix(claims.Expires, 0))
	http.Redirect(w, r, target, http.StatusSeeOther)
	return true
}

// The transport peer is authoritative. Forwarded headers are never source proof.
// IPv6 /64 grouping and 15-minute HMAC rotation avoid retaining raw client IPs.
func (b *browserGateway) sourceFingerprint(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	source := ip.String()
	if ip.Is6() {
		source = netip.PrefixFrom(ip, 64).Masked().String()
	}
	mac := hmac.New(sha256.New, b.sourceKey)
	mac.Write([]byte("waiting-room/source/v1/" + strconv.FormatInt(time.Now().Unix()/900, 10) + "/" + source))
	return hex.EncodeToString(mac.Sum(nil))
}

func (b *browserGateway) verifyAdmission(token string, now time.Time) (admission.Claims, error) {
	if b.binding.Keys != nil {
		for _, k := range b.binding.Keys.Keys() {
			if claims, err := admission.Verify(k.AdmissionPublic, token, k.AdmissionKid(), b.binding.Room, b.binding.Audience, b.binding.Epoch, now, 30*time.Second); err == nil {
				return claims, nil
			}
		}
		return admission.Claims{}, admission.ErrInvalid
	}
	return admission.Verify(b.public, token, b.binding.Kid, b.binding.Room, b.binding.Audience, b.binding.Epoch, now, 30*time.Second)
}

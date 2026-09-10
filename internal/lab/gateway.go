// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"waiting-room/internal/admission"
	"waiting-room/internal/control"
	"waiting-room/internal/waiting"
)

func loopbackURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, e
	}
	host := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || host == nil || !host.IsLoopback() || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, admission.ErrInvalid
	}
	return u, nil
}
func strip(h http.Header) {
	for k := range h {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-wr-") || strings.HasPrefix(lower, "x-waiting-room-") || strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
			h.Del(k)
		}
	}
}
func stripCookies(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if !strings.HasPrefix(c.Name, "__Host-wr") && !strings.HasPrefix(c.Name, "wr_dev_") {
			r.AddCookie(c)
		}
	}
}
func NewGateway(coordinator, origin, service string, public ed25519.PublicKey) (http.Handler, error) {
	return NewGatewayWithTemplate(coordinator, origin, service, public, "calm")
}
func NewGatewayWithTemplate(coordinator, origin, service string, public ed25519.PublicKey, templateID string) (http.Handler, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return NewGatewayWithReturnKey(coordinator, origin, service, public, templateID, key)
}

// NewGatewayWithReturnKey constructs an independent Gateway using the same
// installation-scoped return key as its peers. No affinity is required behind
// one public authority. This lab key is memory-only; deployment mounts, key IDs,
// distribution acknowledgements and rotation remain production requirements.
func NewGatewayWithReturnKey(coordinator, origin, service string, public ed25519.PublicKey, templateID string, key []byte) (http.Handler, error) {
	c, e := loopbackURL(coordinator)
	if e != nil {
		return nil, e
	}
	o, e := loopbackURL(origin)
	if e != nil {
		return nil, e
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, ResponseHeaderTimeout: 2 * time.Second, MaxIdleConnsPerHost: 32}
	browser, e := newBrowserGatewayWithKey(coordinator, service, public, transport, templateID, key)
	if e != nil {
		return nil, e
	}
	return gatewayHandler(c, o, service, public, transport, transport, browser, false, nil, "HOLD"), nil
}

// NewBoundGateway requires caller-provided HTTPS transports that pin approved
// origin addresses and mutually authenticate the Coordinator. It never consults
// environment proxy settings or accepts an origin supplied by a public request.
func NewBoundGateway(coordinator, origin, service string, public ed25519.PublicKey, theme control.Theme, key []byte, binding Binding, authority string, coordinatorTransport, originTransport http.RoundTripper, off bool, onProtected func(), runtimeMode ...string) (http.Handler, error) {
	c, err := url.Parse(coordinator)
	if err != nil {
		return nil, err
	}
	o, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	if (binding.Keys != nil && binding.Keys.Validate("gateway") != nil) || binding.validate() != nil || c.Scheme != "https" || o.Scheme != "https" || c.User != nil || o.User != nil || c.Host == "" || o.Host == "" || coordinatorTransport == nil || originTransport == nil || authority == "" || len(service) < 32 || len(public) != ed25519.PublicKeySize {
		return nil, admission.ErrInvalid
	}
	if !regexp.MustCompile(`^#[a-fA-F0-9]{6}$`).MatchString(theme.PrimaryColor) {
		return nil, admission.ErrInvalid
	}
	browser, err := newBrowserGatewayWithKey(coordinator, service, public, coordinatorTransport, theme.TemplateID, key)
	if err != nil {
		return nil, err
	}
	browser.binding = binding
	if len(binding.SourceKey) > 0 {
		if len(binding.SourceKey) != 32 {
			return nil, admission.ErrInvalid
		}
		browser.sourceKey = append([]byte(nil), binding.SourceKey...)
	}
	browser.secure = true
	browser.color = theme.PrimaryColor
	browser.theme = waiting.Page{ThemeEnabled: true, ThemeTitle: theme.Title, ThemeMessage: theme.Message, ThemeLocale: theme.Locale, ShowEstimatedWait: theme.ShowEstimatedWait, ThemeURL: "/_wr/theme/" + binding.Room + ".css"}
	logo, err := control.LogoBytes(theme.LogoImage)
	if err != nil {
		return nil, admission.ErrInvalid
	}
	if len(logo) > 0 {
		browser.theme.LogoURL = "/_wr/theme/" + binding.Room + "/logo.png"
	}
	browser.hostCheck = func(r *http.Request) bool { return r.TLS != nil && r.Host == authority }
	mode := "HOLD"
	if off {
		mode = "OFF"
	}
	if len(runtimeMode) > 1 {
		return nil, admission.ErrInvalid
	}
	if len(runtimeMode) == 1 {
		mode = runtimeMode[0]
	}
	if mode != "AUTO" && mode != "HOLD" && mode != "DRAINING" && mode != "OFF" {
		return nil, admission.ErrInvalid
	}
	inner := gatewayHandler(c, o, service, public, coordinatorTransport, originTransport, browser, off, onProtected, mode)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !browser.hostCheck(r) {
			problem(w, 421, "MISDIRECTED_REQUEST")
			return
		}
		if browser.theme.LogoURL != "" && r.URL.Path == browser.theme.LogoURL {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
			if r.Method != "GET" && r.Method != "HEAD" {
				w.Header().Set("Allow", "GET, HEAD")
				w.WriteHeader(405)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			if r.Method == "GET" {
				_, _ = w.Write(logo)
			}
			return
		}
		if browser.secure && r.URL.Path == browser.theme.ThemeURL {
			if r.Method != "GET" && r.Method != "HEAD" {
				w.WriteHeader(405)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if r.Method == "GET" {
				_, _ = w.Write([]byte(":root{--accent:" + browser.color + "}.description{white-space:pre-line}"))
			}
			return
		}
		inner.ServeHTTP(w, r)
	}), nil
}

func gatewayHandler(c, o *url.URL, service string, public ed25519.PublicKey, coordinatorTransport, originTransport http.RoundTripper, browser *browserGateway, off bool, onProtected func(), mode string) http.Handler {
	errorHandler := func(w http.ResponseWriter, r *http.Request, e error) {
		navigationProblem(w, r, 503, "QUEUE_UNAVAILABLE")
	}
	cp := &httputil.ReverseProxy{Transport: coordinatorTransport, ErrorHandler: errorHandler, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(c)
		strip(p.Out.Header)
		p.Out.Header.Del("Cookie")
		p.Out.Header.Set("X-WR-Service", service)
		p.Out.Header.Set("X-WR-Room", browser.binding.Room)
		p.Out.Header.Set("X-WR-Source", browser.sourceFingerprint(p.In))
	}}
	cp.ModifyResponse = func(r *http.Response) error { r.Header.Del(absoluteHeader); return nil }
	op := &httputil.ReverseProxy{Transport: originTransport, ErrorHandler: errorHandler, Rewrite: func(p *httputil.ProxyRequest) { p.SetURL(o); strip(p.Out.Header); stripCookies(p.Out) }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/livez" && r.Method == "GET" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == browser.binding.base()+"/browser-prepare" {
			browser.prepare(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_wr/v1/") {
			if browser.cookieAPI(w, r) {
				return
			}
			cp.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == browser.waitPath() {
			browser.page(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_wr/assets/") {
			waiting.Asset(w, r)
			return
		}
		if !browser.binding.ValidTarget(r.URL.RequestURI()) {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if onProtected != nil {
			onProtected()
		}
		if off {
			op.ServeHTTP(w, r)
			return
		}
		if len(r.Header.Values("X-Waiting-Room-Admission")) > 1 {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		token := r.Header.Get("X-Waiting-Room-Admission")
		cookie, cookieErr := uniqueCookie(r, browser.admissionCookie())
		if cookieErr != nil || (cookie != "" && token != "") {
			problem(w, 400, "INVALID_REQUEST")
			return
		}
		if cookie != "" {
			token = cookie
		}
		if _, e := browser.verifyAdmission(token, time.Now()); e != nil {
			if mode == "DRAINING" {
				q, _ := uniqueCookie(r, browser.queueCookie())
				if q == "" || !navigation(r) {
					navigationProblem(w, r, 503, "QUEUE_DRAINING")
					return
				}
			}
			if navigation(r) {
				browser.join(w, r)
				return
			}
			problem(w, 429, "WAITING_ROOM_REQUIRED")
			return
		}
		op.ServeHTTP(w, r)
	})
}

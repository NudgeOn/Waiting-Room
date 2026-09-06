// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"waiting-room/internal/localcontrol"
)

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func originDial(raw string, lookup resolver) (func(context.Context, string, string) (net.Conn, error), error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || lookup == nil {
		return nil, errors.New("invalid origin")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	expected := net.JoinHostPort(u.Hostname(), port)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != expected {
			return nil, errors.New("origin authority mismatch")
		}
		ips, err := lookup.LookupNetIP(ctx, "ip", u.Hostname())
		if err != nil || len(ips) == 0 || len(ips) > 32 {
			return nil, errors.New("origin DNS unavailable")
		}
		// The compiled Docker example is the sole private-address exception; it is
		// not a user-controlled suffix/allowlist and must use installation mTLS.
		demo := raw == "https://demo-origin:20445"
		for _, ip := range ips {
			if !publicAddress(ip) && !demo {
				return nil, errors.New("private origin denied")
			}
		}
		d := net.Dialer{Timeout: 2 * time.Second}
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, errors.New("origin connection unavailable")
	}, nil
}
func OriginTransport(raw string, n localcontrol.NodeIdentity) (*http.Transport, error) {
	dial, err := originDial(raw, net.DefaultResolver)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if raw == "https://demo-origin:20445" {
		tlsConfig, err = n.TLS(true)
		if err != nil {
			return nil, err
		}
	}
	return &http.Transport{Proxy: nil, DialContext: dial, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxIdleConnsPerHost: 32, IdleConnTimeout: 30 * time.Second}, nil
}
func internalTransport(n localcontrol.NodeIdentity) (*http.Transport, error) {
	c, err := n.TLS(true)
	if err != nil {
		return nil, err
	}
	return &http.Transport{Proxy: nil, TLSClientConfig: c, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxIdleConnsPerHost: 32, IdleConnTimeout: 30 * time.Second}, nil
}
func healthy(ctx context.Context, client *http.Client, raw string) bool {
	r, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return false
	}
	r.Header.Set("User-Agent", "Waiting-Room-Health/1")
	resp, err := client.Do(r)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
func stripOriginHeaders(h http.Header) {
	for k := range h {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-wr-") || strings.HasPrefix(lower, "x-waiting-room-") || strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
			h.Del(k)
		}
	}
}

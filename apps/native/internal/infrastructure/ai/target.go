package ai

import (
	"context"
	"crypto/tls"
	"errors"
	"golang.org/x/net/idna"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrTarget = errors.New("ai_destination_rejected")

type lookup func(context.Context, string) ([]netip.Addr, error)
type trustedTarget struct {
	base      *url.URL
	addresses []netip.Addr
	port      string
}

func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}
func resolveTarget(ctx context.Context, value string, lookup lookup) (trustedTarget, error) {
	t := trustedTarget{}
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return t, ErrTarget
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Opaque != "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") {
		return t, ErrTarget
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, "%\\ \t\n\r") {
		return t, ErrTarget
	}
	explicitPort := u.Port()
	port := explicitPort
	if port == "" {
		if strings.HasSuffix(u.Host, ":") {
			return t, ErrTarget
		}
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return t, ErrTarget
	}
	ip, err := netip.ParseAddr(host)
	if err == nil {
		if !ip.Unmap().IsLoopback() && (u.Scheme != "https" || !publicAddress(ip)) {
			return t, ErrTarget
		}
		t.addresses = []netip.Addr{ip}
	} else {
		if u.Scheme != "https" {
			return t, ErrTarget
		}
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil || ascii == "" {
			return t, ErrTarget
		}
		resolveCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		addresses, err := lookup(resolveCtx, ascii)
		if err != nil || len(addresses) == 0 {
			return t, ErrTarget
		}
		seen := map[netip.Addr]bool{}
		for _, ip := range addresses {
			if !publicAddress(ip) {
				return t, ErrTarget
			}
			if !seen[ip] {
				t.addresses = append(t.addresses, ip)
				seen[ip] = true
			}
		}
		u.Host = ascii
		if explicitPort != "" {
			u.Host = net.JoinHostPort(ascii, port)
		}
	}
	t.base, t.port = u, port
	return t, nil
}
func (t trustedTarget) endpoint() string {
	u := *t.base
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/") + "/chat/completions"
	}
	return u.String()
}
func (t trustedTarget) transport() *http.Transport {
	dialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 120 * time.Second, MaxResponseHeaderBytes: 1 << 20,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.base.Hostname()},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, t.base.Hostname()) || port != t.port {
				return nil, ErrTarget
			}
			var last error = ErrNetwork
			for _, ip := range t.addresses {
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				last = err
				if ctx.Err() != nil {
					break
				}
			}
			return nil, last
		},
	}
}

// Same reserved ranges as infrastructure/network/trusted_http.rs. A literal
// loopback URL is allowed above; DNS names resolving there are always rejected.
func publicAddress(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if ip.Is4() {
		b := ip.As4()
		a, c, d := b[0], b[1], b[2]
		return !(a == 0 || a == 10 || a == 127 || a >= 224 || (a == 100 && c >= 64 && c <= 127) || (a == 169 && c == 254) || (a == 172 && c >= 16 && c <= 31) || (a == 192 && c == 168) || (a == 192 && c == 0 && (d == 0 || d == 2)) || (a == 192 && c == 88 && d == 99) || (a == 198 && (c == 18 || c == 19)) || (a == 198 && c == 51 && d == 100) || (a == 203 && c == 0 && d == 113) || b == [4]byte{168, 63, 129, 16})
	}
	b := ip.As16()
	a := uint16(b[0])<<8 | uint16(b[1])
	c := uint16(b[2])<<8 | uint16(b[3])
	d := uint16(b[4])<<8 | uint16(b[5])
	if a&0xe000 != 0x2000 {
		return false
	}
	special := a == 0x2001 && (c == 0 || (c == 2 && d == 0) || (c&0xfff0) == 0x0010 || (c&0xfff0) == 0x0020 || c == 0x0db8)
	return !special && a != 0x2002 && !(a == 0x3fff && (c&0xf000) == 0)
}

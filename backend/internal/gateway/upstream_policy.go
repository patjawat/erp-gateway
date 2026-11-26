package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

var errUpstreamPolicy = errors.New("hospital upstream is not allowed")
var privateRoutes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"),
}
var nonPublicUpstream = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("168.63.129.16/32"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
}

type upstreamPolicy struct {
	bases     map[string]string
	private   []netip.Prefix
	allowHTTP bool
}

// ValidateUpstreamPolicy validates a draft without dialing any destination.
func ValidateUpstreamPolicy(entries, networks []string) error {
	_, err := newUpstreamPolicy(entries, networks, false)
	return err
}

func parseUpstreamURL(value string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.Opaque != "" || u.RawPath != "" ||
		strings.ContainsAny(value, "\\\r\n%#") || strings.HasSuffix(u.Host, ":") {
		return nil, errUpstreamPolicy
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && allowHTTP) {
		return nil, errUpstreamPolicy
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return nil, errUpstreamPolicy
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path != "" && path.Clean(u.Path) != u.Path {
		return nil, errUpstreamPolicy
	}
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func newUpstreamPolicy(entries, networks []string, allowHTTP bool) (*upstreamPolicy, error) {
	p := &upstreamPolicy{bases: map[string]string{}, allowHTTP: allowHTTP}
	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		code, raw, ok := strings.Cut(strings.TrimSpace(entry), "=")
		u, err := parseUpstreamURL(raw, allowHTTP)
		if !ok || !codePattern.MatchString(code) || err != nil || p.bases[code] != "" {
			return nil, errors.New("invalid or duplicate GATEWAY_APPROVED_UPSTREAMS entry")
		}
		p.bases[code] = u.String()
	}
	for _, value := range networks {
		if strings.TrimSpace(value) == "" {
			continue
		}
		cidr, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || !privateCIDR(cidr) {
			return nil, errors.New("GATEWAY_PRIVATE_UPSTREAM_CIDRS must be specific private, VPN or loopback networks")
		}
		p.private = append(p.private, cidr.Masked())
	}
	return p, nil
}

func privateCIDR(cidr netip.Prefix) bool {
	for _, allowed := range privateRoutes {
		if cidr.Bits() >= allowed.Bits() && allowed.Contains(cidr.Masked().Addr()) {
			return true
		}
	}
	return false
}

func (p *upstreamPolicy) approvedURL(code, value string) (*url.URL, error) {
	u, err := parseUpstreamURL(value, p.allowHTTP)
	if err != nil || p.bases[code] != u.String() {
		return nil, errUpstreamPolicy
	}
	return u, nil
}

func (p *upstreamPolicy) allowedIP(ip netip.Addr, plaintext bool) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	for _, cidr := range p.private {
		if cidr.Contains(ip) {
			return true
		}
	}
	if plaintext || !ip.IsGlobalUnicast() {
		return false
	}
	for _, ranges := range [][]netip.Prefix{privateRoutes, nonPublicUpstream} {
		for _, cidr := range ranges {
			if cidr.Contains(ip) {
				return false
			}
		}
	}
	return true
}

type upstreamLookup func(context.Context, string, string) ([]netip.Addr, error)
type upstreamDial func(context.Context, string, string) (net.Conn, error)

func (p *upstreamPolicy) dial(ctx context.Context, network, addr string, plaintext bool, lookup upstreamLookup, dial upstreamDial) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, errUpstreamPolicy
	}
	ip, err := netip.ParseAddr(host)
	ips := []netip.Addr{ip}
	if err != nil {
		ips, err = lookup(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errUpstreamPolicy
		}
	}
	// Reject the entire answer if any candidate is outside policy. Dial the
	// checked address directly, with no second DNS lookup.
	for _, ip := range ips {
		if !p.allowedIP(ip, plaintext) {
			return nil, errUpstreamPolicy
		}
	}
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errUpstreamPolicy
}

type upstreamTransport struct{ secure, local *http.Transport }

func (t upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Scheme {
	case "https":
		return t.secure.RoundTrip(req)
	case "http":
		return t.local.RoundTrip(req)
	default:
		return nil, errUpstreamPolicy
	}
}

func (p *upstreamPolicy) transport() http.RoundTripper {
	build := func(plaintext bool) *http.Transport {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		return &http.Transport{
			// No proxy-from-environment: the policy must control the remote TCP address.
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return p.dial(ctx, network, addr, plaintext, net.DefaultResolver.LookupNetIP, dialer.DialContext)
			},
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 8 * time.Second, ForceAttemptHTTP2: true,
			IdleConnTimeout: 60 * time.Second, MaxIdleConns: 40, MaxIdleConnsPerHost: 4,
		}
	}
	return upstreamTransport{secure: build(false), local: build(true)}
}

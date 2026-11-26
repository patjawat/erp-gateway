// Package webtransport defines the operator-controlled transport policy for the
// Gateway management UI. It does not change Gateway-to-Hospital transport.
package webtransport

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Policy's zero value permits HTTPS URLs only.
type Policy struct {
	lanHTTP bool
	peers   []netip.Prefix
}

var privateNetworks = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// New requires canonical, entirely private client networks when HTTP is enabled.
func New(allowLANHTTP bool, cidrs []string) (Policy, error) {
	var policy Policy
	if len(cidrs) > 128 || (allowLANHTTP && len(cidrs) == 0) {
		return policy, errors.New("GATEWAY_LAN_HTTP_CIDRS must contain 1-128 canonical private client networks when GATEWAY_ALLOW_LAN_HTTP is enabled")
	}
	seen := make(map[netip.Prefix]bool)
	for _, value := range cidrs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() || !privatePrefix(prefix) || seen[prefix] {
			return Policy{}, errors.New("GATEWAY_LAN_HTTP_CIDRS must contain unique canonical private client networks")
		}
		seen[prefix] = true
		policy.peers = append(policy.peers, prefix)
	}
	policy.lanHTTP = allowLANHTTP
	return policy, nil
}

func privatePrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || prefix.Addr().Is4In6() {
		return false
	}
	for _, network := range privateNetworks {
		if prefix.Addr().BitLen() == network.Addr().BitLen() && prefix.Bits() >= network.Bits() && network.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func (p Policy) LANHTTPEnabled() bool { return p.lanHTTP }

// ValidateURL accepts HTTP only for an explicitly enabled literal private IP.
func (p Policy) ValidateURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || value != strings.TrimSpace(value) || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(value, "\\\r\n#") || strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("ที่อยู่ Gateway ต้องไม่มี path, query หรือข้อมูลผู้ใช้ และต้องใช้ HTTPS หรือ HTTP ภายในที่เปิดไว้")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("พอร์ตของ Gateway ต้องอยู่ระหว่าง 1-65535")
		}
	}
	if u.Scheme == "https" {
		return u, nil
	}
	if u.Scheme == "http" && p.lanHTTP {
		ip, err := netip.ParseAddr(u.Hostname())
		if err == nil && ip.Zone() == "" && !ip.Is4In6() && ip.IsPrivate() {
			// Browsers normalize literal IPv6 addresses and omit the default
			// HTTP port. Compare request Host/Origin with that same authority.
			host := ip.String()
			if ip.Is6() {
				host = "[" + host + "]"
			}
			if port := u.Port(); port != "" {
				number, _ := strconv.Atoi(port) // Validated above.
				if number != 80 {
					host = net.JoinHostPort(ip.String(), strconv.Itoa(number))
				}
			}
			u.Host = host
			return u, nil
		}
	}
	return nil, errors.New("ที่อยู่ Gateway ต้องเป็น HTTPS; HTTP ใช้ได้เฉพาะ IP ภายในเมื่อเปิด GATEWAY_ALLOW_LAN_HTTP")
}

func (p Policy) IsLANHTTPURL(value string) bool {
	u, err := p.ValidateURL(value)
	return err == nil && u.Scheme == "http"
}

// AllowsLANRequest permits only direct requests with an exact configured Host.
// Forwarded metadata is never used to authorize plaintext management traffic.
func (p Policy) AllowsLANRequest(r *http.Request, gatewayURL string) bool {
	u, err := p.ValidateURL(gatewayURL)
	if err != nil || u.Scheme != "http" || r == nil || r.TLS != nil || r.Host != u.Host {
		return false
	}
	for name := range r.Header {
		name = strings.ToLower(name)
		if name == "forwarded" || strings.HasPrefix(name, "x-forwarded-") || name == "x-real-ip" || name == "front-end-https" || name == "x-url-scheme" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || peer.Zone() != "" {
		return false
	}
	peer = peer.Unmap()
	for _, network := range p.peers {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

// AllowsLANOrigin additionally requires a browser's exact HTTP origin.
func (p Policy) AllowsLANOrigin(r *http.Request, gatewayURL string) bool {
	if !p.AllowsLANRequest(r, gatewayURL) {
		return false
	}
	u, _ := p.ValidateURL(gatewayURL)
	return r.Header.Get("Origin") == "http://"+u.Host
}

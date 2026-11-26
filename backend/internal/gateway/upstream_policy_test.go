package gateway

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"testing"
)

func TestApprovedUpstreamIsBoundToHospitalAndBase(t *testing.T) {
	p, err := newUpstreamPolicy([]string{"01447=https://approved.example.org/prefix", "02000=https://other.example.org"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		code, url string
		ok        bool
	}{
		{"01447", "https://approved.example.org/prefix/", true},
		{"01447", "https://approved.example.org", false},
		{"01447", "https://other.example.org", false},
		{"02000", "https://other.example.org:8443", false},
		{"02000", "http://other.example.org", false},
		{"09000", "https://other.example.org", false},
	} {
		_, err := p.approvedURL(test.code, test.url)
		if (err == nil) != test.ok {
			t.Fatalf("unexpected policy for %v: %v", test, err)
		}
	}
	empty, err := newUpstreamPolicy(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.approvedURL("01447", "https://approved.example.org"); err == nil {
		t.Fatal("missing configuration must fail closed")
	}
}

func TestUpstreamApprovedPrivateRouteAndResolution(t *testing.T) {
	p, err := newUpstreamPolicy(nil, []string{"10.20.30.55/32", "127.0.0.1/32", "fd50::20/128"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		ip                 string
		plaintext, allowed bool
	}{
		{"10.20.30.55", false, true}, {"10.20.30.56", false, false}, {"10.20.30.55", true, true},
		{"127.0.0.1", true, true}, {"127.0.0.2", true, false}, {"fd50::20", false, true},
		{"::ffff:10.20.30.56", false, false}, {"169.254.2.4", false, false},
		{"8.8.8.8", true, false}, {"8.8.8.8", false, true},
	} {
		if got := p.allowedIP(netip.MustParseAddr(test.ip), test.plaintext); got != test.allowed {
			t.Fatalf("unexpected IP policy for %v", test)
		}
	}
	if _, err := newUpstreamPolicy(nil, []string{"0.0.0.0/0"}, true); err == nil {
		t.Fatal("broad range accepted")
	}
	var actual string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		actual = address
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	resolver := func(ips ...string) upstreamLookup {
		return func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			out := make([]netip.Addr, 0, len(ips))
			for _, ip := range ips {
				out = append(out, netip.MustParseAddr(ip))
			}
			return out, nil
		}
	}
	c, err := p.dial(context.Background(), "tcp", "approved.example.org:443", false, resolver("10.20.30.55", "10.20.30.56"), dial)
	if c != nil || err == nil || actual != "" {
		t.Fatal("mixed resolution must fail without dialing")
	}
	c, err = p.dial(context.Background(), "tcp", "approved.example.org:443", false, resolver("10.20.30.55"), dial)
	if err != nil || actual != "10.20.30.55:443" {
		t.Fatalf("verified address not pinned: %q %v", actual, err)
	}
	c.Close()
	transport := p.transport().(upstreamTransport)
	for _, tr := range []*http.Transport{transport.local, transport.secure} {
		if tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.ServerName != "" {
			t.Fatal("direct transport must retain normal TLS name verification")
		}
	}
}

func TestUnapprovedRegistryChangeRejectedBeforeUpstream(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	other := "https://11448.example.invalid"
	request := map[string]string{"code": h.Code, "name": "Fixture hospital", "api_url": other}
	res := r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, request, "", true)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("registry allowed a base not approved for this hospital: %d", res.Code)
	}
	// Simulate an existing persisted registration that no longer matches the
	// operator allowlist; the business request must not call the test upstream.
	r.app.egress.bases[h.Code] = other
	want := r.erpCalls[h.Code]
	res = r.request(t, "GET", "/api/profile", nil, token, false)
	if res.Code != http.StatusServiceUnavailable || r.erpCalls[h.Code] != want {
		t.Fatal("persisted registration mismatch did not fail before forwarding")
	}
}

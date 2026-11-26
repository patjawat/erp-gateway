package webtransport

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestPolicyOptInAndPrivateURLValidation(t *testing.T) {
	policy, err := New(true, []string{"10.10.8.0/24", "fd12:3456::/48"})
	if err != nil || !policy.LANHTTPEnabled() {
		t.Fatalf("enable policy: %v", err)
	}
	for _, value := range []string{"http://10.10.8.127:8080", "http://[fd12:3456::1]:8080", "https://gateway.example.org"} {
		if _, err := policy.ValidateURL(value); err != nil {
			t.Errorf("allowed URL %q: %v", value, err)
		}
	}
	for _, value := range []string{"http://gateway.example.org", "http://8.8.8.8", "http://127.0.0.1", "http://169.254.1.1", "http://[::ffff:10.10.8.127]", "http://10.10.8.127:0", "http://10.10.8.127:65536", "http://10.10.8.127:", "http://user@10.10.8.127", "http://10.10.8.127/setup", "http://10.10.8.127?", "http://10.10.8.127#", " http://10.10.8.127"} {
		if _, err := policy.ValidateURL(value); err == nil {
			t.Errorf("unsafe URL accepted: %q", value)
		}
	}
	var strict Policy
	if _, err := strict.ValidateURL("http://10.10.8.127:8080"); err == nil || strict.LANHTTPEnabled() || strict.IsLANHTTPURL("http://10.10.8.127:8080") {
		t.Fatal("zero policy enabled plaintext HTTP")
	}
	if _, err := strict.ValidateURL("https://gateway.example.org"); err != nil {
		t.Fatal(err)
	}
	for _, cidrs := range [][]string{nil, {"10.10.8.127"}, {"10.10.8.1/24"}, {"0.0.0.0/0"}, {"10.0.0.0/7"}, {"8.8.8.8/32"}, {"127.0.0.1/32"}, {"fe80::/10"}, {"::/0"}, {"::ffff:10.0.0.0/104"}, {"10.10.8.0/24", "10.10.8.0/24"}} {
		if _, err := New(true, cidrs); err == nil {
			t.Errorf("unsafe/missing client allowlist accepted: %v", cidrs)
		}
	}
}

func TestDirectLANRequestAndOrigin(t *testing.T) {
	policy, _ := New(true, []string{"10.10.8.0/24"})
	const publicURL = "http://10.10.8.127:8080"
	request := httptest.NewRequest("POST", publicURL+"/setup/api/session", nil)
	request.RemoteAddr = "10.10.8.20:54321"
	request.Header.Set("Origin", publicURL)
	if !policy.AllowsLANRequest(request, publicURL) || !policy.AllowsLANOrigin(request, publicURL) {
		t.Fatal("configured direct LAN request denied")
	}
	for _, peer := range []string{"10.10.9.20:54321", "203.0.113.1:54321", "127.0.0.1:54321", "invalid"} {
		copy := request.Clone(request.Context())
		copy.RemoteAddr = peer
		if policy.AllowsLANRequest(copy, publicURL) {
			t.Errorf("unlisted peer accepted: %s", peer)
		}
	}
	for _, host := range []string{"10.10.8.127", "10.10.8.127:8081", "attacker.example:8080"} {
		copy := request.Clone(request.Context())
		copy.Host = host
		if policy.AllowsLANRequest(copy, publicURL) {
			t.Errorf("wrong Host accepted: %s", host)
		}
	}
	for _, origin := range []string{"", "https://10.10.8.127:8080", "http://attacker.example", publicURL + "/", "http://10.10.8.127"} {
		copy := request.Clone(request.Context())
		copy.Header.Set("Origin", origin)
		if policy.AllowsLANOrigin(copy, publicURL) {
			t.Errorf("wrong Origin accepted: %s", origin)
		}
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP", "Front-End-Https", "X-Url-Scheme"} {
		copy := request.Clone(request.Context())
		copy.Header.Set(header, "forged")
		if policy.AllowsLANRequest(copy, publicURL) || policy.AllowsLANOrigin(copy, publicURL) {
			t.Errorf("forwarded request accepted: %s", header)
		}
	}
	copy := request.Clone(request.Context())
	copy.TLS = &tls.ConnectionState{}
	if policy.AllowsLANRequest(copy, publicURL) {
		t.Fatal("HTTPS request confused with direct HTTP")
	}
	if (Policy{}).AllowsLANRequest(request, publicURL) {
		t.Fatal("disabled policy accepted LAN HTTP")
	}
}

func TestHTTPAuthorityMatchesBrowserNormalization(t *testing.T) {
	policy, err := New(true, []string{"10.10.8.0/24", "fd12:3456::/48"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		configured, browserURL, peer, wrongPort string
	}{
		{"http://10.10.8.127:80", "http://10.10.8.127", "10.10.8.20:1234", "10.10.8.127:8080"},
		{"http://10.10.8.127:080", "http://10.10.8.127", "10.10.8.20:1234", "10.10.8.127:8080"},
		{"http://[FD12:3456:0000:0000:0000:0000:0000:0001]:80", "http://[fd12:3456::1]", "[fd12:3456::20]:1234", "[fd12:3456::1]:8080"},
		{"http://[FD12:3456:0000:0000:0000:0000:0000:0001]:8080", "http://[fd12:3456::1]:8080", "[fd12:3456::20]:1234", "[fd12:3456::1]:8081"},
	} {
		t.Run(test.configured, func(t *testing.T) {
			u, err := policy.ValidateURL(test.configured)
			if err != nil || u.String() != test.browserURL {
				t.Fatalf("URL did not match browser normalization: %v, %v", u, err)
			}
			request := httptest.NewRequest("POST", test.browserURL+"/setup/api/session", nil)
			request.RemoteAddr = test.peer
			request.Header.Set("Origin", test.browserURL)
			if !policy.AllowsLANRequest(request, test.configured) || !policy.AllowsLANOrigin(request, test.configured) {
				t.Fatal("normalized browser Host/Origin denied")
			}
			request.Host = test.wrongPort
			request.Header.Set("Origin", "http://"+test.wrongPort)
			if policy.AllowsLANRequest(request, test.configured) || policy.AllowsLANOrigin(request, test.configured) {
				t.Fatal("different port accepted after normalization")
			}
		})
	}
	const originalHTTPS = "https://[FD12:3456:0000:0000:0000:0000:0000:0001]:443"
	if u, err := policy.ValidateURL(originalHTTPS); err != nil || u.String() != originalHTTPS {
		t.Fatal("HTTP normalization changed HTTPS authority")
	}
}

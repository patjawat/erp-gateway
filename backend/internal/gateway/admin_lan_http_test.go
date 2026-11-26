package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"erp-platform/gateway/internal/webtransport"
)

const lanAdminURL = "http://10.10.8.127:8080"

func lanAdminConfig(t *testing.T) Config {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"), bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	policy, err := webtransport.New(true, []string{"10.10.8.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	return Config{Store: store, AdminUsername: "admin", AdminPassword: "a-test-only-password-12345", PublicURL: lanAdminURL, WebPolicy: policy, CookieSecure: true}
}

func lanAdminRequest(handler http.Handler, method, path, origin string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, lanAdminURL+path, strings.NewReader(`{"username":"admin","password":"a-test-only-password-12345"}`))
	req.RemoteAddr = "10.10.8.5:12345"
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestAdminLANHTTPLoginCookieAndCSRF(t *testing.T) {
	app, err := New(lanAdminConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	login := lanAdminRequest(handler, "POST", "/admin/api/login", lanAdminURL, nil, "")
	if login.Code != http.StatusOK {
		t.Fatal(login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("incorrect LAN cookie: %+v", cookies)
	}
	var response struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &response); err != nil || response.Data.CSRF == "" {
		t.Fatal("missing csrf token", err)
	}
	if got := lanAdminRequest(handler, "GET", "/admin/api/session", "", cookies[0], ""); got.Code != http.StatusOK {
		t.Fatal("LAN session failed", got.Code, got.Body.String())
	}
	for _, tt := range []struct{ origin, csrf string }{
		{lanAdminURL, ""},
		{"http://10.10.8.126:8080", response.Data.CSRF},
		{"", response.Data.CSRF},
		{"https://10.10.8.127:8080", response.Data.CSRF},
	} {
		got := lanAdminRequest(handler, "POST", "/admin/api/logout", tt.origin, cookies[0], tt.csrf)
		if got.Code != http.StatusForbidden {
			t.Fatalf("unsafe LAN mutation accepted: origin=%q csrf=%q code=%d", tt.origin, tt.csrf, got.Code)
		}
	}
	logout := lanAdminRequest(handler, "POST", "/admin/api/logout", lanAdminURL, cookies[0], response.Data.CSRF)
	if logout.Code != http.StatusOK || len(logout.Result().Cookies()) != 1 || logout.Result().Cookies()[0].Secure {
		t.Fatal("LAN logout failed", logout.Code, logout.Body.String())
	}
}

func TestAdminLANHTTPIngressProtectsEveryAdminRoute(t *testing.T) {
	app, err := New(lanAdminConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin", "/admin/", "/admin/main.js", "/admin/api/login", "/admin/api/session"} {
		for _, tt := range []struct{ name, peer, host, forwarded string }{
			{"outside subnet", "10.10.9.5:1000", "10.10.8.127:8080", ""},
			{"public peer", "203.0.113.5:1000", "10.10.8.127:8080", ""},
			{"wrong host", "10.10.8.5:1000", "10.10.8.126:8080", ""},
			{"forwarded spoof", "10.10.8.5:1000", "10.10.8.127:8080", "https"},
		} {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				req := httptest.NewRequest("GET", lanAdminURL+path, nil)
				req.RemoteAddr, req.Host = tt.peer, tt.host
				if tt.forwarded != "" {
					req.Header.Set("X-Forwarded-Proto", tt.forwarded)
				}
				w := httptest.NewRecorder()
				app.Handler().ServeHTTP(w, req)
				if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "LAN_HTTP_FORBIDDEN") {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
	// Health checks remain reachable from the container loopback, which is not
	// part of the operator's LAN client allowlist.
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/healthz", nil)
	req.RemoteAddr = "127.0.0.1:1000"
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatal("healthcheck was blocked by Admin LAN guard", w.Code)
	}
}

func TestAdminLANHTTPLoginRequiresOrigin(t *testing.T) {
	app, err := New(lanAdminConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := lanAdminRequest(app.Handler(), "POST", "/admin/api/login", "", nil, ""); got.Code != http.StatusForbidden {
		t.Fatal("LAN login without Origin accepted", got.Code)
	}
}

func TestConfiguredGatewayURLControlsCookieSecurity(t *testing.T) {
	cfg := lanAdminConfig(t)
	cfg.WebPolicy = webtransport.Policy{}
	if _, err := New(cfg); err == nil {
		t.Fatal("HTTP public URL accepted without opt-in")
	}
	cfg.PublicURL, cfg.CookieSecure = "https://gateway.example.org", false
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "https://gateway.example.org/admin/api/login", strings.NewReader(`{"username":"admin","password":"a-test-only-password-12345"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://gateway.example.org")
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 || !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie security was downgraded", w.Code, w.Body.String())
	}
}

func TestAdminSettingsExposeLANHTTPCapability(t *testing.T) {
	cfg := lanAdminConfig(t)
	cfg.ReadSettings = func() (RuntimeSettings, error) { return RuntimeSettings{PublicURL: lanAdminURL}, nil }
	cfg.ApplySettings = func(_ context.Context, _ RuntimeSettings) error { return nil }
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	login := lanAdminRequest(app.Handler(), "POST", "/admin/api/login", lanAdminURL, nil, "")
	if login.Code != http.StatusOK {
		t.Fatal(login.Code, login.Body.String())
	}
	got := lanAdminRequest(app.Handler(), "GET", "/admin/api/settings", "", login.Result().Cookies()[0], "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"lan_http_enabled":true`) {
		t.Fatal("missing read-only LAN capability", got.Code, got.Body.String())
	}
}

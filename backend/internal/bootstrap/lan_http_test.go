package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"erp-platform/gateway/internal/gateway"
	"erp-platform/gateway/internal/webtransport"
)

const lanPublicURL = "http://10.10.8.127:8080"

func lanRequest(t *testing.T, manager *Manager, method, publicURL, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, publicURL+path, bytes.NewReader(raw))
	request.RemoteAddr = "10.10.8.20:54321"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", publicURL)
	request.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	manager.Handler().ServeHTTP(w, request)
	return w
}

func lanStatus(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func requireHTTPResult(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP %d, expected %d: %s", response.Code, status, response.Body.String())
	}
}

func requireLANIngressDenied(t *testing.T, manager *Manager, publicURL string) {
	t.Helper()
	for _, path := range []string{"/setup/", "/setup/api/status"} {
		for _, change := range []func(*http.Request){
			func(r *http.Request) { r.RemoteAddr = "203.0.113.10:1234" },
			func(r *http.Request) { r.RemoteAddr = "10.10.9.10:1234" },
			func(r *http.Request) { r.Host = "attacker.example:8080" },
			func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
			func(r *http.Request) { r.Header.Set("X-Forwarded-For", "10.10.8.20") },
		} {
			r := httptest.NewRequest("GET", publicURL+path, nil)
			r.RemoteAddr = "10.10.8.20:1234"
			change(r)
			w := httptest.NewRecorder()
			manager.Handler().ServeHTTP(w, r)
			requireHTTPResult(t, w, http.StatusForbidden)
		}
	}
}

// This exercises the real bootstrap and Gateway handlers together. All state is
// in test directories; no listener, upstream or background worker is started.
func TestLANHTTPSetupAdminSettingsRestartAndRecovery(t *testing.T) {
	policy, err := webtransport.New(true, []string{"10.10.8.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	options := Options{Directory: filepath.Join(dir, "config"), DatabasePath: filepath.Join(dir, "gateway.db"), PublicSetupURL: lanPublicURL, WebPolicy: policy, ValidatePolicy: gateway.ValidateUpstreamPolicy}
	var manager *Manager
	options.Build = func(c RuntimeConfig, key []byte) (Runtime, error) {
		if err := CheckDatabaseKey(options.DatabasePath, key); err != nil {
			return Runtime{}, err
		}
		store, err := gateway.OpenStore(options.DatabasePath, key)
		if err != nil {
			return Runtime{}, err
		}
		config := gateway.Config{Store: store, AdminUsername: c.AdminUsername, AdminPasswordVerifier: c.VerifyPassword, PublicURL: c.PublicURL, WebPolicy: policy, CookieSecure: true, TrustedProxyCIDRs: c.TrustedProxyCIDRs, ApprovedUpstreams: c.ApprovedUpstreams, PrivateUpstreamCIDRs: c.PrivateUpstreamCIDRs}
		config.ReadSettings = func() (gateway.RuntimeSettings, error) {
			in, err := manager.Settings()
			return gateway.RuntimeSettings{PublicURL: in.PublicURL, TrustedProxyCIDRs: in.TrustedProxyCIDRs, ApprovedUpstreams: in.ApprovedUpstreams, PrivateUpstreamCIDRs: in.PrivateUpstreamCIDRs}, err
		}
		config.ApplySettings = func(ctx context.Context, in gateway.RuntimeSettings) error {
			return manager.ApplySettings(ctx, PublicSettings{PublicURL: in.PublicURL, TrustedProxyCIDRs: in.TrustedProxyCIDRs, ApprovedUpstreams: in.ApprovedUpstreams, PrivateUpstreamCIDRs: in.PrivateUpstreamCIDRs})
		}
		app, err := gateway.New(config)
		if err != nil {
			store.Close()
			return Runtime{}, err
		}
		return Runtime{Handler: app.Handler(), Close: func() { _ = store.Close() }}, nil
	}
	manager, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close() })
	status := lanRequest(t, manager, "GET", lanPublicURL, "/setup/api/status", nil, nil, "")
	requireHTTPResult(t, status, 200)
	data := lanStatus(t, status)
	if data["state"] != "setup" || data["lan_http_enabled"] != true || data["public_url"] != lanPublicURL {
		t.Fatal("setup capability or initial URL missing")
	}
	requireLANIngressDenied(t, manager, lanPublicURL)
	code, err := IssueSetupCode(options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	session := lanRequest(t, manager, "POST", lanPublicURL, "/setup/api/session", map[string]string{"code": code}, nil, "")
	requireHTTPResult(t, session, 200)
	cookies := session.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("LAN setup cookie does not follow explicit plaintext policy")
	}
	setupCookie := cookies[0]
	csrf, _ := lanStatus(t, session)["csrf_token"].(string)
	input := testInput(t)
	input.PublicURL = lanPublicURL
	requireHTTPResult(t, lanRequest(t, manager, "POST", lanPublicURL, "/setup/api/activate", input, setupCookie, ""), 403)
	requireHTTPResult(t, lanRequest(t, manager, "POST", lanPublicURL, "/setup/api/validate", input, setupCookie, csrf), 200)
	requireHTTPResult(t, lanRequest(t, manager, "POST", lanPublicURL, "/setup/api/activate", input, setupCookie, csrf), 200)
	before, originalKey, err := LoadRuntime(options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	requireLANIngressDenied(t, manager, lanPublicURL)
	status = lanRequest(t, manager, "GET", lanPublicURL, "/setup/api/status", nil, nil, "")
	if data = lanStatus(t, status); data["state"] != "active" || data["public_url"] != lanPublicURL || data["lan_http_enabled"] != true {
		t.Fatal("active setup status metadata missing")
	}
	login := lanRequest(t, manager, "POST", lanPublicURL, "/admin/api/login", map[string]string{"username": input.AdminUsername, "password": input.AdminPassword}, nil, "")
	requireHTTPResult(t, login, 200)
	adminCookies := login.Result().Cookies()
	if len(adminCookies) != 1 || adminCookies[0].Secure || !adminCookies[0].HttpOnly || adminCookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("activated runtime did not derive a LAN Admin cookie")
	}
	adminCookie := adminCookies[0]
	adminCSRF, _ := lanStatus(t, login)["csrf_token"].(string)
	settings := lanRequest(t, manager, "GET", lanPublicURL, "/admin/api/settings", nil, adminCookie, "")
	requireHTTPResult(t, settings, 200)
	if lanStatus(t, settings)["lan_http_enabled"] != true {
		t.Fatal("Admin settings capability missing")
	}
	const changedURL = "http://10.10.8.127:8081"
	updated := gateway.RuntimeSettings{PublicURL: changedURL}
	requireHTTPResult(t, lanRequest(t, manager, "PUT", lanPublicURL, "/admin/api/settings", updated, adminCookie, ""), 403)
	requireHTTPResult(t, lanRequest(t, manager, "PUT", lanPublicURL, "/admin/api/settings", updated, adminCookie, adminCSRF), 200)
	after, afterKey, err := LoadRuntime(options.Directory)
	if err != nil || after.PublicURL != changedURL || !bytes.Equal(originalKey, afterKey) || before.PasswordHash != after.PasswordHash || before.ClientCertFile != after.ClientCertFile || before.ClientKeyFile != after.ClientKeyFile {
		t.Fatal("settings failed to preserve identity/key/credentials")
	}
	requireHTTPResult(t, lanRequest(t, manager, "GET", changedURL, "/admin/api/settings", nil, adminCookie, ""), 401)
	manager.Close()
	manager, err = New(options)
	if err != nil || manager.state != "active" {
		t.Fatalf("persisted HTTP reload: %v", err)
	}
	status = lanRequest(t, manager, "GET", changedURL, "/setup/api/status", nil, nil, "")
	requireHTTPResult(t, status, 200)
	if lanStatus(t, status)["public_url"] != changedURL {
		t.Fatal("stored URL did not take precedence over initial setup URL")
	}
	requireHTTPResult(t, lanRequest(t, manager, "GET", lanPublicURL, "/setup/api/status", nil, nil, ""), 403)
	requireLANIngressDenied(t, manager, changedURL)
	login = lanRequest(t, manager, "POST", changedURL, "/admin/api/login", map[string]string{"username": input.AdminUsername, "password": input.AdminPassword}, nil, "")
	requireHTTPResult(t, login, 200)
	const passphrase = "independent-test-recovery-passphrase"
	archive, err := manager.ExportRecovery(context.Background(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	strictDestination := filepath.Join(dir, "strict-restore")
	if err := RestoreRecovery(context.Background(), archive, passphrase, strictDestination, gateway.ValidateUpstreamPolicy); err == nil {
		t.Fatal("archive enabled HTTP without destination opt-in")
	}
	if _, err := os.Stat(strictDestination); !os.IsNotExist(err) {
		t.Fatal("refused restore created a destination")
	}
	allowedDestination := filepath.Join(dir, "lan-restore")
	if err := RestoreRecoveryWithPolicy(context.Background(), archive, passphrase, allowedDestination, gateway.ValidateUpstreamPolicy, policy); err != nil {
		t.Fatal(err)
	}
	restored, restoredKey, err := LoadRuntime(allowedDestination)
	if err != nil || restored.PublicURL != changedURL || !bytes.Equal(originalKey, restoredKey) {
		t.Fatal("LAN recovery did not preserve URL/key")
	}
	manager.Close()
	storedBefore, err := os.ReadFile(filepath.Join(options.Directory, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	strictOptions := options
	strictOptions.PublicSetupURL = ""
	strictOptions.WebPolicy = webtransport.Policy{}
	strictOptions.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		t.Fatal("strict restart attempted to activate stored HTTP")
		return Runtime{}, nil
	}
	strictManager, err := New(strictOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer strictManager.Close()
	if strictManager.state != "recovery" {
		t.Fatal("stored HTTP silently enabled after losing env opt-in")
	}
	storedAfter, _ := os.ReadFile(filepath.Join(options.Directory, "runtime.json"))
	_, strictKey, err := LoadRuntime(options.Directory)
	if err != nil || !bytes.Equal(storedBefore, storedAfter) || !bytes.Equal(originalKey, strictKey) {
		t.Fatal("strict restart changed stored configuration or key")
	}
}

func TestSetupPublicURLValidationNamesEnvironmentWithoutEchoingInput(t *testing.T) {
	dir := t.TempDir()
	_, err := New(Options{Directory: filepath.Join(dir, "config"), DatabasePath: filepath.Join(dir, "gateway.db"), PublicSetupURL: "http://username:do-not-log-this@10.10.8.127", Build: dummyRuntime})
	if err == nil || !strings.Contains(err.Error(), "GATEWAY_SETUP_PUBLIC_URL") || strings.Contains(err.Error(), "do-not-log-this") {
		t.Fatal("invalid setup URL error must name env without exposing its value")
	}
}

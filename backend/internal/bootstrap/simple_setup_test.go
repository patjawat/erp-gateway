package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"erp-platform/gateway/internal/gateway"
	"erp-platform/gateway/internal/webtransport"
)

type managedFixture struct {
	manager *Manager
	options Options
	builds  int
}

func newManagedFixture(t *testing.T, policy webtransport.Policy, setup *bool) *managedFixture {
	t.Helper()
	dir := t.TempDir()
	f := &managedFixture{options: Options{Directory: filepath.Join(dir, "config"), DatabasePath: filepath.Join(dir, "gateway.db"), WebPolicy: policy, SetupEnabled: setup, ValidatePolicy: gateway.ValidateUpstreamPolicy}}
	f.options.Build = func(c RuntimeConfig, key []byte) (Runtime, error) {
		f.builds++
		resolved, err := c.ManagementPolicy(f.options.WebPolicy)
		if err != nil {
			return Runtime{}, err
		}
		store, err := gateway.OpenStore(f.options.DatabasePath, key)
		if err != nil {
			return Runtime{}, err
		}
		config := gateway.Config{Store: store, AdminUsername: c.AdminUsername, AdminPasswordVerifier: c.VerifyPassword, PublicURL: c.PublicURL, WebPolicy: resolved, ApprovedUpstreams: c.ApprovedUpstreams, PrivateUpstreamCIDRs: c.PrivateUpstreamCIDRs}
		config.ReadSettings = func() (gateway.RuntimeSettings, error) {
			in, err := f.manager.Settings()
			return gateway.RuntimeSettings{PublicURL: in.PublicURL, TrustedProxyCIDRs: in.TrustedProxyCIDRs, ApprovedUpstreams: in.ApprovedUpstreams, PrivateUpstreamCIDRs: in.PrivateUpstreamCIDRs}, err
		}
		config.ApplySettings = func(ctx context.Context, in gateway.RuntimeSettings) error {
			return f.manager.ApplySettings(ctx, PublicSettings{PublicURL: in.PublicURL, TrustedProxyCIDRs: in.TrustedProxyCIDRs, ApprovedUpstreams: in.ApprovedUpstreams, PrivateUpstreamCIDRs: in.PrivateUpstreamCIDRs})
		}
		app, err := gateway.New(config)
		if err != nil {
			store.Close()
			return Runtime{}, err
		}
		return Runtime{Handler: app.Handler(), Close: func() { _ = store.Close() }}, nil
	}
	t.Cleanup(func() {
		if f.manager != nil {
			f.manager.Close()
		}
	})
	return f
}

func (f *managedFixture) open(t *testing.T) {
	t.Helper()
	if f.manager != nil {
		f.manager.Close()
	}
	manager, err := New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	f.manager = manager
}

func (f *managedFixture) request(t *testing.T, method, origin, peer, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, origin+path, bytes.NewReader(raw))
	r.RemoteAddr = peer
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.manager.Handler().ServeHTTP(w, r)
	return w
}

func (f *managedFixture) unlock(t *testing.T, origin, peer string) (*http.Cookie, string) {
	t.Helper()
	code, err := IssueSetupCode(f.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	w := f.request(t, "POST", origin, peer, "/setup/api/session", map[string]string{"code": code}, nil, "")
	requireHTTPResult(t, w, 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("managed HTTP session cookie invalid")
	}
	data := lanStatus(t, w)
	if f.options.SetupEnabled != nil && (data["public_url"] != origin || data["authenticated"] != true) {
		t.Fatal("verified session did not return its bound origin")
	}
	csrf, _ := data["csrf_token"].(string)
	return cookies[0], csrf
}

func TestSingleSwitchPrivateAndLocalSetupSurvivesDisabledRestart(t *testing.T) {
	for _, test := range []struct{ origin, peer string }{
		{lanPublicURL, "10.10.8.20:1234"},
		{"http://localhost:8080", "172.18.0.1:1234"},
	} {
		t.Run(test.origin, func(t *testing.T) {
			enabled := true
			fixture := newManagedFixture(t, webtransport.DefaultSetupPolicy(), &enabled)
			fixture.open(t)
			status := fixture.request(t, "GET", test.origin, test.peer, "/setup/api/status", nil, nil, "")
			requireHTTPResult(t, status, 200)
			data := lanStatus(t, status)
			if data["authenticated"] != false || data["public_url"] != nil || data["setup_enabled"] != true || data["local_http_enabled"] != true {
				t.Fatal("unauthenticated proposed URL leaked or Setup metadata invalid")
			}
			requireHTTPResult(t, fixture.request(t, "POST", test.origin, test.peer, "/setup/api/session", map[string]string{"code": "wrong"}, nil, ""), 401)
			cookie, csrf := fixture.unlock(t, test.origin, test.peer)
			status = fixture.request(t, "GET", test.origin, test.peer, "/setup/api/status", nil, cookie, "")
			if data = lanStatus(t, status); data["authenticated"] != true || data["public_url"] != test.origin {
				t.Fatal("authenticated status lost its confirmed origin")
			}
			input := testInput(t)
			input.PublicURL = test.origin
			const changedOrigin = "http://10.10.8.127:8081"
			status = fixture.request(t, "GET", changedOrigin, test.peer, "/setup/api/status", nil, cookie, "")
			if data = lanStatus(t, status); data["authenticated"] != false || data["public_url"] != nil {
				t.Fatal("setup session followed a changed browser origin")
			}
			requireHTTPResult(t, fixture.request(t, "POST", changedOrigin, test.peer, "/setup/api/activate", input, cookie, csrf), 401)
			requireHTTPResult(t, fixture.request(t, "POST", test.origin, test.peer, "/setup/api/activate", input, cookie, ""), 403)
			requireHTTPResult(t, fixture.request(t, "POST", test.origin, test.peer, "/setup/api/validate", input, cookie, csrf), 200)
			requireHTTPResult(t, fixture.request(t, "POST", test.origin, test.peer, "/setup/api/activate", input, cookie, csrf), 200)
			before, key, err := LoadRuntime(fixture.options.Directory)
			if err != nil || before.WebAccess == nil || !before.WebAccess.AllowLocalHTTP {
				t.Fatal("activation did not save the management policy")
			}
			login := fixture.request(t, "POST", test.origin, test.peer, "/admin/api/login", map[string]string{"username": input.AdminUsername, "password": input.AdminPassword}, nil, "")
			requireHTTPResult(t, login, 200)
			adminCookie := login.Result().Cookies()[0]
			adminCSRF, _ := lanStatus(t, login)["csrf_token"].(string)
			requireHTTPResult(t, fixture.request(t, "PUT", test.origin, test.peer, "/admin/api/settings", gateway.RuntimeSettings{PublicURL: test.origin}, adminCookie, ""), 403)
			requireHTTPResult(t, fixture.request(t, "PUT", test.origin, test.peer, "/admin/api/settings", gateway.RuntimeSettings{PublicURL: test.origin}, adminCookie, adminCSRF), 200)
			originalFile, _ := os.ReadFile(filepath.Join(fixture.options.Directory, "runtime.json"))
			// Once saved, neither the SETUP switch nor a strict environment fallback
			// can replace this installation's selected management policy.
			fixture.options.WebPolicy = webtransport.Policy{}
			for _, setup := range []bool{false, true} {
				fixture.options.SetupEnabled = &setup
				fixture.open(t)
				if fixture.manager.state != "active" {
					t.Fatal("saved management policy was lost on restart")
				}
				status = fixture.request(t, "GET", test.origin, test.peer, "/setup/api/status", nil, nil, "")
				requireHTTPResult(t, status, 200)
				if data = lanStatus(t, status); data["state"] != "active" || data["setup_enabled"] != false {
					t.Fatal("healthy runtime reopened Setup")
				}
				requireHTTPResult(t, fixture.request(t, "POST", test.origin, test.peer, "/setup/api/session", map[string]string{"code": "wrong"}, nil, ""), 410)
				requireHTTPResult(t, fixture.request(t, "POST", test.origin, test.peer, "/admin/api/login", map[string]string{"username": input.AdminUsername, "password": input.AdminPassword}, nil, ""), 200)
				requireHTTPResult(t, fixture.request(t, "GET", test.origin, test.peer, "/api/hospitals", nil, nil, ""), 200)
			}
			after, afterKey, err := LoadRuntime(fixture.options.Directory)
			afterFile, _ := os.ReadFile(filepath.Join(fixture.options.Directory, "runtime.json"))
			if err != nil || !bytes.Equal(key, afterKey) || !bytes.Equal(originalFile, afterFile) || before.PasswordHash != after.PasswordHash || before.ClientKeyFile != after.ClientKeyFile || !reflect.DeepEqual(before.WebAccess, after.WebAccess) {
				t.Fatal("switch/restart changed credentials, identity, key or saved policy")
			}
		})
	}
}

func TestManagedSetupDisabledAndUntrustedIngress(t *testing.T) {
	disabled := false
	f := newManagedFixture(t, webtransport.DefaultSetupPolicy(), &disabled)
	f.open(t)
	for _, path := range []string{"/setup/", "/setup/api/status", "/setup/api/session", "/setup/api/csr", "/admin/"} {
		w := f.request(t, "GET", lanPublicURL, "10.10.8.20:1234", path, nil, nil, "")
		requireHTTPResult(t, w, 403)
		if !bytes.Contains(w.Body.Bytes(), []byte("SETUP_DISABLED")) {
			t.Fatal("disabled Setup did not explain the switch")
		}
	}
	requireHTTPResult(t, f.request(t, "GET", lanPublicURL, "10.10.8.20:1234", "/healthz", nil, nil, ""), 200)
	enabled := true
	f.options.SetupEnabled = &enabled
	f.open(t)
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "203.0.113.1:1234" },
		func(r *http.Request) { r.Host = "public.example.org:8080" },
		func(r *http.Request) { r.Host = "203.0.113.1:8080" },
		func(r *http.Request) { r.Header.Set("Forwarded", "proto=https") },
		func(r *http.Request) { r.Header.Set("X-Forwarded-For", "10.10.8.20") },
	} {
		r := httptest.NewRequest("GET", lanPublicURL+"/setup/api/status", nil)
		r.RemoteAddr = "10.10.8.20:1234"
		change(r)
		w := httptest.NewRecorder()
		f.manager.Handler().ServeHTTP(w, r)
		requireHTTPResult(t, w, 403)
	}
}

func TestHealthyLegacyPolicySnapshotPreservesExactCIDRs(t *testing.T) {
	manual, _ := webtransport.New(true, []string{"10.10.8.0/24"})
	f := newManagedFixture(t, manual, nil)
	f.options.PublicSetupURL = lanPublicURL
	f.open(t)
	cookie, csrf := f.unlock(t, lanPublicURL, "10.10.8.20:1234")
	input := testInput(t)
	input.PublicURL = lanPublicURL
	requireHTTPResult(t, f.request(t, "POST", lanPublicURL, "10.10.8.20:1234", "/setup/api/activate", input, cookie, csrf), 200)
	before, key, err := LoadRuntime(f.options.Directory)
	if err != nil || before.WebAccess != nil {
		t.Fatal("legacy fixture unexpectedly has a saved policy")
	}
	beforeFile, _ := os.ReadFile(filepath.Join(f.options.Directory, "runtime.json"))
	f.manager.Close()
	disabled := false
	failedOptions := f.options
	failedOptions.SetupEnabled = &disabled
	failedOptions.Build = func(RuntimeConfig, []byte) (Runtime, error) { return Runtime{}, errors.New("candidate refused") }
	failed, err := New(failedOptions)
	if err != nil {
		t.Fatal(err)
	}
	failed.Close()
	failedFile, _ := os.ReadFile(filepath.Join(f.options.Directory, "runtime.json"))
	if failed.state != "recovery" || !bytes.Equal(beforeFile, failedFile) {
		t.Fatal("failed runtime build migrated or changed the legacy config")
	}
	f.options.SetupEnabled = &disabled
	f.options.PublicSetupURL = ""
	f.open(t)
	after, afterKey, err := LoadRuntime(f.options.Directory)
	if err != nil || after.WebAccess == nil || !reflect.DeepEqual(after.WebAccess.LANHTTPCIDRs, []string{"10.10.8.0/24"}) || after.WebAccess.AllowLocalHTTP || !bytes.Equal(key, afterKey) || before.PasswordHash != after.PasswordHash || before.ClientCertFile != after.ClientCertFile {
		t.Fatal("healthy migration widened policy or changed credentials/key/identity")
	}
	f.options.WebPolicy = webtransport.DefaultSetupPolicy()
	f.open(t)
	requireHTTPResult(t, f.request(t, "GET", lanPublicURL, "10.10.9.20:1234", "/admin/", nil, nil, ""), 403)
	requireHTTPResult(t, f.request(t, "GET", lanPublicURL, "10.10.8.20:1234", "/setup/api/status", nil, nil, ""), 200)
	f.manager.Close()
	after.WebAccess.LANHTTPCIDRs = []string{"0.0.0.0/0"}
	if err := persistJSON(filepath.Join(f.options.Directory, "runtime.json"), after); err != nil {
		t.Fatal(err)
	}
	malformedFile, _ := os.ReadFile(filepath.Join(f.options.Directory, "runtime.json"))
	builds := f.builds
	f.open(t)
	unchangedFile, _ := os.ReadFile(filepath.Join(f.options.Directory, "runtime.json"))
	if f.manager.state != "recovery" || f.builds != builds || !bytes.Equal(malformedFile, unchangedFile) {
		t.Fatal("malformed saved policy fell back or rewrote configuration")
	}
}

func TestManagedRecoveryValidatesSavedAndOperatorPolicies(t *testing.T) {
	enabled := true
	manual, _ := webtransport.New(true, []string{"10.10.8.0/24"})
	f := newManagedFixture(t, manual, &enabled)
	f.open(t)
	cookie, csrf := f.unlock(t, lanPublicURL, "10.10.8.20:1234")
	input := testInput(t)
	input.PublicURL = lanPublicURL
	requireHTTPResult(t, f.request(t, "POST", lanPublicURL, "10.10.8.20:1234", "/setup/api/activate", input, cookie, csrf), 200)
	const passphrase = "a-separate-managed-recovery-passphrase"
	archive, err := f.manager.ExportRecovery(context.Background(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := RestoreRecovery(context.Background(), archive, passphrase, filepath.Join(dir, "strict"), gateway.ValidateUpstreamPolicy); err == nil {
		t.Fatal("saved archive policy bypassed strict destination transport")
	}
	destination := filepath.Join(dir, "restored")
	if err := RestoreRecoveryWithPolicy(context.Background(), archive, passphrase, destination, gateway.ValidateUpstreamPolicy, webtransport.DefaultSetupPolicy()); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadRuntime(destination)
	if err != nil || cfg.WebAccess == nil || !reflect.DeepEqual(cfg.WebAccess.LANHTTPCIDRs, []string{"10.10.8.0/24"}) || cfg.WebAccess.AllowLocalHTTP {
		t.Fatal("restoring under a broader policy widened saved client scope")
	}
	payload, err := DecryptRecovery(context.Background(), archive, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	for index, badSettings := range []webtransport.Settings{
		{AllowLANHTTP: true, LANHTTPCIDRs: []string{"0.0.0.0/0"}},
		{}, // The saved policy must allow the saved URL, independently of fallback.
	} {
		payload.Config.WebAccess = &badSettings
		badArchive, err := sealRecovery(context.Background(), payload, passphrase)
		if err != nil {
			t.Fatal(err)
		}
		badDestination := filepath.Join(dir, []string{"malformed", "inconsistent"}[index])
		if err := RestoreRecoveryWithPolicy(context.Background(), badArchive, passphrase, badDestination, gateway.ValidateUpstreamPolicy, webtransport.DefaultSetupPolicy()); err == nil {
			t.Fatal("invalid archive policy fell back to destination defaults")
		}
		if _, err := os.Stat(badDestination); !os.IsNotExist(err) {
			t.Fatal("refused restore wrote a destination")
		}
	}
	// A broad archive may not override a deliberately narrow destination, even
	// though both policies validate the same configured private-IP URL.
	broad := webtransport.DefaultSetupPolicy().Settings()
	payload.Config.WebAccess = &broad
	broadArchive, err := sealRecovery(context.Background(), payload, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	narrowDestination := filepath.Join(dir, "narrow-operator")
	if err := RestoreRecoveryWithPolicy(context.Background(), broadArchive, passphrase, narrowDestination, gateway.ValidateUpstreamPolicy, manual); err == nil {
		t.Fatal("archive broadened the destination's explicit management scope")
	}
	if _, err := os.Stat(narrowDestination); !os.IsNotExist(err) {
		t.Fatal("scope-expanding restore wrote a destination")
	}
	// Test CIDR scope separately from localhost permission so the rejection
	// cannot pass solely because the broad archive supports local HTTP.
	broad.AllowLocalHTTP = false
	payload.Config.WebAccess = &broad
	broadArchive, err = sealRecovery(context.Background(), payload, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreRecoveryWithPolicy(context.Background(), broadArchive, passphrase, filepath.Join(dir, "wide-networks"), gateway.ValidateUpstreamPolicy, manual); err == nil {
		t.Fatal("archive broadened the destination's explicit client CIDRs")
	}
	payload.Config.PublicURL = "https://gateway.example.org"
	for index, settings := range []webtransport.Settings{
		{AllowLANHTTP: true, LANHTTPCIDRs: []string{"10.10.8.0/24"}},
		{AllowLocalHTTP: true},
	} {
		payload.Config.WebAccess = &settings
		capabilityArchive, err := sealRecovery(context.Background(), payload, passphrase)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(dir, []string{"https-with-lan", "https-with-local"}[index])
		if err := RestoreRecovery(context.Background(), capabilityArchive, passphrase, destination, gateway.ValidateUpstreamPolicy); err == nil {
			t.Fatal("HTTPS archive enabled HTTP capabilities beyond the operator policy")
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatal("capability-expanding HTTPS archive wrote a destination")
		}
	}
}

func TestManualSetupSessionReturnsConfiguredURLAfterCodeVerification(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	m.options.PublicSetupURL = "https://gateway.example.org/"
	code, err := IssueSetupCode(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, m, "/setup/api/session", map[string]string{"code": code}, nil, "")
	requireHTTPResult(t, w, 200)
	data := lanStatus(t, w)
	if data["authenticated"] != true || data["public_url"] != "https://gateway.example.org" || data["lan_http_enabled"] != false || data["local_http_enabled"] != false || data["setup_enabled"] != true {
		t.Fatal("manual HTTPS session did not return authenticated canonical metadata")
	}
}

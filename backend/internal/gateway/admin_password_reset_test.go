package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"erp-platform/gateway/internal/bootstrap"
	"erp-platform/gateway/internal/webtransport"
)

func passwordResetSetupRequest(t *testing.T, manager *bootstrap.Manager, path string, input any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost"+path, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:19001"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	manager.Handler().ServeHTTP(response, request)
	return response
}

func TestLocalAdminPasswordResetPreservesMobileSessionsAndData(t *testing.T) {
	// Keep the Unix socket path short on macOS; no live configuration is used.
	directory, err := os.MkdirTemp("/tmp", "gateway-password-reset-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	configDir := filepath.Join(directory, "config")
	databasePath := filepath.Join(directory, "gateway.db")
	const username = "owner-admin"
	const oldPassword = "old-test-only-admin-password"
	const newPassword = "new-test-only-admin-password"

	var calls atomic.Int32
	var blockProfile atomic.Bool
	profileEntered := make(chan struct{})
	releaseProfile := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseProfile) }) }
	defer release()
	hospitalFixture := fixtureHospital(t, "11447", func() { calls.Add(1) })
	client := &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "11447.example.invalid" {
			return nil, fmt.Errorf("unexpected test upstream %s", request.URL.Host)
		}
		if request.URL.Path == "/api/profile" && blockProfile.CompareAndSwap(true, false) {
			close(profileEntered)
			select {
			case <-releaseProfile:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		response := httptest.NewRecorder()
		hospitalFixture.ServeHTTP(response, request)
		return response.Result(), nil
	})}

	var currentApp *Server
	build := func(config bootstrap.RuntimeConfig, key []byte) (bootstrap.Runtime, error) {
		store, err := OpenStore(databasePath, key)
		if err != nil {
			return bootstrap.Runtime{}, err
		}
		policy, err := config.ManagementPolicy(webtransport.DefaultSetupPolicy())
		if err != nil {
			_ = store.Close()
			return bootstrap.Runtime{}, err
		}
		pair, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			_ = store.Close()
			return bootstrap.Runtime{}, err
		}
		app, err := New(Config{
			Store: store, AdminUsername: config.AdminUsername, AdminPasswordVerifier: config.VerifyPassword,
			PublicURL: config.PublicURL, WebPolicy: policy, Client: client,
			ApprovedUpstreams: config.ApprovedUpstreams, PrivateUpstreamCIDRs: config.PrivateUpstreamCIDRs,
			GatewayClientCert: &pair,
		})
		if err != nil {
			_ = store.Close()
			return bootstrap.Runtime{}, err
		}
		currentApp = app
		return bootstrap.Runtime{Handler: app.Handler(), Close: func() { _ = store.Close() }}, nil
	}
	newManager := func(enabled bool) *bootstrap.Manager {
		manager, err := bootstrap.New(bootstrap.Options{
			Directory: configDir, DatabasePath: databasePath, SetupEnabled: &enabled,
			WebPolicy: webtransport.DefaultSetupPolicy(), Build: build, ValidatePolicy: ValidateUpstreamPolicy,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(manager.Close)
		return manager
	}
	manager := newManager(true)
	code, err := bootstrap.IssueSetupCode(configDir)
	if err != nil {
		t.Fatal(err)
	}
	response := passwordResetSetupRequest(t, manager, "/setup/api/session", map[string]string{"code": code}, nil, "")
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var setupSession struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &setupSession); err != nil {
		t.Fatal(err)
	}
	setupCookie := response.Result().Cookies()[0]
	pair, _, _ := testClientIdentity(t)
	var chain []byte
	for _, certificate := range pair.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	input := bootstrap.Input{
		AdminUsername: username, AdminPassword: oldPassword, PublicURL: "https://gateway.example.invalid",
		ApprovedUpstreams:    []string{"11447=https://11447.example.invalid"},
		ClientCertificatePEM: string(chain), ClientPrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
	}
	response = passwordResetSetupRequest(t, manager, "/setup/api/activate", input, setupCookie, setupSession.Data.CSRF)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}

	// Recovery works with Setup closed, using the same saved data and identity.
	manager.Close()
	manager = newManager(false)
	rig := &testRig{app: currentApp, handler: manager.Handler(), dbPath: databasePath}
	loginAdmin := func(password string) *httptest.ResponseRecorder {
		return rig.request(t, http.MethodPost, "/admin/api/login", map[string]string{"username": username, "password": password}, "", false)
	}
	response = loginAdmin(oldPassword)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	rig.cookie = response.Result().Cookies()[0]
	var adminSession struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &adminSession); err != nil {
		t.Fatal(err)
	}
	rig.csrf = adminSession.Data.CSRF
	hospital := rig.add(t, "11447")
	rig.enable(t, hospital)
	mobileToken := rig.login(t, hospital.Code)
	if response := rig.request(t, http.MethodGet, "/api/profile", nil, mobileToken, false); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	beforeConfig, beforeKey, err := bootstrap.LoadRuntime(configDir)
	if err != nil {
		t.Fatal(err)
	}
	identityFiles := map[string][]byte{}
	for _, path := range []string{beforeConfig.ClientCertFile, beforeConfig.ClientKeyFile, filepath.Join(configDir, "encryption.key")} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		identityFiles[path] = body
	}
	beforeHospital, err := currentApp.config.Store.hospital(context.Background(), "id", hospital.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeSession, err := currentApp.config.Store.session(context.Background(), mobileToken)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(beforeHospital.key, []byte(strings.Repeat(hospital.Code, 8))) || bytes.Contains(beforeSession.upstream, []byte("local-token-for-tenant-11447")) {
		t.Fatal("test records were not encrypted")
	}
	if response := rig.request(t, http.MethodPost, "/password", map[string]string{"password": newPassword}, "", false); response.Code != http.StatusNotFound {
		t.Fatal("private password control route was exposed on the public Gateway", response.Code)
	}
	unchanged, _, err := bootstrap.LoadRuntime(configDir)
	if err != nil || !unchanged.VerifyPassword(oldPassword) || unchanged.VerifyPassword(newPassword) {
		t.Fatal("public password request changed the saved administrator verifier", err)
	}
	if response := rig.request(t, http.MethodGet, "/admin/api/session", nil, "", true); response.Code != http.StatusOK {
		t.Fatal("public password request invalidated the existing Admin session", response.Code)
	}
	if response := loginAdmin(oldPassword); response.Code != http.StatusOK {
		t.Fatal("old Admin login failed after the rejected public password request", response.Code)
	}

	control, err := bootstrap.StartAdminControl(configDir, manager.ResetAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	blockProfile.Store(true)
	inFlight := make(chan *httptest.ResponseRecorder, 1)
	go func() { inFlight <- rig.request(t, http.MethodGet, "/api/profile", nil, mobileToken, false) }()
	select {
	case <-profileEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("Mobile request did not reach the ERP fixture")
	}
	resetDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		name, err := bootstrap.RequestAdminPasswordReset(ctx, configDir, newPassword)
		if err == nil && name != username {
			err = fmt.Errorf("password recovery changed username to %q", name)
		}
		resetDone <- err
	}()
	select {
	case err := <-resetDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("password reset waited for an in-flight Mobile request instead of handing off the runtime")
	}

	if response := rig.request(t, http.MethodGet, "/admin/api/session", nil, "", true); response.Code != http.StatusUnauthorized {
		t.Fatal("old Admin cookie still works", response.Code)
	}
	if response := loginAdmin(oldPassword); response.Code != http.StatusUnauthorized {
		t.Fatal("old Admin password still works", response.Code)
	}
	if response := loginAdmin(newPassword); response.Code != http.StatusOK {
		t.Fatal("new Admin password failed", response.Code, response.Body.String())
	}
	if response := rig.request(t, http.MethodGet, "/api/profile", nil, mobileToken, false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Staff 11447") {
		t.Fatal("existing Mobile session failed on replacement runtime", response.Code, response.Body.String())
	}
	release()
	select {
	case response := <-inFlight:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Staff 11447") {
			t.Fatal("in-flight Mobile request did not complete", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight Mobile request was not drained")
	}
	if calls.Load() < 4 {
		t.Fatal("Mobile checks did not reach the actual fixture transport")
	}

	assertPreserved := func() {
		t.Helper()
		afterConfig, afterKey, err := bootstrap.LoadRuntime(configDir)
		if err != nil {
			t.Fatal(err)
		}
		if afterConfig.AdminUsername != username || !afterConfig.VerifyPassword(newPassword) || afterConfig.VerifyPassword(oldPassword) {
			t.Fatal("saved password reset was not limited to the expected administrator")
		}
		expected := beforeConfig
		expected.PasswordHash, expected.PasswordSalt = afterConfig.PasswordHash, afterConfig.PasswordSalt
		if !reflect.DeepEqual(expected, afterConfig) || !bytes.Equal(beforeKey, afterKey) {
			t.Fatal("password recovery changed non-password configuration or encryption key")
		}
		for path, before := range identityFiles {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("password recovery changed identity/encryption file", filepath.Base(path), err)
			}
		}
		afterHospital, err := currentApp.config.Store.hospital(context.Background(), "id", hospital.ID)
		if err != nil || !reflect.DeepEqual(beforeHospital, afterHospital) {
			t.Fatal("hospital data or encrypted Gateway key changed", err)
		}
		afterSession, err := currentApp.config.Store.session(context.Background(), mobileToken)
		if err != nil || !reflect.DeepEqual(beforeSession, afterSession) {
			t.Fatal("existing Mobile token or encrypted ERP token changed", err)
		}
	}
	assertPreserved()
	_ = control.Close()
	manager.Close()
	manager = newManager(false)
	rig.handler = manager.Handler()
	if response := loginAdmin(newPassword); response.Code != http.StatusOK {
		t.Fatal("new Admin password did not survive restart", response.Code, response.Body.String())
	}
	if response := loginAdmin(oldPassword); response.Code != http.StatusUnauthorized {
		t.Fatal("old Admin password revived after restart", response.Code)
	}
	if response := rig.request(t, http.MethodGet, "/api/profile", nil, mobileToken, false); response.Code != http.StatusOK {
		t.Fatal("existing Mobile session did not survive restart", response.Code, response.Body.String())
	}
	assertPreserved()
}

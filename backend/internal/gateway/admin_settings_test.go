package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func loginNextManagementServer(t *testing.T, r *testRig, next *Server) {
	t.Helper()
	r.app = next
	r.handler = next.Handler()
	w := r.request(t, "POST", "/admin/api/login", map[string]string{"username": "admin", "password": "correct password for tests"}, "", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r.cookie = w.Result().Cookies()[0]
	var envelope struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	r.csrf = envelope.Data.CSRF
}

func TestAdminSettingsProtectActiveDestinationsAndNetworks(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	r.app.config.PrivateUpstreamCIDRs = []string{"10.1.1.1/32"}
	wanted := RuntimeSettings{PublicURL: "https://gateway.example.invalid", ApprovedUpstreams: r.app.config.ApprovedUpstreams, PrivateUpstreamCIDRs: []string{"10.1.1.1/32"}}
	calls := 0
	r.app.config.ReadSettings = func() (RuntimeSettings, error) { return wanted, nil }
	r.app.config.ApplySettings = func(_ context.Context, in RuntimeSettings) error { calls++; return nil }
	if w := r.request(t, "GET", "/admin/api/settings", nil, "", false); w.Code != 401 {
		t.Fatal("public settings exposed")
	}
	w := r.request(t, "GET", "/admin/api/settings", nil, "", true)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"editable":true`)) || bytes.Contains(w.Body.Bytes(), []byte(r.app.config.AdminPassword)) {
		t.Fatal(w.Code, w.Body.String())
	}
	missing := wanted
	missing.ApprovedUpstreams = []string{"12448=https://12448.example.invalid"}
	if w = r.request(t, "PUT", "/admin/api/settings", missing, "", true); w.Code != 409 || calls != 0 {
		t.Fatal("active destination removed", w.Code, w.Body.String())
	}
	missing = wanted
	missing.PrivateUpstreamCIDRs = nil
	if w = r.request(t, "PUT", "/admin/api/settings", missing, "", true); w.Code != 409 || calls != 0 {
		t.Fatal("active network removed", w.Code, w.Body.String())
	}
	if w = r.request(t, "PUT", "/admin/api/settings", wanted, "", true); w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSettingsAndRecoveryRequireCSRFAndRejectSecretFields(t *testing.T) {
	r := rig(t)
	calls := 0
	r.app.config.ApplySettings = func(context.Context, RuntimeSettings) error { calls++; return nil }
	request := httptest.NewRequest("PUT", "https://gateway.example.invalid/admin/api/settings", strings.NewReader(`{"public_url":"https://gateway.example.invalid"}`))
	request.AddCookie(r.cookie)
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, request)
	if w.Code != 403 || calls != 0 {
		t.Fatal("settings accepted missing CSRF")
	}
	w = r.request(t, "PUT", "/admin/api/settings", map[string]any{"public_url": "https://gateway.example.invalid", "admin_password": "must never be changed here"}, "", true)
	if w.Code != 400 || calls != 0 {
		t.Fatal("secret settings accepted", w.Code)
	}
	pass := "a separate recovery passphrase"
	exports := 0
	r.app.config.ExportRecovery = func(_ context.Context, received string) ([]byte, error) {
		if received != pass {
			t.Fatal("wrong passphrase forwarded")
		}
		exports++
		return []byte(`{"schema":"erp-gateway-recovery.v1","ciphertext":"encrypted-fixture"}`), nil
	}
	w = r.request(t, "POST", "/admin/api/recovery/export", map[string]string{"passphrase": "short"}, "", true)
	if w.Code != 422 || exports != 0 {
		t.Fatal("short passphrase accepted")
	}
	w = r.request(t, "POST", "/admin/api/recovery/export", map[string]string{"passphrase": pass}, "", true)
	if w.Code != 200 || exports != 1 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment;") || bytes.Contains(w.Body.Bytes(), []byte(pass)) {
		t.Fatal(w.Code, w.Body.String())
	}
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["ciphertext"] == nil {
		t.Fatal("export is not raw encrypted envelope")
	}
}

func TestLegacySettingsExplainMigration(t *testing.T) {
	r := rig(t)
	w := r.request(t, "GET", "/admin/api/settings", nil, "", true)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"editable":false`)) || !bytes.Contains(w.Body.Bytes(), []byte("WEB_SETUP")) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = r.request(t, "POST", "/admin/api/recovery/export", map[string]string{"passphrase": "a separate recovery passphrase"}, "", true); w.Code != 409 {
		t.Fatal("legacy export unexpectedly available")
	}
}

func TestSuspendedSiteCannotEnableAfterDestinationRemoval(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	if w := r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/suspend", map[string]string{"reason": "QA pause"}, "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var next *Server
	r.app.config.ApplySettings = func(_ context.Context, in RuntimeSettings) error {
		cfg := r.app.config
		cfg.Management = r.app.management
		cfg.ApprovedUpstreams = in.ApprovedUpstreams
		var err error
		next, err = New(cfg)
		return err
	}
	in := RuntimeSettings{PublicURL: "https://gateway.example.invalid", ApprovedUpstreams: []string{"12448=https://12448.example.invalid"}}
	if w := r.request(t, "PUT", "/admin/api/settings", in, "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/enable", nil, "", true); w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte("STALE_RUNTIME")) {
		t.Fatal("retired handler admitted activation", w.Code, w.Body.String())
	}
	loginNextManagementServer(t, r, next)
	if w := r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/enable", nil, "", true); w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte("DESTINATION_NOT_APPROVED")) {
		t.Fatal("removed destination re-enabled", w.Code, w.Body.String())
	}
}

func TestSettingsCommitSerializesAlreadyAdmittedActivation(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	if w := r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/suspend", map[string]string{"reason": "QA pause"}, "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	entered, release := make(chan struct{}), make(chan struct{})
	r.app.config.ApplySettings = func(_ context.Context, in RuntimeSettings) error {
		close(entered)
		<-release
		cfg := r.app.config
		cfg.Management = r.app.management
		cfg.ApprovedUpstreams = in.ApprovedUpstreams
		_, err := New(cfg)
		return err
	}
	settingsDone, enableDone := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() {
		settingsDone <- r.request(t, "PUT", "/admin/api/settings", RuntimeSettings{PublicURL: "https://gateway.example.invalid", ApprovedUpstreams: []string{"12448=https://12448.example.invalid"}}, "", true)
	}()
	<-entered
	go func() { enableDone <- r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/enable", nil, "", true) }()
	close(release)
	if w := <-settingsDone; w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := <-enableDone; w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte("STALE_RUNTIME")) {
		t.Fatal("inflight old activation escaped settings commit", w.Code, w.Body.String())
	}
	var enabled bool
	if err := r.app.config.Store.db.QueryRow("SELECT enabled FROM hospitals WHERE id=?", h.ID).Scan(&enabled); err != nil || enabled {
		t.Fatal("active site escaped new policy", enabled, err)
	}
}

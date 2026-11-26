package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/contracts"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testRig struct {
	app         *Server
	handler     http.Handler
	cookie      *http.Cookie
	csrf        string
	dbPath      string
	erpCalls    map[string]int
	localTokens map[string]string
}

// Standalone Gateway tests exercise the Hospital HTTP wire boundary, not its
// implementation. The API module separately tests real Hospital behavior.
func fixtureHospital(t *testing.T, code string, count func()) http.Handler {
	t.Helper()
	token := "local-token-for-tenant-" + code
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count()
		if r.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || r.Header.Get("Cookie") != "" {
			t.Error("incorrect credentials forwarded to fixture for", code)
			fail(w, http.StatusUnauthorized, "GATEWAY_UNAUTHORIZED", "fixture")
			return
		}
		switch r.URL.Path {
		case "/api/status":
			writeData(w, contracts.Status{HospitalCode: code, APIVersion: contracts.Version, ERPReachable: true})
			return
		case "/api/auth/login":
			var in contracts.Login
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Username != "staff" || in.Password != "secret" {
				fail(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "fixture")
				return
			}
			writeData(w, contracts.Token{UserID: json.RawMessage("7"), AccessToken: token, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).Unix(), Scope: "profile:read"})
			return
		case "/api/auth/login-methods":
			writeData(w, map[string]any{"hospital_code": code, "login_methods": contracts.LoginMethods{Password: true, ProviderID: true, ThaID: true}, "external_auth": map[string]contracts.ExternalAuthAvailability{"provider_id": {}, "thaid": {}}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			fail(w, http.StatusUnauthorized, "SESSION_EXPIRED", "fixture")
			return
		}
		switch r.URL.Path {
		case "/api/profile":
			writeData(w, contracts.Profile{ID: json.RawMessage("7"), Username: "staff", Email: "staff@example.invalid", Employee: &contracts.Employee{ID: json.RawMessage("9"), Fullname: "Staff " + code, Department: "Department"}})
		case "/api/profile/avatar":
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write([]byte("GIF89a" + code))
		case "/api/leaves":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == 0 {
				page = 1
			}
			perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			if perPage == 0 {
				perPage = 20
			}
			writeData(w, contracts.LeaveHistory{Items: []contracts.Leave{{ID: "1", LeaveType: "Leave " + code, Status: "Approve", StatusLabel: "อนุมัติ"}}, Page: page, PerPage: perPage, TotalCount: 1})
		case "/depdrop/employee-by-id":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"id":9,"fullname":"Test staff","position_name":"Nurse"}],"total_count":1}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func rig(t *testing.T) *testRig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenStore(path, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	rig := &testRig{dbPath: path, erpCalls: map[string]int{}, localTokens: map[string]string{}}
	upstreams := map[string]http.Handler{}
	for _, code := range []string{"11447", "12448"} {
		upstreams[code+".example.invalid"] = fixtureHospital(t, code, func() { rig.erpCalls[code]++ })
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		handler := upstreams[r.URL.Host]
		if handler == nil {
			t.Fatalf("unexpected upstream %s", r.URL.Host)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if r.URL.Path == "/api/auth/login" && w.Code == http.StatusOK {
			var envelope struct {
				Data contracts.Token `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			rig.localTokens[strings.TrimSuffix(r.URL.Host, ".example.invalid")] = envelope.Data.AccessToken
		}
		return w.Result(), nil
	})}
	approved := make([]string, 0, len(upstreams))
	for host := range upstreams {
		approved = append(approved, strings.TrimSuffix(host, ".example.invalid")+"=https://"+host)
	}
	app, err := New(Config{Store: store, AdminUsername: "admin", AdminPassword: "correct password for tests", Client: client, ApprovedUpstreams: approved})
	if err != nil {
		t.Fatal(err)
	}
	rig.app = app
	rig.handler = app.Handler()
	login := rig.request(t, "POST", "/admin/api/login", map[string]string{"username": "admin", "password": "correct password for tests"}, "", false)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing admin cookie")
	}
	rig.cookie = cookies[0]
	var envelope struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	json.Unmarshal(login.Body.Bytes(), &envelope)
	rig.csrf = envelope.Data.CSRF
	return rig
}
func (r *testRig) request(t *testing.T, method, path string, body any, token string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, "https://gateway.example.invalid"+path, bytes.NewReader(data))
	req.RemoteAddr = "192.0.2.5:1234"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if admin {
		req.AddCookie(r.cookie)
		req.Header.Set("X-CSRF-Token", r.csrf)
	}
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}
func (r *testRig) add(t *testing.T, code string) Hospital {
	t.Helper()
	w := r.request(t, "POST", "/admin/api/hospitals", map[string]string{"code": code, "name": "Hospital " + code, "province": "Test", "api_url": "https://" + code + ".example.invalid", "gateway_key": strings.Repeat(code, 8)}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var env struct {
		Data Hospital `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	return env.Data
}
func (r *testRig) enable(t *testing.T, h Hospital) {
	t.Helper()
	for _, action := range []string{"test", "approve"} {
		w := r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/"+action, nil, "", true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func (r *testRig) login(t *testing.T, code string) string {
	t.Helper()
	w := r.request(t, "POST", "/api/auth/login", contracts.Login{HospitalCode: code, Username: "staff", Password: "secret"}, "", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var env struct {
		Data contracts.Token `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	return env.Data.AccessToken
}

func TestTenantRoutingSuspensionAndReenable(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	b := r.add(t, "12448")
	w := r.request(t, "GET", "/api/hospitals", nil, "", false)
	if strings.Contains(w.Body.String(), a.Code) {
		t.Fatal("pending hospital is public")
	}
	w = r.request(t, "POST", "/api/auth/login", contracts.Login{HospitalCode: a.Code, Username: "staff", Password: "secret"}, "", false)
	if w.Code != 403 || r.erpCalls[a.Code] != 0 {
		t.Fatal("pending login reached ERP")
	}
	r.enable(t, a)
	r.enable(t, b)
	tokenA := r.login(t, a.Code)
	tokenB := r.login(t, b.Code)
	if tokenA == tokenB || strings.Contains(tokenA, a.Code) {
		t.Fatal("central token is not opaque")
	}
	for _, entry := range []struct{ code, token string }{{a.Code, tokenA}, {b.Code, tokenB}} {
		w = r.request(t, "GET", "/api/profile", nil, entry.token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Staff "+entry.code) || !strings.Contains(w.Body.String(), `"code":"`+entry.code+`"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		w = r.request(t, "GET", "/api/profile/avatar", nil, entry.token, false)
		if w.Code != 200 || w.Body.String() != "GIF89a"+entry.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = r.request(t, "GET", "/api/hospitals", nil, "", false)
	if strings.Contains(w.Body.String(), "api_url") || strings.Contains(w.Body.String(), "gateway_key") {
		t.Fatal("public registry leaks credentials")
	}
	w = r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test suspension"}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/profile", "/api/profile/avatar"} {
		w = r.request(t, "GET", path, nil, tokenA, false)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "HOSPITAL_DISABLED") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = r.request(t, "GET", "/api/profile", nil, tokenB, false)
	if w.Code != 200 {
		t.Fatal("another hospital was suspended", w.Body.String())
	}
	w = r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/enable", nil, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = r.request(t, "GET", "/api/profile", nil, tokenA, false)
	if w.Code != 401 {
		t.Fatal("old session revived after enable", w.Code)
	}
	w = r.request(t, "POST", "/api/auth/logout", nil, tokenB, false)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = r.request(t, "GET", "/api/profile", nil, tokenB, false)
	if w.Code != 401 {
		t.Fatal("logout did not revoke", w.Code)
	}
}

func TestAdminSecurityAndConfigurationCorrection(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	req := httptest.NewRequest("POST", "https://gateway.example.invalid/admin/api/hospitals/"+h.ID+"/suspend", strings.NewReader(`{"reason":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(r.cookie)
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	req.Header.Set("X-CSRF-Token", r.csrf)
	req.Header.Set("Origin", "https://evil.example.invalid")
	w = httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	w = r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, map[string]string{"code": h.Code, "name": "Corrected name", "province": "Test", "api_url": h.APIURL, "gateway_key": ""}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"approval_status":"pending"`) || strings.Contains(w.Body.String(), strings.Repeat(h.Code, 8)) {
		t.Fatal(w.Body.String())
	}
	w = r.request(t, "GET", "/api/profile", nil, token, false)
	if w.Code != 403 {
		t.Fatal("config update did not disable", w.Code)
	}
	w = r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/approve", nil, "", true)
	if w.Code != 409 {
		t.Fatal("approval without new test accepted")
	}
	r.enable(t, h)
	w = r.request(t, "GET", "/admin/api/audit", nil, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "update") {
		t.Fatal(w.Body.String())
	}
}

func TestDatabaseEncryptionPersistenceAndWrongKey(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	var encrypted []byte
	if err := r.app.config.Store.db.QueryRow("SELECT erp_token FROM sessions WHERE token_hash=?", tokenHash(token)).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if r.localTokens[h.Code] == "" || bytes.Contains(encrypted, []byte(r.localTokens[h.Code])) {
		t.Fatal("plaintext local API token stored")
	}
	// WAL can contain active rows, inspect both files to ensure secrets were never written in plaintext.
	for _, path := range []string{r.dbPath, r.dbPath + "-wal"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, secret := range []string{token, strings.Repeat(h.Code, 8), r.localTokens[h.Code]} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("secret in database", path)
			}
		}
	}
	if err := r.app.config.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(r.dbPath, bytes.Repeat([]byte{8}, 32)); err == nil {
		store.Close()
		t.Fatal("wrong key accepted")
	}
	store, err := OpenStore(r.dbPath, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value, err := store.session(context.Background(), token)
	if err != nil || value.hospitalID != h.ID {
		t.Fatal("session not persistent", err)
	}
}

func TestUntrustedForwardingAndHTTPURLs(t *testing.T) {
	r := rig(t)
	req := httptest.NewRequest("GET", "https://gateway.example.invalid/", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")
	if r.app.clientIP(req) != "192.0.2.1" {
		t.Fatal("trusted arbitrary forwarded IP")
	}
	for _, value := range []string{"http://private.invalid", "https://user:password@host.invalid", "https://host.invalid?token=secret", "file:///etc/passwd"} {
		if _, err := r.app.validURL(value); err == nil {
			t.Fatal("unsafe URL accepted", value)
		}
	}
	w := r.request(t, "POST", "/api/auth/login", map[string]string{"hospital_code": "11447", "username": "staff", "password": "password", "api_url": "https://attacker.invalid"}, "", false)
	if w.Code != 400 {
		t.Fatal("client supplied routing accepted", w.Code)
	}
}

func TestPersonnelRelayUsesOnlySupportedQueryAndSafeFields(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	w := r.request(t, "GET", "/depdrop/employee-by-id?q=staff&page=1", nil, token, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test staff") || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestInstallationKeyFailurePreservesCentralSession(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"GATEWAY_UNAUTHORIZED","message":"bad key"}}`))}, nil
	})
	w := r.request(t, "GET", "/api/profile", nil, token, false)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	v, err := r.app.config.Store.session(context.Background(), token)
	if err != nil || v.revoked {
		t.Fatal("configuration outage revoked user session", err)
	}
}

func TestStaleProbeCannotApproveEditedConfiguration(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	snapshot, err := r.app.config.Store.hospital(context.Background(), "id", h.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, map[string]string{"code": h.Code, "name": "Edited", "province": "Test", "api_url": h.APIURL, "gateway_key": ""}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := r.app.config.Store.connection(context.Background(), snapshot, true); !errors.Is(err, errConfigChanged) {
		t.Fatal("stale probe was accepted", err)
	}
	w = r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/approve", nil, "", true)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestProfileIdentityChangeRevokesTheSession(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"id":8,"username":"another-user","employee":null,"avatar_url":null}}`))}, nil
	})
	w := r.request(t, "GET", "/api/profile", nil, token, false)
	if w.Code != 401 || strings.Contains(w.Body.String(), "another-user") {
		t.Fatal(w.Code, w.Body.String())
	}
	v, err := r.app.config.Store.session(context.Background(), token)
	if err != nil || !v.revoked {
		t.Fatal("changed identity did not revoke the central session", err)
	}
}

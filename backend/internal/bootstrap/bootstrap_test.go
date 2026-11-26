package bootstrap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func testCA(t *testing.T) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test-only root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(der)
	return authority{parsed, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}
func signedIdentity(t *testing.T, ca authority, key *ecdsa.PrivateKey, usage x509.ExtKeyUsage) (string, string) {
	t.Helper()
	if key == nil {
		key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	c := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "ERP Mobile Gateway"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(6 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, c, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return string(append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.pem...)), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
func testInput(t *testing.T) Input {
	cert, key := signedIdentity(t, testCA(t), nil, x509.ExtKeyUsageClientAuth)
	return Input{AdminUsername: "operator", AdminPassword: "a-test-only-password-12345", PublicURL: "https://gateway.example.org", ClientCertificatePEM: cert, ClientPrivateKeyPEM: key}
}
func managerForTest(t *testing.T, build func(RuntimeConfig, []byte) (Runtime, error)) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := New(Options{Directory: filepath.Join(dir, "config"), DatabasePath: filepath.Join(dir, "gateway.db"), Build: build})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func dummyRuntime(RuntimeConfig, []byte) (Runtime, error) {
	return Runtime{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}, nil
}
func request(t *testing.T, m *Manager, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	method := http.MethodPost
	if body == nil {
		method = http.MethodGet
	}
	r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:19001"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	return w
}
func setupSessionForTest(t *testing.T, m *Manager) (*http.Cookie, string) {
	t.Helper()
	code, err := IssueSetupCode(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, m, "/setup/api/session", map[string]string{"code": code}, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("missing protected setup cookie")
	}
	return cookies[0], out.Data.CSRF
}

func TestFirstRunActivationPersistenceAndClosedSetup(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	cookie, csrf := setupSessionForTest(t, m)
	in := testInput(t)
	if w := request(t, m, "/setup/api/validate", in, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if exists(filepath.Join(m.options.Directory, "encryption.key")) {
		t.Fatal("validation generated a key")
	}
	if w := request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 || !cfg.VerifyPassword(in.AdminPassword) || cfg.VerifyPassword("wrong") {
		t.Fatal("invalid key/verifier")
	}
	body, _ := os.ReadFile(filepath.Join(m.options.Directory, "runtime.json"))
	if bytes.Contains(body, []byte(in.AdminPassword)) || bytes.Contains(body, []byte(in.ClientPrivateKeyPEM)) {
		t.Fatal("plaintext secret leaked into config")
	}
	info, _ := os.Stat(filepath.Join(m.options.Directory, "encryption.key"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	m2, err := New(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	_, key2, err := LoadRuntime(m.options.Directory)
	if err != nil || !bytes.Equal(key, key2) {
		t.Fatal("restart changed key")
	}
	if m2.state != "active" {
		t.Fatalf("restart state %s", m2.state)
	}
	if w := request(t, m2, "/setup/api/activate", in, cookie, csrf); w.Code != 410 {
		t.Fatal("setup reopened", w.Code)
	}
}

func TestOneTimeCodeExpiryRateLimitAndOrigin(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	code, err := IssueSetupCode(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(m.options.Directory, "setup-code.json"))
	if bytes.Contains(raw, []byte(code)) {
		t.Fatal("raw setup code persisted")
	}
	if w := request(t, m, "/setup/api/session", map[string]string{"code": code}, nil, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, m, "/setup/api/session", map[string]string{"code": code}, nil, ""); w.Code != 401 {
		t.Fatal("code reused")
	}
	if err = persistJSON(filepath.Join(m.options.Directory, "setup-code.json"), codeRecord{Hash: hashToken(code), ExpiresAt: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if w := request(t, m, "/setup/api/session", map[string]string{"code": code}, nil, ""); w.Code != 401 {
		t.Fatal("expired code accepted")
	}
	for range 10 {
		request(t, m, "/setup/api/session", map[string]string{"code": "wrong"}, nil, "")
	}
	if w := request(t, m, "/setup/api/session", map[string]string{"code": "wrong"}, nil, ""); w.Code != 429 {
		t.Fatal("attempt ceiling missing")
	}
	r := httptest.NewRequest("POST", "http://localhost/setup/api/session", strings.NewReader(`{"code":"bad"}`))
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
}

func TestSetupIngressAndDNSRebindingGuard(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	tests := []struct {
		peer, host, forwarded string
		allow                 bool
	}{{"127.0.0.1:123", "localhost", "", true}, {"127.0.0.1:123", "attacker.example", "", false}, {"192.168.1.2:123", "localhost", "", false}, {"127.0.0.1:123", "localhost", "https", false}, {"203.0.113.1:123", "localhost", "", false}}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "http://"+tt.host+"/setup/api/status", nil)
		r.RemoteAddr = tt.peer
		r.Header.Set("X-Forwarded-Proto", tt.forwarded)
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		if (w.Code == 200) != tt.allow {
			t.Errorf("peer=%s host=%s forwarded=%s code=%d", tt.peer, tt.host, tt.forwarded, w.Code)
		}
	}
	m.options.LocalSetupCIDRs = []string{"172.18.0.0/16"}
	r := httptest.NewRequest("GET", "http://localhost/setup/api/status", nil)
	r.RemoteAddr = "172.18.0.1:123"
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("explicit Docker bridge denied")
	}
	r.Host = "gateway.example.org"
	w = httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("Docker ingress allowed public host")
	}
	r.Host = "localhost"
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("Docker ingress trusted forwarded HTTPS")
	}
	m.options.PublicSetupURL = "https://gateway.example.org"
	m.options.TrustedProxyCIDRs = []string{"172.18.0.3/32"}
	r.Host = "gateway.example.org"
	r.RemoteAddr = "172.18.0.3:123"
	w = httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("trusted HTTPS proxy denied")
	}
	r.RemoteAddr = "172.18.0.4:123"
	w = httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted proxy accepted")
	}
}

func TestMissingAndWrongDatabaseKeysNeverInitialize(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	key := bytes.Repeat([]byte{23}, 32)
	writeSentinel(t, m.options.DatabasePath, key)
	before, _ := os.ReadFile(m.options.DatabasePath)
	m2, err := New(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if m2.state != "recovery" {
		t.Fatal("existing data entered fresh setup")
	}
	cookie, csrf := setupSessionForTest(t, m2)
	in := testInput(t)
	if w := request(t, m2, "/setup/api/activate", in, cookie, csrf); w.Code != 409 {
		t.Fatal("missing key permitted", w.Code, w.Body.String())
	}
	in.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	if w := request(t, m2, "/setup/api/activate", in, cookie, csrf); w.Code != 409 {
		t.Fatal("wrong key permitted", w.Code)
	}
	after, _ := os.ReadFile(m.options.DatabasePath)
	if !bytes.Equal(before, after) || exists(filepath.Join(m.options.Directory, "encryption.key")) {
		t.Fatal("recovery mutated database or generated replacement key")
	}
	in.EncryptionKey = base64.StdEncoding.EncodeToString(key)
	if w := request(t, m2, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
		t.Fatal("correct recovery key rejected", w.Code, w.Body.String())
	}
}

func writeSentinel(t *testing.T, path string, key []byte) {
	t.Helper()
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	sealed := aead.Seal(nonce, nonce, []byte("erp-platform-gateway"), []byte("store-check"))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE secrets_check(id INTEGER PRIMARY KEY,value BLOB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO secrets_check(id,value) VALUES(1,?)", sealed); err != nil {
		t.Fatal(err)
	}
}

func TestFailedActivationRetainsOriginalKeyAndNoActiveConfig(t *testing.T) {
	m := managerForTest(t, func(RuntimeConfig, []byte) (Runtime, error) { return Runtime{}, errors.New("runtime refused") })
	cookie, csrf := setupSessionForTest(t, m)
	in := testInput(t)
	w := request(t, m, "/setup/api/activate", in, cookie, csrf)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	key, err := readEncryptionKey(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(m.options.Directory, "runtime.json")) {
		t.Fatal("failed candidate became current")
	}
	m.options.Build = dummyRuntime
	if w = request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, again, err := LoadRuntime(m.options.Directory)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("retry replaced encryption key")
	}
}

func TestCSRPrivateKeyStaysOnGatewayAndCanActivate(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	cookie, csrf := setupSessionForTest(t, m)
	w := request(t, m, "/setup/api/csr", map[string]any{}, cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("PRIVATE KEY")) {
		t.Fatal("CSR response leaked key")
	}
	var response struct {
		Data struct {
			CSR         string `json:"csr_pem"`
			Fingerprint string `json:"fingerprint"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	block, _ := pem.Decode([]byte(response.Data.CSR))
	if block == nil {
		t.Fatal("CSR missing")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || len(response.Data.Fingerprint) != 64 {
		t.Fatal("invalid CSR")
	}
	body, _ := os.ReadFile(filepath.Join(m.options.Directory, "staged-client-key.pem"))
	keyBlock, _ := pem.Decode(body)
	parsed, _ := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	cert, _ := signedIdentity(t, testCA(t), parsed.(*ecdsa.PrivateKey), x509.ExtKeyUsageClientAuth)
	in := testInput(t)
	in.ClientPrivateKeyPEM = ""
	in.ClientCertificatePEM = cert
	if w = request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestIdentityRejectsWrongPurposeAndReloadDrainsRequest(t *testing.T) {
	ca := testCA(t)
	cert, key := signedIdentity(t, ca, nil, x509.ExtKeyUsageServerAuth)
	if _, err := ValidateIdentity([]byte(cert), []byte(key)); err == nil {
		t.Fatal("serverAuth accepted for client identity")
	}
	var closed atomic.Int32
	var m *Manager
	validCert, validKey := signedIdentity(t, ca, nil, x509.ExtKeyUsageClientAuth)
	m = managerForTest(t, func(RuntimeConfig, []byte) (Runtime, error) {
		return Runtime{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/renew" {
				if err := m.ReplaceIdentity(r.Context(), []byte(validCert), []byte(validKey), nil); err != nil {
					t.Error(err)
				}
				if closed.Load() != 0 {
					t.Error("old runtime closed during its request")
				}
			}
			w.WriteHeader(204)
		}), Close: func() { closed.Add(1) }}, nil
	})
	cookie, csrf := setupSessionForTest(t, m)
	in := testInput(t)
	if w := request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w := request(t, m, "/renew", nil, nil, "")
		if w.Code != 204 {
			t.Errorf("renew code %d", w.Code)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("identity reload deadlocked inside old request")
	}
	deadline := time.After(time.Second)
	for closed.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("old runtime not retired")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := m.ReplaceIdentity(context.Background(), []byte("invalid"), []byte(validKey), nil); err == nil {
		t.Fatal("invalid identity activated")
	}
}

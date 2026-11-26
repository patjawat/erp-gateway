package bootstrap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"erp-platform/gateway/internal/gateway"
)

func fullBackupTool(t *testing.T) string {
	t.Helper()
	p := os.Getenv("GATEWAY_TEST_RESTIC")
	if p == "" {
		t.Skip("set GATEWAY_TEST_RESTIC for real encrypted backup integration")
	}
	return p
}
func newBackupIntegrationManager(t *testing.T, erp *httptest.Server, active bool) *Manager {
	t.Helper()
	tool := fullBackupTool(t)
	root := t.TempDir()
	var m *Manager
	roots := x509.NewCertPool()
	roots.AddCert(erp.Certificate())
	build := func(c RuntimeConfig, key []byte) (Runtime, error) {
		store, e := gateway.OpenStore(filepath.Join(root, "gateway.db"), key)
		if e != nil {
			return Runtime{}, e
		}
		pair, e := tls.LoadX509KeyPair(c.ClientCertFile, c.ClientKeyFile)
		if e != nil {
			store.Close()
			return Runtime{}, e
		}
		app, e := gateway.New(gateway.Config{Store: store, AdminUsername: c.AdminUsername, AdminPasswordVerifier: c.VerifyPassword, PublicURL: c.PublicURL, CookieSecure: true, GatewayClientCert: &pair, HospitalServerRoots: roots, ApprovedUpstreams: c.ApprovedUpstreams, PrivateUpstreamCIDRs: c.PrivateUpstreamCIDRs, BackupHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.BackupHandler().ServeHTTP(w, r) })})
		if e != nil {
			store.Close()
			return Runtime{}, e
		}
		return Runtime{Handler: app.Handler(), Close: func() { store.Close() }}, nil
	}
	var e error
	m, e = New(Options{Directory: filepath.Join(root, "config"), DatabasePath: filepath.Join(root, "gateway.db"), Build: build, BackupBinary: tool, ValidatePolicy: gateway.ValidateUpstreamPolicy})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	if active {
		cookie, csrf := setupSessionForTest(t, m)
		in := testInput(t)
		in.AdminPassword = "1"
		in.ApprovedUpstreams = []string{"01447=" + erp.URL}
		in.PrivateUpstreamCIDRs = []string{"127.0.0.1/32"}
		if w := request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if e = m.StartBackups(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m
}
func seedBackupSession(t *testing.T, m *Manager, erpURL string) string {
	t.Helper()
	db, e := sql.Open("sqlite", m.options.DatabasePath)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	block, _ := aes.NewCipher(m.key)
	aead, _ := cipher.NewGCM(block)
	seal := func(purpose, value string) []byte {
		nonce := make([]byte, aead.NonceSize())
		rand.Read(nonce)
		return aead.Seal(nonce, nonce, []byte(value), []byte(purpose))
	}
	_, e = db.Exec("INSERT INTO hospitals(id,code,name,province,api_url,gateway_key,approval_status,enabled,connection_status,created_at) VALUES('site','01447','Hospital','Bangkok',?,?,'approved',1,'online',?)", erpURL, seal("hospital:site", "test-hospital-key"), time.Now().Unix())
	if e != nil {
		t.Fatal(e)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	hexHash := hex.EncodeToString(hash[:])
	_, e = db.Exec("INSERT INTO sessions(token_hash,hospital_id,user_id,erp_token,expires_at) VALUES(?,'site','\"123\"',?,?)", hexHash, seal("session:site:"+hexHash, "test-upstream-token"), time.Now().Add(time.Hour).Unix())
	if e != nil {
		t.Fatal(e)
	}
	return token
}
func integrationERP(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-upstream-token" || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "missing preserved TLS or token", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"id":"123","name":"Test person"}}`)
	}))
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAnyClientCert}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
func callBackupJSON(t *testing.T, m *Manager, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	origin := "http://localhost"
	if strings.HasPrefix(path, "/admin") {
		origin = "https://localhost"
	}
	r.Header.Set("Origin", origin)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	return w
}
func backupAdmin(t *testing.T, m *Manager) (*http.Cookie, string) {
	t.Helper()
	w := callBackupJSON(t, m, http.MethodPost, "/admin/api/login", map[string]string{"username": "operator", "password": "1"}, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	return w.Result().Cookies()[0], result.Data.CSRF
}
func jobFromResponse(t *testing.T, w *httptest.ResponseRecorder) backupJob {
	t.Helper()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Data struct {
			Job backupJob `json:"job"`
		}
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Data.Job.ID == "" {
		t.Fatal("no async job", w.Body.String())
	}
	return result.Data.Job
}
func waitBackupJob(t *testing.T, s *backupService, id string) backupJob {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var raw string
		if e := s.db.QueryRow("SELECT body FROM jobs WHERE id=?", id).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		var j backupJob
		json.Unmarshal([]byte(raw), &j)
		if j.State != "running" {
			if j.State != "completed" {
				t.Fatal(j.Error)
			}
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return backupJob{}
}
func TestManualBackupDownloadFreshSetupImportPreservesMobile(t *testing.T) {
	erp := integrationERP(t)
	source := newBackupIntegrationManager(t, erp, true)
	token := seedBackupSession(t, source, erp.URL)
	cookie, csrf := backupAdmin(t, source)
	if w := callBackupJSON(t, source, http.MethodPost, "/admin/api/backups", map[string]any{}, nil, ""); w.Code != 401 {
		t.Fatal("backup lacks Admin authentication")
	}
	job := jobFromResponse(t, callBackupJSON(t, source, http.MethodPost, "/admin/api/backups", map[string]any{}, cookie, csrf))
	completed := waitBackupJob(t, source.backups, job.ID)
	downloaded := callBackupJSON(t, source, http.MethodGet, "/admin/api/backups/"+completed.BackupID+"/download", nil, cookie, "")
	if downloaded.Code != 200 || downloaded.Body.Len() == 0 {
		t.Fatal(downloaded.Code, "empty bundle")
	}
	if bytes.Contains(downloaded.Body.Bytes(), []byte("test-upstream-token")) {
		t.Fatal("backup exposes plaintext")
	}
	key := source.backups.state.Secret
	destination := newBackupIntegrationManager(t, erp, false)
	setupCookie, setupCSRF := setupSessionForTest(t, destination)
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, _ := writer.CreateFormFile("file", "gateway.bundle")
	part.Write(downloaded.Body.Bytes())
	writer.WriteField("recovery_secret", key)
	writer.Close()
	req := httptest.NewRequest(http.MethodPost, "http://localhost/setup/api/backups/restore", &upload)
	req.RemoteAddr = "127.0.0.1:1000"
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", setupCSRF)
	req.AddCookie(setupCookie)
	response := httptest.NewRecorder()
	destination.Handler().ServeHTTP(response, req)
	imported := waitBackupJob(t, destination.backups, jobFromResponse(t, response).ID)
	inspection, e := destination.backups.loadInspection(imported.RestoreID)
	if e != nil || inspection.Result.Summary["hospitals"] != 1 {
		t.Fatal("complete data not inspected", e)
	}
	if e = source.freeze(context.Background(), "migration"); e != nil {
		t.Fatal(e)
	}
	activate := jobFromResponse(t, callBackupJSON(t, destination, http.MethodPost, "/setup/api/backups/restore/"+imported.RestoreID+"/activate", map[string]any{"confirm_id": imported.RestoreID, "source_stopped": true}, setupCookie, setupCSRF))
	waitBackupJob(t, destination.backups, activate.ID)
	if !destination.config.VerifyPassword("1") || !bytes.Equal(source.key, destination.key) {
		t.Fatal("Admin/key continuity lost")
	}
	mobile := httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil)
	mobile.Header.Set("Authorization", "Bearer "+token)
	result := httptest.NewRecorder()
	destination.Handler().ServeHTTP(result, mobile)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "123") {
		t.Fatal("Mobile session did not continue", result.Code, result.Body.String())
	}
	if destination.backups.state.Policy.Enabled {
		t.Fatal("imported scheduler activated without confirmation")
	}
}
func TestDirectMigrationBaseFinalAndPersistentFence(t *testing.T) {
	erp := integrationERP(t)
	source := newBackupIntegrationManager(t, erp, true)
	seedBackupSession(t, source, erp.URL)
	destination := newBackupIntegrationManager(t, erp, false)
	var finalPhase, lostResponse atomic.Bool
	var baseBytes, finalBytes, resentBaseObjects atomic.Int64
	var baseObjects sync.Map
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/backup/transfer/object" {
			name := r.URL.Query().Get("path")
			if finalPhase.Load() {
				finalBytes.Add(r.ContentLength)
				if _, old := baseObjects.Load(name); old {
					resentBaseObjects.Add(1)
				}
			} else {
				baseBytes.Add(r.ContentLength)
				baseObjects.Store(name, true)
			}
		}
		destination.Handler().ServeHTTP(w, r)
	}))
	defer receiver.Close()
	client := receiver.Client()
	transport := client.Transport
	client.Transport = backupRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, e := transport.RoundTrip(r)
		if e == nil && r.Method == http.MethodPut && r.URL.Path == "/backup/transfer/object" && !lostResponse.Swap(true) {
			response.Body.Close()
			return nil, errors.New("synthetic lost chunk acknowledgement")
		}
		return response, e
	})
	source.backups.transferClient = client
	w := httptest.NewRecorder()
	destination.backups.migrationHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")), "receive")
	var pair struct {
		Data struct {
			Code string `json:"code"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &pair)
	j := backupJob{}
	baseStarted := time.Now()
	if e := source.backups.sendMigration(context.Background(), receiver.URL, pair.Data.Code, true, false, &j); e == nil {
		t.Fatal("lost acknowledgement was not exercised")
	}
	if e := source.backups.sendMigration(context.Background(), receiver.URL, "", true, false, &j); e != nil {
		t.Fatal(e)
	}
	baseDuration := time.Since(baseStarted)
	db, _ := sql.Open("sqlite", source.options.DatabasePath)
	_, e := db.Exec("INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('site','01447','after-base','final data',?)", time.Now().Unix())
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	finalPhase.Store(true)
	interruptionStarted := time.Now()
	if e = source.backups.sendMigration(context.Background(), receiver.URL, "", true, true, &j); e != nil {
		t.Fatal(e)
	}
	source.mu.Lock()
	fenced := source.fenced
	source.mu.Unlock()
	if !fenced {
		t.Fatal("source not fenced")
	}
	destination.backups.mu.Lock()
	transfer := *destination.backups.transfer
	destination.backups.mu.Unlock()
	if transfer.State != "final_ready" {
		t.Fatal(transfer.State)
	}
	inspected, e := destination.backups.loadInspection(transfer.RestoreID)
	if e != nil {
		t.Fatal(e)
	}
	if inspected.Result.Summary["audit"] < 1 {
		t.Fatal("final capture omitted writes after base")
	}
	if e = destination.backups.activateRestore(context.Background(), transfer.RestoreID, "", true); e != nil {
		t.Fatal(e)
	}
	if resentBaseObjects.Load() != 0 {
		t.Fatal("final transfer resent immutable base objects")
	}
	t.Logf("isolated loopback fixture: base preparation=%s bytes=%d; final drain/capture/transfer/activation=%s new_bytes=%d; DNS/proxy propagation excluded", baseDuration, baseBytes.Load(), time.Since(interruptionStarted), finalBytes.Load())
	restored, _ := sql.Open("sqlite", destination.options.DatabasePath)
	var n int
	restored.QueryRow("SELECT count(*) FROM audit WHERE action='after-base'").Scan(&n)
	restored.Close()
	if n != 1 {
		t.Fatal("latest source write lost")
	}
	restart, e := New(source.options)
	if e != nil {
		t.Fatal(e)
	}
	defer restart.Close()
	if !restart.fenced {
		t.Fatal("restart resumed source")
	}
	blocked := httptest.NewRecorder()
	restart.Handler().ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/api/profile", nil))
	if blocked.Code != 503 {
		t.Fatal("fenced source accepted request")
	}
}

type backupRoundTripFunc func(*http.Request) (*http.Response, error)

func (f backupRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMigrationURLAndAddressBoundaries(t *testing.T) {
	for _, raw := range []string{"http://site", "https://site/path", "https://user:pass@site", "https://site?x=1", "file:///etc/passwd"} {
		if _, e := validateMigrationURL(raw); e == nil {
			t.Fatal("unsafe URL accepted", raw)
		}
	}
	for _, raw := range []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "::1", "fe80::1", "224.0.0.1"} {
		if migrationAddressAllowed(netip.MustParseAddr(raw), true) {
			t.Fatal("unsafe address", raw)
		}
	}
	if migrationAddressAllowed(netip.MustParseAddr("10.10.8.127"), false) || !migrationAddressAllowed(netip.MustParseAddr("10.10.8.127"), true) {
		t.Fatal("LAN needs explicit opt in")
	}
}
func TestFreezeDrainsInflightAndBlocksNewRequests(t *testing.T) {
	m := activeSettingsManager(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var closed atomic.Bool
	m.mu.Lock()
	m.current = &running{Runtime: Runtime{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }), Close: func() { closed.Store(true) }}}
	m.mu.Unlock()
	go m.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/work", nil))
	<-entered
	done := make(chan error, 1)
	go func() { done <- m.freeze(context.Background(), "migration") }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		paused := m.fenced
		m.mu.Unlock()
		if paused {
			break
		}
		time.Sleep(time.Millisecond)
	}
	blocked := httptest.NewRecorder()
	m.Handler().ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/api/new", nil))
	if blocked.Code != 503 || closed.Load() {
		t.Fatal("new request accepted or inflight DB closed")
	}
	close(release)
	if e := <-done; e != nil || !closed.Load() {
		t.Fatal("drain did not complete", e)
	}
}

// Opt-in release rehearsal fixture: all credentials/data are synthetic and the
// caller owns a separate temporary output directory, never a live volume.
func TestContainerBackupFixture(t *testing.T) {
	directory := os.Getenv("GATEWAY_TEST_FIXTURE_DIRECTORY")
	if directory == "" {
		t.Skip("set fixture directory for isolated packaged-image rehearsal")
	}
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	erp := integrationERP(t)
	m := newBackupIntegrationManager(t, erp, true)
	if os.Getenv("GATEWAY_TEST_VERIFY_CONTAINER_EXPORT") == "true" {
		kit, e := os.ReadFile(filepath.Join(directory, "roundtrip-key.json"))
		if e != nil {
			t.Fatal(e)
		}
		x, e := m.backups.inspectBundle(context.Background(), filepath.Join(directory, "roundtrip.bundle"), string(kit), "")
		if e != nil || x.Summary["hospitals"] != 1 || x.Summary["sessions"] != 1 || !x.Payload.Config.VerifyPassword("1") {
			t.Fatal("packaged export did not preserve complete Gateway", e)
		}
		return
	}
	seedBackupSession(t, m, erp.URL)
	r, e := m.backups.capture(context.Background(), "", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = copyBackupFile(filepath.Join(m.backups.recordPath(r.ID), "gateway-backup.bundle"), filepath.Join(directory, "fixture.bundle")); e != nil {
		t.Fatal(e)
	}
	kit, _ := json.Marshal(kitForState(m.backups.state))
	if e = writePrivate(filepath.Join(directory, "recovery-kit.json"), kit); e != nil {
		t.Fatal(e)
	}
}

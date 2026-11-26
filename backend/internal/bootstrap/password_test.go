package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const resetPasswordForTest = "new-test-only-admin-password-456"

func activePasswordManagerForTest(t *testing.T) *Manager {
	t.Helper()
	m := activeSettingsManager(t)
	writeSentinel(t, m.options.DatabasePath, m.key)
	return m
}

func passwordFilesForTest(t *testing.T, m *Manager) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, path := range []string{filepath.Join(m.options.Directory, "runtime.json"), filepath.Join(m.options.Directory, "encryption.key"), m.config.ClientCertFile, m.config.ClientKeyFile, m.config.HospitalCAFile, m.options.DatabasePath} {
		if path == "" {
			continue
		}
		body, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[path] = body
	}
	return files
}

func assertPasswordFilesUnchanged(t *testing.T, before map[string][]byte) {
	t.Helper()
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("password recovery changed %s", filepath.Base(path))
		}
	}
}

func TestResetAdminPasswordPreservesDataAndIdentityWithSetupDisabled(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	m.options.ValidatePolicy = testSettingsPolicy
	cookie, csrf := setupSessionForTest(t, m)
	input := testInput(t)
	input.HospitalCAPEM = string(testCA(t).pem)
	if w := request(t, m, "/setup/api/activate", input, cookie, csrf); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	enabled := false
	m.options.SetupEnabled = &enabled
	settings, err := m.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.ApprovedUpstreams = []string{"01447=https://hospital.example.org"}
	settings.PrivateUpstreamCIDRs = []string{"10.20.30.40/32"}
	if err := m.ApplySettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	before, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	writeSentinel(t, m.options.DatabasePath, key)
	files := passwordFilesForTest(t, m)
	oldRuntime := m.current
	username, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest)
	if err != nil || username != before.AdminUsername {
		t.Fatalf("password reset failed: username=%q, error=%v", username, err)
	}
	after, afterKey, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if !after.VerifyPassword(resetPasswordForTest) || after.VerifyPassword("a-test-only-password-12345") {
		t.Fatal("reset did not replace the administrator verifier")
	}
	if before.PasswordSalt == after.PasswordSalt || before.PasswordHash == after.PasswordHash {
		t.Fatal("reset did not generate a new salted verifier")
	}
	unchanged := after
	unchanged.PasswordSalt, unchanged.PasswordHash, unchanged.PasswordIterations = before.PasswordSalt, before.PasswordHash, before.PasswordIterations
	if !reflect.DeepEqual(before, unchanged) || !bytes.Equal(key, afterKey) || m.current == oldRuntime || m.state != "active" {
		t.Fatal("password reset changed non-password state or failed to install the new runtime")
	}
	delete(files, filepath.Join(m.options.Directory, "runtime.json"))
	assertPasswordFilesUnchanged(t, files)
	if err := CheckDatabaseKey(m.options.DatabasePath, key); err != nil {
		t.Fatal("original database/encryption key no longer match", err)
	}
	body, err := os.ReadFile(filepath.Join(m.options.Directory, "runtime.json"))
	if err != nil || bytes.Contains(body, []byte(resetPasswordForTest)) {
		t.Fatal("new password was persisted as plaintext")
	}
	restarted, err := New(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.state != "active" || !restarted.config.VerifyPassword(resetPasswordForTest) {
		t.Fatal("password recovery was not durable across restart")
	}
}

func TestResetAdminPasswordRejectsInvalidAndUnavailableAccounts(t *testing.T) {
	m := activePasswordManagerForTest(t)
	before := passwordFilesForTest(t, m)
	current := m.current
	for _, password := range []string{"", strings.Repeat("x", 4097)} {
		if _, err := m.ResetAdminPassword(context.Background(), password); err == nil || strings.Contains(err.Error(), password) && password != "" {
			t.Fatal("invalid password accepted or disclosed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ResetAdminPassword(ctx, resetPasswordForTest); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reset was accepted", err)
	}
	assertPasswordFilesUnchanged(t, before)
	if m.current != current {
		t.Fatal("failed reset replaced the runtime")
	}
	for _, kind := range []string{"unconfigured", "legacy", "closed"} {
		t.Run(kind, func(t *testing.T) {
			other := managerForTest(t, dummyRuntime)
			if kind == "legacy" {
				other.install(Runtime{Handler: http.NotFoundHandler()})
			} else if kind == "closed" {
				other.Close()
			}
			if _, err := other.ResetAdminPassword(context.Background(), resetPasswordForTest); err == nil {
				t.Fatal("password reset initialized an unavailable account")
			}
			for _, name := range []string{"runtime.json", "encryption.key", "identities"} {
				if exists(filepath.Join(other.options.Directory, name)) {
					t.Fatal("failed reset initialized account material")
				}
			}
		})
	}
}

func TestResetAdminPasswordFailedCandidatesKeepAccount(t *testing.T) {
	m := activePasswordManagerForTest(t)
	before := passwordFilesForTest(t, m)
	current := m.current
	closed := 0
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		return Runtime{Close: func() { closed++ }}, errors.New("candidate failed")
	}
	if _, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest); err == nil || closed != 1 {
		t.Fatal("failed candidate was committed or leaked", err, closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		cancel()
		return Runtime{Handler: http.NotFoundHandler(), Close: func() { closed++ }}, nil
	}
	if _, err := m.ResetAdminPassword(ctx, resetPasswordForTest); !errors.Is(err, context.Canceled) || closed != 2 {
		t.Fatal("cancelled candidate was committed or leaked", err, closed)
	}
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		return Runtime{Close: func() { closed++ }}, nil
	}
	if _, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest); err == nil || closed != 3 {
		t.Fatal("candidate without a handler was committed or leaked", err, closed)
	}
	assertPasswordFilesUnchanged(t, before)
	if m.current != current || !m.config.VerifyPassword("a-test-only-password-12345") {
		t.Fatal("candidate failure disabled the old account")
	}
}

func TestResetAdminPasswordPersistenceFailureKeepsAccount(t *testing.T) {
	m := activePasswordManagerForTest(t)
	before := passwordFilesForTest(t, m)
	current := m.current
	closed := false
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		return Runtime{Handler: http.NotFoundHandler(), Close: func() { closed = true }}, nil
	}
	failBeforeCommit := func(string, RuntimeConfig) error { return os.ErrPermission }
	if _, err := m.resetAdminPassword(context.Background(), resetPasswordForTest, failBeforeCommit); err == nil || !closed {
		t.Fatal("failed persistence was committed or leaked its candidate", err)
	}
	assertPasswordFilesUnchanged(t, before)
	if m.current != current || !m.config.VerifyPassword("a-test-only-password-12345") {
		t.Fatal("failed persistence disabled the original account")
	}
}

func TestResetAdminPasswordCommittedSyncFailureKeepsRuntimeCoherent(t *testing.T) {
	m := activePasswordManagerForTest(t)
	before := passwordFilesForTest(t, m)
	current := m.current
	closed := false
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		return Runtime{Handler: http.NotFoundHandler(), Close: func() { closed = true }}, nil
	}
	failAfterCommit := func(path string, cfg RuntimeConfig) error {
		if err := persistJSON(path, cfg); err != nil {
			return err
		}
		return errors.New("simulated directory sync failure after atomic rename")
	}
	username, err := m.resetAdminPassword(context.Background(), resetPasswordForTest, failAfterCommit)
	var warning *PasswordResetDurabilityWarning
	if username != m.config.AdminUsername || !errors.As(err, &warning) || closed || m.current == current {
		t.Fatal("committed reset was misreported, discarded or left the old runtime active", err)
	}
	stored, _, loadErr := LoadRuntime(m.options.Directory)
	if loadErr != nil || !stored.VerifyPassword(resetPasswordForTest) || !m.config.VerifyPassword(resetPasswordForTest) || m.config.VerifyPassword("a-test-only-password-12345") {
		t.Fatal("disk and live administrator verifiers disagree after the committed write", loadErr)
	}
	delete(before, filepath.Join(m.options.Directory, "runtime.json"))
	assertPasswordFilesUnchanged(t, before)
}

func TestResetAdminPasswordRefusesExternalChangesAndBusyConfig(t *testing.T) {
	m := activePasswordManagerForTest(t)
	before := passwordFilesForTest(t, m)
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest); err == nil {
		unlock()
		t.Fatal("reset ignored another configuration writer")
	}
	unlock()
	assertPasswordFilesUnchanged(t, before)
	changed := m.config
	changed.PublicURL = "https://outside-change.example.org"
	if err := persistJSON(filepath.Join(m.options.Directory, "runtime.json"), changed); err != nil {
		t.Fatal(err)
	}
	external := passwordFilesForTest(t, m)
	if _, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest); err == nil {
		t.Fatal("reset overwrote an external config change")
	}
	assertPasswordFilesUnchanged(t, external)
}

func TestResetAdminPasswordDrainsOldRequests(t *testing.T) {
	m := activePasswordManagerForTest(t)
	entered, release, finished, closed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	m.current.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("old request completed"))
	})
	m.current.Close = func() { close(closed) }
	go func() {
		defer close(finished)
		m.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil))
	}()
	<-entered
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		return Runtime{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("new runtime")) })}, nil
	}
	reset := make(chan error, 1)
	go func() { _, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest); reset <- err }()
	select {
	case err := <-reset:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("password reset waited for or interrupted the old request")
	}
	select {
	case <-closed:
		t.Fatal("old runtime closed before its request finished")
	default:
	}
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/api/profile", nil))
	if w.Body.String() != "new runtime" {
		t.Fatal("new requests did not use the replacement runtime")
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("old runtime was not closed after draining")
	}
	<-finished
}

func TestResetAdminPasswordRefusesMissingOrWrongDatabase(t *testing.T) {
	for _, kind := range []string{"missing", "wrong key"} {
		t.Run(kind, func(t *testing.T) {
			m := activePasswordManagerForTest(t)
			if err := os.Remove(m.options.DatabasePath); err != nil {
				t.Fatal(err)
			}
			if kind == "wrong key" {
				writeSentinel(t, m.options.DatabasePath, bytes.Repeat([]byte{17}, 32))
			}
			before := passwordFilesForTest(t, m)
			current := m.current
			built := false
			m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
				built = true
				return dummyRuntime(RuntimeConfig{}, nil)
			}
			if _, err := m.ResetAdminPassword(context.Background(), resetPasswordForTest); err == nil || built || m.current != current {
				t.Fatal("password reset built a new runtime without the original database", err)
			}
			assertPasswordFilesUnchanged(t, before)
			if kind == "missing" && exists(m.options.DatabasePath) {
				t.Fatal("password reset initialized a replacement database")
			}
		})
	}
}

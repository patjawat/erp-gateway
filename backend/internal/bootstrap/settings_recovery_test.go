package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSettingsPolicy(entries, networks []string) error {
	for _, entry := range entries {
		code, raw, ok := strings.Cut(entry, "=")
		u, err := url.Parse(raw)
		if !ok || len(code) != 5 || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid exact approved upstream")
		}
	}
	for _, network := range networks {
		if !strings.HasSuffix(network, "/32") && !strings.HasSuffix(network, "/128") {
			return errors.New("test requires exact private host network")
		}
	}
	return nil
}
func activeSettingsManager(t *testing.T) *Manager {
	t.Helper()
	m := managerForTest(t, dummyRuntime)
	m.options.ValidatePolicy = testSettingsPolicy
	cookie, csrf := setupSessionForTest(t, m)
	if w := request(t, m, "/setup/api/activate", testInput(t), cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return m
}
func TestSettingsApplyPreservesCredentialsKeyAndIdentity(t *testing.T) {
	m := activeSettingsManager(t)
	before, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := m.Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.PublicURL = "https://new-gateway.example.org"
	settings.TrustedProxyCIDRs = []string{"127.0.0.1/32", "172.18.0.0/16"}
	settings.ApprovedUpstreams = []string{"01447=https://hospital.example.org"}
	settings.PrivateUpstreamCIDRs = []string{"10.20.30.40/32"}
	if err = m.ApplySettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	after, key2, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, key2) || before.AdminUsername != after.AdminUsername || before.PasswordSalt != after.PasswordSalt || before.PasswordHash != after.PasswordHash || before.ClientCertFile != after.ClientCertFile || before.ClientKeyFile != after.ClientKeyFile || before.HospitalCAFile != after.HospitalCAFile {
		t.Fatal("settings changed credentials/key/identity")
	}
	if after.PublicURL != settings.PublicURL || !after.VerifyPassword("a-test-only-password-12345") {
		t.Fatal("settings or verifier lost")
	}
	// Mutating caller-owned or returned slices must not mutate the manager.
	settings.ApprovedUpstreams[0] = "tampered"
	read, err := m.Settings()
	if err != nil || read.ApprovedUpstreams[0] != "01447=https://hospital.example.org" {
		t.Fatal(read, err)
	}
	read.TrustedProxyCIDRs[0] = "tampered"
	if next, err := m.Settings(); err != nil || next.TrustedProxyCIDRs[0] != "127.0.0.1/32" {
		t.Fatal(next, err)
	}
	m2, err := New(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	read, err = m2.Settings()
	if err != nil || read.PublicURL != after.PublicURL {
		t.Fatal("settings not durable after restart", read, err)
	}
}
func TestInvalidFailedAndCancelledSettingsKeepCurrentConfig(t *testing.T) {
	m := activeSettingsManager(t)
	before, _ := os.ReadFile(filepath.Join(m.options.Directory, "runtime.json"))
	key, _ := readEncryptionKey(m.options.Directory)
	original, _ := m.Settings()
	for _, mutate := range []func(*PublicSettings){func(s *PublicSettings) { s.PublicURL = "http://plain.example.org" }, func(s *PublicSettings) { s.PublicURL = "https://gateway.example.org:65536" }, func(s *PublicSettings) { s.TrustedProxyCIDRs = []string{"0.0.0.0/0"} }, func(s *PublicSettings) { s.TrustedProxyCIDRs = []string{"10.0.0.0/8"} }, func(s *PublicSettings) { s.TrustedProxyCIDRs = []string{"172.18.1.5/16"} }, func(s *PublicSettings) { s.ApprovedUpstreams = []string{"01447=http://hospital.example.org"} }} {
		input := original
		mutate(&input)
		if err := m.ApplySettings(context.Background(), input); err == nil {
			t.Fatal("invalid settings accepted", input)
		}
	}
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) { return Runtime{}, errors.New("candidate refused") }
	input := original
	input.PublicURL = "https://candidate.example.org"
	if err := m.ApplySettings(context.Background(), input); err == nil {
		t.Fatal("failed candidate accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	closed := false
	m.options.Build = func(RuntimeConfig, []byte) (Runtime, error) {
		cancel()
		return Runtime{Handler: m.current.Handler, Close: func() { closed = true }}, nil
	}
	if err := m.ApplySettings(ctx, input); err == nil || !closed {
		t.Fatal("cancelled candidate was committed or leaked")
	}
	after, _ := os.ReadFile(filepath.Join(m.options.Directory, "runtime.json"))
	afterKey, _ := readEncryptionKey(m.options.Directory)
	if !bytes.Equal(before, after) || !bytes.Equal(key, afterKey) || m.config.PublicURL != original.PublicURL {
		t.Fatal("failed settings changed persistent/live configuration")
	}
}
func TestSettingsRefuseLegacyAndExternalConfigChanges(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	if _, err := m.Settings(); err == nil {
		t.Fatal("unconfigured settings returned")
	}
	m.options.Legacy = &Runtime{Handler: m.Handler()}
	m.install(*m.options.Legacy)
	if _, err := m.Settings(); err == nil {
		t.Fatal("legacy settings returned")
	}
	if _, err := m.ExportRecovery(context.Background(), "a-recovery-password-123"); err == nil {
		t.Fatal("legacy secrets exported")
	}
	active := activeSettingsManager(t)
	cfg := active.config
	cfg.PublicURL = "https://outside-change.example.org"
	if err := persistJSON(filepath.Join(active.options.Directory, "runtime.json"), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := active.Settings(); err == nil {
		t.Fatal("external changes silently used")
	}
}
func TestRecoveryEncryptionRestoreAndNoOverwrite(t *testing.T) {
	m := activeSettingsManager(t)
	passphrase := "a-dedicated-recovery-passphrase-1234"
	archive, err := m.ExportRecovery(context.Background(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{passphrase, "BEGIN PRIVATE KEY", m.config.PasswordHash, strings.TrimSpace(string(mustRead(t, filepath.Join(m.options.Directory, "encryption.key"))))} {
		if bytes.Contains(archive, []byte(secret)) {
			t.Fatal("plaintext secret in encrypted recovery archive")
		}
	}
	payload, err := DecryptRecovery(context.Background(), archive, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Config.PasswordHash != m.config.PasswordHash || payload.ClientPrivateKeyPEM == "" || payload.EncryptionKey == "" {
		t.Fatal("recovery omitted required material")
	}
	second, err := m.ExportRecovery(context.Background(), passphrase)
	if err != nil || bytes.Equal(archive, second) {
		t.Fatal("recovery salt/nonce were reused")
	}
	if _, err = DecryptRecovery(context.Background(), archive, "wrong-but-long-passphrase"); err == nil {
		t.Fatal("wrong passphrase decrypted archive")
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if err = RestoreRecovery(context.Background(), archive, passphrase, destination, testSettingsPolicy); err != nil {
		t.Fatal(err)
	}
	restored, key, err := LoadRuntime(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, m.key) || !restored.VerifyPassword("a-test-only-password-12345") || !within(destination, restored.ClientKeyFile) {
		t.Fatal("restored credentials or paths invalid")
	}
	for _, name := range []string{filepath.Join(destination, "runtime.json"), filepath.Join(destination, "encryption.key"), restored.ClientKeyFile} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("restore file permissions", name, err)
		}
	}
	info, _ := os.Stat(destination)
	if info.Mode().Perm() != 0700 {
		t.Fatal("restore directory permissions")
	}
	before := mustRead(t, filepath.Join(destination, "runtime.json"))
	if err = RestoreRecovery(context.Background(), archive, passphrase, destination, testSettingsPolicy); err == nil {
		t.Fatal("restore overwrote existing directory")
	}
	if !bytes.Equal(before, mustRead(t, filepath.Join(destination, "runtime.json"))) {
		t.Fatal("existing config changed on rejected restore")
	}
}
func TestRecoveryBadArchivesAndPayloadNeverCreateDestination(t *testing.T) {
	m := activeSettingsManager(t)
	passphrase := "a-dedicated-recovery-passphrase-1234"
	archive, err := m.ExportRecovery(context.Background(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	var envelope recoveryEnvelope
	if err = json.Unmarshal(archive, &envelope); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "must-not-exist")
	if err = RestoreRecovery(context.Background(), archive, "wrong-but-long-passphrase", destination, testSettingsPolicy); err == nil || exists(destination) {
		t.Fatal("wrong passphrase left files")
	}
	envelope.Iterations = 1
	bad, _ := json.Marshal(envelope)
	if err = RestoreRecovery(context.Background(), bad, passphrase, destination, testSettingsPolicy); err == nil || exists(destination) {
		t.Fatal("KDF downgrade accepted or left files")
	}
	payload, err := DecryptRecovery(context.Background(), archive, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	payload.ClientPrivateKeyPEM = "invalid"
	bad, err = sealRecovery(context.Background(), payload, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if err = RestoreRecovery(context.Background(), bad, passphrase, destination, testSettingsPolicy); err == nil || exists(destination) {
		t.Fatal("invalid identity left partial files")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = RestoreRecovery(ctx, archive, passphrase, destination, testSettingsPolicy); err == nil || exists(destination) {
		t.Fatal("cancelled restore left files")
	}
}
func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

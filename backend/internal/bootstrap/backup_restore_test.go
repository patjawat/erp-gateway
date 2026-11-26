package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"erp-platform/gateway/internal/gateway"
)

func TestRestoreJournalRollsBackEveryInterruptedEntryIncludingBackupSettings(t *testing.T) {
	for applied := 0; applied <= 4; applied++ {
		t.Run(string(rune('0'+applied)), func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config")
			db := filepath.Join(dir, "gateway.db")
			if err := os.MkdirAll(filepath.Join(config, ".backup"), 0700); err != nil {
				t.Fatal(err)
			}
			j := cutoverJournal{Version: 1, ID: backupID(), RestoreID: backupID(), Installation: backupID(), Phase: "prepared"}
			for _, target := range []string{db, filepath.Join(config, "encryption.key"), filepath.Join(config, "runtime.json"), filepath.Join(config, ".backup", "state.enc")} {
				if err := os.WriteFile(target, []byte("original:"+filepath.Base(target)), 0600); err != nil {
					t.Fatal(err)
				}
				next := target + ".next-" + j.ID
				if err := os.WriteFile(next, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
				j.Entries = append(j.Entries, cutoverEntry{Target: target, Next: next, Old: target + ".before-" + j.ID, Existed: true})
			}
			if err := persistJSON(journalPath(config), j); err != nil {
				t.Fatal(err)
			}
			if err := persistJSON(filepath.Join(config, ".backup", "source-fence.json"), restoreFence{Version: 1, Kind: "restore", Installation: j.Installation, CutoverID: j.ID, RestoreID: j.RestoreID}); err != nil {
				t.Fatal(err)
			}
			for _, entry := range j.Entries[:applied] {
				if err := os.Rename(entry.Target, entry.Old); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(entry.Next, entry.Target); err != nil {
					t.Fatal(err)
				}
			}
			if err := recoverBackupCutover(config, db); err != nil {
				t.Fatal(err)
			}
			if err := recoverBackupCutover(config, db); err != nil {
				t.Fatal("recovery was not idempotent", err)
			}
			for _, entry := range j.Entries {
				body, err := os.ReadFile(entry.Target)
				if err != nil || string(body) != "original:"+filepath.Base(entry.Target) {
					t.Fatalf("mixed generation: %s body=%q err=%v", entry.Target, body, err)
				}
			}
			if exists(filepath.Join(config, ".backup", "source-fence.json")) {
				t.Fatal("completed pre-active rollback retained its destination fence")
			}
		})
	}
}

func TestActiveRestoreJournalClearsOnlyMatchingDestinationFence(t *testing.T) {
	for _, kind := range []string{"restore", "migration", "other-installation"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config")
			db := filepath.Join(dir, "gateway.db")
			if err := os.MkdirAll(filepath.Join(config, ".backup"), 0700); err != nil {
				t.Fatal(err)
			}
			j := cutoverJournal{Version: 1, ID: backupID(), RestoreID: backupID(), Installation: backupID(), Phase: "active"}
			if err := persistJSON(journalPath(config), j); err != nil {
				t.Fatal(err)
			}
			fence := restoreFence{Version: 1, Kind: kind, Installation: j.Installation, CutoverID: j.ID, RestoreID: j.RestoreID}
			if kind == "other-installation" {
				fence.Kind = "restore"
				fence.Installation = backupID()
			}
			if err := persistJSON(filepath.Join(config, ".backup", "source-fence.json"), fence); err != nil {
				t.Fatal(err)
			}
			if err := recoverBackupCutover(config, db); err != nil {
				t.Fatal(err)
			}
			present := exists(filepath.Join(config, ".backup", "source-fence.json"))
			if present == (kind == "restore") {
				t.Fatalf("unexpected fence state kind=%s present=%v", kind, present)
			}
			if !exists(restoreReceiptPath(config, j.RestoreID)) {
				t.Fatal("active restore receipt missing")
			}
			if err := recoverBackupCutover(config, db); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func realBackupManager(t *testing.T, active bool) *Manager {
	t.Helper()
	binary := os.Getenv("GATEWAY_TEST_RESTIC")
	if binary == "" {
		t.Skip("set GATEWAY_TEST_RESTIC for complete restore rehearsal")
	}
	dir := t.TempDir()
	database := filepath.Join(dir, "gateway.db")
	build := func(cfg RuntimeConfig, key []byte) (Runtime, error) {
		store, err := gateway.OpenStore(database, key)
		if err != nil {
			return Runtime{}, err
		}
		var once sync.Once
		return Runtime{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), Close: func() { once.Do(func() { _ = store.Close() }) }}, nil
	}
	m, err := New(Options{Directory: filepath.Join(dir, "config"), DatabasePath: database, Build: build, BackupBinary: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if active {
		cookie, csrf := setupSessionForTest(t, m)
		input := testInput(t)
		input.AdminPassword = "1"
		if w := request(t, m, "/setup/api/activate", input, cookie, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if err := m.StartBackups(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFullRestorePreservesStateAndActivationRetryDoesNotPauseDestination(t *testing.T) {
	source := realBackupManager(t, true)
	destination := realBackupManager(t, false)
	record, err := source.backups.capture(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := destination.backups.inspectBundle(context.Background(), filepath.Join(source.backups.recordPath(record.ID), "gateway-backup.bundle"), source.backups.state.Secret, "https://production.example.org")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := destination.backups.loadInspection(inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Dataset == "" || loaded.Payload.Config.AdminUsername == "" || loaded.Settings.Secret == "" {
		t.Fatal("private inspection fields were lost")
	}
	installation := destination.backups.state.Installation
	// Emulate a scheduler/OAuth/kit settings write already in flight while the
	// candidate runtime is being validated. It must persist the imported state,
	// never the old destination's secret/dataset beside the imported database.
	originalBuild := destination.options.Build
	writerDone := make(chan error, 1)
	destination.options.Build = func(config RuntimeConfig, key []byte) (Runtime, error) {
		attempted := make(chan struct{})
		go func() {
			close(attempted)
			destination.backups.mu.Lock()
			defer destination.backups.mu.Unlock()
			destination.backups.state.Exported = true
			writerDone <- destination.backups.saveLocked()
		}()
		<-attempted
		select {
		case err := <-writerDone:
			t.Fatalf("settings writer crossed incomplete cutover: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		return originalBuild(config, key)
	}
	if err := destination.backups.activateRestore(context.Background(), inspection.ID, "", true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("settings writer remained blocked after commit")
	}
	var persisted backupState
	if err := destination.backups.unseal(mustRead(t, filepath.Join(destination.backups.dir, "state.enc")), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Dataset != source.backups.state.Dataset || persisted.Secret != source.backups.state.Secret {
		t.Fatal("old settings were persisted beside restored data")
	}
	cfg, key, err := LoadRuntime(destination.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, source.key) || !cfg.VerifyPassword("1") || cfg.PasswordHash != source.config.PasswordHash {
		t.Fatal("restored data key or Admin verifier changed")
	}
	if destination.backups.state.Installation != installation || destination.backups.state.Installation == source.backups.state.Installation || destination.backups.state.Dataset != source.backups.state.Dataset {
		t.Fatal("installation control identity or dataset identity incorrect")
	}
	if destination.backups.state.Policy.Enabled {
		t.Fatal("restored schedule resumed automatically")
	}
	active := destination.current
	if err := destination.backups.activateRestore(context.Background(), inspection.ID, "", true); err != nil {
		t.Fatal("lost-ack retry failed", err)
	}
	if destination.fenced || destination.current != active {
		t.Fatal("activation retry paused or replaced live runtime")
	}
}

func TestRestoreHealthyTargetPinsVerifiedRollbackAndRemovesAbsentFCM(t *testing.T) {
	source := realBackupManager(t, true)
	destination := realBackupManager(t, true)
	if err := writePrivate(filepath.Join(destination.options.Directory, "integrations", "fcm.json"), []byte("old-target-only")); err != nil {
		t.Fatal(err)
	}
	record, err := source.backups.capture(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := destination.backups.inspectBundle(context.Background(), filepath.Join(source.backups.recordPath(record.ID), "gateway-backup.bundle"), source.backups.state.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	// A persistent source fence cannot be replaced/cleared by a restore request
	// from another tab, even when the incoming backup has already been verified.
	fencePath := filepath.Join(destination.options.Directory, ".backup", "source-fence.json")
	if err := persistJSON(fencePath, restoreFence{Version: 1, Kind: "migration"}); err != nil {
		t.Fatal(err)
	}
	destination.mu.Lock()
	destination.fenced = true
	destination.mu.Unlock()
	if err := destination.backups.activateRestore(context.Background(), inspection.ID, "", true); err == nil {
		t.Fatal("restore replaced an authoritative source migration fence")
	}
	var fence restoreFence
	if err := readBackupJSON(fencePath, &fence, 8<<10); err != nil || fence.Kind != "migration" {
		t.Fatal("source fence changed", err)
	}
	if err := os.Remove(fencePath); err != nil {
		t.Fatal(err)
	}
	destination.mu.Lock()
	destination.fenced = false
	destination.mu.Unlock()
	if err := destination.backups.activateRestore(context.Background(), inspection.ID, "", true); err != nil {
		t.Fatal(err)
	}
	var body string
	if err := destination.backups.db.QueryRow("SELECT record FROM backups WHERE json_extract(record,'$.pinned')=1 LIMIT 1").Scan(&body); err != nil {
		t.Fatal("missing pinned rollback backup", err)
	}
	var rollback backupRecord
	if err := json.Unmarshal([]byte(body), &rollback); err != nil {
		t.Fatal(err)
	}
	if !rollback.LocalAvailable || !exists(filepath.Join(destination.backups.recordPath(rollback.ID), "gateway-backup.bundle")) {
		t.Fatal("rollback backup was not complete")
	}
	if exists(filepath.Join(destination.options.Directory, "integrations", "fcm.json")) {
		t.Fatal("source without FCM inherited destination credentials")
	}
}

func TestRecoveryCSRAndRepairRetainOriginalKeyAndAdminVerifier(t *testing.T) {
	m := activeSettingsManager(t)
	before, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	original, err := readBounded(before.ClientKeyFile, 32<<10)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := identityPrivateKey(original)
	if err != nil {
		t.Fatal(err)
	}
	ca := testCA(t)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: time.Now().Add(-4 * time.Hour), NotAfter: time.Now().Add(-2 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca.cert, signer.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	expired := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.pem...)
	if err := writePrivate(before.ClientCertFile, expired); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.current = nil
	m.state = "recovery"
	m.mu.Unlock()
	// A key left by an earlier unconfigured Setup must never replace identity.
	_, unrelated := signedIdentity(t, testCA(t), nil, x509.ExtKeyUsageClientAuth)
	if err := writePrivate(filepath.Join(m.options.Directory, "staged-client-key.pem"), []byte(unrelated)); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := setupSessionForTest(t, m)
	csrPEM := setupCSRForIdentityTest(t, m, cookie, csrf)
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := x509.MarshalPKIXPublicKey(signer.Public())
	got, _ := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if !bytes.Equal(want, got) {
		t.Fatal("recovery CSR rotated the original public key")
	}
	renewed := signPublicKeyForIdentityTest(t, ca, csr.PublicKey)
	if err := m.RepairRestoredIdentity(context.Background(), []byte(renewed), []byte{}); err != nil {
		t.Fatal(err)
	}
	after, keyAfter, err := LoadRuntime(m.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	afterSigner, err := identityPrivateKey(mustRead(t, after.ClientKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	afterPublic, _ := x509.MarshalPKIXPublicKey(afterSigner.Public())
	if !bytes.Equal(want, afterPublic) || !bytes.Equal(key, keyAfter) || after.PasswordHash != before.PasswordHash || after.PasswordSalt != before.PasswordSalt || after.AdminUsername != before.AdminUsername {
		t.Fatal("repair replaced identity, data key or Admin credentials")
	}
	if m.current == nil || m.fenced {
		t.Fatal("repair did not activate recovered runtime")
	}
}

func TestRestoreFailedBuildRollsBackDataKeyConfigAndEncryptedBackupSettings(t *testing.T) {
	source := realBackupManager(t, true)
	destination := realBackupManager(t, true)
	record, err := source.backups.capture(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := destination.backups.inspectBundle(context.Background(), filepath.Join(source.backups.recordPath(record.ID), "gateway-backup.bundle"), source.backups.state.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(destination.options.Directory, "runtime.json"), filepath.Join(destination.options.Directory, "encryption.key"), filepath.Join(destination.options.Directory, ".backup", "state.enc")}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path] = mustRead(t, path)
	}
	oldKey := append([]byte(nil), destination.key...)
	oldBuild := destination.options.Build
	restoreCtx, cancelRestore := context.WithCancel(context.Background())
	defer cancelRestore()
	destination.options.Build = func(config RuntimeConfig, key []byte) (Runtime, error) {
		if !bytes.Equal(key, oldKey) {
			cancelRestore()
			return Runtime{}, errors.New("candidate build deliberately rejected")
		}
		return oldBuild(config, key)
	}
	if err := destination.backups.activateRestore(restoreCtx, inspection.ID, "", true); err == nil {
		t.Fatal("failed runtime activated")
	}
	for _, path := range paths {
		if !bytes.Equal(before[path], mustRead(t, path)) {
			t.Fatalf("rollback changed generation at %s", path)
		}
	}
	if err := CheckDatabaseKey(destination.options.DatabasePath, oldKey); err != nil {
		t.Fatal("old database was not restored", err)
	}
	if exists(journalPath(destination.options.Directory)) {
		t.Fatal("finished rollback journal retained")
	}
	if destination.current == nil || destination.fenced {
		t.Fatal("safe pre-active rollback did not resume the old runtime")
	}
}

func TestFullRestoreExpiredClientAndHospitalCAAllowRepairWithoutRotatingIdentity(t *testing.T) {
	source := realBackupManager(t, true)
	destination := realBackupManager(t, false)
	private := mustRead(t, source.config.ClientKeyFile)
	signer, err := identityPrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	ca := testCA(t)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(17), NotBefore: time.Now().Add(-45 * time.Minute), NotAfter: time.Now().Add(-15 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca.cert, signer.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(source.config.ClientCertFile, append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.pem...)); err != nil {
		t.Fatal(err)
	}
	root := *ca.cert
	root.NotBefore = time.Now().Add(-2 * time.Hour)
	root.NotAfter = time.Now().Add(-time.Hour)
	rootDER, err := x509.CreateCertificate(rand.Reader, &root, &root, &ca.key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	source.config.HospitalCAFile = filepath.Join(filepath.Dir(source.config.ClientCertFile), "hospital-ca.pem")
	if err := writePrivate(source.config.HospitalCAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})); err != nil {
		t.Fatal(err)
	}
	if err := persistJSON(filepath.Join(source.options.Directory, "runtime.json"), source.config); err != nil {
		t.Fatal(err)
	}
	record, err := source.backups.capture(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := destination.backups.inspectBundle(context.Background(), filepath.Join(source.backups.recordPath(record.ID), "gateway-backup.bundle"), source.backups.state.Secret, "")
	if err != nil {
		t.Fatal("valid data rejected for certificate time validity", err)
	}
	if !inspection.CertificateExpired || !inspection.HospitalCAExpired || !inspection.RepairRequired {
		t.Fatalf("missing repair flags %+v", inspection)
	}
	if err := destination.backups.activateRestore(context.Background(), inspection.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if destination.current != nil || destination.state != "recovery" || destination.fenced {
		t.Fatal("expired certificates started active traffic or prevented local repair")
	}
	if err := CheckDatabaseKey(destination.options.DatabasePath, source.key); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := setupSessionForTest(t, destination)
	csrPEM := setupCSRForIdentityTest(t, destination, cookie, csrf)
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := x509.MarshalPKIXPublicKey(signer.Public())
	got, _ := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if !bytes.Equal(want, got) {
		t.Fatal("expired data restore rotated identity")
	}
	renewed := []byte(signPublicKeyForIdentityTest(t, ca, csr.PublicKey))
	if err := destination.RepairRestoredIdentity(context.Background(), renewed, nil); err == nil {
		t.Fatal("expired Hospital CA activated")
	}
	if err := destination.RepairRestoredIdentity(context.Background(), renewed, ca.pem); err != nil {
		t.Fatal(err)
	}
	config, key, err := LoadRuntime(destination.options.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if !config.VerifyPassword("1") || config.PasswordHash != source.config.PasswordHash || !bytes.Equal(key, source.key) {
		t.Fatal("repair changed original Admin/data identity")
	}
	if destination.current == nil {
		t.Fatal("repaired restore did not activate")
	}
}

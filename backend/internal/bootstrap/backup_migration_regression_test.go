package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/backup"
)

func controlBackupFixture(t *testing.T) *backupService {
	t.Helper()
	m := activeSettingsManager(t)
	if e := m.StartBackups(context.Background()); e != nil {
		t.Fatal(e)
	}
	return m.backups
}
func pairBackupReceiver(t *testing.T, s *backupService) (string, string, map[string]string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.migrationHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")), "receive")
	var response struct {
		Data struct {
			Code string `json:"code"`
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Data.Code == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	in := map[string]string{"code": response.Data.Code, "secret": backupID() + backupID(), "dataset": backupID(), "installation": backupID()}
	body, _ := json.Marshal(in)
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/backup/transfer/pair", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	s.transportHTTP(w, r)
	var paired map[string]string
	json.Unmarshal(w.Body.Bytes(), &paired)
	if w.Code != 200 || paired["token"] == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	return paired["session_id"], paired["token"], in
}
func TestPairingResponseLossCanRecoverUntilManifest(t *testing.T) {
	s := controlBackupFixture(t)
	session, token, input := pairBackupReceiver(t, s)
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, "/backup/transfer/pair", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.transportHTTP(w, r)
	var pair map[string]string
	json.Unmarshal(w.Body.Bytes(), &pair)
	if w.Code != 200 || pair["token"] != token || pair["session_id"] != session {
		t.Fatal("lost response caused permanent pairing lock", w.Code, w.Body.String())
	}
	sum := sha256.Sum256([]byte("x"))
	manifest := transferManifest{SessionID: session, SnapshotID: strings.Repeat("a", 64), Objects: []backup.Object{{Path: "config", Size: 1, SHA256: hex.EncodeToString(sum[:])}}}
	b, _ := json.Marshal(manifest)
	r = httptest.NewRequest(http.MethodPost, "/backup/transfer/manifest", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	s.transportHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodPost, "/backup/transfer/pair", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.transportHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("pair code reusable after transfer capability established")
	}
}
func TestOldTransferInspectionCannotBypassFinalActivation(t *testing.T) {
	s := controlBackupFixture(t)
	oldID := backupID()
	inspection := resultInspection{Result: restoreInspection{ID: oldID}, TransferSessionID: backupID()}
	if e := s.saveSealed(filepath.Join(s.root, "staging", oldID, "inspection.enc"), inspection); e != nil {
		t.Fatal(e)
	}
	for _, replacement := range []*transferState{nil, {Direction: "receiver", SessionID: backupID(), RestoreID: backupID(), State: "final_ready"}} {
		s.mu.Lock()
		s.transfer = replacement
		s.mu.Unlock()
		body, _ := json.Marshal(map[string]any{"confirm_id": oldID, "source_stopped": true})
		r := httptest.NewRequest(http.MethodPost, "/admin/api/backups/restore/"+oldID+"/activate", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.serveHTTP(w, r)
		if w.Code != 409 || s.active != nil {
			t.Fatal("older base bypassed migration gate", w.Code, w.Body.String())
		}
	}
}
func TestOfflineOverrideCannotResumeKnownActiveDestination(t *testing.T) {
	s := controlBackupFixture(t)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "active destination", 409) }))
	defer receiver.Close()
	s.transferClient = receiver.Client()
	s.mu.Lock()
	s.transfer = &transferState{Direction: "sender", SessionID: backupID(), Token: backupID(), DestinationURL: receiver.URL, State: "final_ready", Final: true}
	s.mu.Unlock()
	if e := s.m.freeze(context.Background(), "migration"); e != nil {
		t.Fatal(e)
	}
	raw := `{"destination_inactive_confirmed":true,"offline_override":true,"authoritative_source":"USE SOURCE DATA"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.migrationHTTP(w, r, "cancel")
	if w.Code != 409 || !s.m.fenced {
		t.Fatal("explicitly active destination was bypassed", w.Code, w.Body.String())
	}
}
func TestCancelledWaitingTransferRequestCannotMutate(t *testing.T) {
	s := controlBackupFixture(t)
	_, token, _ := pairBackupReceiver(t, s)
	s.transferMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/backup/transfer/cancel", strings.NewReader("{}")).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct{})
	go func() { s.transportHTTP(httptest.NewRecorder(), r); close(done) }()
	cancel()
	s.transferMu.Unlock()
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transfer.State == "cancelled" {
		t.Fatal("timed-out queued request mutated transfer state")
	}
}
func TestBackupReadyDuringUploadAndKitStaysBound(t *testing.T) {
	erp := integrationERP(t)
	m := newBackupIntegrationManager(t, erp, true)
	seedBackupSession(t, m, erp.URL)
	s := m.backups
	release := make(chan struct{})
	ready := make(chan string, 1)
	job, e := s.startJob("capturing", func(ctx context.Context, j *backupJob) error {
		record, e := s.capture(ctx, "", false)
		if e != nil {
			return e
		}
		j.BackupID = record.ID
		s.publishBackup(record.ID)
		s.phase("uploading")
		ready <- record.ID
		<-release
		os.RemoveAll(filepath.Join(s.recordPath(record.ID), "capture"))
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var id string
	select {
	case id = <-ready:
	case <-time.After(60 * time.Second):
		t.Fatal("capture not ready")
	}
	r := httptest.NewRequest(http.MethodGet, "/admin/api/backups/"+id+"/download", nil)
	w := httptest.NewRecorder()
	s.serveHTTP(w, r)
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal("local download waits for Drive upload", w.Code)
	}
	var body string
	s.db.QueryRow("SELECT body FROM jobs WHERE id=?", job.ID).Scan(&body)
	var running backupJob
	json.Unmarshal([]byte(body), &running)
	if running.State != "running" || running.Phase != "uploading" || running.BackupID != id {
		t.Fatal("local readiness not published", body)
	}
	oldSecret := s.state.Secret
	s.mu.Lock()
	s.state.Secret = backupID() + backupID()
	s.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{"backup_id": id})
	r = httptest.NewRequest(http.MethodPost, "/admin/api/backups/recovery-kit", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.serveHTTP(w, r)
	var kit recoveryKit
	if json.Unmarshal(w.Body.Bytes(), &kit) != nil || kit.Secret != oldSecret {
		t.Fatal("exported wrong recovery key for older bundle", w.Code)
	}
	close(release)
	waitBackupJob(t, s, job.ID)
}
func TestPolicyBusyDoesNotApplyAfterTimeout(t *testing.T) {
	s := controlBackupFixture(t)
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	p := s.state.Policy
	p.Enabled = true
	done := make(chan error, 1)
	go func() { done <- s.applyPolicy(p) }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("busy policy was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("policy queued behind destructive maintenance")
	}
	if s.state.Policy.Enabled {
		t.Fatal("rejected policy changed state")
	}
}
func TestBackupServiceLockProtectsLiveTemporaryCredentials(t *testing.T) {
	s := controlBackupFixture(t)
	path := filepath.Join(s.dir, ".rclone-active-test", "config")
	if e := writePrivate(path, []byte("synthetic protected temporary credentials")); e != nil {
		t.Fatal(e)
	}
	other := &backupService{m: s.m, dir: s.dir, root: s.root, binary: s.binary}
	if e := other.open(context.Background()); e == nil {
		other.close()
		t.Fatal("second backup service shared active control state")
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("second service deleted first service credentials")
	}
}
func TestQuarantinePreservesTerminalNotificationStates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("CREATE TABLE notify_jobs(id INTEGER PRIMARY KEY,status TEXT NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	states := []string{"accepted", "invalid_token", "sent", "completed", "failed", "cancelled", "quarantined", "pending", "retry", "in_progress"}
	for i, state := range states {
		if _, e = db.Exec("INSERT INTO notify_jobs(id,status) VALUES(?,?)", i, state); e != nil {
			t.Fatal(e)
		}
	}
	if e = quarantineRestored(context.Background(), path); e != nil {
		t.Fatal(e)
	}
	for i, state := range states {
		var got string
		if e = db.QueryRow("SELECT status FROM notify_jobs WHERE id=?", i).Scan(&got); e != nil {
			t.Fatal(e)
		}
		want := state
		if i >= 7 {
			want = "quarantined"
		}
		if got != want {
			t.Fatalf("state %s became %s, want %s", state, got, want)
		}
	}
}

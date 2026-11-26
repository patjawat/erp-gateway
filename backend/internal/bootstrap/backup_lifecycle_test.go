package bootstrap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/backupcloud"
)

func lifecycleService(t *testing.T) (*backupService, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	dir := filepath.Join(config, ".backup")
	if err := os.MkdirAll(filepath.Join(root, "backups", "records"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	primaryPath := filepath.Join(root, "gateway.db")
	primary, err := sql.Open("sqlite", primaryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { primary.Close() })
	if _, err := primary.Exec(`CREATE TABLE notify_jobs(id INTEGER PRIMARY KEY,hospital_id TEXT,event_id TEXT,status TEXT,available_at INTEGER,created_at INTEGER);
CREATE TABLE update_jobs(id TEXT PRIMARY KEY,hospital_id TEXT,status TEXT,stage TEXT,message TEXT,updated_at INTEGER,created_at INTEGER);`); err != nil {
		t.Fatal(err)
	}
	catalog, err := sql.Open("sqlite", filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { catalog.Close() })
	catalog.SetMaxOpenConns(1)
	if _, err := catalog.Exec("CREATE TABLE backups(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,created_at TEXT NOT NULL,record TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(bytes.Repeat([]byte{7}, 32))
	seal, _ := cipher.NewGCM(block)
	m := &Manager{options: Options{Directory: config, DatabasePath: primaryPath}}
	s := &backupService{m: m, dir: dir, root: filepath.Join(root, "backups"), db: catalog, seal: seal, downloads: map[string]int{}, state: backupState{Version: 1, Installation: backupID(), Dataset: backupID(), Secret: strings.Repeat("s", 43), Policy: defaultBackupPolicy()}}
	m.backups = s
	return s, primary
}

func lifecycleRequest(method, path string, body any) *http.Request {
	var input bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&input).Encode(body)
	}
	r := httptest.NewRequest(method, "http://localhost"+path, &input)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestRestoredWorkPaginationScopesAndExplicitResumeCancel(t *testing.T) {
	s, db := lifecycleService(t)
	for i := 1; i <= 6; i++ {
		if _, err := db.Exec("INSERT INTO notify_jobs VALUES(?,?,?,'quarantined',0,?)", i, "hospital", "event-"+strconv.Itoa(i), 100); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO notify_jobs VALUES(90,'hospital','sent','sent',0,200); INSERT INTO update_jobs VALUES('a','hospital','blocked','restored_quarantine','',0,100); INSERT INTO update_jobs VALUES('b','hospital','blocked','restored_quarantine','',0,90); INSERT INTO update_jobs VALUES('ordinary','hospital','queued','queued','',0,200)"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		w := httptest.NewRecorder()
		s.restoredWorkHTTP(w, lifecycleRequest("GET", "/work?limit=3&cursor="+url.QueryEscape(cursor), nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out struct {
			Data struct {
				Items []restoredWork `json:"items"`
				Next  string         `json:"next_cursor"`
				More  bool           `json:"has_more"`
				Total int            `json:"total"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Data.Items) > 3 || out.Data.Total != 8 {
			t.Fatalf("unbounded/unscoped page %+v", out)
		}
		for _, item := range out.Data.Items {
			if seen[item.ID] {
				t.Fatal("duplicate ID", item.ID)
			}
			seen[item.ID] = true
			if !strings.HasPrefix(item.ID, item.Kind+":") {
				t.Fatal("ID not globally unique", item)
			}
			if item.CanResume != (item.Kind == "notification") {
				t.Fatal("updater resumable", item)
			}
		}
		pages++
		if !out.Data.More {
			if out.Data.Next != "" {
				t.Fatal("terminal cursor")
			}
			break
		}
		if out.Data.Next == "" || out.Data.Next == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = out.Data.Next
	}
	if len(seen) != 8 || pages != 3 {
		t.Fatalf("missing work rows: %d, pages %d", len(seen), pages)
	}
	change := func(kind, action string, ids []string, confirmed bool) int {
		w := httptest.NewRecorder()
		s.restoredWorkHTTP(w, lifecycleRequest("POST", "/work", map[string]any{"kind": kind, "action": action, "ids": ids, "confirmed": confirmed}))
		return w.Code
	}
	if code := change("notification", "resume", []string{"notification:1", "notification:2"}, true); code != 200 {
		t.Fatal("resume", code)
	}
	if code := change("notification", "cancel", []string{"notification:3"}, true); code != 200 {
		t.Fatal("cancel", code)
	}
	if code := change("updater", "resume", []string{"updater:a"}, true); code != 409 {
		t.Fatal("retired updater revived", code)
	}
	if code := change("updater", "cancel", []string{"updater:a"}, true); code != 200 {
		t.Fatal("updater cancel", code)
	}
	if code := change("notification", "resume", []string{"updater:4"}, true); code != 400 {
		t.Fatal("cross-kind ID accepted", code)
	}
	if code := change("notification", "resume", []string{"notification:4"}, false); code != 400 {
		t.Fatal("unconfirmed replay accepted", code)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM notify_jobs WHERE status='pending' AND available_at>0").Scan(&count); err != nil || count != 2 {
		t.Fatal("notification replay count", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM notify_jobs").Scan(&count); err != nil || count != 7 {
		t.Fatal("notifications deleted", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM update_jobs WHERE id='a' AND status='cancelled'").Scan(&count); err != nil || count != 1 {
		t.Fatal("updater cancel failed", count, err)
	}
	s.m.fenced = true
	if code := change("notification", "resume", []string{"notification:4"}, true); code != 409 {
		t.Fatal("fenced source mutated", code)
	}
	w := httptest.NewRecorder()
	s.restoredWorkHTTP(w, lifecycleRequest("GET", "/work", nil))
	if w.Code != 409 {
		t.Fatal("fenced source listed", w.Code)
	}
	s.m.fenced = false
	s.operation.Lock()
	defer s.operation.Unlock()
	w = httptest.NewRecorder()
	started := time.Now()
	s.restoredWorkHTTP(w, lifecycleRequest("GET", "/work", nil))
	if w.Code != 409 || time.Since(started) > time.Second {
		t.Fatal("busy operation waited")
	}
}

func addLifecycleRecord(t *testing.T, s *backupService, pinned bool) backupRecord {
	t.Helper()
	r := backupRecord{ID: backupID(), Status: "completed", CreatedAt: time.Now().UTC(), Size: 4, Pinned: pinned, LocalAvailable: true}
	if err := os.MkdirAll(s.recordPath(r.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.recordPath(r.ID), "gateway-backup.bundle"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.putRecord(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBackupRecordPinDeleteProtectsLastCopyJobsTransfersAndDownloads(t *testing.T) {
	s, _ := lifecycleService(t)
	first := addLifecycleRecord(t, s, true)
	second := addLifecycleRecord(t, s, false)
	remove := func(id string) int {
		w := httptest.NewRecorder()
		s.deleteRecordHTTP(w, lifecycleRequest("DELETE", "/records/"+id, nil), id)
		return w.Code
	}
	pin := func(id string, value bool) int {
		w := httptest.NewRecorder()
		s.pinRecordHTTP(w, lifecycleRequest("POST", "/records/"+id+"/pin", map[string]bool{"pinned": value}), id)
		return w.Code
	}
	if remove(first.ID) != 409 {
		t.Fatal("pinned backup deleted")
	}
	if pin(first.ID, false) != 200 || remove(first.ID) != 200 {
		t.Fatal("released backup could not delete")
	}
	if exists(filepath.Join(s.recordPath(first.ID), "gateway-backup.bundle")) {
		t.Fatal("local bundle retained after deletion")
	}
	old, err := s.getRecord(first.ID)
	if err != nil || old.LocalAvailable {
		t.Fatal("history lost or availability stale", old, err)
	}
	if remove(second.ID) != 409 {
		t.Fatal("last verified local copy deleted")
	}
	third := addLifecycleRecord(t, s, false)
	s.downloads[third.ID] = 1
	if remove(third.ID) != 409 || pin(third.ID, true) != 409 {
		t.Fatal("active download mutated")
	}
	delete(s.downloads, third.ID)
	s.transfer = &transferState{RecordID: third.ID, State: "final_ready"}
	if remove(third.ID) != 409 || pin(third.ID, false) != 409 {
		t.Fatal("active migration artifact mutated")
	}
	s.transfer.State = "retired"
	s.transfer.Direction = "sender"
	s.m.fenced = true
	if pin(third.ID, false) != 200 {
		t.Fatal("retired migration could not release pin")
	}
	s.m.fenced = false
	s.active = &backupJob{State: "running"}
	if remove(third.ID) != 409 {
		t.Fatal("active capture artifact deleted")
	}
	s.active = nil
	s.policyMu.Lock()
	started := time.Now()
	if remove(third.ID) != 409 || pin(third.ID, true) != 409 || time.Since(started) > time.Second {
		t.Fatal("policy busy did not fail quickly")
	}
	s.policyMu.Unlock()
	if remove(third.ID) != 200 {
		t.Fatal("ordinary redundant local copy not deleted")
	}
	if remove("../escape") != 400 {
		t.Fatal("invalid record ID accepted")
	}
}

func TestImportedDriveTokensUseOnlyFreshMatchingRecoveryAndConsumeAfterActive(t *testing.T) {
	s, _ := lifecycleService(t)
	archived := s.state
	archived.Drive = backupcloud.Credentials{ClientID: "client", ClientSecret: "secret", FolderID: "folder", RedirectURL: "https://gateway.example/callback", AccessToken: "stale-access", RefreshToken: "stale-refresh", Expiry: time.Now().Add(time.Hour)}
	without := s.importedBackupStateLocked(archived, s.state.Installation)
	if without.Drive.AccessToken != "" || without.Drive.RefreshToken != "" || !without.Drive.Expiry.IsZero() {
		t.Fatal("archived OAuth tokens revived")
	}
	fresh := archived.Drive
	fresh.AccessToken = "fresh-access"
	fresh.RefreshToken = "fresh-refresh"
	s.recovery = &cloudRecovery{Secret: archived.Secret, Dataset: archived.Dataset, Credentials: fresh}
	if got := s.importedBackupStateLocked(archived, s.state.Installation); got.Drive.RefreshToken != "fresh-refresh" {
		t.Fatal("fresh matching recovery tokens lost")
	}
	s.recovery.Dataset = backupID()
	if got := s.importedBackupStateLocked(archived, s.state.Installation); got.Drive.RefreshToken != "" {
		t.Fatal("unrelated recovery tokens adopted")
	}
	s.recovery.Dataset = archived.Dataset
	path := filepath.Join(s.dir, "cloud-recovery.enc")
	if err := s.saveSealed(path, s.recovery); err != nil {
		t.Fatal(err)
	}
	journal := cutoverJournal{Version: 1, ID: backupID(), RestoreID: backupID(), Installation: s.state.Installation, Phase: "prepared"}
	if err := s.consumeRestoreRecoveryLocked(&journal); err == nil || s.recovery == nil || !exists(path) {
		t.Fatal("recovery context consumed before active commit")
	}
	journal.Phase = "active"
	if err := persistJSON(journalPath(s.m.options.Directory), journal); err != nil {
		t.Fatal(err)
	}
	if err := s.finishRestoreRecovery(); err != nil {
		t.Fatal(err)
	}
	if s.recovery != nil || exists(path) {
		t.Fatal("recovery context retained after durable activation")
	}
	if err := readBackupJSON(journalPath(s.m.options.Directory), &journal, 1<<20); err != nil || !journal.RecoveryConsumed {
		t.Fatal("recovery consumption not durable", err)
	}
	// A later explicit recovery attempt must survive subsequent startups.
	s.recovery = &cloudRecovery{Secret: archived.Secret, Dataset: backupID(), Credentials: fresh}
	if err := s.saveSealed(path, s.recovery); err != nil {
		t.Fatal(err)
	}
	if err := s.finishRestoreRecovery(); err != nil || s.recovery == nil || !exists(path) {
		t.Fatal("later recovery context erased by old journal", err)
	}
}

func TestManualDriveDeleteProtectsVerifiedCopyAndPreservesUnknownSnapshots(t *testing.T) {
	s, _ := lifecycleService(t)
	s.state.Drive = backupcloud.Credentials{ClientID: "fixture-client", ClientSecret: "fixture-secret", FolderID: "fixture-folder", AccessToken: "fixture-access", RefreshToken: "fixture-refresh", Expiry: time.Now().Add(time.Hour)}
	first, second, unknown := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	var snapshots []map[string]any
	for _, id := range []string{first, second, unknown} {
		snapshots = append(snapshots, map[string]any{"id": id, "time": time.Now().UTC(), "paths": []string{"/temporary/capture"}})
	}
	body, _ := json.Marshal(snapshots)
	logPath := filepath.Join(t.TempDir(), "operations")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\noperation=''\nfor arg in \"$@\"; do\n case \"$arg\" in snapshots|forget|prune) operation=\"$arg\"; break;; esac\ndone\ncase \"$operation\" in\n snapshots) cat <<'JSON'\n" + string(body) + "\nJSON\n ;;\n forget|prune) printf '%s\\n' \"$@\" >> " + quote(logPath) + ";;\n *) exit 99;;\nesac\n"
	s.binary = filepath.Join(t.TempDir(), "restic-fixture")
	if err := os.WriteFile(s.binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	record := addLifecycleRecord(t, s, false)
	now := time.Now().UTC()
	record.RemoteSnapshotID = first
	record.RemoteScope = cloudScope(s.state.Drive, s.state.Dataset)
	record.OffsiteAt = &now
	if err := s.putRecord(record); err != nil {
		t.Fatal(err)
	}
	remove := func() *httptest.ResponseRecorder {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		w := httptest.NewRecorder()
		s.deleteRecordHTTP(w, lifecycleRequest("DELETE", "/records/"+record.ID+"?destination=drive", nil).WithContext(ctx), record.ID)
		return w
	}
	if w := remove(); w.Code != 409 {
		t.Fatal("unknown native snapshots counted as verified good copies", w.Code, w.Body.String())
	}
	if exists(logPath) {
		t.Fatal("last verified remote copy mutated")
	}
	alternative := backupRecord{ID: backupID(), Status: "completed", CreatedAt: now, RemoteSnapshotID: second, RemoteScope: record.RemoteScope, OffsiteAt: &now}
	if err := s.putRecord(alternative); err != nil {
		t.Fatal(err)
	}
	if w := remove(); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	log := string(mustRead(t, logPath))
	// Repository.Forget accepts keep IDs and translates them into the exact
	// native IDs to delete; only the selected first snapshot may reach CLI args.
	if !strings.Contains(log, first) || strings.Contains(log, second) || strings.Contains(log, unknown) || !strings.Contains(log, "prune") {
		t.Fatal("manual deletion removed unknown snapshot or omitted pack cleanup")
	}
	updated, err := s.getRecord(record.ID)
	if err != nil || updated.RemoteSnapshotID != "" || updated.OffsiteAt == nil || !updated.LocalAvailable {
		t.Fatal("deletion lost history or changed local copy", updated, err)
	}
}

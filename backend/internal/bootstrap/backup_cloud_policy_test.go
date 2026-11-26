package bootstrap

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/backupcloud"
)

func cloudPolicyFixture(t *testing.T) (*backupService, backupRecord) {
	t.Helper()
	directory := t.TempDir()
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	seal, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE backups(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,created_at TEXT NOT NULL,record TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &backupService{dir: directory, root: filepath.Join(directory, "data"), db: db, seal: seal, state: backupState{Version: 1, Installation: backupID(), Dataset: backupID(), Secret: strings.Repeat("s", 43), Policy: defaultBackupPolicy()}}
	r := backupRecord{ID: backupID(), CreatedAt: time.Now().UTC(), Status: "completed", LocalAvailable: true}
	if err = os.MkdirAll(filepath.Join(s.recordPath(r.ID), "capture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.recordPath(r.ID), "capture", "identity.key"), []byte("fixture-private-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.putRecord(r); err != nil {
		t.Fatal(err)
	}
	return s, r
}

func cloudPolicyFailureTool(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "failed-restic")
	body := "#!/bin/sh\ncat > \"$RCLONE_CONFIG\" <<'TOKEN'\n[gateway]\ntoken = {\"access_token\":\"rotated-access\",\"refresh_token\":\"rotated-refresh\",\"expiry\":\"2099-01-01T00:00:00Z\",\"token_type\":\"Bearer\"}\nTOKEN\nexit 17\n"
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func cloudPolicyCredentials() backupcloud.Credentials {
	return backupcloud.Credentials{ClientID: "fixture-owner-client", ClientSecret: "fixture-client-secret", RedirectURL: "https://gateway.example/backup/oauth/callback", FolderID: "fixture-folder", AccessToken: "fixture-access", RefreshToken: "fixture-refresh", Expiry: time.Now().Add(time.Hour)}
}

func TestProtectRemovesPlaintextWhenRetentionFails(t *testing.T) {
	s, r := cloudPolicyFixture(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.protect(context.Background(), r); err == nil {
		t.Fatal("expected closed catalog failure")
	}
	if _, err := os.Stat(filepath.Join(s.recordPath(r.ID), "capture")); !os.IsNotExist(err) {
		t.Fatal("plaintext capture remains after retention failure")
	}
}

func TestProtectPersistsRotatedCredentialsAfterUploadFailure(t *testing.T) {
	s, r := cloudPolicyFixture(t)
	s.state.Policy.Destination = "drive"
	s.state.Drive = cloudPolicyCredentials()
	s.binary = cloudPolicyFailureTool(t, s.dir)
	if err := s.protect(context.Background(), r); err == nil {
		t.Fatal("failed upload reported success")
	}
	assertCloudPolicyRotation(t, s)
	got, err := s.getRecord(r.ID)
	if err != nil || got.OffsiteAt != nil || got.Error == "" {
		t.Fatalf("failed upload advanced offsite success: %v", err)
	}
	if _, err = os.Stat(filepath.Join(s.recordPath(r.ID), "capture")); !os.IsNotExist(err) {
		t.Fatal("plaintext capture remains after upload failure")
	}
}

func TestRetainDrivePersistsRotatedCredentialsAfterForgetFailure(t *testing.T) {
	s, r := cloudPolicyFixture(t)
	s.state.Drive = cloudPolicyCredentials()
	s.binary = filepath.Join(s.dir, "retention-restic")
	body := "#!/bin/sh\ncat > \"$RCLONE_CONFIG\" <<'TOKEN'\n[gateway]\ntoken = {\"access_token\":\"rotated-access\",\"refresh_token\":\"rotated-refresh\",\"expiry\":\"2099-01-01T00:00:00Z\",\"token_type\":\"Bearer\"}\nTOKEN\nfor operation do\ncase \"$operation\" in\nsnapshots) printf '%s\\n' '[{\"id\":\"" + strings.Repeat("a", 64) + "\",\"time\":\"2026-11-26T03:00:00Z\"},{\"id\":\"" + strings.Repeat("b", 64) + "\",\"time\":\"2026-11-25T03:00:00Z\"}]'; exit 0;;\nforget) exit 17;;\nesac\ndone\nexit 23\n"
	if err := os.WriteFile(s.binary, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	r.CreatedAt = r.CreatedAt.Add(-time.Hour)
	r.OffsiteAt = &r.CreatedAt
	r.RemoteSnapshotID = strings.Repeat("b", 64)
	r.RemoteScope = cloudScope(s.state.Drive, s.state.Dataset)
	if err := s.putRecord(r); err != nil {
		t.Fatal(err)
	}
	newest := backupRecord{ID: backupID(), CreatedAt: time.Now(), RemoteSnapshotID: strings.Repeat("a", 64), RemoteScope: r.RemoteScope}
	newest.OffsiteAt = &newest.CreatedAt
	if err := s.putRecord(newest); err != nil {
		t.Fatal(err)
	}
	if err := s.retainDrive(context.Background(), backupRetention{Latest: 1}); err == nil || !strings.Contains(err.Error(), "forget failed") {
		t.Fatalf("retention should reach and fail forget: %v", err)
	}
	assertCloudPolicyRotation(t, s)
}

func assertCloudPolicyRotation(t *testing.T, s *backupService) {
	t.Helper()
	if s.state.Drive.AccessToken != "rotated-access" || s.state.Drive.RefreshToken != "rotated-refresh" {
		t.Fatal("rotated tokens lost from live state")
	}
	body, err := os.ReadFile(filepath.Join(s.dir, "state.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "rotated-access") || strings.Contains(string(body), "rotated-refresh") {
		t.Fatal("tokens persisted in plaintext")
	}
	var persisted backupState
	if err = s.unseal(body, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Drive.AccessToken != "rotated-access" || persisted.Drive.RefreshToken != "rotated-refresh" {
		t.Fatal("rotated tokens lost from persisted state")
	}
	paths, err := filepath.Glob(filepath.Join(s.dir, ".rclone-*"))
	if err != nil || len(paths) != 0 {
		t.Fatal("plaintext rclone credentials remain after operation")
	}
}

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/backup"
)

func TestRemoteIntegrityRotatesAndRehearsesSelectedSnapshot(t *testing.T) {
	erp := integrationERP(t)
	m := newBackupIntegrationManager(t, erp, true)
	seedBackupSession(t, m, erp.URL)
	s := m.backups
	r, e := s.capture(context.Background(), "", false)
	if e != nil {
		t.Fatal(e)
	}
	repo, clean, e := s.repository(filepath.Join(s.recordPath(r.ID), "repository"), s.state.Secret)
	if e != nil {
		t.Fatal(e)
	}
	defer clean()
	dataset := s.state.Dataset
	scope := cloudScope(s.state.Drive, dataset)
	if e = s.verifyRemoteBackup(context.Background(), repo, r.SnapshotID, dataset, scope); e != nil {
		t.Fatal(e)
	}
	if s.state.LastIntegrityAt.IsZero() || s.state.LastRehearsalAt.IsZero() || s.state.IntegrityPart != 2 || s.state.IntegrityNextRun.Before(time.Now()) || s.state.RehearsalNextRun.Before(time.Now()) {
		t.Fatal("verification schedule not persisted")
	}
	last := s.state.LastRehearsalAt
	if e = s.verifyRemoteBackup(context.Background(), repo, r.SnapshotID, dataset, scope); e != nil || s.state.LastRehearsalAt != last || s.state.IntegrityPart != 2 {
		t.Fatal("full data reread on every upload", e)
	}
	objects, e := backup.Inventory(context.Background(), repo.Path)
	if e != nil {
		t.Fatal(e)
	}
	corrupted := false
	for _, object := range objects {
		if strings.HasPrefix(object.Path, "data/") && object.Size > 32 {
			path := filepath.Join(repo.Path, filepath.FromSlash(object.Path))
			if e = os.Chmod(path, 0600); e != nil {
				t.Fatal(e)
			}
			f, e := os.OpenFile(path, os.O_RDWR, 0600)
			if e != nil {
				t.Fatal(e)
			}
			b := []byte{0}
			f.ReadAt(b, 16)
			b[0] ^= 0xff
			f.WriteAt(b, 16)
			f.Close()
			corrupted = true
			break
		}
	}
	if !corrupted {
		t.Fatal("fixture has no encrypted data pack")
	}
	s.mu.Lock()
	s.state.RehearsalNextRun = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	if e = s.verifyRemoteBackup(context.Background(), repo, r.SnapshotID, dataset, scope); e == nil || s.state.LastRehearsalAt != last || s.state.IntegrityError == "" {
		t.Fatalf("corrupt verification error=%v; last_success_unchanged=%t; failure_visible=%t", e, s.state.LastRehearsalAt == last, s.state.IntegrityError != "")
	}
	entries, _ := filepath.Glob(filepath.Join(s.root, "staging", ".rehearsal-*"))
	if len(entries) != 0 {
		t.Fatal("plaintext rehearsal left behind")
	}
}

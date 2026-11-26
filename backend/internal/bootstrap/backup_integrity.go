package bootstrap

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"time"

	"erp-platform/gateway/internal/backup"
)

// Fast metadata validation on every upload; rotating old-data reads and an
// actual newest-snapshot restore run when due on the next successful backup.
func (s *backupService) verifyRemoteBackup(ctx context.Context, repo backup.Repository, snapshot, dataset, scope string) (result error) {
	s.mu.Lock()
	same := s.state.IntegrityScope == scope
	part := s.state.IntegrityPart
	subsetDue := !same || !s.state.IntegrityNextRun.After(time.Now())
	rehearsalDue := !same || !s.state.RehearsalNextRun.After(time.Now())
	s.mu.Unlock()
	if part < 1 || part > 30 {
		part = 1
	}
	if !same {
		part = 1
	}
	now := time.Now().UTC()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.state.Dataset != dataset || cloudScope(s.state.Drive, dataset) != scope {
			return
		}
		if result != nil {
			s.state.IntegrityError = "ตรวจข้อมูลสำรองนอกเครื่องไม่สำเร็จ กรุณาตรวจการเชื่อมต่อและชุดสำรองก่อนลบสำเนาเดิม"
			s.saveLocked()
			return
		}
		s.state.IntegrityError = ""
		s.state.IntegrityScope = scope
		if subsetDue {
			s.state.IntegrityPart = part%30 + 1
			s.state.IntegrityNextRun = now.Add(24 * time.Hour)
			s.state.LastIntegrityAt = now
		}
		if rehearsalDue {
			s.state.RehearsalNextRun = now.Add(7 * 24 * time.Hour)
			s.state.LastRehearsalAt = now
		}
		result = s.saveLocked()
	}()
	if e := repo.CheckMetadata(ctx); e != nil {
		return e
	}
	if subsetDue {
		s.phase("verifying")
		if e := repo.CheckSubset(ctx, part, 30); e != nil {
			return e
		}
	}
	if !rehearsalDue {
		return nil
	}
	s.phase("verifying")
	directory := filepath.Join(s.root, "staging", ".rehearsal-"+backupID())
	defer os.RemoveAll(directory)
	cache := filepath.Join(s.root, "staging", ".rehearsal-cache-"+backupID())
	if e := os.MkdirAll(cache, 0700); e != nil {
		return e
	}
	defer os.RemoveAll(cache)
	// Rehearsal must fetch remote metadata too, not succeed using a healthy cache
	// after the off-machine repository itself has become corrupt.
	repo.Env = append(repo.Env, "RESTIC_CACHE_DIR="+cache)
	if e := repo.Restore(ctx, snapshot, directory); e != nil {
		return e
	}
	var manifest captureManifest
	if e := readBackupJSON(filepath.Join(directory, "manifest.json"), &manifest, 1<<20); e != nil {
		return e
	}
	if manifest.Version != 1 || manifest.Schema != "gateway-full-v1" || manifest.Dataset != dataset {
		return errors.New("remote restore rehearsal dataset mismatch")
	}
	if e := verifyCaptureFiles(directory, manifest); e != nil {
		return e
	}
	var payload RecoveryPayload
	if e := readBackupJSON(filepath.Join(directory, "config.json"), &payload, 4<<20); e != nil {
		return e
	}
	key, e := base64.StdEncoding.DecodeString(payload.EncryptionKey)
	if e != nil || len(key) != 32 {
		return errors.New("remote restore rehearsal key invalid")
	}
	if e = backup.VerifySQLite(ctx, filepath.Join(directory, "gateway.db")); e != nil {
		return e
	}
	return CheckDatabaseKey(filepath.Join(directory, "gateway.db"), key)
}

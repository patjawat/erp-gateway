package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"erp-platform/gateway/internal/backup"
)

type captureManifest struct {
	Version            int               `json:"version"`
	Schema             string            `json:"schema"`
	CaptureID          string            `json:"capture_id"`
	Dataset            string            `json:"dataset"`
	SourceInstallation string            `json:"source_installation"`
	Generation         uint64            `json:"generation"`
	MigrationID        string            `json:"migration_id,omitempty"`
	BaseSnapshot       string            `json:"base_snapshot,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	Files              map[string]string `json:"files"`
}

func (m *Manager) captureConfig() (RecoveryPayload, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, key, e := LoadRuntime(m.options.Directory)
	if e != nil {
		return RecoveryPayload{}, 0, e
	}
	current, _ := json.Marshal(m.config)
	stored, _ := json.Marshal(cfg)
	if m.current == nil || !bytes.Equal(current, stored) || !bytes.Equal(key, m.key) {
		return RecoveryPayload{}, 0, errors.New("การตั้งค่าปัจจุบันเปลี่ยน กรุณาตรวจสอบก่อนสำรอง")
	}
	cert, e := readBounded(cfg.ClientCertFile, 256<<10)
	if e != nil {
		return RecoveryPayload{}, 0, e
	}
	private, e := readBounded(cfg.ClientKeyFile, 32<<10)
	if e != nil {
		return RecoveryPayload{}, 0, e
	}
	var roots []byte
	if cfg.HospitalCAFile != "" {
		roots, e = readBounded(cfg.HospitalCAFile, 256<<10)
		if e != nil {
			return RecoveryPayload{}, 0, e
		}
	}
	// Paths are normalized on import, never interpreted as destination paths.
	cfg.ClientCertFile = "client-chain.pem"
	cfg.ClientKeyFile = "client-key.pem"
	if cfg.HospitalCAFile != "" {
		cfg.HospitalCAFile = "hospital-ca.pem"
	}
	return RecoveryPayload{Version: 1, Config: cfg, EncryptionKey: base64.StdEncoding.EncodeToString(key), ClientCertificatePEM: string(cert), ClientPrivateKeyPEM: string(private), HospitalCAPEM: string(roots)}, m.generation, nil
}
func fileDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *backupService) capture(ctx context.Context, reuse string, pinned bool) (backupRecord, error) {
	var r backupRecord
	id := reuse
	if id == "" {
		id = backupID()
	}
	if !validBackupID(id) {
		return r, errors.New("invalid backup identity")
	}
	path := s.recordPath(id)
	if e := os.MkdirAll(path, 0700); e != nil {
		return r, e
	}
	capturePath := filepath.Join(path, "capture")
	succeeded := false
	defer func() {
		if !succeeded {
			os.RemoveAll(capturePath)
			if reuse == "" {
				os.RemoveAll(path)
			}
		}
	}()
	for attempt := 0; attempt < 3; attempt++ {
		if e := ctx.Err(); e != nil {
			return r, e
		}
		os.RemoveAll(capturePath)
		if e := os.MkdirAll(capturePath, 0700); e != nil {
			return r, e
		}
		payload, generation, e := s.m.captureConfig()
		if e != nil {
			return r, e
		}
		cfgBody, e := json.Marshal(payload)
		if e != nil {
			return r, e
		}
		s.mu.Lock()
		saved := s.state
		revision := saved.Policy.Revision
		s.mu.Unlock()
		saved.Installation = ""
		saved.Exported = false
		saved.NextRun = time.Time{}
		if e = writePrivate(filepath.Join(capturePath, "config.json"), cfgBody); e != nil {
			return r, e
		}
		stateBody, e := json.Marshal(saved)
		if e != nil {
			return r, e
		}
		if e = writePrivate(filepath.Join(capturePath, "backup-settings.json"), stateBody); e != nil {
			return r, e
		}
		names := []string{"config.json", "backup-settings.json", "gateway.db"}
		source := s.m.options.IntegrationFiles["fcm.json"]
		s.m.mu.Lock()
		fcmDisabled := s.m.config.ManagedFCM != nil && !*s.m.config.ManagedFCM
		s.m.mu.Unlock()
		if restored := filepath.Join(s.m.options.Directory, "integrations", "fcm.json"); exists(restored) {
			source = restored
		}
		if fcmDisabled {
			source = ""
		}
		if source != "" {
			b, e := readBounded(source, 1<<20)
			if e != nil {
				return r, errors.New("cannot capture configured FCM credentials")
			}
			if e = writePrivate(filepath.Join(capturePath, "fcm.json"), b); e != nil {
				return r, e
			}
			names = append(names, "fcm.json")
		}
		s.phase("capturing")
		if e = backup.SnapshotSQLite(ctx, s.m.options.DatabasePath, filepath.Join(capturePath, "gateway.db")); e != nil {
			return r, e
		}
		again, g2, e := s.m.captureConfig()
		if e != nil {
			return r, e
		}
		againBody, _ := json.Marshal(again)
		s.mu.Lock()
		unchanged := s.state.Policy.Revision == revision && s.state.Secret == saved.Secret && s.state.Drive == saved.Drive
		s.mu.Unlock()
		if generation != g2 || !bytes.Equal(cfgBody, againBody) || !unchanged {
			continue
		}
		key, e := base64.StdEncoding.DecodeString(payload.EncryptionKey)
		if e != nil {
			return r, e
		}
		if e = CheckDatabaseKey(filepath.Join(capturePath, "gateway.db"), key); e != nil {
			return r, e
		}
		manifest := captureManifest{Version: 1, Schema: "gateway-full-v1", CaptureID: backupID(), Dataset: saved.Dataset, SourceInstallation: s.stateInstallation(), Generation: generation, CreatedAt: time.Now().UTC(), Files: map[string]string{}}
		s.mu.Lock()
		if pinned && s.transfer != nil && s.transfer.Direction == "sender" {
			manifest.MigrationID = s.transfer.SessionID
			manifest.BaseSnapshot = s.transfer.BaseSnapshot
		}
		s.mu.Unlock()
		for _, name := range names {
			digest, e := fileDigest(filepath.Join(capturePath, name))
			if e != nil {
				return r, e
			}
			manifest.Files[name] = digest
		}
		b, _ := json.Marshal(manifest)
		if e = writePrivate(filepath.Join(capturePath, "manifest.json"), b); e != nil {
			return r, e
		}
		s.phase("encrypting")
		repo, clean, e := s.repository(filepath.Join(path, "repository"), saved.Secret)
		if e != nil {
			return r, e
		}
		if exists(filepath.Join(repo.Path, "config")) {
			// Dedicated local migration repository: service.lock + operation lock
			// exclude live peers before clearing a killed old container's native locks.
			e = repo.RecoverLocalLocks(ctx)
			if e == nil {
				e = repo.Check(ctx)
			}
		} else {
			e = repo.Init(ctx)
		}
		var snap string
		if e == nil {
			snap, e = repo.Backup(ctx, capturePath)
		}
		if e == nil {
			e = repo.Check(ctx)
		}
		clean()
		if e != nil {
			return r, e
		}
		s.phase("packaging")
		bundle := filepath.Join(path, "gateway-backup.bundle")
		temp := bundle + ".partial"
		if e = backup.Pack(ctx, repo.Path, temp); e != nil {
			return r, e
		}
		if e = os.Rename(temp, bundle); e != nil {
			return r, e
		}
		if e = syncBackupDir(path); e != nil {
			return r, e
		}
		stat, e := os.Stat(bundle)
		if e != nil {
			return r, e
		}
		if stat.Size() > maxFullBackupBytes {
			return r, errors.New("ชุดสำรองเกินขอบเขต 16 GiB ของรุ่นนี้ กรุณาจัดการขนาดข้อมูลก่อนสำรอง")
		}
		r = backupRecord{ID: id, CreatedAt: manifest.CreatedAt, Status: "completed", Size: stat.Size(), SnapshotID: snap, Pinned: pinned, LocalAvailable: true}
		r.RecoveryKeyID = recoveryKeyID(saved.Secret)
		if e = s.saveSealed(filepath.Join(path, "recovery-kit.enc"), kitForState(saved)); e != nil {
			return r, e
		}
		if e = s.putRecord(r); e != nil {
			return r, e
		}
		succeeded = true
		return r, nil
	}
	return r, errors.New("การตั้งค่าเปลี่ยนระหว่างสำรอง กรุณาลองใหม่")
}
func (s *backupService) stateInstallation() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Installation
}

package bootstrap

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"erp-platform/gateway/internal/backup"
	"erp-platform/gateway/internal/backupcloud"
)

// Backup control state belongs to this installation, never to the restored DB.
type backupService struct {
	unlock            func()
	downloads         map[string]int
	oauthBase         backupcloud.Credentials
	oauthDataset      string
	transferMu        sync.Mutex
	m                 *Manager
	mu                sync.Mutex
	operation         sync.Mutex
	policyMu          sync.Mutex
	dir, root, binary string
	db                *sql.DB
	state             backupState
	seal              cipher.AEAD
	active            *backupJob
	jobCancel         context.CancelFunc
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	oauth             *backupcloud.Attempt
	oauthCredentials  backupcloud.Credentials
	oauthRecovery     *cloudRecovery
	recovery          *cloudRecovery
	transferClient    *http.Client
	transfer          *transferState
}
type backupState struct {
	IntegrityScope   string                  `json:"integrity_scope,omitempty"`
	IntegrityPart    int                     `json:"integrity_part,omitempty"`
	IntegrityNextRun time.Time               `json:"integrity_next_run,omitempty"`
	RehearsalNextRun time.Time               `json:"rehearsal_next_run,omitempty"`
	LastIntegrityAt  time.Time               `json:"last_integrity_at,omitempty"`
	LastRehearsalAt  time.Time               `json:"last_rehearsal_at,omitempty"`
	IntegrityError   string                  `json:"integrity_error,omitempty"`
	Version          int                     `json:"version"`
	Installation     string                  `json:"installation"`
	Dataset          string                  `json:"dataset"`
	Secret           string                  `json:"secret"`
	Exported         bool                    `json:"exported"`
	Policy           BackupPolicy            `json:"policy"`
	Drive            backupcloud.Credentials `json:"drive"`
	NextRun          time.Time               `json:"next_run"`
}
type backupRecord struct {
	RecoveryKeyID    string     `json:"recovery_key_id"`
	ID               string     `json:"id"`
	CreatedAt        time.Time  `json:"created_at"`
	Status           string     `json:"status"`
	Size             int64      `json:"size"`
	SnapshotID       string     `json:"snapshot_id"`
	OffsiteAt        *time.Time `json:"offsite_at,omitempty"`
	Error            string     `json:"error,omitempty"`
	Pinned           bool       `json:"pinned"`
	LocalAvailable   bool       `json:"local_available"`
	RemoteSnapshotID string     `json:"remote_snapshot_id,omitempty"`
	RemoteScope      string     `json:"remote_scope,omitempty"`
}
type backupJob struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Phase     string    `json:"phase"`
	Error     string    `json:"error,omitempty"`
	BackupID  string    `json:"backup_id,omitempty"`
	RestoreID string    `json:"restore_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func backupID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func validBackupID(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 16 }

func (m *Manager) StartBackups(ctx context.Context) error {
	m.mu.Lock()
	if m.backups != nil {
		m.mu.Unlock()
		return nil
	}
	s := &backupService{m: m, dir: filepath.Join(m.options.Directory, ".backup"), root: filepath.Join(filepath.Dir(m.options.DatabasePath), "backups"), binary: m.options.BackupBinary}
	if s.binary == "" {
		s.binary = "/usr/local/bin/restic"
		if p, e := exec.LookPath("restic"); e == nil {
			s.binary = p
		}
	}
	m.backups = s
	m.mu.Unlock()
	if err := s.open(ctx); err != nil {
		m.mu.Lock()
		m.backups = nil
		m.mu.Unlock()
		return err
	}
	return nil
}
func (s *backupService) open(ctx context.Context) (result error) {
	defer func() {
		if result != nil {
			if s.cancel != nil {
				s.cancel()
			}
			if s.db != nil {
				s.db.Close()
			}
			if s.unlock != nil {
				s.unlock()
				s.unlock = nil
			}
		}
	}()
	for _, d := range []string{s.dir, s.root, filepath.Join(s.root, "records"), filepath.Join(s.root, "staging")} {
		if e := os.MkdirAll(d, 0700); e != nil {
			return e
		}
	}
	var lockErr error
	s.unlock, lockErr = lockDirectory(s.dir)
	if lockErr != nil {
		return errors.New("มีระบบสำรองข้อมูลใช้ config volume นี้อยู่แล้ว")
	}
	for _, pattern := range []string{filepath.Join(s.dir, ".rclone-*"), filepath.Join(s.dir, ".restic-password-*"), filepath.Join(s.root, "staging", ".upload-*"), filepath.Join(s.root, "staging", ".rehearsal-*")} {
		paths, _ := filepath.Glob(pattern)
		for _, p := range paths {
			os.RemoveAll(p)
		}
	}
	keyPath := filepath.Join(s.dir, "control.key")
	key, e := os.ReadFile(keyPath)
	if errors.Is(e, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		if e = writePrivate(keyPath, key); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if len(key) != 32 {
		return errors.New("invalid backup control key")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return e
	}
	s.seal, e = cipher.NewGCM(block)
	if e != nil {
		return e
	}
	statePath := filepath.Join(s.dir, "state.enc")
	body, e := os.ReadFile(statePath)
	if e == nil {
		if e = s.unseal(body, &s.state); e != nil {
			return errors.New("cannot unlock backup settings")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, e = rand.Read(secret); e != nil {
			return e
		}
		s.state = backupState{Version: 1, Installation: backupID(), Dataset: backupID(), Secret: base64.RawURLEncoding.EncodeToString(secret), Policy: defaultBackupPolicy()}
		if e = s.saveLocked(); e != nil {
			return e
		}
	} else {
		return e
	}
	if s.state.Version != 1 || !validBackupID(s.state.Installation) || !validBackupID(s.state.Dataset) || len(s.state.Secret) < 32 {
		return errors.New("unsupported backup control state")
	}
	if e = s.state.Policy.validate(); e != nil {
		return e
	}
	s.db, e = sql.Open("sqlite", filepath.Join(s.dir, "catalog.db"))
	if e != nil {
		return e
	}
	s.db.SetMaxOpenConns(1)
	if _, e = s.db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS backups(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,created_at TEXT NOT NULL,record TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,body TEXT NOT NULL);
 UPDATE jobs SET body=json_set(body,'$.state','failed','$.phase','interrupted','$.error','งานขาดช่วง กรุณาตรวจสอบแล้วเริ่มใหม่') WHERE json_extract(body,'$.state')='running';`); e != nil {
		s.db.Close()
		return e
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	// A killed capture must not leave plaintext database/identity/Google tokens.
	entries, _ := os.ReadDir(filepath.Join(s.root, "records"))
	for _, entry := range entries {
		if entry.IsDir() && validBackupID(entry.Name()) {
			if e := os.RemoveAll(filepath.Join(s.recordPath(entry.Name()), "capture")); e != nil {
				return e
			}
			os.RemoveAll(filepath.Join(s.recordPath(entry.Name()), "handoff"))
		}
	}
	if e = s.loadTransfer(); e != nil {
		s.db.Close()
		s.cancel()
		return e
	}
	if b, e := os.ReadFile(filepath.Join(s.dir, "cloud-recovery.enc")); e == nil {
		var recovered cloudRecovery
		if e = s.unseal(b, &recovered); e != nil {
			return e
		}
		s.recovery = &recovered
	}
	if e := s.finishRestoreRecovery(); e != nil {
		return e
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.scheduler() }()
	return nil
}
func (s *backupService) close() {
	s.cancel()
	s.wg.Wait()
	s.db.Close()
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}
func (s *backupService) unseal(body []byte, v any) error {
	n := s.seal.NonceSize()
	if len(body) < n {
		return errors.New("invalid sealed backup state")
	}
	plain, e := s.seal.Open(nil, body[:n], body[n:], []byte("gateway-backup-control-v1"))
	if e != nil {
		return e
	}
	return json.Unmarshal(plain, v)
}
func (s *backupService) saveLocked() error {
	return s.saveSealed(filepath.Join(s.dir, "state.enc"), s.state)
}
func (s *backupService) saveSealed(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	n := make([]byte, s.seal.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return e
	}
	return writePrivate(path, s.seal.Seal(n, n, b, []byte("gateway-backup-control-v1")))
}
func writePrivate(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".write-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, path)
	}
	if e == nil {
		e = syncBackupDir(filepath.Dir(path))
	}
	return e
}
func syncBackupDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (s *backupService) recordPath(id string) string { return filepath.Join(s.root, "records", id) }
func (s *backupService) repository(path, secret string) (backup.Repository, func(), error) {
	scratch := filepath.Join(s.root, "work")
	cache := filepath.Join(s.root, "cache")
	for _, d := range []string{scratch, cache} {
		if e := os.MkdirAll(d, 0700); e != nil {
			return backup.Repository{}, nil, e
		}
	}
	f, e := os.CreateTemp(s.dir, ".restic-password-*")
	if e != nil {
		return backup.Repository{}, nil, e
	}
	name := f.Name()
	if e = f.Chmod(0600); e == nil {
		_, e = io.WriteString(f, secret)
	}
	f.Close()
	if e != nil {
		os.Remove(name)
		return backup.Repository{}, nil, e
	}
	return backup.Repository{Binary: s.binary, Path: path, PasswordFile: name, Env: []string{"TMPDIR=" + scratch, "RESTIC_CACHE_DIR=" + cache}}, func() { os.Remove(name) }, nil
}
func (s *backupService) putRecord(r backupRecord) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.db.Exec("INSERT INTO backups(id,created_at,record) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record", r.ID, r.CreatedAt.UTC().Format(time.RFC3339Nano), string(b))
	return e
}
func (s *backupService) getRecord(id string) (backupRecord, error) {
	var r backupRecord
	var b string
	e := s.db.QueryRow("SELECT record FROM backups WHERE id=?", id).Scan(&b)
	if e == nil {
		e = json.Unmarshal([]byte(b), &r)
	}
	return r, e
}
func (s *backupService) putJob(j backupJob) error {
	b, e := json.Marshal(j)
	if e == nil {
		_, e = s.db.Exec("INSERT INTO jobs(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", j.ID, string(b))
	}
	return e
}
func (s *backupService) phase(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active.Phase != "cancelling" {
		s.active.Phase = phase
		s.putJob(*s.active)
	}
}
func (s *backupService) startJob(phase string, fn func(context.Context, *backupJob) error) (backupJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return *s.active, errors.New("มีงานสำรองหรือย้ายข้อมูลกำลังทำงานอยู่")
	}
	if s.ctx == nil || fn == nil {
		return backupJob{}, errors.New("ระบบสำรองข้อมูลยังไม่พร้อมเริ่มงาน")
	}
	if err := s.ctx.Err(); err != nil {
		return backupJob{}, err
	}
	jobContext, cancelJob := context.WithCancel(s.ctx)
	j := backupJob{ID: backupID(), State: "running", Phase: phase, CreatedAt: time.Now().UTC()}
	s.active = &j
	s.jobCancel = cancelJob
	if e := s.putJob(j); e != nil {
		cancelJob()
		s.active = nil
		s.jobCancel = nil
		return j, e
	}
	initial := j
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancelJob()
		s.operation.Lock()
		defer s.operation.Unlock()
		local := initial
		err := jobContext.Err()
		if err == nil {
			err = fn(jobContext, &local)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if local.BackupID == "" && s.active != nil && s.active.ID == initial.ID {
			local.BackupID = s.active.BackupID
		}
		local.State = "completed"
		local.Phase = "completed"
		if err != nil {
			local.State = "failed"
			local.Phase = "failed"
			local.Error = safeBackupError(err)
			if errors.Is(jobContext.Err(), context.Canceled) {
				local.State = "cancelled"
				local.Phase = "cancelled"
				local.Error = "ยกเลิกงานแล้ว ไฟล์สำรองที่ตรวจสอบผ่าน (ถ้ามี) ยังดาวน์โหลดได้ เครื่องที่พักสำหรับย้ายข้อมูลยังคงพักอยู่"
			}
		}
		s.putJob(local)
		if s.active != nil && s.active.ID == initial.ID {
			s.active = nil
			s.jobCancel = nil
		}
	}()
	return j, nil
}
func safeBackupError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "งานถูกยกเลิกหรือ Gateway หยุดทำงาน กรุณาตรวจสอบแล้วลองใหม่"
	}
	return "ดำเนินการไม่สำเร็จ: " + err.Error()
}
func (s *backupService) publishBackup(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active.BackupID = id
		s.putJob(*s.active)
	}
}

func (m *Manager) BackupHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		s := m.backups
		m.mu.Unlock()
		if s == nil {
			jsonError(w, 503, "BACKUP_UNAVAILABLE", "ระบบสำรองข้อมูลยังไม่พร้อม")
			return
		}
		s.serveHTTP(w, r)
	})
}

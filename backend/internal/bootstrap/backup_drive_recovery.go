package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"erp-platform/gateway/internal/backup"
	"erp-platform/gateway/internal/backupcloud"
)

type cloudRecovery struct {
	Secret      string                  `json:"secret"`
	Dataset     string                  `json:"dataset"`
	Credentials backupcloud.Credentials `json:"credentials"`
}

func (s *backupService) recoverDriveHTTP(w http.ResponseWriter, r *http.Request, path string) {
	fail := func(e error) { jsonError(w, 409, "DRIVE_RECOVERY_REJECTED", safeBackupError(e)) }
	switch {
	case path == "drive/recover" && r.Method == http.MethodPost:
		var in struct {
			Kit         string `json:"recovery_kit"`
			RedirectURL string `json:"redirect_url"`
		}
		if !readJSON(w, r, &in, 64<<10) {
			return
		}
		var kit struct {
			Version      int    `json:"version"`
			Secret       string `json:"secret"`
			Dataset      string `json:"dataset"`
			FolderID     string `json:"folder_id"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			RedirectURL  string `json:"redirect_url"`
		}
		if len(in.Kit) > 32<<10 || json.Unmarshal([]byte(in.Kit), &kit) != nil || kit.Version != 1 || !validBackupID(kit.Dataset) || len(kit.Secret) < 32 || kit.FolderID == "" || kit.ClientID == "" || kit.ClientSecret == "" {
			fail(errors.New("ชุดกู้คืนต้องมีข้อมูล Google Drive และกุญแจที่ดาวน์โหลดหลังเชื่อมบัญชีแล้ว"))
			return
		}
		redirect := in.RedirectURL
		if redirect == "" {
			redirect = kit.RedirectURL
		}
		attempt, e := backupcloud.Begin(kit.ClientID, redirect)
		if e != nil {
			fail(e)
			return
		}
		recovery := cloudRecovery{Secret: kit.Secret, Dataset: kit.Dataset, Credentials: backupcloud.Credentials{ClientID: kit.ClientID, ClientSecret: kit.ClientSecret, RedirectURL: redirect, FolderID: kit.FolderID}}
		s.mu.Lock()
		s.oauth = &attempt
		s.oauthCredentials = recovery.Credentials
		s.oauthRecovery = &recovery
		s.oauthBase = s.state.Drive
		s.oauthDataset = s.state.Dataset
		s.mu.Unlock()
		jsonData(w, map[string]string{"authorization_url": attempt.URL})
	case path == "drive/snapshots" && r.Method == http.MethodGet:
		limit := 25
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 100 {
				fail(errors.New("ขนาดหน้าไม่ถูกต้อง"))
				return
			}
			limit = n
		}
		recovered, e := s.cloudSource()
		if e != nil {
			fail(e)
			return
		}
		var page backupcloud.SnapshotPage
		e = s.withCloudCredentials(r.Context(), recovered, func(c backupcloud.Credentials) (backupcloud.Credentials, error) {
			var err error
			page, err = backupcloud.ListSnapshots(r.Context(), c, recovered.Dataset, r.URL.Query().Get("cursor"), limit)
			return c, err
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"items": page.Items, "next_cursor": page.NextCursor, "has_more": page.NextCursor != ""})
	case path == "drive/restore" && r.Method == http.MethodPost:
		var in struct {
			SnapshotID string `json:"snapshot_id"`
			PublicURL  string `json:"public_url"`
		}
		if !readJSON(w, r, &in, 8<<10) {
			return
		}
		if len(in.SnapshotID) != 64 {
			fail(errors.New("snapshot ID ไม่ถูกต้อง"))
			return
		}
		source, e := s.cloudSource()
		if e != nil {
			fail(e)
			return
		}
		j, e := s.startJob("downloading", func(ctx context.Context, j *backupJob) error {
			return s.withCloudRepository(ctx, source, func(repo backup.Repository) error {
				id := backupID()
				if e := os.MkdirAll(filepath.Join(s.root, "staging", id), 0700); e != nil {
					return e
				}
				result, e := s.inspectRepo(ctx, id, repo, in.SnapshotID, in.PublicURL)
				if e == nil {
					j.RestoreID = result.ID
				}
				return e
			})
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	default:
		jsonError(w, 404, "NOT_FOUND", "ไม่พบคำสั่ง Google Drive")
	}
}
func (s *backupService) cloudSource() (cloudRecovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, _, err := s.cloudSourceLocked()
	return source, err
}

// cloudSourceLocked keeps the explicit recovery selection separate from the
// configured account. A normal reconnect/disconnect clears that selection.
func (s *backupService) cloudSourceLocked() (cloudRecovery, string, error) {
	if s.recovery != nil && s.recovery.Credentials.RefreshToken != "" {
		return *s.recovery, "recovery", nil
	}
	if s.state.Drive.RefreshToken == "" {
		return cloudRecovery{}, "none", errors.New("เชื่อมบัญชี Google ด้วยชุดกู้คืนก่อน")
	}
	return cloudRecovery{Secret: s.state.Secret, Dataset: s.state.Dataset, Credentials: s.state.Drive}, "normal", nil
}

// clearCloudRecoveryLocked requires s.mu. Replacing the encrypted context with
// an empty durable tombstone before deletion prevents a lost directory update
// from resurrecting old credentials after restart. Pending OAuth attempts are
// cancelled as part of the same local selection change.
func (s *backupService) clearCloudRecoveryLocked() error {
	path := filepath.Join(s.dir, "cloud-recovery.enc")
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err = s.saveSealed(path, cloudRecovery{}); err != nil {
			return err
		}
		if os.Remove(path) == nil {
			_ = syncBackupDir(s.dir)
		}
	}
	s.recovery = nil
	s.clearPendingCloudOAuthLocked()
	return nil
}

func (s *backupService) clearPendingCloudOAuthLocked() {
	s.oauth = nil
	s.oauthRecovery = nil
	s.oauthCredentials = backupcloud.Credentials{}
	s.oauthBase = backupcloud.Credentials{}
	s.oauthDataset = ""
}

func (s *backupService) withCloudRepository(ctx context.Context, source cloudRecovery, fn func(backup.Repository) error) (result error) {
	return s.withCloudRepositoryMode(ctx, source, fn, false)
}

// withCloudRepositoryLocked is for destructive operations whose caller already
// holds policyMu throughout. Token persistence must not lock it recursively.
func (s *backupService) withCloudRepositoryLocked(ctx context.Context, source cloudRecovery, fn func(backup.Repository) error) error {
	return s.withCloudRepositoryMode(ctx, source, fn, true)
}

func (s *backupService) withCloudRepositoryMode(ctx context.Context, source cloudRecovery, fn func(backup.Repository) error, policyLocked bool) error {
	if !validBackupID(source.Dataset) {
		return errors.New("invalid cloud dataset")
	}
	return s.withCloudCredentialsMode(ctx, source, func(credentials backupcloud.Credentials) (backupcloud.Credentials, error) {
		prepared, e := backupcloud.Prepare(ctx, credentials, s.dir)
		if e != nil {
			return credentials, e
		}
		repo, clean, e := s.repository(prepared.Repository+"/"+source.Dataset, source.Secret)
		if e == nil {
			repo.Env = append(repo.Env, prepared.Env...)
			e = fn(repo)
			clean()
		}
		if refreshed, re := prepared.Credentials(); re == nil {
			credentials = refreshed
		} else {
			e = errors.Join(e, re)
		}
		e = errors.Join(e, prepared.Close())
		return credentials, e
	}, policyLocked)
}
func (s *backupService) withCloudCredentials(ctx context.Context, source cloudRecovery, fn func(backupcloud.Credentials) (backupcloud.Credentials, error)) (result error) {
	return s.withCloudCredentialsMode(ctx, source, fn, false)
}

func (s *backupService) withCloudCredentialsMode(ctx context.Context, source cloudRecovery, fn func(backupcloud.Credentials) (backupcloud.Credentials, error), policyLocked bool) (result error) {
	original := source.Credentials
	credentials, e := backupcloud.Refresh(ctx, original)
	if e != nil {
		return e
	}
	defer func() {
		if !policyLocked {
			s.policyMu.Lock()
			defer s.policyMu.Unlock()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.recovery != nil && s.recovery.Dataset == source.Dataset && s.recovery.Secret == source.Secret && s.recovery.Credentials == original {
			s.recovery.Credentials = credentials
			result = errors.Join(result, s.saveSealed(filepath.Join(s.dir, "cloud-recovery.enc"), s.recovery))
		} else if s.state.Dataset == source.Dataset && s.state.Secret == source.Secret && s.state.Drive == original {
			s.state.Drive = credentials
			result = errors.Join(result, s.saveLocked())
		} else {
			result = errors.Join(result, errors.New("การเชื่อมต่อ Google Drive เปลี่ยนระหว่างทำงาน กรุณาเลือกแหล่งข้อมูลใหม่"))
		}
	}()
	credentials, result = fn(credentials)
	return result
}

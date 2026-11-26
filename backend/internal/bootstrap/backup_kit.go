package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

type recoveryKit struct {
	Version      int    `json:"version"`
	Secret       string `json:"secret"`
	Dataset      string `json:"dataset"`
	FolderID     string `json:"folder_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURL  string `json:"redirect_url"`
	KeyID        string `json:"recovery_key_id"`
}

func recoveryKeyID(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
func kitForState(state backupState) recoveryKit {
	return recoveryKit{Version: 1, Secret: state.Secret, Dataset: state.Dataset, FolderID: state.Drive.FolderID, ClientID: state.Drive.ClientID, ClientSecret: state.Drive.ClientSecret, RedirectURL: state.Drive.RedirectURL, KeyID: recoveryKeyID(state.Secret)}
}
func (s *backupService) kitHTTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BackupID string `json:"backup_id"`
	}
	if !readJSON(w, r, &in, 8<<10) {
		return
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	kit := kitForState(s.state)
	if in.BackupID != "" {
		if !validBackupID(in.BackupID) {
			jsonError(w, 400, "INVALID_BACKUP", "รหัสชุดสำรองไม่ถูกต้อง")
			return
		}
		b, e := os.ReadFile(filepath.Join(s.recordPath(in.BackupID), "recovery-kit.enc"))
		if e != nil || s.unseal(b, &kit) != nil {
			jsonError(w, 404, "KIT_NOT_FOUND", "ไม่พบชุดกู้คืนที่คู่กับไฟล์สำรองนี้")
			return
		}
	}
	if kit.Version != 1 || kit.KeyID != recoveryKeyID(kit.Secret) {
		jsonError(w, 409, "KIT_INVALID", errors.New("ชุดกู้คืนไม่ถูกต้อง").Error())
		return
	}
	if kit.Secret == s.state.Secret && kit.FolderID == s.state.Drive.FolderID && kit.ClientID == s.state.Drive.ClientID {
		s.state.Exported = true
		if e := s.saveLocked(); e != nil {
			jsonError(w, 503, "KIT_SAVE_FAILED", "บันทึกสถานะชุดกู้คืนไม่ได้")
			return
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="gateway-recovery-kit.json"`)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(kit)
}

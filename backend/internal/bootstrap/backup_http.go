package bootstrap

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"erp-platform/gateway/internal/backupcloud"
)

func (m *Manager) authorizeBackupSetup(w http.ResponseWriter, r *http.Request) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.session(r)
	if !ok {
		jsonError(w, 401, "SETUP_LOGIN_REQUIRED", "กรุณายืนยันรหัสตั้งค่า")
		return false
	}
	if r.Method != http.MethodGet && (!m.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(session.csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1) {
		jsonError(w, 403, "CSRF_REJECTED", "โหลดหน้าตั้งค่าใหม่แล้วลองอีกครั้ง")
		return false
	}
	return true
}
func (s *backupService) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := backupSuffix(r.URL.Path)
	parts := strings.Split(path, "/")
	fail := func(err error) { jsonError(w, 409, "BACKUP_REJECTED", safeBackupError(err)) }
	fresh := strings.HasPrefix(r.URL.Path, "/setup/")
	s.m.mu.Lock()
	fenced := s.m.fenced
	s.m.mu.Unlock()
	retiredArtifacts := false
	if validBackupID(parts[0]) && ((len(parts) == 1 && r.Method == http.MethodDelete) || (len(parts) == 2 && parts[1] == "pin" && r.Method == http.MethodPost)) {
		s.mu.Lock()
		retiredArtifacts = s.transfer != nil && s.transfer.Direction == "sender" && s.transfer.State == "retired"
		s.mu.Unlock()
	}
	if fenced && !retiredArtifacts && r.Method != http.MethodGet && !strings.HasPrefix(path, "migration/") && !(len(parts) == 3 && parts[0] == "jobs" && parts[2] == "cancel") && path != "recovery-kit" && path != "restore/repair" {
		jsonError(w, 409, "MIGRATION_PAUSED", "พักการเปลี่ยนแปลงระหว่างย้ายเครื่อง")
		return
	}
	if fresh && !(path == "" || path == "migration/receive" || path == "migration/activate" || strings.HasPrefix(path, "jobs/") || path == "restore" || strings.HasPrefix(path, "restore/") || path == "drive/recover" || path == "drive/snapshots" || path == "drive/restore") {
		jsonError(w, 403, "SETUP_SCOPE", "คำสั่งนี้ต้องเข้าสู่ระบบผู้ดูแล")
		return
	}
	switch {
	case path == "" && r.Method == http.MethodGet:
		s.statusHTTP(w, r)
	case path == "" && r.Method == http.MethodPost:
		s.m.mu.Lock()
		paused := s.m.fenced
		s.m.mu.Unlock()
		if paused {
			fail(errors.New("เครื่องนี้พักสำหรับย้ายข้อมูล"))
			return
		}
		j, e := s.startJob("capturing", func(ctx context.Context, j *backupJob) error {
			record, e := s.capture(ctx, "", false)
			if e != nil {
				return e
			}
			j.BackupID = record.ID
			s.publishBackup(record.ID)
			return s.protect(ctx, record)
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	case path == "history" && r.Method == http.MethodGet:
		s.historyHTTP(w, r)
	case path == "restored-work":
		s.restoredWorkHTTP(w, r)
	case len(parts) == 2 && parts[1] == "pin" && r.Method == http.MethodPost:
		s.pinRecordHTTP(w, r, parts[0])
	case len(parts) == 1 && validBackupID(parts[0]) && r.Method == http.MethodDelete:
		s.deleteRecordHTTP(w, r, parts[0])
	case len(parts) == 2 && parts[0] == "jobs" && r.Method == http.MethodGet:
		var body string
		if e := s.db.QueryRow("SELECT body FROM jobs WHERE id=?", parts[1]).Scan(&body); e != nil {
			jsonError(w, 404, "JOB_NOT_FOUND", "ไม่พบงาน")
			return
		}
		var j backupJob
		if json.Unmarshal([]byte(body), &j) != nil {
			fail(errors.New("cannot read job"))
			return
		}
		jsonData(w, j)
	case len(parts) == 3 && parts[0] == "jobs" && parts[2] == "cancel" && r.Method == http.MethodPost:
		s.cancelJobHTTP(w, r, parts[1])
	case len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet:
		record, e := s.getRecord(parts[0])
		if e != nil || !record.LocalAvailable {
			jsonError(w, 404, "BACKUP_NOT_FOUND", "ไม่มีไฟล์ในเครื่องนี้แล้ว กรุณาใช้ชุดบน Google Drive")
			return
		}
		s.policyMu.Lock()
		file, e := os.Open(filepath.Join(s.recordPath(record.ID), "gateway-backup.bundle"))
		if e != nil {
			s.policyMu.Unlock()
			jsonError(w, 404, "BACKUP_NOT_FOUND", "ไม่พบไฟล์สำรองชุดนี้")
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil {
			s.policyMu.Unlock()
			fail(e)
			return
		}
		s.mu.Lock()
		if s.downloads == nil {
			s.downloads = map[string]int{}
		}
		s.downloads[record.ID]++
		s.mu.Unlock()
		s.policyMu.Unlock()
		defer func() { s.mu.Lock(); s.downloads[record.ID]--; s.mu.Unlock() }()
		http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="gateway-`+record.ID+`.bundle"`)
		http.ServeContent(w, r, "gateway-backup.bundle", info.ModTime(), file)
	case path == "recovery-kit" && r.Method == http.MethodPost:
		s.kitHTTP(w, r)
	case path == "policy" && r.Method == http.MethodPut:
		var p BackupPolicy
		if !readJSON(w, r, &p, 32<<10) {
			return
		}
		if e := s.applyPolicy(p); e != nil {
			fail(e)
			return
		}
		s.mu.Lock()
		p = s.state.Policy
		s.mu.Unlock()
		jsonData(w, p)
	case path == "restore" && r.Method == http.MethodPost:
		s.uploadRestoreHTTP(w, r)
	case path == "restore/repair" && r.Method == http.MethodPost:
		var in struct {
			Certificate string  `json:"client_certificate_pem"`
			HospitalCA  *string `json:"hospital_ca_pem"`
		}
		if !readJSON(w, r, &in, 1<<20) {
			return
		}
		j, e := s.startJob("repairing", func(ctx context.Context, j *backupJob) error {
			var roots []byte
			if in.HospitalCA != nil {
				roots = []byte(*in.HospitalCA)
			}
			return s.m.RepairRestoredIdentity(ctx, []byte(in.Certificate), roots)
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	case len(parts) == 2 && parts[0] == "restore" && r.Method == http.MethodGet:
		x, e := s.loadInspection(parts[1])
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, x.Result)
	case len(parts) == 3 && parts[0] == "restore" && parts[2] == "activate" && r.Method == http.MethodPost:
		inspected, inspectErr := s.loadInspection(parts[1])
		if inspectErr != nil {
			fail(inspectErr)
			return
		}
		if inspected.TransferSessionID != "" {
			fail(errors.New("ชุดนี้มาจากการโอนตรง ต้องเปิดผ่านขั้นตอนย้ายเครื่องชุดสุดท้ายเท่านั้น"))
			return
		}
		s.mu.Lock()
		transferOwned := s.transfer != nil && s.transfer.Direction == "receiver" && s.transfer.RestoreID == parts[1]
		s.mu.Unlock()
		if transferOwned {
			fail(errors.New("ชุดนี้อยู่ในขั้นตอนย้ายเครื่อง กรุณาใช้ปุ่มเปิดใช้งานปลายทางหลังตรวจชุดสุดท้าย"))
			return
		}
		var in struct {
			ConfirmID     string `json:"confirm_id"`
			PublicURL     string `json:"public_url"`
			SourceStopped bool   `json:"source_stopped"`
		}
		if !readJSON(w, r, &in, 8<<10) {
			return
		}
		if in.ConfirmID != parts[1] || !in.SourceStopped {
			fail(errors.New("ยืนยันชุดข้อมูลและการหยุดต้นทางก่อนนำเข้า"))
			return
		}
		j, e := s.startJob("activating", func(ctx context.Context, j *backupJob) error {
			return s.activateRestore(ctx, parts[1], in.PublicURL, in.SourceStopped)
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	case path == "drive/connect" && r.Method == http.MethodPost:
		var in struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			RedirectURL  string `json:"redirect_url"`
		}
		if !readJSON(w, r, &in, 16<<10) {
			return
		}
		attempt, e := backupcloud.Begin(in.ClientID, in.RedirectURL)
		if e != nil {
			fail(e)
			return
		}
		s.mu.Lock()
		s.oauth = &attempt
		s.oauthCredentials = backupcloud.Credentials{ClientID: in.ClientID, ClientSecret: in.ClientSecret, RedirectURL: in.RedirectURL}
		s.oauthRecovery = nil
		s.oauthBase = s.state.Drive
		s.oauthDataset = s.state.Dataset
		s.mu.Unlock()
		jsonData(w, map[string]string{"authorization_url": attempt.URL})
	case path == "drive/disconnect" && r.Method == http.MethodPost:
		s.policyMu.Lock()
		s.mu.Lock()
		old := s.state
		e := s.clearCloudRecoveryLocked()
		if e == nil {
			s.state.Drive.AccessToken = ""
			s.state.Drive.RefreshToken = ""
			s.state.Policy.Destination = "local"
			s.state.Policy.Revision++
			e = s.saveLocked()
			if e != nil {
				s.state = old
			}
		}
		s.mu.Unlock()
		s.policyMu.Unlock()
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]bool{"disconnected": true})
	case strings.HasPrefix(path, "migration/"):
		s.migrationHTTP(w, r, strings.TrimPrefix(path, "migration/"))
	case strings.HasPrefix(path, "drive/"):
		s.recoverDriveHTTP(w, r, path)
	default:
		jsonError(w, 404, "NOT_FOUND", "ไม่พบคำสั่งสำรองข้อมูล")
	}
}
func (s *backupService) statusHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p := s.state.Policy
	next := s.state.NextRun
	exported := s.state.Exported
	c := s.state.Drive
	dataset, keyID := s.state.Dataset, recoveryKeyID(s.state.Secret)
	selected, connectionSource, _ := s.cloudSourceLocked()
	recoveryReady := s.recovery != nil && s.recovery.Credentials.RefreshToken != ""
	var recoveryContext any
	if recoveryReady {
		recoveryContext = map[string]any{"dataset": s.recovery.Dataset, "folder_id": s.recovery.Credentials.FolderID, "redirect_url": s.recovery.Credentials.RedirectURL}
	}
	var job *backupJob
	if s.active != nil {
		x := *s.active
		job = &x
	}
	migration := s.publicMigrationLocked()
	s.mu.Unlock()
	var latestLocal, latestRemote *backupRecord
	var latestJob *backupJob
	var latestBody string
	if e := s.db.QueryRow("SELECT body FROM jobs ORDER BY json_extract(body,'$.created_at') DESC,id DESC LIMIT 1").Scan(&latestBody); e == nil {
		var j backupJob
		if json.Unmarshal([]byte(latestBody), &j) == nil {
			latestJob = &j
		}
	}
	for _, remote := range []bool{false, true} {
		var body string
		var args []any
		query := "SELECT record FROM backups WHERE json_extract(record,'$.local_available')=1 ORDER BY seq DESC LIMIT 1"
		if remote {
			query = "SELECT record FROM backups WHERE json_extract(record,'$.offsite_at') IS NOT NULL AND json_extract(record,'$.remote_scope')=? AND coalesce(json_extract(record,'$.remote_snapshot_id'),'')<>'' ORDER BY seq DESC LIMIT 1"
			args = []any{cloudScope(c, dataset)}
		}
		if e := s.db.QueryRow(query, args...).Scan(&body); e == nil {
			var x backupRecord
			if json.Unmarshal([]byte(body), &x) == nil {
				if remote {
					latestRemote = &x
				} else {
					latestLocal = &x
				}
			}
		}
	}
	_, toolErr := os.Stat(s.binary)
	health := map[string]any{"local_stale": false, "offsite_stale": false, "message": ""}
	if p.AlertsEnabled && p.Enabled {
		threshold := time.Duration(p.StaleMinutes) * time.Minute
		if latestLocal == nil || time.Since(latestLocal.CreatedAt) > threshold {
			health["local_stale"] = true
		}
		if p.Destination == "drive" && (latestRemote == nil || latestRemote.OffsiteAt == nil || time.Since(*latestRemote.OffsiteAt) > threshold) {
			health["offsite_stale"] = true
			health["message"] = "ยังไม่มีข้อมูลสำรองนอกเครื่องที่สำเร็จภายในช่วงเวลาที่กำหนด"
		}
	}
	jsonData(w, map[string]any{"policy": p, "next_run": next, "latest_local": latestLocal, "latest_offsite": latestRemote, "active_job": job, "latest_job": latestJob, "drive": map[string]any{"connected": c.RefreshToken != "", "folder_id": c.FolderID, "redirect_url": c.RedirectURL, "recovery_ready": recoveryReady, "connection_source": connectionSource, "selected_folder_id": selected.Credentials.FolderID, "selected_dataset": selected.Dataset, "recovery_context": recoveryContext}, "migration": migration, "tools_available": toolErr == nil, "recovery_exported": exported, "recovery_key_id": keyID, "health": health})
}
func (s *backupService) historyHTTP(w http.ResponseWriter, r *http.Request) {
	cursor, e := parseBackupCursor(r.URL.Query().Get("cursor"))
	if e != nil {
		jsonError(w, 400, "INVALID_CURSOR", e.Error())
		return
	}
	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > 100 {
			jsonError(w, 400, "INVALID_LIMIT", "เลือกขนาดหน้า 1 ถึง 100")
			return
		}
	}
	rows, e := s.db.QueryContext(r.Context(), "SELECT seq,record FROM backups WHERE seq<? ORDER BY seq DESC LIMIT ?", cursor, limit+1)
	if e != nil {
		jsonError(w, 503, "CATALOG_UNAVAILABLE", "อ่านประวัติไม่ได้")
		return
	}
	items := []backupRecord{}
	var last int64
	more := false
	for rows.Next() {
		var seq int64
		var b string
		if rows.Scan(&seq, &b) != nil {
			break
		}
		if len(items) == limit {
			more = true
			break
		}
		var x backupRecord
		if json.Unmarshal([]byte(b), &x) != nil {
			continue
		}
		items = append(items, x)
		last = seq
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		jsonError(w, 503, "CATALOG_UNAVAILABLE", "อ่านประวัติไม่ได้")
		return
	}
	var total int64
	s.db.QueryRowContext(r.Context(), "SELECT count(*) FROM backups").Scan(&total)
	var next any
	if more {
		next = strconv.FormatInt(last, 10)
	}
	jsonData(w, map[string]any{"items": items, "next_cursor": next, "has_more": more, "total": total})
}
func (s *backupService) uploadRestoreHTTP(w http.ResponseWriter, r *http.Request) {
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(20 * time.Minute))
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(20 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, maxFullBackupBytes+(64<<10))
	reader, e := r.MultipartReader()
	if e != nil {
		jsonError(w, 400, "MULTIPART_REQUIRED", "เลือกไฟล์สำรอง")
		return
	}
	upload, e := os.CreateTemp(filepath.Join(s.root, "staging"), ".upload-*")
	if e != nil {
		jsonError(w, 503, "UPLOAD_FAILED", "สร้างพื้นที่รับไฟล์ไม่ได้")
		return
	}
	upload.Chmod(0600)
	path := upload.Name()
	keep := false
	defer func() {
		upload.Close()
		if !keep {
			os.Remove(path)
		}
	}()
	secret, public := "", ""
	files := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			e = err
			break
		}
		switch part.FormName() {
		case "file":
			files++
			if files > 1 {
				e = errors.New("เลือกไฟล์สำรองหนึ่งไฟล์")
			} else {
				var n int64
				n, e = io.Copy(upload, io.LimitReader(part, maxFullBackupBytes+1))
				if n > maxFullBackupBytes {
					e = errors.New("ไฟล์ใหญ่เกินขอบเขต")
				}
			}
		case "recovery_secret", "public_url":
			var b []byte
			b, e = io.ReadAll(io.LimitReader(part, 32<<10))
			if len(b) >= 32<<10 {
				e = errors.New("ข้อมูลช่องกรอกใหญ่เกินขอบเขต")
			}
			if part.FormName() == "recovery_secret" {
				secret = string(b)
			} else {
				public = string(b)
			}
		default:
			e = errors.New("ช่องนำเข้าไม่ถูกต้อง")
		}
		part.Close()
		if e != nil {
			break
		}
	}
	if e != nil || files != 1 {
		jsonError(w, 400, "UPLOAD_FAILED", "รับไฟล์ไม่สำเร็จ ตรวจไฟล์และพื้นที่แล้วลองใหม่")
		return
	}
	upload.Sync()
	upload.Close()
	j, e := s.startJob("verifying", func(ctx context.Context, j *backupJob) error {
		defer os.Remove(path)
		result, e := s.inspectBundle(ctx, path, secret, public)
		if e == nil {
			j.RestoreID = result.ID
		}
		return e
	})
	if e != nil {
		jsonError(w, 409, "BACKUP_BUSY", e.Error())
		return
	}
	keep = true
	jsonData(w, map[string]any{"job": j})
}
func (s *backupService) oauthCallback(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	attempt := s.oauth
	base, dataset := s.oauthBase, s.oauthDataset
	creds := s.oauthCredentials
	recovery := s.oauthRecovery
	valid := attempt != nil && attempt.ExpiresAt.After(time.Now()) && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(attempt.State)) == 1
	if valid {
		s.oauth = nil
	}
	s.mu.Unlock()
	if !valid {
		jsonError(w, 403, "OAUTH_STATE_INVALID", "การเชื่อมบัญชีหมดอายุหรือไม่ตรง กรุณาเริ่มเชื่อมจากหน้า Gateway ใหม่")
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Error(w, "Google connection was not approved. Return to Gateway and reconnect.", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	creds, e := backupcloud.Exchange(ctx, creds, r.URL.Query().Get("code"), attempt.Verifier)
	if e == nil {
		creds, e = backupcloud.EnsureFolder(ctx, creds)
	}
	if e != nil {
		jsonError(w, 409, "GOOGLE_CONNECT_FAILED", "เชื่อม Google Drive ไม่สำเร็จ ตรวจ OAuth client, callback และการอนุญาตแล้วลองใหม่")
		return
	}
	s.policyMu.Lock()
	s.m.mu.Lock()
	paused := s.m.fenced
	s.m.mu.Unlock()
	s.mu.Lock()
	if paused || s.state.Dataset != dataset || s.oauthDataset != dataset || s.oauthBase != base || s.oauthRecovery != recovery || (recovery == nil && s.state.Drive != base) {
		s.mu.Unlock()
		s.policyMu.Unlock()
		jsonError(w, 409, "OAUTH_STATE_CHANGED", "Gateway เปลี่ยนข้อมูลหรือพักสำหรับย้ายเครื่อง กรุณาเชื่อม Google ใหม่หลังเปิดปลายทาง")
		return
	}
	if recovery != nil {
		recovery.Credentials = creds
		e = s.saveSealed(filepath.Join(s.dir, "cloud-recovery.enc"), recovery)
		if e == nil {
			s.recovery = recovery
			s.clearPendingCloudOAuthLocked()
		}
	} else {
		old := s.state
		e = s.clearCloudRecoveryLocked()
		if e == nil {
			s.state.Drive = creds
			s.state.Exported = false
			e = s.saveLocked()
			if e != nil {
				s.state = old
			}
		}
	}
	s.mu.Unlock()
	s.policyMu.Unlock()
	if e != nil {
		jsonError(w, 503, "GOOGLE_SAVE_FAILED", "บันทึกการเชื่อมต่อไม่ได้")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "เชื่อม Google Drive สำเร็จ กลับไปหน้า Gateway แล้วกดรีเฟรชสถานะ และดาวน์โหลดชุดกู้คืนฉบับล่าสุด")
}

var _ = sql.ErrNoRows

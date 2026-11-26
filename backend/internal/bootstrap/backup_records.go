package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"erp-platform/gateway/internal/backup"
)

// recordMutationLocked is called with policyMu held. In-flight downloads hold
// that same lock, and a running capture/restore protects all of its artifacts.
func (s *backupService) recordMutationLocked(id string) error {
	s.m.mu.Lock()
	fenced, closed := s.m.fenced, s.m.closed
	s.m.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	retiredSource := s.transfer != nil && s.transfer.Direction == "sender" && s.transfer.State == "retired"
	if closed || (fenced && !retiredSource) {
		return errors.New("พักการจัดการชุดข้อมูลระหว่างย้ายเครื่อง")
	}
	if s.downloads[id] > 0 {
		return errors.New("กำลังดาวน์โหลดชุดข้อมูลนี้ กรุณารอให้ส่งไฟล์เสร็จ")
	}
	if s.active != nil {
		return errors.New("มีงานสำรองหรือย้ายข้อมูลกำลังทำงาน กรุณาลองใหม่")
	}
	if transfer := s.transfer; transfer != nil && transfer.RecordID == id && transfer.State != "retired" && transfer.State != "cancelled" {
		return errors.New("ชุดข้อมูลนี้ยังใช้กับการย้ายเครื่อง ต้องจบหรือยกเลิกการย้ายก่อน")
	}
	return nil
}

func (s *backupService) pinRecordHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, 405, "METHOD_NOT_ALLOWED", "ใช้ POST")
		return
	}
	if !validBackupID(id) {
		jsonError(w, 400, "INVALID_BACKUP_ID", "รหัสข้อมูลสำรองไม่ถูกต้อง")
		return
	}
	var input struct {
		Pinned *bool `json:"pinned"`
	}
	if !readJSON(w, r, &input, 2048) {
		return
	}
	if input.Pinned == nil {
		jsonError(w, 400, "PIN_REQUIRED", "ระบุการเก็บชุดข้อมูล")
		return
	}
	if !s.policyMu.TryLock() {
		jsonError(w, 409, "BACKUP_BUSY", "กำลังส่งไฟล์หรือจัดการข้อมูลสำรอง กรุณาลองใหม่")
		return
	}
	defer s.policyMu.Unlock()
	if err := s.recordMutationLocked(id); err != nil {
		jsonError(w, 409, "BACKUP_PROTECTED", err.Error())
		return
	}
	record, err := s.getRecord(id)
	if err != nil {
		jsonError(w, 404, "BACKUP_NOT_FOUND", "ไม่พบข้อมูลสำรอง")
		return
	}
	record.Pinned = *input.Pinned
	if err := s.putRecord(record); err != nil {
		jsonError(w, 503, "PIN_FAILED", "บันทึกการเก็บชุดข้อมูลไม่สำเร็จ")
		return
	}
	jsonData(w, record)
}

func (s *backupService) deleteRecordHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		jsonError(w, 405, "METHOD_NOT_ALLOWED", "ใช้ DELETE")
		return
	}
	if !validBackupID(id) {
		jsonError(w, 400, "INVALID_BACKUP_ID", "รหัสข้อมูลสำรองไม่ถูกต้อง")
		return
	}
	destination := r.URL.Query().Get("destination")
	if destination == "" {
		destination = "local"
	}
	if destination != "local" && destination != "drive" && destination != "all" {
		jsonError(w, 400, "INVALID_DESTINATION", "เลือก local, drive หรือ all")
		return
	}
	if !s.policyMu.TryLock() {
		jsonError(w, 409, "BACKUP_BUSY", "กำลังส่งไฟล์หรือจัดการข้อมูลสำรอง กรุณาลองใหม่")
		return
	}
	defer s.policyMu.Unlock()
	if err := s.recordMutationLocked(id); err != nil {
		jsonError(w, 409, "BACKUP_PROTECTED", err.Error())
		return
	}
	record, err := s.getRecord(id)
	if err != nil {
		jsonError(w, 404, "BACKUP_NOT_FOUND", "ไม่พบข้อมูลสำรอง")
		return
	}
	if record.Pinned {
		jsonError(w, 409, "BACKUP_PINNED", "ยกเลิกการเก็บชุดข้อมูลนี้ก่อนลบ")
		return
	}
	removeLocal := destination == "local" || destination == "all"
	removeDrive := destination == "drive" || destination == "all"
	if removeDrive {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	}
	// Validate local protection before any remote mutation in an all request.
	if removeLocal && record.LocalAvailable && record.Status == "completed" {
		if err := s.checkLocalAlternative(r.Context(), record); err != nil {
			jsonError(w, 409, "LAST_GOOD_BACKUP", err.Error())
			return
		}
	}
	if removeDrive && record.RemoteSnapshotID != "" {
		if err := s.deleteRemoteRecordLocked(r.Context(), record); err != nil {
			jsonError(w, 409, "REMOTE_DELETE_REJECTED", safeBackupError(err))
			return
		}
		record.RemoteSnapshotID = ""
		// Retain the historical upload time/scope; an empty native ID denotes
		// that the remote copy is no longer available for restore.
		// Persist a completed remote deletion even if later local I/O fails.
		if err := s.putRecord(record); err != nil {
			jsonError(w, 503, "CATALOG_UPDATE_FAILED", "ลบชุดบน Drive แล้ว แต่บันทึกประวัติไม่สำเร็จ กรุณาตรวจสถานะก่อนลองใหม่")
			return
		}
	}
	if removeLocal {
		if err := os.RemoveAll(s.recordPath(id)); err != nil {
			jsonError(w, 503, "BACKUP_DELETE_FAILED", "ลบไฟล์ในเครื่องไม่สำเร็จ กรุณาตรวจสิทธิ์ไฟล์")
			return
		}
		record.LocalAvailable = false
	}
	if err := s.putRecord(record); err != nil {
		jsonError(w, 503, "CATALOG_UPDATE_FAILED", "ลบไฟล์แล้ว แต่บันทึกประวัติไม่สำเร็จ กรุณารีเฟรชสถานะ")
		return
	}
	jsonData(w, record)
}

func (s *backupService) checkLocalAlternative(ctx context.Context, record backupRecord) error {
	// An already-missing artifact is a catalog correction, not deletion of the
	// final existing copy. Do not pretend it is downloadable forever.
	path := filepath.Join(s.recordPath(record.ID), "gateway-backup.bundle")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT record FROM backups WHERE id<>? AND json_extract(record,'$.local_available')=1 AND json_extract(record,'$.status')='completed' ORDER BY seq DESC LIMIT 1", record.ID).Scan(&raw)
	if err != nil {
		return errors.New("ต้องมีข้อมูลสำรองในเครื่องที่ตรวจสอบแล้วอย่างน้อยหนึ่งชุด สร้างชุดใหม่ก่อนลบ")
	}
	var alternative backupRecord
	if json.Unmarshal([]byte(raw), &alternative) != nil || !validBackupID(alternative.ID) {
		return errors.New("ตรวจข้อมูลสำรองชุดที่เหลือไม่ได้")
	}
	info, err := os.Lstat(filepath.Join(s.recordPath(alternative.ID), "gateway-backup.bundle"))
	if err != nil || !info.Mode().IsRegular() || (alternative.Size > 0 && info.Size() != alternative.Size) {
		return errors.New("ไม่พบไฟล์สำรองชุดอื่นที่พร้อมใช้ กรุณาสำรองใหม่ก่อนลบ")
	}
	return nil
}

func (s *backupService) deleteRemoteRecordLocked(ctx context.Context, record backupRecord) error {
	s.mu.Lock()
	source := cloudRecovery{Secret: s.state.Secret, Dataset: s.state.Dataset, Credentials: s.state.Drive}
	s.mu.Unlock()
	if source.Credentials.RefreshToken == "" || record.RemoteScope != cloudScope(source.Credentials, source.Dataset) {
		return errors.New("เชื่อมบัญชีและโฟลเดอร์ Drive เดิมของข้อมูลสำรองก่อนลบ")
	}
	return s.withCloudRepositoryLocked(ctx, source, func(repository backup.Repository) error {
		snapshots, err := repository.Snapshots(ctx)
		if err != nil {
			return err
		}
		// Native snapshots from a prior server must be retained, but an unknown
		// or partial native snapshot is not proof of another verified good copy.
		verified := map[string]bool{}
		rows, err := s.db.QueryContext(ctx, "SELECT json_extract(record,'$.remote_snapshot_id') FROM backups WHERE id<>? AND json_extract(record,'$.status')='completed' AND json_extract(record,'$.remote_scope')=? AND json_extract(record,'$.remote_snapshot_id') IS NOT NULL AND json_extract(record,'$.remote_snapshot_id')<>'' ORDER BY seq DESC LIMIT 10000", record.ID, record.RemoteScope)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			verified[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		keep := make([]string, 0, len(snapshots))
		found := false
		otherCaptures := 0
		for _, snapshot := range snapshots {
			if snapshot.ID == record.RemoteSnapshotID {
				found = true
				continue
			}
			keep = append(keep, snapshot.ID)
			if verified[snapshot.ID] && len(snapshot.Paths) == 1 && filepath.Base(snapshot.Paths[0]) == "capture" {
				otherCaptures++
			}
		}
		if found && otherCaptures == 0 {
			return errors.New("ต้องเก็บข้อมูลสำรองบน Drive ที่พร้อมใช้อย่างน้อยหนึ่งชุด สร้างชุดใหม่ก่อนลบ")
		}
		// Keep every other native snapshot, including ones created on an earlier
		// machine and absent from this local catalog. Never prune unknown history.
		if found {
			if err := repository.Forget(ctx, keep); err != nil {
				return err
			}
		}
		// Manual deletion must also reclaim unreachable encrypted packs when the
		// selected policy is keep-all (there may be no scheduled forget/prune).
		// If a prior attempt already removed metadata, retry pruning safely.
		return repository.Prune(ctx)
	})
}

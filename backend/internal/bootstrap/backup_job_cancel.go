package bootstrap

import (
	"net/http"
)

// cancelJobHTTP requests cancellation only for the identified running job.
// It never resumes a migration fence or rolls back a durable activation.
func (s *backupService) cancelJobHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "ใช้ POST สำหรับคำสั่งนี้")
		return
	}
	if !validBackupID(id) {
		jsonError(w, http.StatusBadRequest, "INVALID_JOB_ID", "หมายเลขงานไม่ถูกต้อง")
		return
	}
	var input struct{}
	if !readJSON(w, r, &input, 8<<10) {
		return
	}
	s.mu.Lock()
	if r.Context().Err() != nil {
		s.mu.Unlock()
		jsonError(w, http.StatusRequestTimeout, "REQUEST_CANCELLED", "คำขอหมดเวลา ยังไม่ได้ส่งคำสั่งยกเลิกงาน")
		return
	}
	if s.active == nil || s.active.ID != id || s.jobCancel == nil {
		s.mu.Unlock()
		jsonError(w, http.StatusConflict, "JOB_NOT_RUNNING", "งานนี้สิ้นสุดแล้วหรือไม่ใช่งานที่กำลังทำงาน กรุณารีเฟรชสถานะ")
		return
	}
	cancel := s.jobCancel
	s.mu.Unlock()
	if r.Context().Err() != nil {
		jsonError(w, http.StatusRequestTimeout, "REQUEST_CANCELLED", "คำขอหมดเวลา ยังไม่ได้ส่งคำสั่งยกเลิกงาน")
		return
	}
	// Capture the old job's function under the lock, invoke it outside. If that
	// job completed and a new job began meanwhile, only the old context is hit.
	cancel()
	jsonData(w, map[string]any{"job_id": id, "cancel_requested": true})
}

package bootstrap

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type restoredWork struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	HospitalID string `json:"hospital_id"`
	EventID    string `json:"event_id,omitempty"`
	CanResume  bool   `json:"can_resume"`
}
type restoredWorkCursor struct {
	CreatedAt int64  `json:"at"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
}

const restoredWorkUnion = `SELECT 'notification:'||CAST(id AS TEXT) AS id,'notification' AS kind,status,created_at,hospital_id,event_id,1 AS can_resume FROM notify_jobs WHERE status='quarantined'
 UNION ALL SELECT 'updater:'||id AS id,'updater' AS kind,status,created_at,hospital_id,'' AS event_id,0 AS can_resume FROM update_jobs WHERE stage='restored_quarantine' AND status='blocked'`

func (s *backupService) restoredWorkHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, 405, "METHOD_NOT_ALLOWED", "ใช้ GET หรือ POST")
		return
	}
	// Backup control requests are excluded from the public drain counter. These
	// bounded primary-DB operations therefore join the operation lock explicitly
	// so cutover can never rename a DB underneath an untracked reader/writer.
	if !s.operation.TryLock() {
		jsonError(w, 409, "BACKUP_BUSY", "มีงานสำรองหรือย้ายข้อมูลกำลังทำงาน กรุณาลองใหม่")
		return
	}
	defer s.operation.Unlock()
	s.m.mu.Lock()
	fenced := s.m.fenced || s.m.closed
	s.m.mu.Unlock()
	if fenced {
		jsonError(w, 409, "MIGRATION_PAUSED", "พักการจัดการงานค้างระหว่างย้ายเครื่อง")
		return
	}
	mode := "ro"
	if r.Method == http.MethodPost {
		mode = "rw"
	}
	databasePath, err := filepath.Abs(s.m.options.DatabasePath)
	if err != nil {
		jsonError(w, 503, "WORK_UNAVAILABLE", "เปิดรายการงานค้างไม่ได้")
		return
	}
	u := &url.URL{Scheme: "file", Path: databasePath, RawQuery: "mode=" + mode + "&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		jsonError(w, 503, "WORK_UNAVAILABLE", "เปิดรายการงานค้างไม่ได้")
		return
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if r.Method == http.MethodGet {
		s.listRestoredWork(w, r, db)
		return
	}
	s.changeRestoredWork(w, r, db)
}

func (s *backupService) listRestoredWork(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			jsonError(w, 400, "INVALID_LIMIT", "ขนาดหน้าต้องอยู่ระหว่าง 1 ถึง 100")
			return
		}
		limit = n
	}
	where := ""
	args := []any{}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var cursor restoredWorkCursor
		body, err := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 1024 || err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt < 0 || (cursor.Kind != "notification" && cursor.Kind != "updater") || cursor.ID == "" || len(cursor.ID) > 128 {
			jsonError(w, 400, "INVALID_CURSOR", "ตำแหน่งรายการไม่ถูกต้อง กรุณาโหลดใหม่")
			return
		}
		where = " WHERE created_at<? OR (created_at=? AND (kind>? OR (kind=? AND id<?)))"
		args = []any{cursor.CreatedAt, cursor.CreatedAt, cursor.Kind, cursor.Kind, cursor.ID}
	}
	args = append(args, limit+1)
	rows, err := db.QueryContext(r.Context(), "SELECT id,kind,status,created_at,hospital_id,event_id,can_resume FROM ("+restoredWorkUnion+")"+where+" ORDER BY created_at DESC,kind ASC,id DESC LIMIT ?", args...)
	if err != nil {
		jsonError(w, 503, "WORK_UNAVAILABLE", "อ่านรายการงานค้างไม่ได้")
		return
	}
	items := make([]restoredWork, 0, limit)
	more := false
	for rows.Next() {
		var item restoredWork
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.CreatedAt, &item.HospitalID, &item.EventID, &item.CanResume); err != nil {
			rows.Close()
			jsonError(w, 503, "WORK_UNAVAILABLE", "อ่านรายการงานค้างไม่ได้")
			return
		}
		if len(items) == limit {
			more = true
			break
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		jsonError(w, 503, "WORK_UNAVAILABLE", "อ่านรายการงานค้างไม่ได้")
		return
	}
	var total int64
	if err := db.QueryRowContext(r.Context(), "SELECT count(*) FROM ("+restoredWorkUnion+")").Scan(&total); err != nil {
		jsonError(w, 503, "WORK_UNAVAILABLE", "นับรายการงานค้างไม่ได้")
		return
	}
	var next any
	if more {
		last := items[len(items)-1]
		body, _ := json.Marshal(restoredWorkCursor{CreatedAt: last.CreatedAt, Kind: last.Kind, ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(body)
	}
	jsonData(w, map[string]any{"items": items, "next_cursor": next, "has_more": more, "total": total})
}

func (s *backupService) changeRestoredWork(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var input struct {
		Kind      string   `json:"kind"`
		IDs       []string `json:"ids"`
		Action    string   `json:"action"`
		Confirmed bool     `json:"confirmed"`
	}
	if !readJSON(w, r, &input, 32<<10) {
		return
	}
	if !input.Confirmed || (input.Kind != "notification" && input.Kind != "updater") || (input.Action != "resume" && input.Action != "cancel") || len(input.IDs) < 1 || len(input.IDs) > 100 {
		jsonError(w, 400, "INVALID_WORK_ACTION", "เลือกงานไม่เกิน 100 รายการและยืนยันการดำเนินการ")
		return
	}
	if input.Kind == "updater" && input.Action == "resume" {
		jsonError(w, 409, "UPDATER_RETIRED", "งานอัปเดตที่กู้คืนยกเลิกได้เท่านั้น ระบบจะไม่เปิดใช้ตัวอัปเดตเดิม")
		return
	}
	ids := make([]any, 0, len(input.IDs))
	seen := map[string]bool{}
	for _, id := range input.IDs {
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "\x00\r\n") {
			jsonError(w, 400, "INVALID_WORK_ID", "รหัสงานไม่ถูกต้อง")
			return
		}
		if prefix, localID, found := strings.Cut(id, ":"); found {
			if prefix != input.Kind || localID == "" {
				jsonError(w, 400, "INVALID_WORK_ID", "ชนิดงานไม่ตรงกับรายการที่เลือก")
				return
			}
			id = localID
		}
		if input.Kind == "notification" {
			value, err := strconv.ParseInt(id, 10, 64)
			if err != nil || value < 1 {
				jsonError(w, 400, "INVALID_WORK_ID", "รหัสงานแจ้งเตือนไม่ถูกต้อง")
				return
			}
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	now := time.Now().Unix()
	var query string
	args := []any{}
	if input.Kind == "notification" {
		status := "cancelled"
		if input.Action == "resume" {
			status = "pending"
		}
		query = "UPDATE notify_jobs SET status=?,available_at=? WHERE status='quarantined' AND id IN (" + placeholders + ")"
		args = []any{status, now}
	} else {
		query = "UPDATE update_jobs SET status='cancelled',stage='restored_quarantine_cancelled',message='Owner cancelled restored pending work',updated_at=? WHERE status='blocked' AND stage='restored_quarantine' AND id IN (" + placeholders + ")"
		args = []any{now}
	}
	result, err := db.ExecContext(r.Context(), query, append(args, ids...)...)
	if err != nil {
		jsonError(w, 503, "WORK_CHANGE_FAILED", "บันทึกการจัดการงานค้างไม่สำเร็จ กรุณาลองใหม่")
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		jsonError(w, 503, "WORK_CHANGE_FAILED", "ตรวจผลการจัดการงานไม่ได้")
		return
	}
	jsonData(w, map[string]int64{"changed": changed})
}

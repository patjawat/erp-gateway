package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Cursors carry a stable key and are scoped to the server-side filters. They
// never grant access; every page still requires the administrator session.
type adminPageCursor struct {
	Scope string `json:"s"`
	Key   string `json:"k"`
	ID    string `json:"i,omitempty"`
	Time  int64  `json:"t,omitempty"`
}

func pageScope(values ...string) string {
	b, _ := json.Marshal(values)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}
func pageRequest(w http.ResponseWriter, r *http.Request, scope string) (int, adminPageCursor, bool) {
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fail(w, 400, "INVALID_PAGE", "จำนวนต่อหน้าต้องอยู่ระหว่าง 1 ถึง 100")
			return 0, adminPageCursor{}, false
		}
		limit = n
	}
	c := adminPageCursor{Scope: scope}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 2048 || err != nil || json.Unmarshal(b, &c) != nil || c.Scope != scope || c.Key == "" {
			fail(w, 400, "INVALID_CURSOR", "หน้ารายการไม่ตรงกับตัวกรอง กรุณาโหลดรายการใหม่")
			return 0, c, false
		}
	}
	return limit, c, true
}
func nextAdminCursor(c adminPageCursor) *string {
	b, _ := json.Marshal(c)
	v := base64.RawURLEncoding.EncodeToString(b)
	return &v
}

type hospitalSummary struct {
	Total    int64 `json:"total"`
	Enabled  int64 `json:"enabled"`
	Pending  int64 `json:"pending"`
	Disabled int64 `json:"disabled"`
	Online   int64 `json:"online"`
	Offline  int64 `json:"offline"`
	Unknown  int64 `json:"unknown"`
}

const hospitalSummarySQL = `COUNT(*),COALESCE(SUM(enabled=1),0),COALESCE(SUM(approval_status='pending'),0),COALESCE(SUM(enabled=0),0),COALESCE(SUM(connection_status='online'),0),COALESCE(SUM(connection_status='offline'),0),COALESCE(SUM(connection_status NOT IN ('online','offline')),0)`

func scanHospitalSummary(row scanner) (hospitalSummary, error) {
	var v hospitalSummary
	err := row.Scan(&v.Total, &v.Enabled, &v.Pending, &v.Disabled, &v.Online, &v.Offline, &v.Unknown)
	return v, err
}
func likeText(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return "%" + q + "%"
}
func (s *Server) adminHospitalPage(w http.ResponseWriter, r *http.Request) {
	q, status := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("status")
	if status == "" {
		status = "all"
	}
	if len(q) > 200 {
		fail(w, 400, "INVALID_FILTER", "คำค้นหายาวเกิน 200 ไบต์")
		return
	}
	where, args := "1=1", []any{}
	if q != "" {
		where += ` AND (name LIKE ? ESCAPE '\' OR code LIKE ? ESCAPE '\' OR province LIKE ? ESCAPE '\')`
		for range 3 {
			args = append(args, likeText(q))
		}
	}
	switch status {
	case "all":
	case "pending":
		where += " AND approval_status='pending'"
	case "enabled":
		where += " AND enabled=1"
	case "disabled":
		where += " AND enabled=0"
	case "online", "offline":
		where += " AND connection_status=?"
		args = append(args, status)
	default:
		fail(w, 400, "INVALID_FILTER", "สถานะตัวกรองไม่ถูกต้อง")
		return
	}
	limit, c, ok := pageRequest(w, r, pageScope("hospitals", q, status))
	if !ok {
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	summary, err := scanHospitalSummary(tx.QueryRowContext(r.Context(), "SELECT "+hospitalSummarySQL+" FROM hospitals WHERE "+where, args...))
	if err != nil {
		unavailable(w)
		return
	}
	pageArgs := append([]any{}, args...)
	if c.Key != "" {
		where += " AND (code>? OR (code=? AND id>?))"
		pageArgs = append(pageArgs, c.Key, c.Key, c.ID)
	}
	pageArgs = append(pageArgs, limit+1)
	rows, err := tx.QueryContext(r.Context(), "SELECT "+hospitalColumns+" FROM hospitals WHERE "+where+" ORDER BY code,id LIMIT ?", pageArgs...)
	if err != nil {
		unavailable(w)
		return
	}
	items := make([]Hospital, 0, limit+1)
	for rows.Next() {
		h, e := scanHospital(rows)
		if e != nil {
			rows.Close()
			unavailable(w)
			return
		}
		items = append(items, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		unavailable(w)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = nextAdminCursor(adminPageCursor{Scope: c.Scope, Key: last.Code, ID: last.ID})
	}
	if err = s.attachUsagePage(r.Context(), tx, items); err != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]any{"hospitals": items, "total": summary.Total, "summary": summary, "next_cursor": next})
}

func (s *Server) adminAuditPage(w http.ResponseWriter, r *http.Request) {
	q, hospital, action := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("hospital_id"), r.URL.Query().Get("action")
	if len(q) > 200 || len(hospital) > 128 || len(action) > 100 {
		fail(w, 400, "INVALID_FILTER", "ตัวกรองยาวเกินกำหนด")
		return
	}
	where, args := "1=1", []any{}
	if q != "" {
		where += ` AND (hospital_code LIKE ? ESCAPE '\' OR action LIKE ? ESCAPE '\' OR reason LIKE ? ESCAPE '\')`
		for range 3 {
			args = append(args, likeText(q))
		}
	}
	if hospital != "" {
		where += " AND hospital_id=?"
		args = append(args, hospital)
	}
	if action != "" {
		where += " AND action=?"
		args = append(args, action)
	}
	for _, field := range []string{"after", "before"} {
		if v := r.URL.Query().Get(field); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				fail(w, 400, "INVALID_FILTER", "ช่วงเวลาต้องเป็น Unix timestamp")
				return
			}
			if field == "after" {
				where += " AND created_at>=?"
			} else {
				where += " AND created_at<?"
			}
			args = append(args, n)
		}
	}
	limit, c, ok := pageRequest(w, r, pageScope("audit", q, hospital, action, r.URL.Query().Get("after"), r.URL.Query().Get("before")))
	if !ok {
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM audit WHERE "+where, args...).Scan(&total); err != nil {
		unavailable(w)
		return
	}
	if c.Key != "" {
		n, e := strconv.ParseInt(c.Key, 10, 64)
		if e != nil || n < 1 {
			fail(w, 400, "INVALID_CURSOR", "หน้าประวัติไม่ถูกต้อง")
			return
		}
		where += " AND id<?"
		args = append(args, n)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(r.Context(), "SELECT id,hospital_id,hospital_code,action,reason,created_at FROM audit WHERE "+where+" ORDER BY id DESC LIMIT ?", args...)
	if err != nil {
		unavailable(w)
		return
	}
	items := make([]Audit, 0, limit+1)
	for rows.Next() {
		var a Audit
		if e := rows.Scan(&a.ID, &a.HospitalID, &a.HospitalCode, &a.Action, &a.Reason, &a.CreatedAt); e != nil {
			rows.Close()
			unavailable(w)
			return
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		unavailable(w)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		next = nextAdminCursor(adminPageCursor{Scope: c.Scope, Key: strconv.FormatInt(items[len(items)-1].ID, 10)})
	}
	writeData(w, map[string]any{"events": items, "next_cursor": next, "total": total})
}

func (s *Server) adminAttachmentPage(w http.ResponseWriter, r *http.Request, h Hospital) {
	limit, c, ok := pageRequest(w, r, pageScope("attachments", h.ID))
	if !ok {
		return
	}
	where, args := "hospital_id=?", []any{h.ID}
	tx, err := s.config.Store.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM hospital_attachments WHERE "+where, args...).Scan(&total); err != nil {
		unavailable(w)
		return
	}
	if c.Key != "" {
		if c.Time < 0 {
			fail(w, 400, "INVALID_CURSOR", "หน้ารายการไม่ถูกต้อง")
			return
		}
		where += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, c.Time, c.Time, c.Key)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(r.Context(), "SELECT id,hospital_id,filename,content_type,category,size,created_at FROM hospital_attachments WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ?", args...)
	if err != nil {
		unavailable(w)
		return
	}
	items := make([]hospitalAttachment, 0, limit+1)
	for rows.Next() {
		var a hospitalAttachment
		if e := rows.Scan(&a.ID, &a.HospitalID, &a.Filename, &a.ContentType, &a.Category, &a.Size, &a.CreatedAt); e != nil {
			rows.Close()
			unavailable(w)
			return
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		unavailable(w)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = nextAdminCursor(adminPageCursor{Scope: c.Scope, Key: last.ID, Time: last.CreatedAt})
	}
	writeData(w, map[string]any{"attachments": items, "total": total, "next_cursor": next})
}

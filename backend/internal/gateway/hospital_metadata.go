package gateway

import (
	"net/http"
	"strings"
	"time"
)

// Existing installations gain optional dates without changing their access state.
// Attachments stay in the same private database and backup as the hospital record.
func (s *Store) migrateHospitalMetadata() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT name FROM pragma_table_info('hospitals')")
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []string{"start_date", "expiry_date"} {
		if !columns[column] {
			if _, err := tx.Exec("ALTER TABLE hospitals ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS hospital_attachments (
 id TEXT PRIMARY KEY, hospital_id TEXT NOT NULL REFERENCES hospitals(id),
 filename TEXT NOT NULL, content_type TEXT NOT NULL, category TEXT NOT NULL,
 size INTEGER NOT NULL, content BLOB NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS hospital_attachments_hospital ON hospital_attachments(hospital_id,created_at);`); err != nil {
		return err
	}
	return tx.Commit()
}

func validHospitalDate(value string) bool {
	if value == "" {
		return true
	}
	date, err := time.Parse(time.DateOnly, value)
	return err == nil && date.Year() >= 1 && date.Format(time.DateOnly) == value
}

// Omitted fields retain their values for older admin clients; empty fields clear them.
func hospitalDates(w http.ResponseWriter, h Hospital, in hospitalInput) (string, string, bool) {
	start, expiry := h.StartDate, h.ExpiryDate
	if in.StartDate != nil {
		start = strings.TrimSpace(*in.StartDate)
	}
	if in.ExpiryDate != nil {
		expiry = strings.TrimSpace(*in.ExpiryDate)
	}
	if !validHospitalDate(start) || !validHospitalDate(expiry) || (start != "" && expiry != "" && expiry < start) {
		fail(w, http.StatusBadRequest, "INVALID_HOSPITAL_DATES", "กรุณาระบุวันที่เป็น YYYY-MM-DD และวันหมดอายุต้องไม่ก่อนวันเริ่มใช้งาน")
		return "", "", false
	}
	return start, expiry, true
}

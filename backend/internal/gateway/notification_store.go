package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"erp-platform/gateway/internal/contracts"
)

func (s *Store) migrateNotifications() error {
	_, e := s.db.Exec(`CREATE TABLE IF NOT EXISTS notify_devices(installation TEXT PRIMARY KEY,hospital_id TEXT NOT NULL REFERENCES hospitals(id),user_id TEXT NOT NULL,session_hash TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,token BLOB NOT NULL,platform TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,updated_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS notify_device_recipient ON notify_devices(hospital_id,user_id,enabled);
CREATE TABLE IF NOT EXISTS notify_receipts(hospital_id TEXT NOT NULL,event_id TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(hospital_id,event_id));
CREATE TABLE IF NOT EXISTS notify_jobs(id INTEGER PRIMARY KEY AUTOINCREMENT,hospital_id TEXT NOT NULL,event_id TEXT NOT NULL,installation TEXT NOT NULL,token_hash TEXT NOT NULL,payload TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,available_at INTEGER NOT NULL,created_at INTEGER NOT NULL,UNIQUE(hospital_id,event_id,installation));
CREATE INDEX IF NOT EXISTS notify_jobs_pending ON notify_jobs(available_at,id) WHERE status='pending';`)
	return e
}
func (s *Store) registerNotificationDevice(ctx context.Context, h Hospital, sessionToken string, in contracts.NotificationDeviceInput) error {
	v, e := s.session(ctx, sessionToken)
	if e != nil || v.hospitalID != h.ID || v.revoked || v.expires <= time.Now().Unix() {
		return errors.New("invalid device session")
	}
	user := identity(json.RawMessage(v.userID))
	if user == "" {
		return errors.New("invalid device identity")
	}
	encrypted, e := s.encrypt("notify-device:"+in.InstallationID, in.FID)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM notify_devices d JOIN sessions s ON s.token_hash=d.session_hash WHERE d.hospital_id=? AND d.user_id=? AND d.installation<>? AND d.enabled=1 AND s.revoked=0 AND s.expires_at>?", h.ID, user, in.InstallationID, time.Now().Unix()).Scan(&count); e != nil {
		return e
	}
	if count >= 10 {
		return errors.New("notification device limit reached")
	}
	// A token/installation belongs to one active account at a time. Rebinding
	// invalidates jobs for an old account without leaking its token or history.
	_, e = tx.ExecContext(ctx, "DELETE FROM notify_devices WHERE token_hash=? AND installation<>?", tokenHash(in.FID), in.InstallationID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO notify_devices(installation,hospital_id,user_id,session_hash,token_hash,token,platform,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(installation) DO UPDATE SET hospital_id=excluded.hospital_id,user_id=excluded.user_id,session_hash=excluded.session_hash,token_hash=excluded.token_hash,token=excluded.token,platform=excluded.platform,enabled=1,updated_at=excluded.updated_at`, in.InstallationID, h.ID, user, tokenHash(sessionToken), tokenHash(in.FID), encrypted, in.Platform, time.Now().Unix())
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) removeNotificationDevice(ctx context.Context, sessionToken, installation string) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM notify_devices WHERE installation=? AND session_hash=?", installation, tokenHash(sessionToken))
	return e
}
func (s *Store) ingestNotificationBatch(ctx context.Context, h Hospital, b contracts.NotificationPushBatch) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, item := range b.Items {
		r, e := tx.ExecContext(ctx, "INSERT INTO notify_receipts(hospital_id,event_id,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING", h.ID, item.ID, now)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n == 0 || item.Notification.IsRead {
			continue
		}
		raw, e := json.Marshal(item)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO notify_jobs(hospital_id,event_id,installation,token_hash,payload,available_at,created_at) SELECT ?,?,d.installation,d.token_hash,?,?,? FROM notify_devices d JOIN sessions s ON s.token_hash=d.session_hash WHERE d.hospital_id=? AND d.user_id=? AND d.enabled=1 AND s.revoked=0 AND s.expires_at>? AND d.updated_at>? ON CONFLICT DO NOTHING`, h.ID, item.ID, string(raw), now, now, h.ID, item.UserID, now, now-90*86400)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

type notificationJob struct {
	id                                                                       int64
	hospital, event, installation, tokenHash, sessionHash, token, localToken string
	payload                                                                  contracts.NotificationPushItem
	attempts                                                                 int
	created                                                                  int64
}

func (s *Store) nextNotificationJobs(ctx context.Context) ([]notificationJob, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,hospital_id,event_id,installation,token_hash,payload,attempts,created_at FROM notify_jobs WHERE status='pending' AND available_at<=? ORDER BY available_at,id LIMIT 50`, time.Now().Unix())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	jobs := []notificationJob{}
	for rows.Next() {
		var j notificationJob
		var raw string
		if e = rows.Scan(&j.id, &j.hospital, &j.event, &j.installation, &j.tokenHash, &raw, &j.attempts, &j.created); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &j.payload); e != nil {
			return nil, e
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (s *Store) notificationJobToken(ctx context.Context, j *notificationJob) (bool, error) {
	var encrypted []byte
	var localEncrypted []byte
	var rawUser string
	e := s.db.QueryRowContext(ctx, `SELECT d.token,d.session_hash,s.user_id,s.erp_token FROM notify_devices d JOIN sessions s ON s.token_hash=d.session_hash WHERE d.installation=? AND d.token_hash=? AND d.hospital_id=? AND d.user_id=? AND d.enabled=1 AND s.revoked=0 AND s.expires_at>? AND d.updated_at>?`, j.installation, j.tokenHash, j.hospital, j.payload.UserID, time.Now().Unix(), time.Now().Add(-90*24*time.Hour).Unix()).Scan(&encrypted, &j.sessionHash, &rawUser, &localEncrypted)
	if e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if identity(json.RawMessage(rawUser)) != j.payload.UserID {
		return false, nil
	}
	j.localToken, e = s.decrypt("session:"+j.hospital+":"+j.sessionHash, localEncrypted)
	if e != nil {
		return false, e
	}
	j.token, e = s.decrypt("notify-device:"+j.installation, encrypted)
	return e == nil, e
}
func (s *Store) finishNotificationJob(ctx context.Context, j notificationJob, status string, delay time.Duration) error {
	_, e := s.db.ExecContext(ctx, "UPDATE notify_jobs SET status=?,attempts=attempts+1,available_at=? WHERE id=?", status, time.Now().Add(delay).Unix(), j.id)
	return e
}
func (s *Store) notificationHospitals(ctx context.Context, after string) ([]Hospital, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+hospitalColumns+" FROM hospitals WHERE id>? AND enabled=1 AND approval_status='approved' ORDER BY id LIMIT 5", after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	v := []Hospital{}
	for rows.Next() {
		h, e := scanHospital(rows)
		if e != nil {
			return nil, e
		}
		v = append(v, h)
	}
	return v, rows.Err()
}
func validPushBatch(b contracts.NotificationPushBatch) bool {
	if b.Items == nil || len(b.Items) > contracts.NotificationBatchSize {
		return false
	}
	seen := map[string]bool{}
	for _, v := range b.Items {
		if !contracts.ValidNotificationID(v.ID) || !contracts.ValidNotificationID(v.UserID) || v.Notification.ID != v.ID || !contracts.ValidNotification(v.Notification) || v.UnreadCount < 0 || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	return true
}
func notificationJobKey(j notificationJob) string {
	return fmt.Sprintf("%s:%s:%s", j.hospital, j.event, j.installation)
}

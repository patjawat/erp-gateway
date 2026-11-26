package gateway

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"erp-platform/gateway/internal/contracts"
	_ "modernc.org/sqlite"
)

type Hospital struct {
	contracts.Hospital
	APIURL           string             `json:"api_url"`
	ApprovalStatus   string             `json:"approval_status"`
	Enabled          bool               `json:"enabled"`
	ConnectionStatus string             `json:"connection_status"`
	CheckedAt        *int64             `json:"checked_at"`
	CreatedAt        int64              `json:"created_at"`
	StartDate        string             `json:"start_date"`
	ExpiryDate       string             `json:"expiry_date"`
	Usage            *hospitalUsageView `json:"usage,omitempty"`
	key              []byte
}

type Audit struct {
	ID           int64  `json:"id"`
	HospitalID   string `json:"hospital_id"`
	HospitalCode string `json:"hospital_code"`
	Action       string `json:"action"`
	Reason       string `json:"reason"`
	CreatedAt    int64  `json:"created_at"`
}

type session struct {
	hospitalID string
	userID     string
	upstream   []byte
	expires    int64
	revoked    bool
}

type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

func OpenStore(path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be exactly 32 bytes")
	}
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		f.Close()
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, aead: aead}
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS hospitals (
 id TEXT PRIMARY KEY, code TEXT NOT NULL UNIQUE, name TEXT NOT NULL, province TEXT NOT NULL,
 api_url TEXT NOT NULL, gateway_key BLOB NOT NULL, approval_status TEXT NOT NULL DEFAULT 'pending',
 enabled INTEGER NOT NULL DEFAULT 0, connection_status TEXT NOT NULL DEFAULT 'unknown',
 checked_at INTEGER, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash TEXT PRIMARY KEY, hospital_id TEXT NOT NULL REFERENCES hospitals(id), user_id TEXT NOT NULL,
 erp_token BLOB NOT NULL, expires_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS sessions_hospital ON sessions(hospital_id,user_id);
CREATE TABLE IF NOT EXISTS audit (
 id INTEGER PRIMARY KEY AUTOINCREMENT, hospital_id TEXT NOT NULL, hospital_code TEXT NOT NULL,
 action TEXT NOT NULL, reason TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS secrets_check (id INTEGER PRIMARY KEY CHECK(id=1), value BLOB NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Refuse a wrong key at startup, before an operator can overwrite credentials.
	var check []byte
	err = db.QueryRow("SELECT value FROM secrets_check WHERE id=1").Scan(&check)
	if errors.Is(err, sql.ErrNoRows) {
		check, err = s.encrypt("store-check", "erp-platform-gateway")
		if err == nil {
			_, err = db.Exec("INSERT INTO secrets_check(id,value) VALUES(1,?)", check)
		}
	} else if err == nil {
		var plain string
		plain, err = s.decrypt("store-check", check)
		if err == nil && plain != "erp-platform-gateway" {
			err = errors.New("invalid encryption key")
		}
	}
	if err != nil {
		db.Close()
		return nil, errors.New("cannot unlock gateway database with configured encryption key")
	}
	if err := s.migrateHospitalMetadata(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate hospital metadata: %w", err)
	}
	if err := s.migrateNotifications(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateUpdates(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateUsage(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Store) encrypt(purpose, value string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(value), []byte(purpose)), nil
}
func (s *Store) decrypt(purpose string, value []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(value) < n {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := s.aead.Open(nil, value[:n], value[n:], []byte(purpose))
	return string(plain), err
}

const hospitalColumns = "id,code,name,province,api_url,gateway_key,approval_status,enabled,connection_status,checked_at,created_at,start_date,expiry_date"

type scanner interface{ Scan(...any) error }

func scanHospital(row scanner) (Hospital, error) {
	var h Hospital
	err := row.Scan(&h.ID, &h.Code, &h.Name, &h.Province, &h.APIURL, &h.key, &h.ApprovalStatus, &h.Enabled, &h.ConnectionStatus, &h.CheckedAt, &h.CreatedAt, &h.StartDate, &h.ExpiryDate)
	return h, err
}
func (s *Store) hospital(ctx context.Context, field, value string) (Hospital, error) {
	if field != "id" && field != "code" {
		return Hospital{}, errors.New("invalid lookup")
	}
	return scanHospital(s.db.QueryRowContext(ctx, "SELECT "+hospitalColumns+" FROM hospitals WHERE "+field+"=?", value))
}
func (s *Store) hospitals(ctx context.Context) ([]Hospital, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+hospitalColumns+" FROM hospitals ORDER BY name,code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Hospital, 0)
	for rows.Next() {
		h, err := scanHospital(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, rows.Err()
}
func (s *Store) addHospital(ctx context.Context, h Hospital, key string) (Hospital, error) {
	id, err := randomToken()
	if err != nil {
		return h, err
	}
	h.ID = id
	h.ApprovalStatus = "pending"
	h.ConnectionStatus = "unknown"
	h.CreatedAt = time.Now().Unix()
	h.key, err = s.encrypt("hospital:"+id, key)
	if err != nil {
		return h, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return h, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO hospitals(id,code,name,province,api_url,gateway_key,created_at,start_date,expiry_date) VALUES(?,?,?,?,?,?,?,?,?)`, h.ID, h.Code, h.Name, h.Province, h.APIURL, h.key, h.CreatedAt, h.StartDate, h.ExpiryDate)
	if err != nil {
		return h, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)", h.ID, h.Code, "create", "", h.CreatedAt)
	if err != nil {
		return h, err
	}
	return h, tx.Commit()
}
func (s *Store) connection(ctx context.Context, snapshot Hospital, online bool) error {
	status := "offline"
	if online {
		status = "online"
	}
	result, err := s.db.ExecContext(ctx, "UPDATE hospitals SET connection_status=?,checked_at=? WHERE id=? AND api_url=? AND gateway_key=? AND name=? AND province=?", status, time.Now().Unix(), snapshot.ID, snapshot.APIURL, snapshot.key, snapshot.Name, snapshot.Province)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return errConfigChanged
	}
	return err
}
func (s *Store) change(ctx context.Context, id, action, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	h, err := scanHospital(tx.QueryRowContext(ctx, "SELECT "+hospitalColumns+" FROM hospitals WHERE id=?", id))
	if err != nil {
		return err
	}
	switch action {
	case "approve", "enable":
		if action == "enable" && h.ApprovalStatus != "approved" {
			return errors.New("hospital must be approved first")
		}
		if h.ConnectionStatus != "online" || h.CheckedAt == nil || *h.CheckedAt < time.Now().Add(-5*time.Minute).Unix() {
			return errors.New("test the connection successfully within five minutes first")
		}
		_, err = tx.ExecContext(ctx, "UPDATE hospitals SET approval_status='approved',enabled=1 WHERE id=?", id)
	case "suspend":
		_, err = tx.ExecContext(ctx, "UPDATE hospitals SET enabled=0 WHERE id=?", id)
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE sessions SET revoked=1,erp_token=X'' WHERE hospital_id=?", id)
		}
	default:
		return errors.New("invalid action")
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)", id, h.Code, action, reason, time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) createSession(ctx context.Context, h Hospital, upstream contracts.Token) (string, error) {
	if upstream.AccessToken == "" || upstream.ExpiresAt <= time.Now().Unix() || identity(upstream.UserID) == "" {
		return "", errors.New("invalid local API token response")
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	encrypted, err := s.encrypt("session:"+h.ID+":"+tokenHash(token), upstream.AccessToken)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	current, err := scanHospital(tx.QueryRowContext(ctx, "SELECT "+hospitalColumns+" FROM hospitals WHERE id=?", h.ID))
	if err != nil {
		return "", err
	}
	if !active(current) {
		return "", errDisabled
	}
	if current.APIURL != h.APIURL || !bytes.Equal(current.key, h.key) {
		return "", errConfigChanged
	}
	// Keep one active mobile session per user and hospital independently of ERP web sessions.
	_, err = tx.ExecContext(ctx, "UPDATE sessions SET revoked=1,erp_token=X'' WHERE hospital_id=? AND user_id=?", h.ID, string(upstream.UserID))
	if err != nil {
		return "", err
	}
	expires := upstream.ExpiresAt
	if max := time.Now().Add(24 * time.Hour).Unix(); expires > max {
		expires = max
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix())
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,hospital_id,user_id,erp_token,expires_at) VALUES(?,?,?,?,?)", tokenHash(token), h.ID, string(upstream.UserID), encrypted, expires)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}
func (s *Store) session(ctx context.Context, token string) (session, error) {
	var v session
	err := s.db.QueryRowContext(ctx, "SELECT hospital_id,user_id,erp_token,expires_at,revoked FROM sessions WHERE token_hash=?", tokenHash(token)).Scan(&v.hospitalID, &v.userID, &v.upstream, &v.expires, &v.revoked)
	return v, err
}
func (s *Store) revoke(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET revoked=1,erp_token=X'' WHERE token_hash=?", tokenHash(token))
	return err
}
func (s *Store) audits(ctx context.Context) ([]Audit, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,hospital_id,hospital_code,action,reason,created_at FROM audit ORDER BY id DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Audit, 0)
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.HospitalID, &a.HospitalCode, &a.Action, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

var errDisabled = fmt.Errorf("hospital is disabled")
var errConfigChanged = errors.New("hospital configuration changed")

func identity(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) != nil {
		text = string(value)
	}
	if len(text) == 0 || len(text) > 20 {
		return ""
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return text
}

package gateway

// The update channel is deliberately separate from Mobile sessions. It accepts
// signed immutable image manifests and gives an enrolled site only its own job.
import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const updateLeaseSeconds = 180

type updateManifest struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	Image             string `json:"image"`
	Architecture      string `json:"architecture"`
	MinUpdaterVersion string `json:"min_updater_version"`
	RollbackSafe      bool   `json:"rollback_safe"`
	Signature         string `json:"signature"`
	CreatedAt         int64  `json:"created_at,omitempty"`
}

func (m updateManifest) signingBytes() []byte {
	return []byte(fmt.Sprintf("hospital-api-release.v1\n%s\n%s\n%s\n%s\n%s\n%t\n", m.ID, m.Version, m.Image, m.Architecture, m.MinUpdaterVersion, m.RollbackSafe))
}

var updateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
var updateImagePattern = regexp.MustCompile(`^([a-z0-9][a-z0-9._:/-]+)@sha256:([a-f0-9]{64})$`)
var updateVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})$`)

func updateVersion(v string) int {
	if !updateVersionPattern.MatchString(v) {
		return -1
	}
	p := strings.Split(v, ".")
	a, _ := strconv.Atoi(p[0])
	b, _ := strconv.Atoi(p[1])
	c, _ := strconv.Atoi(p[2])
	return a*1000000 + b*1000 + c
}

func updatePolicy() (ed25519.PublicKey, []string) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("GATEWAY_UPDATE_SIGNING_PUBLIC_KEY")))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, nil
	}
	var repos []string
	for _, repo := range strings.Split(os.Getenv("GATEWAY_UPDATE_REPOSITORIES"), ",") {
		repo = strings.TrimSpace(repo)
		if updateImagePattern.MatchString(repo + "@sha256:" + strings.Repeat("a", 64)) {
			repos = append(repos, repo)
		}
	}
	return ed25519.PublicKey(key), repos
}

func verifyUpdateManifest(m updateManifest) error {
	key, repos := updatePolicy()
	if len(key) == 0 || len(repos) == 0 {
		return errors.New("configure the release signing public key and allowed repositories on Gateway first")
	}
	match := updateImagePattern.FindStringSubmatch(m.Image)
	if !updateIDPattern.MatchString(m.ID) || updateVersion(m.Version) < 0 || updateVersion(m.MinUpdaterVersion) < 0 || (m.Architecture != "amd64" && m.Architecture != "arm64") || match == nil {
		return errors.New("invalid immutable release manifest")
	}
	allowed := false
	for _, repo := range repos {
		if repo == match[1] {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("image repository is not approved on Gateway")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || !ed25519.Verify(key, m.signingBytes(), sig) {
		return errors.New("release signature is invalid")
	}
	return nil
}

func (s *Store) migrateUpdates() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS update_agents (
 hospital_id TEXT PRIMARY KEY REFERENCES hospitals(id), token_hash TEXT NOT NULL UNIQUE,
 architecture TEXT NOT NULL DEFAULT '', updater_version TEXT NOT NULL DEFAULT '', version_number INTEGER NOT NULL DEFAULT 0,
 actual_image TEXT NOT NULL DEFAULT '', last_seen INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS update_releases (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,version TEXT NOT NULL,image TEXT NOT NULL,
 architecture TEXT NOT NULL,min_version INTEGER NOT NULL,manifest TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS update_rollouts (
 id TEXT PRIMARY KEY, release_id TEXT NOT NULL REFERENCES update_releases(id),max_parallel INTEGER NOT NULL,
 state TEXT NOT NULL DEFAULT 'active',pilot_job_id TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS update_jobs (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,rollout_id TEXT NOT NULL REFERENCES update_rollouts(id),
 hospital_id TEXT NOT NULL REFERENCES hospitals(id), release_id TEXT NOT NULL REFERENCES update_releases(id),
 status TEXT NOT NULL DEFAULT 'queued',stage TEXT NOT NULL DEFAULT 'queued',message TEXT NOT NULL DEFAULT '',
 lease_hash TEXT NOT NULL DEFAULT '',lease_until INTEGER NOT NULL DEFAULT 0,report_seq INTEGER NOT NULL DEFAULT 0,report_hash TEXT NOT NULL DEFAULT '',
 actual_image TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,expires_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS update_jobs_site ON update_jobs(hospital_id,status);
CREATE INDEX IF NOT EXISTS update_jobs_rollout ON update_jobs(rollout_id,status);`)
	if err != nil {
		return err
	}
	// Additive migration also covers an interrupted deployment of the first schema.
	rows, e := s.db.Query("PRAGMA table_info(update_jobs)")
	if e != nil {
		return e
	}
	found := false
	for rows.Next() {
		var n, notnull, pk int
		var name, kind string
		var def any
		if e = rows.Scan(&n, &name, &kind, &notnull, &def, &pk); e != nil {
			rows.Close()
			return e
		}
		if name == "report_hash" {
			found = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if !found {
		_, e = s.db.Exec("ALTER TABLE update_jobs ADD COLUMN report_hash TEXT NOT NULL DEFAULT ''")
	}
	return e
}

func (s *Server) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/updates/status", s.adminUpdateStatus)
	mux.HandleFunc("GET /admin/api/updates/releases", s.adminUpdateReleases)
	mux.HandleFunc("POST /admin/api/updates/releases", s.adminPublishUpdate)
	mux.HandleFunc("GET /admin/api/updates/jobs", s.adminUpdateJobs)
	mux.HandleFunc("POST /admin/api/updates/jobs", s.adminDispatchUpdate)
	mux.HandleFunc("POST /admin/api/updates/rollouts/{id}/pause", s.adminPauseRollout)
	mux.HandleFunc("GET /admin/api/updates/agents", s.adminUpdateAgents)
	mux.HandleFunc("POST /admin/api/hospitals/{id}/updater-token", s.adminUpdaterToken)
	mux.HandleFunc("POST /agent/v1/poll", s.agentUpdatePoll)
	mux.HandleFunc("POST /agent/v1/jobs/{id}/report", s.agentUpdateReport)
	mux.HandleFunc("POST /release/v1/catalog", s.publishReleaseFromCI)
}

func updatePage(w http.ResponseWriter, r *http.Request) (int, int64, bool) {
	limit := 20
	var cursor int64
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			fail(w, 400, "INVALID_PAGE", "ขนาดหน้าต้องอยู่ระหว่าง 1-100")
			return 0, 0, false
		}
		limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < 0 {
			fail(w, 400, "INVALID_CURSOR", "ตำแหน่งรายการไม่ถูกต้อง")
			return 0, 0, false
		}
		cursor = n
	}
	return limit, cursor, true
}

func (s *Server) adminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	key, repos := updatePolicy()
	counts := map[string]int{"queued": 0, "running": 0, "awaiting_external": 0, "succeeded": 0, "failed": 0, "blocked": 0, "rolled_back": 0}
	rows, err := s.config.Store.db.QueryContext(r.Context(), "SELECT status,COUNT(*) FROM update_jobs GROUP BY status")
	if err != nil {
		unavailable(w)
		return
	}
	for rows.Next() {
		var status string
		var n int
		if rows.Scan(&status, &n) != nil {
			rows.Close()
			unavailable(w)
			return
		}
		counts[status] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		unavailable(w)
		return
	}
	var agents int
	if s.config.Store.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM update_agents").Scan(&agents) != nil {
		unavailable(w)
		return
	}
	fingerprint := ""
	if len(key) > 0 {
		sum := sha256.Sum256(key)
		fingerprint = hex.EncodeToString(sum[:])
	}
	writeData(w, map[string]any{"configured": len(key) > 0 && len(repos) > 0, "repositories": repos, "signing_key_fingerprint": fingerprint, "counts": counts, "agents_total": agents, "transport": "oci-digest", "updater_version": "1.0.0"})
}

func (s *Server) adminPublishUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	s.publishUpdate(w, r)
}

func (s *Server) publishReleaseFromCI(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	secure := r.TLS != nil || (s.trusted(net.ParseIP(host)) && r.Header.Get("X-Forwarded-Proto") == "https")
	expected := os.Getenv("GATEWAY_RELEASE_PUBLISH_TOKEN_SHA256")
	match := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
	if !secure || len(expected) != 64 || match == nil || !equal(expected, tokenHash(match[1])) {
		fail(w, 401, "PUBLISHER_UNAUTHORIZED", "Invalid release publisher credential")
		return
	}
	if !s.limit(w, r, "release-publish:"+s.clientIP(r), 10, time.Minute) {
		return
	}
	s.publishUpdate(w, r)
}

func (s *Server) publishUpdate(w http.ResponseWriter, r *http.Request) {
	var m updateManifest
	if !decode(w, r, &m) {
		return
	}
	if err := verifyUpdateManifest(m); err != nil {
		fail(w, 400, "RELEASE_REJECTED", err.Error())
		return
	}
	m.CreatedAt = time.Now().Unix()
	b, _ := json.Marshal(m)
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO update_releases(id,version,image,architecture,min_version,manifest,created_at) VALUES(?,?,?,?,?,?,?)", m.ID, m.Version, m.Image, m.Architecture, updateVersion(m.MinUpdaterVersion), string(b), m.CreatedAt); err != nil {
		fail(w, 409, "RELEASE_EXISTS", "รหัสรุ่นนี้มีอยู่แล้วและแก้ไขไม่ได้")
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','release-published',?,?)", m.ID, m.CreatedAt)
	if err != nil || tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, m)
}

func (s *Server) adminUpdateReleases(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	limit, cursor, ok := updatePage(w, r)
	if !ok {
		return
	}
	where := "1=1"
	args := []any{}
	arch := r.URL.Query().Get("architecture")
	if arch != "" {
		if arch != "amd64" && arch != "arm64" {
			fail(w, 400, "INVALID_ARCHITECTURE", "ไม่รองรับสถาปัตยกรรมนี้")
			return
		}
		where += " AND architecture=?"
		args = append(args, arch)
	}
	var total int
	if s.config.Store.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM update_releases WHERE "+where, args...).Scan(&total) != nil {
		unavailable(w)
		return
	}
	if cursor > 0 {
		where += " AND seq<?"
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := s.config.Store.db.QueryContext(r.Context(), "SELECT seq,manifest FROM update_releases WHERE "+where+" ORDER BY seq DESC LIMIT ?", args...)
	if err != nil {
		unavailable(w)
		return
	}
	defer rows.Close()
	list := []updateManifest{}
	var next string
	var last int64
	for rows.Next() {
		var seq int64
		var raw string
		if rows.Scan(&seq, &raw) != nil {
			unavailable(w)
			return
		}
		if len(list) == limit {
			next = strconv.FormatInt(last, 10)
			break
		}
		var m updateManifest
		if json.Unmarshal([]byte(raw), &m) != nil {
			unavailable(w)
			return
		}
		list = append(list, m)
		last = seq
	}
	if rows.Err() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]any{"releases": list, "total": total, "next_cursor": next})
}

func (s *Server) adminUpdaterToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	h, err := s.config.Store.hospital(r.Context(), "id", r.PathValue("id"))
	if err != nil {
		fail(w, 404, "NOT_FOUND", "ไม่พบหน่วยงาน")
		return
	}
	token, err := randomToken()
	if err != nil {
		unavailable(w)
		return
	}
	now := time.Now().Unix()
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	var active int
	err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM update_jobs WHERE hospital_id=? AND status IN ('running','awaiting_external')", h.ID).Scan(&active)
	if err != nil {
		unavailable(w)
		return
	}
	if active > 0 {
		fail(w, 409, "UPDATE_RUNNING", "รอให้งานอัปเดตจบก่อนเปลี่ยนรหัส Updater")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO update_agents(hospital_id,token_hash,created_at) VALUES(?,?,?) ON CONFLICT(hospital_id) DO UPDATE SET token_hash=excluded.token_hash,last_seen=0`, h.ID, tokenHash(token), now)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)", h.ID, h.Code, "updater-token-rotated", "previous updater credential revoked", now)
	}
	if err != nil || tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]string{"token": token, "hospital_code": h.Code, "warning": "แสดงรหัสครั้งเดียว เก็บเป็น secret ที่เครื่อง Updater; รหัสเดิมใช้ไม่ได้แล้ว"})
}

func (s *Server) adminDispatchUpdate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	var in struct {
		ReleaseID   string   `json:"release_id"`
		Scope       string   `json:"scope"`
		HospitalIDs []string `json:"hospital_ids"`
		MaxParallel int      `json:"max_parallel"`
	}
	if !decode(w, r, &in) {
		return
	}
	if (in.Scope != "all" && in.Scope != "selected") || len(in.HospitalIDs) > 500 || (in.Scope == "selected" && len(in.HospitalIDs) == 0) {
		fail(w, 400, "INVALID_TARGETS", "เลือกหน่วยงานหรือเลือกทุกหน่วยงานที่รองรับ")
		return
	}
	if in.MaxParallel == 0 {
		in.MaxParallel = 2
	}
	if in.MaxParallel < 1 || in.MaxParallel > 5 {
		fail(w, 400, "INVALID_PARALLELISM", "อัปเดตพร้อมกันได้ 1-5 หน่วยงาน")
		return
	}
	var raw string
	err := s.config.Store.db.QueryRowContext(r.Context(), "SELECT manifest FROM update_releases WHERE id=?", in.ReleaseID).Scan(&raw)
	var manifest updateManifest
	if err != nil || json.Unmarshal([]byte(raw), &manifest) != nil {
		fail(w, 404, "RELEASE_NOT_FOUND", "ไม่พบรุ่นที่เลือก")
		return
	}
	if err = verifyUpdateManifest(manifest); err != nil {
		fail(w, 409, "RELEASE_REJECTED", err.Error())
		return
	}
	now := time.Now().Unix()
	rollout, err := randomToken()
	if err != nil {
		unavailable(w)
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	// A lease timeout requires reconciliation, never an automatic replay of apply.
	if err = expireUpdateJobs(r.Context(), tx, now); err != nil {
		unavailable(w)
		return
	}
	where := `h.enabled=1 AND h.approval_status='approved' AND a.architecture=? AND a.version_number>=? AND a.last_seen>0 AND NOT EXISTS(SELECT 1 FROM update_jobs j WHERE j.hospital_id=h.id AND j.status IN ('queued','running','awaiting_external'))`
	args := []any{manifest.Architecture, updateVersion(manifest.MinUpdaterVersion)}
	if in.Scope == "selected" {
		marks := make([]string, len(in.HospitalIDs))
		for i, id := range in.HospitalIDs {
			marks[i] = "?"
			args = append(args, id)
		}
		where += " AND h.id IN (" + strings.Join(marks, ",") + ")"
	}
	var total int
	if tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM hospitals h JOIN update_agents a ON a.hospital_id=h.id WHERE "+where, args...).Scan(&total) != nil {
		unavailable(w)
		return
	}
	if total == 0 {
		fail(w, 409, "NO_COMPATIBLE_SITES", "ไม่มีหน่วยงานที่พร้อม ต้องเปิดใช้งานและให้ Updater ติดต่อพร้อมรุ่น/สถาปัตยกรรมที่รองรับก่อน")
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT INTO update_rollouts(id,release_id,max_parallel,created_at) VALUES(?,?,?,?)", rollout, manifest.ID, in.MaxParallel, now)
	if err != nil {
		unavailable(w)
		return
	}
	// INSERT SELECT targets all compatible sites without loading the fleet in memory.
	insertArgs := []any{rollout, manifest.ID, now, now, now + 86400}
	insertArgs = append(insertArgs, args...)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO update_jobs(id,rollout_id,hospital_id,release_id,created_at,updated_at,expires_at) SELECT lower(hex(randomblob(16))),?,h.id,?,?,?,? FROM hospitals h JOIN update_agents a ON a.hospital_id=h.id WHERE `+where+" ORDER BY a.last_seen DESC,h.code", insertArgs...)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE update_rollouts SET pilot_job_id=(SELECT id FROM update_jobs WHERE rollout_id=? ORDER BY seq LIMIT 1) WHERE id=?", rollout, rollout)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','api-rollout-created',?,?)", fmt.Sprintf("release=%s sites=%d pilot=1 max_parallel=%d rollout=%s", manifest.ID, total, in.MaxParallel, rollout), now)
	}
	if err != nil || tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]any{"rollout_id": rollout, "queued": total, "total": total, "pilot_first": true, "expires_at": now + 86400})
}

func expireUpdateJobs(ctx context.Context, tx *sql.Tx, now int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE update_jobs SET status='expired',stage='expired',message='Job expired before execution',updated_at=? WHERE status='queued' AND expires_at<=?", now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE update_rollouts SET state='paused' WHERE id IN (SELECT rollout_id FROM update_jobs WHERE status IN ('running','awaiting_external') AND lease_until<=?)", now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE update_jobs SET status='blocked',stage='lease_expired',message='Updater lease expired; verify the local journal before retrying',updated_at=? WHERE status IN ('running','awaiting_external') AND lease_until<=?", now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE update_jobs SET status='blocked',stage='rollout_paused',message='Rollout paused after an unresolved site result; review before creating a new rollout',updated_at=? WHERE status='queued' AND rollout_id IN (SELECT id FROM update_rollouts WHERE state='paused')", now)
	return err
}

func (s *Server) adminPauseRollout(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), "UPDATE update_rollouts SET state='paused' WHERE id=?", r.PathValue("id"))
	if err != nil {
		unavailable(w)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "NOT_FOUND", "ไม่พบชุดอัปเดต")
		return
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE update_jobs SET status='cancelled',stage='cancelled',message='Pending work cancelled by administrator',updated_at=? WHERE rollout_id=? AND status='queued'", time.Now().Unix(), r.PathValue("id"))
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','api-rollout-paused',?,?)", "rollout="+r.PathValue("id"), time.Now().Unix())
	}
	if err != nil || tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]bool{"paused": true})
}

func (s *Server) adminUpdateJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	limit, cursor, ok := updatePage(w, r)
	if !ok {
		return
	}
	where := "1=1"
	args := []any{}
	for _, field := range []string{"status", "hospital_id", "rollout_id"} {
		v := r.URL.Query().Get(field)
		if len(v) > 120 {
			fail(w, 400, "INVALID_FILTER", "ตัวกรองยาวเกินไป")
			return
		}
		if v != "" {
			where += " AND j." + field + "=?"
			args = append(args, v)
		}
	}
	var total int
	if s.config.Store.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM update_jobs j WHERE "+where, args...).Scan(&total) != nil {
		unavailable(w)
		return
	}
	if cursor > 0 {
		where += " AND j.seq<?"
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := s.config.Store.db.QueryContext(r.Context(), `SELECT j.seq,j.id,j.rollout_id,j.hospital_id,h.code,h.name,j.release_id,j.status,j.stage,j.message,r.image,j.actual_image,j.created_at,j.updated_at,j.expires_at,j.lease_until FROM update_jobs j JOIN hospitals h ON h.id=j.hospital_id JOIN update_releases r ON r.id=j.release_id WHERE `+where+" ORDER BY j.seq DESC LIMIT ?", args...)
	if err != nil {
		unavailable(w)
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	next := ""
	var last int64
	for rows.Next() {
		var seq, created, updated, expires, lease int64
		var id, rollout, hospital, code, name, release, status, stage, message, target, actual string
		if rows.Scan(&seq, &id, &rollout, &hospital, &code, &name, &release, &status, &stage, &message, &target, &actual, &created, &updated, &expires, &lease) != nil {
			unavailable(w)
			return
		}
		if len(list) == limit {
			next = strconv.FormatInt(last, 10)
			break
		}
		list = append(list, map[string]any{"id": id, "rollout_id": rollout, "hospital_id": hospital, "hospital_code": code, "hospital_name": name, "release_id": release, "status": status, "stage": stage, "message": message, "target_image": target, "actual_image": actual, "created_at": created, "updated_at": updated, "expires_at": expires, "lease_until": lease})
		last = seq
	}
	if rows.Err() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]any{"jobs": list, "next_cursor": next, "total": total})
}

func (s *Server) adminUpdateAgents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	limit, cursor, ok := updatePage(w, r)
	if !ok {
		return
	}
	where := "1=1"
	args := []any{}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 100 {
		fail(w, 400, "INVALID_FILTER", "คำค้นหายาวเกินไป")
		return
	}
	if q != "" {
		where += " AND (h.code LIKE ? OR h.name LIKE ?)"
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	var total int
	if s.config.Store.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM update_agents a JOIN hospitals h ON h.id=a.hospital_id WHERE "+where, args...).Scan(&total) != nil {
		unavailable(w)
		return
	}
	if cursor > 0 {
		where += " AND a.rowid<?"
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := s.config.Store.db.QueryContext(r.Context(), "SELECT a.rowid,a.hospital_id,h.code,h.name,a.architecture,a.updater_version,a.actual_image,a.last_seen FROM update_agents a JOIN hospitals h ON h.id=a.hospital_id WHERE "+where+" ORDER BY a.rowid DESC LIMIT ?", args...)
	if err != nil {
		unavailable(w)
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	next := ""
	var last int64
	for rows.Next() {
		var seq, seen int64
		var id, code, name, arch, version, image string
		if rows.Scan(&seq, &id, &code, &name, &arch, &version, &image, &seen) != nil {
			unavailable(w)
			return
		}
		if len(list) == limit {
			next = strconv.FormatInt(last, 10)
			break
		}
		status := "online"
		if seen == 0 {
			status = "not_connected"
		} else if seen < time.Now().Unix()-180 {
			status = "offline"
		}
		list = append(list, map[string]any{"hospital_id": id, "hospital_code": code, "hospital_name": name, "architecture": arch, "updater_version": version, "actual_image": image, "last_seen": seen, "status": status})
		last = seq
	}
	if rows.Err() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]any{"agents": list, "next_cursor": next, "total": total})
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) (Hospital, bool) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	secure := r.TLS != nil || (s.trusted(net.ParseIP(host)) && r.Header.Get("X-Forwarded-Proto") == "https")
	if !secure {
		fail(w, 403, "HTTPS_REQUIRED", "Updater requires HTTPS")
		return Hospital{}, false
	}
	if !s.limit(w, r, "updater:"+s.clientIP(r), 120, time.Minute) {
		return Hospital{}, false
	}
	match := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
	if match == nil {
		fail(w, 401, "AGENT_UNAUTHORIZED", "Invalid updater credential")
		return Hospital{}, false
	}
	var id string
	if err := s.config.Store.db.QueryRowContext(r.Context(), "SELECT hospital_id FROM update_agents WHERE token_hash=?", tokenHash(match[1])).Scan(&id); err != nil {
		fail(w, 401, "AGENT_UNAUTHORIZED", "Invalid updater credential")
		return Hospital{}, false
	}
	h, err := s.config.Store.hospital(r.Context(), "id", id)
	if err != nil {
		fail(w, 401, "AGENT_UNAUTHORIZED", "Invalid updater credential")
		return Hospital{}, false
	}
	return h, true
}

func (s *Server) agentUpdatePoll(w http.ResponseWriter, r *http.Request) {
	h, ok := s.updateAgent(w, r)
	if !ok {
		return
	}
	var in struct {
		HospitalCode   string `json:"hospital_code"`
		Architecture   string `json:"architecture"`
		UpdaterVersion string `json:"updater_version"`
		ActualImage    string `json:"actual_image"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.HospitalCode != h.Code || (in.Architecture != "amd64" && in.Architecture != "arm64") || updateVersion(in.UpdaterVersion) < 0 || (in.ActualImage != "" && !updateImagePattern.MatchString(in.ActualImage)) {
		fail(w, 400, "INVALID_AGENT_STATE", "Invalid site or updater compatibility")
		return
	}
	now := time.Now().Unix()
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), "UPDATE update_agents SET architecture=?,updater_version=?,version_number=?,actual_image=?,last_seen=? WHERE hospital_id=?", in.Architecture, in.UpdaterVersion, updateVersion(in.UpdaterVersion), in.ActualImage, now, h.ID)
	if err != nil || expireUpdateJobs(r.Context(), tx, now) != nil {
		unavailable(w)
		return
	}
	var job, rollout, release, raw string
	var expires int64
	err = tx.QueryRowContext(r.Context(), `SELECT j.id,j.rollout_id,j.release_id,j.expires_at,r.manifest FROM update_jobs j JOIN update_rollouts b ON b.id=j.rollout_id JOIN update_releases r ON r.id=j.release_id WHERE j.hospital_id=? AND j.status='queued' AND b.state='active' AND j.expires_at>? AND (j.id=b.pilot_job_id OR EXISTS(SELECT 1 FROM update_jobs p WHERE p.id=b.pilot_job_id AND p.status='succeeded')) AND (SELECT COUNT(*) FROM update_jobs a WHERE a.rollout_id=b.id AND a.status IN ('running','awaiting_external'))<b.max_parallel AND (SELECT COUNT(*) FROM update_jobs g WHERE g.status IN ('running','awaiting_external'))<5 ORDER BY j.seq LIMIT 1`, h.ID, now).Scan(&job, &rollout, &release, &expires, &raw)
	if errors.Is(err, sql.ErrNoRows) || !active(h) {
		if tx.Commit() != nil {
			unavailable(w)
			return
		}
		writeData(w, map[string]any{"job": nil, "poll_after_seconds": 60})
		return
	}
	if err != nil {
		unavailable(w)
		return
	}
	var manifest updateManifest
	if json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Architecture != in.Architecture || updateVersion(in.UpdaterVersion) < updateVersion(manifest.MinUpdaterVersion) || verifyUpdateManifest(manifest) != nil {
		_, _ = tx.ExecContext(r.Context(), "UPDATE update_jobs SET status='blocked',stage='compatibility',message='Release policy or compatibility changed',updated_at=? WHERE id=?", now, job)
		_, _ = tx.ExecContext(r.Context(), "UPDATE update_rollouts SET state='paused' WHERE id=?", rollout)
		if tx.Commit() != nil {
			unavailable(w)
			return
		}
		writeData(w, map[string]any{"job": nil, "poll_after_seconds": 60})
		return
	}
	lease, err := randomToken()
	if err != nil {
		unavailable(w)
		return
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE update_jobs SET status='running',stage='preflight',lease_hash=?,lease_until=?,updated_at=? WHERE id=? AND status='queued'", tokenHash(lease), now+updateLeaseSeconds, now, job)
	if err != nil || tx.Commit() != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]any{"job": map[string]any{"id": job, "hospital_code": h.Code, "rollout_id": rollout, "release": manifest, "lease_token": lease, "lease_until": now + updateLeaseSeconds, "expires_at": expires}, "poll_after_seconds": 60})
}

func (s *Server) agentUpdateReport(w http.ResponseWriter, r *http.Request) {
	h, ok := s.updateAgent(w, r)
	if !ok {
		return
	}
	var in struct {
		LeaseToken  string `json:"lease_token"`
		Sequence    int64  `json:"sequence"`
		Stage       string `json:"stage"`
		Message     string `json:"message"`
		ActualImage string `json:"actual_image"`
	}
	if !decode(w, r, &in) {
		return
	}
	allowed := map[string]bool{"preflight": true, "downloading": true, "applying": true, "verifying": true, "awaiting_external": true, "failed": true, "rolled_back": true, "blocked": true}
	if !allowed[in.Stage] || len(in.Message) > 500 || in.Sequence < 1 || (in.ActualImage != "" && !updateImagePattern.MatchString(in.ActualImage)) {
		fail(w, 400, "INVALID_REPORT", "Invalid update status report")
		return
	}
	now := time.Now().Unix()
	var leaseHash, status, rollout, target, reportHash string
	var leaseUntil, seq int64
	err := s.config.Store.db.QueryRowContext(r.Context(), "SELECT j.lease_hash,j.lease_until,j.report_seq,j.report_hash,j.status,j.rollout_id,r.image FROM update_jobs j JOIN update_releases r ON r.id=j.release_id WHERE j.id=? AND j.hospital_id=?", r.PathValue("id"), h.ID).Scan(&leaseHash, &leaseUntil, &seq, &reportHash, &status, &rollout, &target)
	payload, _ := json.Marshal(in)
	payloadHash := tokenHash(string(payload))
	if err != nil || !equal(leaseHash, tokenHash(in.LeaseToken)) {
		fail(w, 409, "STALE_JOB_LEASE", "Job lease or report revision is stale; reconcile the local journal")
		return
	}
	// Lost HTTP acknowledgements must not turn a completed deployment into failure.
	if in.Sequence == seq && equal(reportHash, payloadHash) {
		terminal := status == "succeeded" || status == "failed" || status == "rolled_back" || status == "blocked"
		writeData(w, map[string]any{"status": status, "terminal": terminal, "lease_until": leaseUntil})
		return
	}
	if leaseUntil <= now || in.Sequence <= seq || (status != "running" && status != "awaiting_external") {
		fail(w, 409, "STALE_JOB_LEASE", "Job lease or report revision is stale; reconcile the local journal")
		return
	}
	if in.Stage == "awaiting_external" && in.ActualImage != target {
		fail(w, 409, "IMAGE_MISMATCH", "Reported image does not match the approved target")
		return
	}
	next := "running"
	switch in.Stage {
	case "awaiting_external":
		next = "awaiting_external"
	case "failed", "rolled_back", "blocked":
		next = in.Stage
	}
	res, err := s.config.Store.db.ExecContext(r.Context(), "UPDATE update_jobs SET status=?,stage=?,message=?,actual_image=?,report_seq=?,report_hash=?,lease_until=?,updated_at=? WHERE id=? AND hospital_id=? AND report_seq=? AND lease_hash=? AND status IN ('running','awaiting_external')", next, in.Stage, in.Message, in.ActualImage, in.Sequence, payloadHash, now+updateLeaseSeconds, now, r.PathValue("id"), h.ID, seq, leaseHash)
	if err != nil {
		unavailable(w)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		fail(w, 409, "STALE_JOB_LEASE", "Another report already changed this job")
		return
	}
	_, _ = s.config.Store.db.ExecContext(r.Context(), "UPDATE update_agents SET last_seen=?,actual_image=? WHERE hospital_id=?", now, in.ActualImage, h.ID)
	if next == "awaiting_external" {
		// Local Docker health and this independent mTLS/DB check must both pass.
		resp, e := s.upstream(r.Context(), h, http.MethodGet, "/api/status", "", nil)
		if e == nil && resp.StatusCode == http.StatusOK {
			var v struct {
				HospitalCode string `json:"hospital_code"`
				APIVersion   string `json:"api_version"`
				ERPReachable bool   `json:"erp_reachable"`
			}
			if readEnvelope(resp, &v) == nil && v.HospitalCode == h.Code && v.APIVersion == "1" && v.ERPReachable {
				next = "succeeded"
				_, err = s.config.Store.db.ExecContext(r.Context(), "UPDATE update_jobs SET status='succeeded',stage='succeeded',message='Local health and Gateway mTLS verification passed',updated_at=? WHERE id=? AND report_seq=? AND status='awaiting_external'", time.Now().Unix(), r.PathValue("id"), in.Sequence)
				if err != nil {
					unavailable(w)
					return
				}
			}
		} else if resp != nil {
			resp.Body.Close()
		}
	}
	terminal := next == "succeeded" || next == "failed" || next == "rolled_back" || next == "blocked"
	if next == "failed" || next == "rolled_back" || next == "blocked" {
		_, _ = s.config.Store.db.ExecContext(r.Context(), "UPDATE update_rollouts SET state='paused' WHERE id=?", rollout)
		_, _ = s.config.Store.db.ExecContext(r.Context(), "UPDATE update_jobs SET status='blocked',stage='rollout_paused',message='Another site did not complete successfully; review before creating a new rollout',updated_at=? WHERE rollout_id=? AND status='queued'", now, rollout)
	}
	if terminal {
		_, _ = s.config.Store.db.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)", h.ID, h.Code, "api-update-"+next, "job="+r.PathValue("id"), now)
	}
	writeData(w, map[string]any{"status": next, "terminal": terminal, "lease_until": now + updateLeaseSeconds})
}

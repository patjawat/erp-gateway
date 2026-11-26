package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"erp-platform/gateway/internal/backup"
)

type transferState struct {
	PairExpiresAt      time.Time       `json:"pair_expires_at"`
	Direction          string          `json:"direction"`
	State              string          `json:"state"`
	SessionID          string          `json:"session_id"`
	DestinationURL     string          `json:"destination_url"`
	AllowPrivate       bool            `json:"allow_private"`
	Token              string          `json:"token"`
	PairHash           string          `json:"pair_hash"`
	PairAttempts       int             `json:"pair_attempts"`
	ExpiresAt          time.Time       `json:"expires_at"`
	Secret             string          `json:"secret"`
	SourceInstallation string          `json:"source_installation"`
	SourceDataset      string          `json:"source_dataset"`
	RecordID           string          `json:"record_id"`
	BaseSnapshot       string          `json:"base_snapshot"`
	BaseGeneration     uint64          `json:"base_generation"`
	FinalSnapshot      string          `json:"final_snapshot"`
	HandoffSnapshot    string          `json:"handoff_snapshot"`
	RestoreID          string          `json:"restore_id"`
	Final              bool            `json:"final"`
	Objects            []backup.Object `json:"objects,omitempty"`
}
type transferManifest struct {
	SessionID       string          `json:"session_id"`
	SnapshotID      string          `json:"snapshot_id"`
	HandoffSnapshot string          `json:"handoff_snapshot"`
	Final           bool            `json:"final"`
	Objects         []backup.Object `json:"objects"`
}
type migrationHandoff struct {
	Version            int             `json:"version"`
	SessionID          string          `json:"session_id"`
	RepositoryID       string          `json:"repository_id"`
	Dataset            string          `json:"dataset"`
	SourceInstallation string          `json:"source_installation"`
	BaseSnapshot       string          `json:"base_snapshot"`
	FinalSnapshot      string          `json:"final_snapshot"`
	Generation         uint64          `json:"generation"`
	Objects            []backup.Object `json:"objects"`
}

func (s *backupService) loadTransfer() error {
	b, e := os.ReadFile(filepath.Join(s.dir, "transfer.enc"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var t transferState
	if e = s.unseal(b, &t); e != nil {
		return e
	}
	if !validBackupID(t.SessionID) {
		return errors.New("invalid migration control state")
	}
	s.transfer = &t
	return nil
}
func (s *backupService) saveTransferLocked() error {
	if s.transfer == nil {
		return os.Remove(filepath.Join(s.dir, "transfer.enc"))
	}
	return s.saveSealed(filepath.Join(s.dir, "transfer.enc"), s.transfer)
}
func (s *backupService) publicMigrationLocked() any {
	if s.transfer == nil {
		return map[string]string{"state": "idle"}
	}
	t := s.transfer
	return map[string]any{"state": t.State, "session_id": t.SessionID, "destination_url": t.DestinationURL, "direction": t.Direction, "expires_at": t.ExpiresAt, "restore_id": t.RestoreID, "retry_available": t.Direction == "sender" && t.State != "retired"}
}

func (m *Manager) freeze(ctx context.Context, kind string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("Gateway is closing")
	}
	m.generation++
	fence := map[string]any{"version": 1, "kind": kind, "generation": m.generation, "created_at": time.Now().UTC()}
	if e := persistJSON(filepath.Join(m.options.Directory, ".backup", "source-fence.json"), fence); e != nil {
		m.mu.Unlock()
		return e
	}
	m.fenced = true
	m.state = "migration-paused"
	current := m.current
	m.mu.Unlock()
	if current == nil {
		return nil
	}
	current.stop()
	done := make(chan struct{})
	go func() {
		current.requests.Wait()
		m.retired.Wait()
		current.close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Minute):
		return errors.New("ยังมีคำขอค้าง เครื่องนี้ยังพักอยู่ กรุณาตรวจสอบก่อนยกเลิก")
	}
}
func (s *backupService) migrationHTTP(w http.ResponseWriter, r *http.Request, path string) {
	s.transferMu.Lock()
	defer s.transferMu.Unlock()
	if r.Context().Err() != nil {
		return
	}
	fail := func(e error) { jsonError(w, 409, "MIGRATION_REJECTED", safeBackupError(e)) }
	if r.Method != http.MethodPost {
		jsonError(w, 405, "METHOD_NOT_ALLOWED", "ใช้ POST")
		return
	}
	switch path {
	case "receive":
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.active != nil {
			fail(errors.New("รอให้งานปัจจุบันเสร็จก่อน"))
			return
		}
		if s.transfer != nil && s.transfer.State != "retired" && s.transfer.State != "cancelled" && s.transfer.ExpiresAt.After(time.Now()) {
			fail(errors.New("มีการย้ายข้อมูลอยู่แล้ว กรุณาทำต่อหรือยกเลิกก่อน"))
			return
		}
		code := backupID() + backupID()
		sum := sha256.Sum256([]byte(code))
		s.transfer = &transferState{Direction: "receiver", State: "receiving", SessionID: backupID(), PairHash: hex.EncodeToString(sum[:]), PairExpiresAt: time.Now().Add(15 * time.Minute), ExpiresAt: time.Now().Add(15 * time.Minute)}
		if e := s.saveTransferLocked(); e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"code": code, "expires_at": s.transfer.ExpiresAt, "installation_id": s.state.Installation})
	case "send":
		var in struct {
			DestinationURL string `json:"destination_url"`
			PairingCode    string `json:"pairing_code"`
			AllowPrivate   bool   `json:"allow_private"`
		}
		if !readJSON(w, r, &in, 16<<10) {
			return
		}
		destination, e := validateMigrationURL(in.DestinationURL)
		if e != nil {
			fail(e)
			return
		}
		j, e := s.startJob("pairing", func(ctx context.Context, j *backupJob) error {
			return s.sendMigration(ctx, destination, in.PairingCode, in.AllowPrivate, false, j)
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	case "resume", "finalize":
		s.mu.Lock()
		t := s.transfer
		valid := t != nil && t.Direction == "sender"
		wasFinal := valid && t.Final
		destination, allow := "", false
		if valid {
			destination, allow = t.DestinationURL, t.AllowPrivate
		}
		s.mu.Unlock()
		if !valid {
			fail(errors.New("ยังไม่มีการจับคู่ต้นทาง"))
			return
		}
		final := path == "finalize" || (path == "resume" && wasFinal)
		j, e := s.startJob("transferring", func(ctx context.Context, j *backupJob) error {
			return s.sendMigration(ctx, destination, "", allow, final, j)
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	case "activate":
		var in struct {
			SessionID     string `json:"session_id"`
			SourceStopped bool   `json:"source_stopped"`
			PublicURL     string `json:"public_url"`
		}
		if !readJSON(w, r, &in, 8<<10) {
			return
		}
		s.mu.Lock()
		t := s.transfer
		valid := t != nil && t.Direction == "receiver" && (t.State == "final_ready" || t.State == "active") && t.SessionID == in.SessionID
		id := ""
		if valid {
			id = t.RestoreID
		}
		s.mu.Unlock()
		if !valid || !in.SourceStopped {
			fail(errors.New("ต้องตรวจข้อมูลชุดสุดท้ายและยืนยันต้นทางพักแล้ว"))
			return
		}
		j, e := s.startJob("activating", func(ctx context.Context, j *backupJob) error {
			if e := s.activateRestore(ctx, id, in.PublicURL, true); e != nil {
				return e
			}
			s.mu.Lock()
			s.transfer.State = "active"
			e := s.saveTransferLocked()
			s.mu.Unlock()
			return e
		})
		if e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]any{"job": j})
	case "cancel":
		var in struct {
			Confirmed           bool   `json:"destination_inactive_confirmed"`
			OfflineOverride     bool   `json:"offline_override"`
			AuthoritativeSource string `json:"authoritative_source"`
		}
		if !readJSON(w, r, &in, 8<<10) {
			return
		}
		if !in.Confirmed {
			fail(errors.New("ยืนยันว่าปลายทางยังไม่เปิดใช้งานหรือหยุดปลายทางแล้ว"))
			return
		}
		s.mu.Lock()
		if s.active != nil {
			s.mu.Unlock()
			fail(errors.New("รอให้งานที่กำลังทำจบก่อนยกเลิก"))
			return
		}
		var previous transferState
		if s.transfer != nil {
			previous = *s.transfer
		}
		s.mu.Unlock()
		if previous.State == "active" {
			fail(errors.New("เครื่องปลายทางเปิดใช้งานแล้ว ต้องใช้ขั้นตอนกู้คืนพร้อมสำรองข้อมูลล่าสุด"))
			return
		}
		if previous.Direction == "sender" && previous.Token != "" {
			e := transferCall(r.Context(), s.clientForMigration(previous.AllowPrivate), previous.DestinationURL, "cancel", previous.Token, map[string]any{}, nil)
			var responseError *transferResponseError
			knownActive := errors.As(e, &responseError) && responseError.Status == http.StatusConflict
			if e != nil && (knownActive || !(in.OfflineOverride && in.AuthoritativeSource == "USE SOURCE DATA")) {
				fail(errors.New("ยังยืนยันว่าปลายทางยกเลิกไม่ได้ เครื่องต้นทางจะยังพักอยู่ กรุณาตรวจปลายทางก่อน"))
				return
			}
		}
		s.mu.Lock()
		if s.transfer != nil {
			s.transfer.State = "cancelled"
			s.transfer.Token = ""
			s.transfer.PairHash = ""
			if e := s.saveTransferLocked(); e != nil {
				s.mu.Unlock()
				fail(e)
				return
			}
		}
		s.mu.Unlock()
		s.m.mu.Lock()
		if s.m.fenced {
			cfg, key, e := LoadRuntime(s.m.options.Directory)
			if e == nil {
				var runtime Runtime
				runtime, e = s.m.options.Build(cfg, key)
				if e == nil {
					e = os.Remove(filepath.Join(s.dir, "source-fence.json"))
					if errors.Is(e, os.ErrNotExist) {
						e = nil
					}
					if e == nil {
						s.m.fenced = false
						s.m.config, s.m.key = cfg, key
						s.m.install(runtime)
					} else if runtime.Close != nil {
						runtime.Close()
					}
				}
			}
			s.m.mu.Unlock()
			if e != nil {
				fail(e)
				return
			}
		} else {
			s.m.mu.Unlock()
		}
		jsonData(w, map[string]bool{"cancelled": true})
	case "retire":
		var in struct {
			Confirmed bool `json:"destination_active_confirmed"`
		}
		if !readJSON(w, r, &in, 8<<10) {
			return
		}
		s.m.mu.Lock()
		fenced := s.m.fenced
		s.m.mu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		if !in.Confirmed || !fenced || s.active != nil || s.transfer == nil || s.transfer.Direction != "sender" || !s.transfer.Final || s.transfer.HandoffSnapshot == "" {
			fail(errors.New("ยืนยันว่าปลายทางเปิดใช้งานชุดสุดท้ายแล้ว ก่อนปิดงานเครื่องต้นทาง"))
			return
		}
		s.transfer.State = "retired"
		if e := s.saveTransferLocked(); e != nil {
			fail(e)
			return
		}
		jsonData(w, map[string]bool{"retired": true, "source_fenced": true})
	default:
		jsonError(w, 404, "NOT_FOUND", "ไม่พบคำสั่งย้ายข้อมูล")
	}
}
func validateMigrationURL(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || len(raw) > 2048 || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("ระบุ HTTPS URL ของ Gateway ปลายทางโดยไม่มี path หรือข้อมูลผู้ใช้")
	}
	return "https://" + u.Host, nil
}
func migrationAddressAllowed(ip netip.Addr, allowPrivate bool) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsPrivate() {
		return allowPrivate
	}
	return ip.IsGlobalUnicast()
}
func (s *backupService) clientForMigration(allowPrivate bool) *http.Client {
	if s.transferClient != nil {
		return s.transferClient
	} // trusted test injection only
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, errors.New("resolve destination failed")
		}
		for _, ip := range ips {
			if !migrationAddressAllowed(ip, allowPrivate) {
				return nil, errors.New("ปลายทางเป็นเครือข่ายที่ไม่อนุญาต เลือก LAN อย่างชัดเจนหรือใช้ปลายทาง HTTPS ที่เข้าถึงได้")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("migration redirects are not allowed") }}
}
func transferCall(ctx context.Context, client *http.Client, origin, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/backup/transfer/"+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("เชื่อมต่อ Gateway ปลายทางไม่ได้ ตรวจ URL, HTTPS และเครือข่าย")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &transferResponseError{Status: resp.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
	}
	return nil
}

type transferResponseError struct{ Status int }

func (e *transferResponseError) Error() string {
	return fmt.Sprintf("ปลายทางปฏิเสธการโอน (HTTP %d)", e.Status)
}
func (s *backupService) sendMigration(ctx context.Context, destination, code string, allowPrivate, final bool, j *backupJob) error {
	client := s.clientForMigration(allowPrivate)
	s.mu.Lock()
	var t transferState
	if s.transfer != nil && s.transfer.Direction == "sender" && s.transfer.DestinationURL == destination {
		t = *s.transfer
	}
	secret, dataset, installation := s.state.Secret, s.state.Dataset, s.state.Installation
	s.mu.Unlock()
	if code != "" && t.Token != "" {
		s.m.mu.Lock()
		fenced := s.m.fenced
		s.m.mu.Unlock()
		if fenced {
			return errors.New("ต้นทางพักสำหรับชุดสุดท้าย ต้องยืนยันยกเลิกปลายทางเดิมก่อนจับคู่ใหม่")
		}
		t = transferState{}
	}
	if t.Token == "" {
		if final || len(code) < 32 {
			return errors.New("ระบุรหัสจับคู่จากเครื่องปลายทาง")
		}
		var response struct {
			SessionID string `json:"session_id"`
			Token     string `json:"token"`
		}
		if e := transferCall(ctx, client, destination, "pair", "", map[string]string{"code": code, "secret": secret, "dataset": dataset, "installation": installation}, &response); e != nil {
			return e
		}
		if !validBackupID(response.SessionID) || len(response.Token) < 32 {
			return errors.New("ปลายทางส่งข้อมูลจับคู่ไม่ถูกต้อง")
		}
		t = transferState{Direction: "sender", State: "paired", SessionID: response.SessionID, Token: response.Token, DestinationURL: destination, AllowPrivate: allowPrivate, ExpiresAt: time.Now().Add(24 * time.Hour)}
		s.mu.Lock()
		s.transfer = &t
		e := s.saveTransferLocked()
		s.mu.Unlock()
		if e != nil {
			return e
		}
	}
	if final && t.HandoffSnapshot == "" {
		if t.RecordID == "" || t.BaseSnapshot == "" {
			return errors.New("ส่งข้อมูลรอบแรกให้เสร็จก่อน")
		}
		s.mu.Lock()
		s.transfer.Final = true
		s.transfer.State = "finalizing"
		if e := s.saveTransferLocked(); e != nil {
			s.mu.Unlock()
			return e
		}
		s.mu.Unlock()
		s.m.mu.Lock()
		if s.m.generation < t.BaseGeneration {
			s.m.generation = t.BaseGeneration
		}
		s.m.mu.Unlock()
		s.phase("draining")
		if e := s.m.freeze(ctx, "migration"); e != nil {
			return e
		}
	}
	var r backupRecord
	var e error
	reuse := t.RecordID != "" && (!final || (t.Final && t.HandoffSnapshot != ""))
	if reuse {
		r, e = s.getRecord(t.RecordID)
	} else {
		r, e = s.capture(ctx, t.RecordID, true)
	}
	if e != nil {
		return e
	}
	j.BackupID = r.ID
	defer os.RemoveAll(filepath.Join(s.recordPath(r.ID), "capture"))
	s.mu.Lock()
	s.transfer.RecordID = r.ID
	t = *s.transfer
	s.mu.Unlock()
	if final && t.HandoffSnapshot == "" {
		repo, clean, e := s.repository(filepath.Join(s.recordPath(r.ID), "repository"), secret)
		if e != nil {
			return e
		}
		defer clean()
		repoID, e := repo.ID(ctx)
		if e != nil {
			return e
		}
		objects, e := backup.Inventory(ctx, repo.Path)
		if e != nil {
			return e
		}
		var mf captureManifest
		if e = readBackupJSON(filepath.Join(s.recordPath(r.ID), "capture", "manifest.json"), &mf, 1<<20); e != nil {
			return e
		}
		handoff := migrationHandoff{Version: 1, SessionID: t.SessionID, RepositoryID: repoID, Dataset: dataset, SourceInstallation: installation, BaseSnapshot: t.BaseSnapshot, FinalSnapshot: r.SnapshotID, Generation: mf.Generation, Objects: objects}
		handoffDir := filepath.Join(s.recordPath(r.ID), "handoff")
		os.MkdirAll(handoffDir, 0700)
		b, _ := json.Marshal(handoff)
		if e = writePrivate(filepath.Join(handoffDir, "handoff.json"), b); e != nil {
			return e
		}
		handoffID, e := repo.Backup(ctx, handoffDir)
		os.RemoveAll(handoffDir)
		if e != nil {
			return e
		}
		t.HandoffSnapshot = handoffID
		t.FinalSnapshot = r.SnapshotID
	} else if t.BaseSnapshot == "" {
		t.BaseSnapshot = r.SnapshotID
		var mf captureManifest
		readBackupJSON(filepath.Join(s.recordPath(r.ID), "capture", "manifest.json"), &mf, 1<<20)
		t.BaseGeneration = mf.Generation
	}
	t.Final = final
	t.State = "sending"
	s.mu.Lock()
	s.transfer = &t
	e = s.saveTransferLocked()
	s.mu.Unlock()
	if e != nil {
		return e
	}
	objects, e := backup.Inventory(ctx, filepath.Join(s.recordPath(r.ID), "repository"))
	if e != nil {
		return e
	}
	s.phase("transferring")
	var manifestResponse struct {
		Offsets map[string]int64 `json:"offsets"`
	}
	manifest := transferManifest{SessionID: t.SessionID, SnapshotID: r.SnapshotID, HandoffSnapshot: t.HandoffSnapshot, Final: final, Objects: objects}
	if e = transferCall(ctx, client, destination, "manifest", t.Token, manifest, &manifestResponse); e != nil {
		return e
	}
	for _, object := range objects {
		offset := manifestResponse.Offsets[object.Path]
		if offset < 0 || offset > object.Size {
			return errors.New("invalid receiver offset")
		}
		if offset == object.Size {
			continue
		}
		f, e := os.Open(filepath.Join(s.recordPath(r.ID), "repository", filepath.FromSlash(object.Path)))
		if e != nil {
			return e
		}
		for offset < object.Size {
			size := int64(4 << 20)
			if object.Size-offset < size {
				size = object.Size - offset
			}
			f.Seek(offset, io.SeekStart)
			reader := io.NewSectionReader(f, offset, size)
			address := destination + "/backup/transfer/object?path=" + url.QueryEscape(object.Path) + "&offset=" + strconv.FormatInt(offset, 10)
			req, e := http.NewRequestWithContext(ctx, http.MethodPut, address, reader)
			if e != nil {
				f.Close()
				return e
			}
			req.ContentLength = size
			req.Header.Set("Authorization", "Bearer "+t.Token)
			response, e := client.Do(req)
			if e != nil {
				f.Close()
				return errors.New("การโอนขาดช่วง กดส่งต่อเพื่อเริ่มจากจุดค้าง")
			}
			io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if response.StatusCode != 200 {
				f.Close()
				return fmt.Errorf("ส่งข้อมูลไม่สำเร็จ HTTP %d", response.StatusCode)
			}
			offset += size
		}
		f.Close()
	}
	if e = transferCall(ctx, client, destination, "complete", t.Token, map[string]string{"session_id": t.SessionID}, nil); e != nil {
		return e
	}
	for {
		var status struct {
			State     string `json:"state"`
			RestoreID string `json:"restore_id"`
			Error     string `json:"error"`
		}
		if e = transferCall(ctx, client, destination, "status", t.Token, map[string]any{}, &status); e != nil {
			return e
		}
		if status.State == "prepared" || status.State == "final_ready" {
			s.mu.Lock()
			s.transfer.State = status.State
			s.transfer.RestoreID = status.RestoreID
			e = s.saveTransferLocked()
			s.mu.Unlock()
			return e
		}
		if status.State == "failed" {
			return errors.New("ปลายทางตรวจข้อมูลไม่ผ่าน กรุณาตรวจสอบและส่งใหม่")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *backupService) transportHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/backup/oauth/callback" {
		s.oauthCallback(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/backup/transfer/")
	if path == "pair" && r.Method == http.MethodPost {
		s.pairHTTP(w, r)
		return
	}
	if path != "status" {
		s.transferMu.Lock()
		defer s.transferMu.Unlock()
		if r.Context().Err() != nil {
			return
		}
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	t := s.transfer
	valid := t != nil && t.Direction == "receiver" && t.Token != "" && t.ExpiresAt.After(time.Now()) && subtle.ConstantTimeCompare([]byte(token), []byte(t.Token)) == 1
	var session transferState
	if valid {
		session = *t
	}
	s.mu.Unlock()
	if !valid {
		jsonError(w, 403, "TRANSFER_AUTH_REQUIRED", "การโอนไม่ได้รับอนุญาตหรือหมดอายุ")
		return
	}
	if session.RestoreID != "" {
		active, e := s.restoreAlreadyActive(session.RestoreID)
		if e != nil {
			jsonError(w, 409, "RESTORE_STATE_UNKNOWN", "ตรวจสถานะเปิดใช้งานปลายทางไม่ได้")
			return
		}
		if active {
			session.State = "active"
		}
	}
	errResponse := func(e error) { jsonError(w, 409, "TRANSFER_REJECTED", safeBackupError(e)) }
	root := filepath.Join(s.root, "staging", "transfer-"+session.SessionID, "repository")
	switch {
	case path == "cancel" && r.Method == http.MethodPost:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.active != nil || session.State == "active" {
			errResponse(errors.New("destination active or busy; cannot cancel"))
			return
		}
		s.transfer.State = "cancelled"
		s.transfer.Token = ""
		s.transfer.PairHash = ""
		if e := s.saveTransferLocked(); e != nil {
			errResponse(e)
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"cancelled": true})
	case path == "status" && r.Method == http.MethodPost:
		json.NewEncoder(w).Encode(map[string]string{"state": session.State, "restore_id": session.RestoreID})
	case path == "manifest" && r.Method == http.MethodPost:
		var in transferManifest
		if !readJSON(w, r, &in, 32<<20) {
			return
		}
		if in.SessionID != session.SessionID || len(in.Objects) == 0 || len(in.Objects) > 100000 {
			errResponse(errors.New("invalid transfer manifest"))
			return
		}
		if session.State == "active" {
			errResponse(errors.New("destination already active"))
			return
		}
		if in.Final && session.BaseSnapshot == "" {
			errResponse(errors.New("base snapshot is required"))
			return
		}
		if session.Final && !in.Final {
			errResponse(errors.New("cannot replace final transfer with base"))
			return
		}
		offsets := map[string]int64{}
		seen := map[string]bool{}
		var total int64
		for _, object := range in.Objects {
			if !backup.ValidateObjectPath(object.Path) || seen[object.Path] || len(object.SHA256) != 64 || object.Size < 0 || object.Size > maxFullBackupBytes-total {
				errResponse(errors.New("invalid encrypted object inventory"))
				return
			}
			seen[object.Path] = true
			total += object.Size
			path := filepath.Join(root, filepath.FromSlash(object.Path))
			if info, e := os.Stat(path); e == nil {
				if info.Size() > object.Size {
					errResponse(errors.New("receiver object exceeds inventory"))
					return
				}
				if info.Size() == object.Size {
					got, e := fileDigest(path)
					if e != nil || got != object.SHA256 {
						errResponse(errors.New("existing repository object differs"))
						return
					}
				}
				offsets[object.Path] = info.Size()
			} else if !errors.Is(e, os.ErrNotExist) {
				errResponse(e)
				return
			}
		}
		s.mu.Lock()
		s.transfer.Objects = in.Objects
		s.transfer.Final = in.Final
		s.transfer.FinalSnapshot = in.SnapshotID
		s.transfer.HandoffSnapshot = in.HandoffSnapshot
		s.transfer.State = "receiving"
		s.transfer.PairHash = ""
		e := s.saveTransferLocked()
		s.mu.Unlock()
		if e != nil {
			errResponse(e)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"offsets": offsets})
	case path == "object" && r.Method == http.MethodPut:
		name := r.URL.Query().Get("path")
		offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if e != nil || offset < 0 || !backup.ValidateObjectPath(name) {
			errResponse(errors.New("invalid object path/offset"))
			return
		}
		var object *backup.Object
		for i := range session.Objects {
			if session.Objects[i].Path == name {
				object = &session.Objects[i]
				break
			}
		}
		if object == nil || session.State != "receiving" {
			errResponse(errors.New("object not authorized by manifest"))
			return
		}
		s.policyMu.Lock()
		defer s.policyMu.Unlock()
		file := filepath.Join(root, filepath.FromSlash(name))
		if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
			errResponse(e)
			return
		}
		f, e := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			errResponse(e)
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || info.Size() != offset || offset > object.Size {
			errResponse(errors.New("offset changed; resume from manifest"))
			return
		}
		if r.ContentLength < 0 || r.ContentLength > 4<<20 || r.ContentLength > object.Size-offset {
			errResponse(errors.New("invalid chunk size"))
			return
		}
		f.Seek(offset, io.SeekStart)
		n, e := io.Copy(f, http.MaxBytesReader(w, r.Body, 4<<20))
		if e != nil || n != r.ContentLength {
			f.Truncate(offset)
			errResponse(errors.New("incomplete chunk; retry"))
			return
		}
		if e = f.Sync(); e != nil {
			errResponse(e)
			return
		}
		if offset+n == object.Size {
			got, e := fileDigest(file)
			if e != nil || got != object.SHA256 {
				f.Truncate(0)
				errResponse(errors.New("object checksum mismatch; resend"))
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]bool{"received": true})
	case path == "complete" && r.Method == http.MethodPost:
		if session.State == "prepared" || session.State == "final_ready" || session.State == "verifying" {
			json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
			return
		}
		j, e := s.startJob("verifying", func(ctx context.Context, j *backupJob) error {
			s.transferMu.Lock()
			defer s.transferMu.Unlock()
			fail := func(e error) error {
				s.mu.Lock()
				s.transfer.State = "failed"
				s.saveTransferLocked()
				s.mu.Unlock()
				return e
			}
			for _, object := range session.Objects {
				path := filepath.Join(root, filepath.FromSlash(object.Path))
				info, e := os.Stat(path)
				if e != nil || info.Size() != object.Size {
					return fail(errors.New("incomplete encrypted repository"))
				}
				got, e := fileDigest(path)
				if e != nil || got != object.SHA256 {
					return fail(errors.New("encrypted object mismatch"))
				}
			}
			id := backupID()
			stage := filepath.Join(s.root, "staging", id)
			os.MkdirAll(stage, 0700)
			result, e := s.inspectRepository(ctx, id, root, session.Secret, session.FinalSnapshot, "")
			if e != nil {
				return fail(e)
			}
			mf := result.Manifest
			if mf.SourceInstallation != session.SourceInstallation || mf.Dataset != session.SourceDataset || mf.MigrationID != session.SessionID {
				return fail(errors.New("source dataset mismatch"))
			}
			if session.Final {
				if mf.BaseSnapshot != session.BaseSnapshot {
					return fail(errors.New("final capture does not succeed its paired base"))
				}
				repo, clean, e := s.repository(root, session.Secret)
				if e != nil {
					return fail(e)
				}
				handoffDir := filepath.Join(stage, "handoff")
				e = repo.Restore(ctx, session.HandoffSnapshot, handoffDir)
				if e != nil {
					clean()
					return fail(e)
				}
				var h migrationHandoff
				e = readBackupJSON(filepath.Join(handoffDir, "handoff.json"), &h, 32<<20)
				repoID, re := repo.ID(ctx)
				clean()
				os.RemoveAll(handoffDir)
				if e != nil || re != nil || h.Version != 1 || h.RepositoryID != repoID || h.SessionID != session.SessionID || h.BaseSnapshot != session.BaseSnapshot || h.FinalSnapshot != session.FinalSnapshot || h.Dataset != session.SourceDataset || h.SourceInstallation != session.SourceInstallation || h.Generation != mf.Generation || mf.Generation <= session.BaseGeneration {
					return fail(errors.New("final handoff does not match base/session"))
				}
				for _, object := range h.Objects {
					if !backup.ValidateObjectPath(object.Path) {
						return fail(errors.New("invalid handoff inventory"))
					}
					got, e := fileDigest(filepath.Join(root, filepath.FromSlash(object.Path)))
					if e != nil || got != object.SHA256 {
						return fail(errors.New("handoff inventory mismatch"))
					}
				}
			}
			inspected, e := s.loadInspection(id)
			if e != nil {
				return fail(e)
			}
			inspected.TransferSessionID = session.SessionID
			if e = s.saveSealed(filepath.Join(stage, "inspection.enc"), inspected); e != nil {
				return fail(e)
			}
			s.mu.Lock()
			s.transfer.RestoreID = id
			if session.Final {
				s.transfer.State = "final_ready"
			} else {
				s.transfer.State = "prepared"
				s.transfer.BaseSnapshot = session.FinalSnapshot
				s.transfer.BaseGeneration = mf.Generation
			}
			e = s.saveTransferLocked()
			s.mu.Unlock()
			j.RestoreID = id
			return e
		})
		if e != nil {
			errResponse(e)
			return
		}
		s.mu.Lock()
		s.transfer.State = "verifying"
		e = s.saveTransferLocked()
		s.mu.Unlock()
		if e != nil {
			errResponse(e)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"accepted": true, "job": j})
	default:
		jsonError(w, 404, "NOT_FOUND", "ไม่พบคำสั่งโอนข้อมูล")
	}
}
func (s *backupService) pairHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	candidate := s.transfer
	available := candidate != nil && candidate.Direction == "receiver" && candidate.PairHash != "" && candidate.PairAttempts < 10 && candidate.PairExpiresAt.After(time.Now())
	s.mu.Unlock()
	if !available {
		jsonError(w, 403, "PAIRING_REQUIRED", "เปิดรับย้ายข้อมูลจากหน้าผู้ดูแลปลายทางก่อน")
		return
	}
	var in struct {
		Code         string `json:"code"`
		Secret       string `json:"secret"`
		Dataset      string `json:"dataset"`
		Installation string `json:"installation"`
	}
	if !readJSON(w, r, &in, 16<<10) {
		return
	}
	s.transferMu.Lock()
	defer s.transferMu.Unlock()
	if r.Context().Err() != nil {
		return
	}
	sum := sha256.Sum256([]byte(in.Code))
	hash := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.transfer
	if t == nil || t.Direction != "receiver" || t.PairHash == "" || t.PairAttempts >= 10 || !t.PairExpiresAt.After(time.Now()) {
		jsonError(w, 403, "PAIRING_REQUIRED", "เปิดรับย้ายข้อมูลจากหน้าผู้ดูแลปลายทางก่อน")
		return
	}
	t.PairAttempts++
	if subtle.ConstantTimeCompare([]byte(hash), []byte(t.PairHash)) != 1 || !validBackupID(in.Dataset) || !validBackupID(in.Installation) || len(in.Secret) < 32 || len(in.Secret) > 1024 {
		s.saveTransferLocked()
		jsonError(w, 403, "PAIRING_REJECTED", "รหัสจับคู่ไม่ถูกต้อง")
		return
	}
	if t.Token != "" {
		if t.SourceDataset != in.Dataset || t.SourceInstallation != in.Installation || t.Secret != in.Secret {
			jsonError(w, 403, "PAIRING_REJECTED", "รหัสจับคู่ถูกใช้โดยต้นทางอื่นแล้ว")
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"session_id": t.SessionID, "token": t.Token})
		return
	}
	t.Token = backupID() + backupID()
	t.Secret = in.Secret
	t.SourceDataset = in.Dataset
	t.SourceInstallation = in.Installation
	t.ExpiresAt = time.Now().Add(24 * time.Hour)
	if e := s.saveTransferLocked(); e != nil {
		jsonError(w, 503, "PAIRING_SAVE_FAILED", "บันทึกการจับคู่ไม่ได้")
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"session_id": t.SessionID, "token": t.Token})
}

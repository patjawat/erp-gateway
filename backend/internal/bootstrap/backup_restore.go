package bootstrap

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"erp-platform/gateway/internal/backup"
)

const maxFullBackupBytes int64 = 16 << 30

type restoreInspection struct {
	ID                 string           `json:"id"`
	CreatedAt          time.Time        `json:"created_at"`
	PublicURL          string           `json:"public_url"`
	CertificateExpired bool             `json:"certificate_expired"`
	HospitalCAExpired  bool             `json:"hospital_ca_expired"`
	RepairRequired     bool             `json:"repair_required"`
	Ready              bool             `json:"ready"`
	Summary            map[string]int64 `json:"summary"`
	Manifest           captureManifest  `json:"-"`
	Payload            RecoveryPayload  `json:"-"`
	Settings           backupState      `json:"-"`
}

func backupSecret(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		var kit struct {
			Secret string `json:"secret"`
		}
		if len(raw) > 32<<10 || json.Unmarshal([]byte(raw), &kit) != nil {
			return "", errors.New("ชุดกู้คืนไม่ถูกต้อง")
		}
		raw = kit.Secret
	}
	if len(raw) < 32 || len(raw) > 1024 {
		return "", errors.New("ระบุกุญแจกู้คืนจากชุดกู้คืน")
	}
	return raw, nil
}

func readBackupJSON(path string, out any, max int) error {
	body, err := readBounded(path, int64(max))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (s *backupService) inspectBundle(ctx context.Context, path, secret, public string) (restoreInspection, error) {
	var result restoreInspection
	secret, err := backupSecret(secret)
	if err != nil {
		return result, err
	}
	id := backupID()
	stage := filepath.Join(s.root, "staging", id)
	if err := os.MkdirAll(stage, 0700); err != nil {
		return result, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()
	repository := filepath.Join(stage, "repository")
	if err := backup.Unpack(ctx, path, repository, maxFullBackupBytes); err != nil {
		return result, err
	}
	result, err = s.inspectRepository(ctx, id, repository, secret, "", public)
	if err != nil {
		return result, err
	}
	complete = true
	return result, nil
}

func (s *backupService) inspectRepository(ctx context.Context, id, path, secret, snapshotID, public string) (restoreInspection, error) {
	repository, clean, err := s.repository(path, secret)
	if err != nil {
		return restoreInspection{}, err
	}
	defer clean()
	return s.inspectRepo(ctx, id, repository, snapshotID, public)
}

// Exact Drive recovery does not list/read every remote snapshot. Native
// restore verifies its encrypted tree/data; manifests and the sentinel verify
// the logical Gateway dataset before it may become active.
func (s *backupService) inspectRepo(ctx context.Context, id string, repository backup.Repository, snapshotID, public string) (restoreInspection, error) {
	result := restoreInspection{ID: id}
	if !validBackupID(id) {
		return result, errors.New("invalid restore identity")
	}
	secretBytes, err := readBounded(repository.PasswordFile, 1024)
	if err != nil {
		return result, errors.New("อ่านกุญแจสำหรับตรวจข้อมูลสำรองไม่ได้")
	}
	secret, err := backupSecret(string(secretBytes))
	if err != nil {
		return result, err
	}
	if snapshotID == "" {
		if err := repository.Check(ctx); err != nil {
			return result, err
		}
		snapshots, err := repository.Snapshots(ctx)
		if err != nil {
			return result, err
		}
		sort.Slice(snapshots, func(i, j int) bool {
			if snapshots[i].Time.Equal(snapshots[j].Time) {
				return snapshots[i].ID > snapshots[j].ID
			}
			return snapshots[i].Time.After(snapshots[j].Time)
		})
		for _, snapshot := range snapshots {
			if len(snapshot.Paths) == 1 && filepath.Base(snapshot.Paths[0]) == "capture" {
				snapshotID = snapshot.ID
				break
			}
		}
		if snapshotID == "" {
			return result, errors.New("ไม่พบ snapshot ข้อมูล Gateway")
		}
	}
	stage := filepath.Join(s.root, "staging", id)
	if err := os.MkdirAll(stage, 0700); err != nil {
		return result, err
	}
	capture := filepath.Join(stage, "capture")
	if err := os.RemoveAll(capture); err != nil {
		return result, err
	}
	if err := repository.Restore(ctx, snapshotID, capture); err != nil {
		return result, err
	}
	if err := readBackupJSON(filepath.Join(capture, "manifest.json"), &result.Manifest, 1<<20); err != nil {
		return result, err
	}
	manifest := result.Manifest
	if manifest.Version != 1 || manifest.Schema != "gateway-full-v1" || !validBackupID(manifest.Dataset) || !validBackupID(manifest.CaptureID) || manifest.CreatedAt.IsZero() {
		return result, errors.New("รุ่นข้อมูลสำรองไม่รองรับ")
	}
	if err := verifyCaptureFiles(capture, manifest); err != nil {
		return result, err
	}
	if err := readBackupJSON(filepath.Join(capture, "config.json"), &result.Payload, 4<<20); err != nil {
		return result, err
	}
	if err := readBackupJSON(filepath.Join(capture, "backup-settings.json"), &result.Settings, 1<<20); err != nil {
		return result, err
	}
	if result.Settings.Version != 1 || result.Settings.Dataset != manifest.Dataset || result.Settings.Secret != secret {
		return result, errors.New("กุญแจหรือชุดการตั้งค่าสำรองไม่ตรงกัน")
	}
	if err := result.Settings.Policy.validate(); err != nil {
		return result, err
	}
	payload := result.Payload
	if payload.Version != 1 || payload.Config.Version != 1 || !payload.Config.VerifyPasswordVerifier() {
		return result, errors.New("บัญชีผู้ดูแลในข้อมูลสำรองไม่ถูกต้อง")
	}
	key, err := base64.StdEncoding.DecodeString(payload.EncryptionKey)
	if err != nil || len(key) != 32 {
		return result, errors.New("encryption key ในข้อมูลสำรองไม่ถูกต้อง")
	}
	database := filepath.Join(capture, "gateway.db")
	if err := backup.VerifySQLite(ctx, database); err != nil {
		return result, err
	}
	if err := CheckDatabaseKey(database, key); err != nil {
		return result, err
	}
	result.CertificateExpired, err = inspectRestoredIdentity([]byte(payload.ClientCertificatePEM), []byte(payload.ClientPrivateKeyPEM))
	if err != nil {
		return result, err
	}
	result.HospitalCAExpired, err = inspectRestoredHospitalCA([]byte(payload.HospitalCAPEM))
	if err != nil {
		return result, err
	}
	result.RepairRequired = result.CertificateExpired || result.HospitalCAExpired
	if public != "" {
		payload.Config.PublicURL = public
	}
	policy := s.m.options.WebPolicy
	payload.Config.TrustedProxyCIDRs = cloneStrings(s.m.options.TrustedProxyCIDRs)
	if _, err := policy.ValidateURL(payload.Config.PublicURL); err != nil {
		return result, err
	}
	settings := policy.Settings()
	payload.Config.WebAccess = &settings
	if s.m.options.ValidatePolicy != nil {
		if err := s.m.options.ValidatePolicy(payload.Config.ApprovedUpstreams, payload.Config.PrivateUpstreamCIDRs); err != nil {
			return result, err
		}
	}
	result.Payload = payload
	result.PublicURL = payload.Config.PublicURL
	result.CreatedAt = manifest.CreatedAt
	result.Ready = true
	result.Summary = map[string]int64{}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(database), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return result, err
	}
	defer db.Close()
	for _, table := range []string{"hospitals", "sessions", "audit", "notify_jobs", "update_jobs"} {
		var count int64
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count)
		if err != nil {
			if table == "hospitals" || table == "sessions" || table == "audit" {
				return result, errors.New("ฐานข้อมูลสำรองขาดตาราง Gateway ที่จำเป็น")
			}
			continue
		}
		result.Summary[table] = count
	}
	inspection := resultInspection{Result: result, Manifest: manifest, Payload: payload, Settings: result.Settings, SnapshotID: snapshotID, Secret: secret}
	if err := s.saveSealed(filepath.Join(stage, "inspection.enc"), inspection); err != nil {
		return result, err
	}
	return result, nil
}

func verifyCaptureFiles(directory string, manifest captureManifest) error {
	allowed := map[string]bool{"gateway.db": true, "config.json": true, "backup-settings.json": true, "fcm.json": true}
	if len(manifest.Files) < 3 || len(manifest.Files) > 4 {
		return errors.New("ชุดข้อมูลสำรองไม่ครบ")
	}
	for name, digest := range manifest.Files {
		if !allowed[name] || len(digest) != 64 {
			return errors.New("รายการไฟล์สำรองไม่ถูกต้อง")
		}
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("ไฟล์สำรองไม่ครบหรือชนิดไฟล์ไม่ถูกต้อง")
		}
		got, err := fileDigest(filepath.Join(directory, name))
		if err != nil || got != digest {
			return errors.New("ข้อมูลสำรองเสียหายหรือ checksum ไม่ตรง")
		}
	}
	for _, name := range []string{"gateway.db", "config.json", "backup-settings.json"} {
		if manifest.Files[name] == "" {
			return errors.New("ข้อมูลสำรองขาดไฟล์ที่จำเป็น")
		}
	}
	return nil
}

// Only time validity is repairable. Keys, purposes, signatures and the chain
// must still verify at a time within the imported chain's validity intersection.
func inspectRestoredIdentity(certPEM, keyPEM []byte) (bool, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(pair.Certificate) == 0 {
		return false, errors.New("ใบรับรองและ private key ในข้อมูลสำรองไม่ตรงกัน")
	}
	var from, until time.Time
	for _, raw := range pair.Certificate {
		certificate, err := x509.ParseCertificate(raw)
		if err != nil {
			return false, err
		}
		if from.IsZero() || certificate.NotBefore.After(from) {
			from = certificate.NotBefore
		}
		if until.IsZero() || certificate.NotAfter.Before(until) {
			until = certificate.NotAfter
		}
	}
	if !from.Before(until) {
		return false, errors.New("ช่วงอายุ certificate chain ในข้อมูลสำรองไม่ถูกต้อง")
	}
	now := time.Now()
	expired := now.Before(from) || !now.Before(until)
	verifyAt := now
	if expired {
		verifyAt = from.Add(until.Sub(from) / 2)
	}
	_, err = validateIdentityAt(certPEM, keyPEM, verifyAt)
	return expired, err
}

func inspectRestoredHospitalCA(body []byte) (bool, error) {
	expired := false
	now := time.Now()
	if len(body) == 0 {
		return false, nil
	}
	if len(body) > 256<<10 {
		return false, errors.New("Hospital CA ใหญ่เกินขอบเขต")
	}
	count := 0
	for len(bytes.TrimSpace(body)) > 0 {
		block, rest := pem.Decode(body)
		if block == nil || block.Type != "CERTIFICATE" {
			return false, errors.New("Hospital CA ต้องเป็น PEM certificate เท่านั้น")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA || !certificate.BasicConstraintsValid || !certificate.NotBefore.Before(certificate.NotAfter) {
			return false, errors.New("Hospital CA ในข้อมูลสำรองไม่ถูกต้อง")
		}
		expired = expired || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter)
		count++
		body = rest
	}
	if count == 0 {
		return false, errors.New("ไม่พบ Hospital CA ในไฟล์ PEM")
	}
	return expired, nil
}

func (c RuntimeConfig) VerifyPasswordVerifier() bool {
	salt, se := base64.StdEncoding.DecodeString(c.PasswordSalt)
	hash, he := base64.StdEncoding.DecodeString(c.PasswordHash)
	return c.AdminUsername != "" && c.AdminUsername == strings.TrimSpace(c.AdminUsername) && len(c.AdminUsername) <= 100 && !strings.ContainsAny(c.AdminUsername, "\r\n\t") && se == nil && he == nil && len(salt) == 16 && len(hash) == 32 && c.PasswordIterations == passwordIterations
}

type resultInspection struct {
	TransferSessionID string            `json:"transfer_session_id,omitempty"`
	Result            restoreInspection `json:"result"`
	Manifest          captureManifest   `json:"manifest"`
	Payload           RecoveryPayload   `json:"payload"`
	Settings          backupState       `json:"settings"`
	SnapshotID        string            `json:"snapshot_id"`
	Secret            string            `json:"secret"`
}

func (s *backupService) loadInspection(id string) (resultInspection, error) {
	var result resultInspection
	if !validBackupID(id) {
		return result, errors.New("invalid restore identity")
	}
	body, err := readBounded(filepath.Join(s.root, "staging", id, "inspection.enc"), 8<<20)
	if err == nil {
		err = s.unseal(body, &result)
	}
	result.Result.Manifest = result.Manifest
	result.Result.Payload = result.Payload
	result.Result.Settings = result.Settings
	return result, err
}

type cutoverEntry struct {
	Target  string `json:"target"`
	Next    string `json:"next"`
	Old     string `json:"old"`
	Existed bool   `json:"existed"`
}
type cutoverJournal struct {
	Version          int            `json:"version"`
	ID               string         `json:"id"`
	RestoreID        string         `json:"restore_id"`
	Installation     string         `json:"installation"`
	Phase            string         `json:"phase"`
	Entries          []cutoverEntry `json:"entries"`
	RecoveryConsumed bool           `json:"recovery_consumed,omitempty"`
}
type restoreFence struct {
	Version      int    `json:"version"`
	Kind         string `json:"kind"`
	Generation   uint64 `json:"generation"`
	Installation string `json:"installation"`
	CutoverID    string `json:"cutover_id"`
	RestoreID    string `json:"restore_id"`
}
type restoreReceipt struct {
	Version      int    `json:"version"`
	RestoreID    string `json:"restore_id"`
	CutoverID    string `json:"cutover_id"`
	Installation string `json:"installation"`
}

func journalPath(directory string) string { return filepath.Join(directory, ".backup", "cutover.json") }
func restoreReceiptPath(directory, id string) string {
	return filepath.Join(directory, ".backup", "activated", id+".json")
}

func validateCutoverJournal(j cutoverJournal, configDir, dbPath string) error {
	if j.Version != 1 || !validBackupID(j.ID) || !validBackupID(j.RestoreID) || !validBackupID(j.Installation) || (j.Phase != "prepared" && j.Phase != "active") || len(j.Entries) > 10 {
		return errors.New("invalid restore journal")
	}
	seen := map[string]bool{}
	for _, entry := range j.Entries {
		allowed := entry.Target == dbPath || entry.Target == dbPath+"-wal" || entry.Target == dbPath+"-shm" || entry.Target == dbPath+"-journal" || entry.Target == filepath.Join(configDir, "runtime.json") || entry.Target == filepath.Join(configDir, "encryption.key") || entry.Target == filepath.Join(configDir, "integrations", "fcm.json") || entry.Target == filepath.Join(configDir, ".backup", "state.enc")
		if !allowed || seen[entry.Target] || entry.Old != entry.Target+".before-"+j.ID || (entry.Next != "" && entry.Next != entry.Target+".next-"+j.ID) {
			return errors.New("invalid restore journal paths")
		}
		seen[entry.Target] = true
	}
	return nil
}

func finishActivatedCutover(configDir string, j cutoverJournal) error {
	if j.Phase != "active" {
		return errors.New("restore has not reached durable activation")
	}
	// Per-restore receipts survive subsequent cutovers and lost replies.
	body, err := json.Marshal(restoreReceipt{1, j.RestoreID, j.ID, j.Installation})
	if err != nil {
		return err
	}
	if err := writePrivate(restoreReceiptPath(configDir, j.RestoreID), body); err != nil {
		return err
	}
	return clearDestinationRestoreFence(configDir, j)
}

// Both a durable activation and a completed pre-active rollback may release
// this destination transaction's fence. A migration source's fence never matches.
func clearDestinationRestoreFence(configDir string, j cutoverJournal) error {
	path := filepath.Join(configDir, ".backup", "source-fence.json")
	var fence restoreFence
	if err := readBackupJSON(path, &fence, 8<<10); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if fence.Kind != "restore" || fence.Installation != j.Installation || fence.CutoverID != j.ID || fence.RestoreID != j.RestoreID {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncBackupDir(filepath.Dir(path))
}

func recoverBackupCutover(configDir, dbPath string) error {
	path := journalPath(configDir)
	var journal cutoverJournal
	if err := readBackupJSON(path, &journal, 1<<20); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := validateCutoverJournal(journal, configDir, dbPath); err != nil {
		return err
	}
	if journal.Phase == "active" {
		return finishActivatedCutover(configDir, journal)
	}
	for i := len(journal.Entries) - 1; i >= 0; i-- {
		entry := journal.Entries[i]
		if exists(entry.Old) {
			if err := os.Rename(entry.Old, entry.Target); err != nil {
				return err
			}
		} else if !entry.Existed {
			if err := os.Remove(entry.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if entry.Next != "" {
			if err := os.Remove(entry.Next); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := syncBackupDir(filepath.Dir(entry.Target)); err != nil {
			return err
		}
	}
	if err := clearDestinationRestoreFence(configDir, journal); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncBackupDir(filepath.Dir(path))
}

func copyBackupFile(source, destination string) error {
	if info, err := os.Lstat(source); err != nil || !info.Mode().IsRegular() {
		return errors.New("restore input must be a regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

func (s *backupService) restoreAlreadyActive(id string) (bool, error) {
	if !validBackupID(id) {
		return false, errors.New("invalid restore identity")
	}
	var receipt restoreReceipt
	if err := readBackupJSON(restoreReceiptPath(s.m.options.Directory, id), &receipt, 8<<10); err == nil {
		if receipt.Version != 1 || receipt.RestoreID != id || !validBackupID(receipt.CutoverID) || !validBackupID(receipt.Installation) {
			return false, errors.New("invalid restore activation receipt")
		}
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var journal cutoverJournal
	if err := readBackupJSON(journalPath(s.m.options.Directory), &journal, 1<<20); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := validateCutoverJournal(journal, s.m.options.Directory, s.m.options.DatabasePath); err != nil {
		return false, err
	}
	if journal.Phase == "active" {
		s.m.mu.Lock()
		fenced := s.m.fenced
		s.m.mu.Unlock()
		if !journal.RecoveryConsumed || fenced {
			return false, errors.New("ข้อมูลกู้คืนบันทึกแล้ว กรุณาเริ่ม Gateway ใหม่เพื่อจบ journal และเปิดบริการ")
		}
		if err := finishActivatedCutover(s.m.options.Directory, journal); err != nil {
			return false, err
		}
		return journal.RestoreID == id, nil
	}
	return false, errors.New("พบการกู้คืนที่ยังไม่เสร็จ กรุณาเริ่ม Gateway ใหม่ให้กู้คืน journal ก่อน")
}

func (s *backupService) activateRestore(ctx context.Context, id, public string, sourceStopped bool) error {
	if !sourceStopped {
		return errors.New("ยืนยันว่าเครื่องต้นทางหยุดหรือพักการทำงานแล้วก่อนเปิดเครื่องนี้")
	}
	if active, err := s.restoreAlreadyActive(id); err != nil || active {
		return err
	}
	// A source migration fence is authoritative even if an owner starts a
	// restore from another browser tab. It cannot be overwritten by a local
	// restore fence and later cleared as if this were an inactive destination.
	var existingFence restoreFence
	if err := readBackupJSON(filepath.Join(s.dir, "source-fence.json"), &existingFence, 8<<10); err == nil {
		if existingFence.Kind != "restore" {
			return errors.New("เครื่องนี้พักสำหรับย้ายข้อมูล ต้องตรวจปลายทางและยกเลิกการย้ายอย่างชัดเจนก่อนกู้คืน")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	inspection, err := s.loadInspection(id)
	if err != nil {
		return err
	}
	payload := inspection.Payload
	if public != "" {
		payload.Config.PublicURL = public
	}
	if _, err := s.m.options.WebPolicy.ValidateURL(payload.Config.PublicURL); err != nil {
		return err
	}
	capture := filepath.Join(s.root, "staging", id, "capture")
	if err := verifyCaptureFiles(capture, inspection.Manifest); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(payload.EncryptionKey)
	if err != nil || len(key) != 32 {
		return errors.New("invalid restored encryption key")
	}
	if err := CheckDatabaseKey(filepath.Join(capture, "gateway.db"), key); err != nil {
		return err
	}
	// The healthy destination is backed up before any fence or replacement.
	s.m.mu.Lock()
	healthy := s.m.current != nil && !s.m.fenced
	s.m.mu.Unlock()
	if healthy {
		rollback, err := s.capture(ctx, "", true)
		if err != nil {
			return errors.New("สำรองข้อมูลเดิมก่อนแทนที่ไม่สำเร็จ ข้อมูลเดิมยังไม่ถูกเปลี่ยน")
		}
		if err := os.RemoveAll(filepath.Join(s.recordPath(rollback.ID), "capture")); err != nil {
			return err
		}
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	s.phase("activating")
	configDir := s.m.options.Directory
	s.mu.Lock()
	oldState := s.state
	imported := s.importedBackupStateLocked(inspection.Settings, oldState.Installation)
	s.mu.Unlock()
	transactionID := backupID()
	identity := filepath.Join(configDir, "identities", "restored-"+transactionID)
	if err := os.MkdirAll(identity, 0700); err != nil {
		return err
	}
	config := payload.Config
	managedFCM := inspection.Manifest.Files["fcm.json"] != ""
	config.ManagedFCM = &managedFCM
	config.ClientCertFile = filepath.Join(identity, "client-chain.pem")
	config.ClientKeyFile = filepath.Join(identity, "client-key.pem")
	config.HospitalCAFile = ""
	for name, body := range map[string]string{"client-chain.pem": payload.ClientCertificatePEM, "client-key.pem": payload.ClientPrivateKeyPEM} {
		if err := writePrivate(filepath.Join(identity, name), []byte(body)); err != nil {
			return err
		}
	}
	if payload.HospitalCAPEM != "" {
		config.HospitalCAFile = filepath.Join(identity, "hospital-ca.pem")
		if err := writePrivate(config.HospitalCAFile, []byte(payload.HospitalCAPEM)); err != nil {
			return err
		}
	}
	journal := cutoverJournal{Version: 1, ID: transactionID, RestoreID: id, Installation: oldState.Installation, Phase: "prepared"}
	add := func(target string, body []byte, source string) error {
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		next := target + ".next-" + transactionID
		if source != "" {
			if err := copyBackupFile(source, next); err != nil {
				return err
			}
		} else if body != nil {
			if err := writePrivate(next, body); err != nil {
				return err
			}
		} else {
			next = ""
		}
		journal.Entries = append(journal.Entries, cutoverEntry{Target: target, Next: next, Old: target + ".before-" + transactionID})
		return nil
	}
	// Prepare the large immutable database copy before draining the destination.
	if err := add(s.m.options.DatabasePath, nil, filepath.Join(capture, "gateway.db")); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := add(s.m.options.DatabasePath+suffix, nil, ""); err != nil {
			return err
		}
	}
	configBody, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := add(filepath.Join(configDir, "encryption.key"), []byte(payload.EncryptionKey+"\n"), ""); err != nil {
		return err
	}
	if err := add(filepath.Join(configDir, "runtime.json"), configBody, ""); err != nil {
		return err
	}
	fcmSource := ""
	if inspection.Manifest.Files["fcm.json"] != "" {
		fcmSource = filepath.Join(capture, "fcm.json")
	}
	if err := add(filepath.Join(configDir, "integrations", "fcm.json"), nil, fcmSource); err != nil {
		return err
	}
	stateTarget := filepath.Join(s.dir, "state.enc")
	stateNext := stateTarget + ".next-" + transactionID
	if err := s.saveSealed(stateNext, imported); err != nil {
		return err
	}
	journal.Entries = append(journal.Entries, cutoverEntry{Target: stateTarget, Next: stateNext, Old: stateTarget + ".before-" + transactionID})
	if err := s.m.freeze(ctx, "restore"); err != nil {
		return err
	}
	s.m.mu.Lock()
	generation := s.m.generation
	s.m.mu.Unlock()
	fence := restoreFence{Version: 1, Kind: "restore", Generation: generation, Installation: oldState.Installation, CutoverID: transactionID, RestoreID: id}
	if err := persistJSON(filepath.Join(s.dir, "source-fence.json"), fence); err != nil {
		return err
	}
	for i := range journal.Entries {
		journal.Entries[i].Existed = exists(journal.Entries[i].Target)
	}
	if err := persistJSON(journalPath(configDir), journal); err != nil {
		return err
	}
	rollback := func(cause error) error {
		if err := recoverBackupCutover(configDir, s.m.options.DatabasePath); err != nil {
			return errors.New("restore interrupted; local recovery journal retained")
		}
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := s.m.resumeAfterRestoreRollback(recoveryCtx); err != nil {
			return errors.New("ข้อมูลเดิมกู้คืนแล้ว แต่เปิดบริการเดิมไม่สำเร็จ กรุณาตรวจหน้ากู้คืนก่อนใช้งาน")
		}
		return cause
	}
	// All backup settings writers already hold mu around saveLocked. Hold it
	// from the LAST (state.enc) swap through durable commit and memory handoff,
	// so a scheduler/OAuth/kit writer cannot put old settings beside new data.
	stateLocked := false
	defer func() {
		if stateLocked {
			s.mu.Unlock()
		}
	}()
	for _, entry := range journal.Entries {
		if entry.Target == stateTarget {
			s.mu.Lock()
			stateLocked = true
		}
		if entry.Existed {
			if err := os.Rename(entry.Target, entry.Old); err != nil {
				return rollback(err)
			}
		}
		if entry.Next != "" {
			if err := os.Rename(entry.Next, entry.Target); err != nil {
				return rollback(err)
			}
		}
		if err := syncBackupDir(filepath.Dir(entry.Target)); err != nil {
			return rollback(err)
		}
	}
	if err := quarantineRestored(ctx, s.m.options.DatabasePath); err != nil {
		return rollback(err)
	}
	var runtime Runtime
	if !inspection.Result.RepairRequired {
		runtime, err = s.m.options.Build(config, key)
		if err != nil {
			return rollback(err)
		}
	}
	if err := ctx.Err(); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return rollback(err)
	}
	journal.Phase = "active"
	if err := persistJSON(journalPath(configDir), journal); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		// A directory fsync can fail after the active journal was renamed.
		// Once that marker is visible, do not enter the pre-active rollback
		// path or reopen a generation using stale in-memory backup settings.
		var durable cutoverJournal
		if readBackupJSON(journalPath(configDir), &durable, 1<<20) == nil && durable.Phase == "active" && durable.ID == transactionID {
			s.state = imported
			return errors.New("ข้อมูลกู้คืนบันทึกถึงขั้นเปิดใช้งานแล้ว กรุณาเริ่ม Gateway ใหม่เพื่อตรวจ journal และเปิดบริการ")
		}
		return rollback(err)
	}
	// No automatic rollback beyond this durable activation boundary.
	s.state = imported
	if err := s.consumeRestoreRecoveryLocked(&journal); err != nil {
		return errors.New("ข้อมูลกู้คืนเปิดใช้งานแล้ว กรุณาเริ่ม Gateway ใหม่เพื่อจบการเชื่อม Google Drive")
	}
	s.mu.Unlock()
	stateLocked = false
	if err := finishActivatedCutover(configDir, journal); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return errors.New("ข้อมูลกู้คืนบันทึกแล้ว กรุณาเริ่ม Gateway ใหม่เพื่อจบการเปิดใช้งาน")
	}
	s.m.mu.Lock()
	s.m.config, s.m.key = config, key
	s.m.current = nil
	s.m.fenced = false
	if inspection.Result.RepairRequired {
		s.m.state = "recovery"
		s.m.reason = "กู้คืนข้อมูลครบแล้ว กรุณาต่ออายุใบรับรองด้วย private key เดิมก่อนเปิดการเชื่อมต่อ ERP"
	} else {
		s.m.install(runtime)
	}
	s.m.mu.Unlock()
	if err := os.RemoveAll(capture); err != nil {
		return errors.New("เปิดข้อมูลกู้คืนแล้ว แต่ลบพื้นที่ชั่วคราวไม่สำเร็จ")
	}
	return nil
}

// Never restore possibly revoked historical OAuth tokens from an archive. Only
// a fresh, explicitly reauthorized matching recovery context may provide them.
func (s *backupService) importedBackupStateLocked(imported backupState, installation string) backupState {
	imported.Installation = installation
	imported.Policy.Enabled = false
	imported.NextRun = time.Time{}
	imported.Drive.AccessToken = ""
	imported.Drive.RefreshToken = ""
	imported.Drive.Expiry = time.Time{}
	if recovery := s.recovery; recovery != nil && recovery.Dataset == imported.Dataset && recovery.Secret == imported.Secret && recovery.Credentials.ClientID == imported.Drive.ClientID && recovery.Credentials.FolderID == imported.Drive.FolderID && recovery.Credentials.RefreshToken != "" {
		imported.Drive = recovery.Credentials
	}
	return imported
}

func (s *backupService) consumeRestoreRecoveryLocked(journal *cutoverJournal) error {
	if journal.Phase != "active" {
		return errors.New("recovery context cannot be consumed before durable activation")
	}
	if journal.RecoveryConsumed {
		return nil
	}
	if err := s.clearCloudRecoveryLocked(); err != nil {
		return err
	}
	journal.RecoveryConsumed = true
	return persistJSON(journalPath(s.m.options.Directory), journal)
}

// Startup calls this after loading its private recovery context and before
// accepting requests. A crash after active commit cannot leave old recovery
// credentials preferred over the newly restored operational connection.
func (s *backupService) finishRestoreRecovery() error {
	var journal cutoverJournal
	if err := readBackupJSON(journalPath(s.m.options.Directory), &journal, 1<<20); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := validateCutoverJournal(journal, s.m.options.Directory, s.m.options.DatabasePath); err != nil {
		return err
	}
	if journal.Phase != "active" || journal.RecoveryConsumed {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumeRestoreRecoveryLocked(&journal)
}

func quarantineRestored(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []string{"UPDATE notify_jobs SET status='quarantined' WHERE status NOT IN ('accepted','invalid_token','sent','completed','failed','cancelled','quarantined')", "UPDATE update_jobs SET status='blocked',stage='restored_quarantine',message='Restored pending work requires administrator review' WHERE status NOT IN ('succeeded','failed','cancelled','expired','rolled_back')", "UPDATE update_rollouts SET state='paused'"}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query); err != nil && !strings.Contains(err.Error(), "no such table") {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

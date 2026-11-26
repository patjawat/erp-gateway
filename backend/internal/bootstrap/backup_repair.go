package bootstrap

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
)

// csrPrivateKey is called while the manager and configuration directory are
// locked. Recovery always takes the original persistent identity over a stale
// first-run CSR key. An unreadable existing identity is never regenerated.
func (m *Manager) csrPrivateKey() (crypto.Signer, error) {
	name := filepath.Join(m.options.Directory, "staged-client-key.pem")
	if exists(filepath.Join(m.options.Directory, "runtime.json")) {
		config, _, err := LoadRuntime(m.options.Directory)
		if err != nil {
			return nil, errors.New("อ่านข้อมูลเดิมไม่ได้ กรุณากู้คืนข้อมูลก่อนสร้าง CSR")
		}
		name = config.ClientKeyFile
	}
	if exists(name) {
		body, err := readBounded(name, 32<<10)
		if err != nil {
			return nil, errors.New("อ่าน private key เดิมไม่ได้ กรุณากู้คืนไฟล์เดิม")
		}
		private, err := identityPrivateKey(body)
		if err != nil {
			return nil, errors.New("private key เดิมไม่ถูกต้อง กรุณากู้คืนไฟล์เดิม")
		}
		return private, nil
	}
	if name != filepath.Join(m.options.Directory, "staged-client-key.pem") {
		return nil, errors.New("ไม่พบ private key เดิม ระบบจะไม่สร้างกุญแจทับ identity ที่กู้คืน")
	}
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	if err := atomicFile(name, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return private, nil
}

// RepairRestoredIdentity repairs time-invalid client/CA certificates after a
// complete restore without changing the original private key, encryption key,
// Admin verifier, hospitals or Mobile sessions. A nil roots slice preserves the
// old Hospital CA; an explicit empty slice selects normal system server roots.
// The caller must authenticate the local Setup session and enforce CSRF.
func (m *Manager) RepairRestoredIdentity(ctx context.Context, certPEM, rootsPEM []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.current != nil || m.fenced {
		return errors.New("ซ่อมใบรับรองได้เฉพาะเครื่องที่กู้คืนและยังไม่เปิดใช้งาน")
	}
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		return err
	}
	defer unlock()
	config, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		return err
	}
	if !config.VerifyPasswordVerifier() {
		return errors.New("บัญชีผู้ดูแลเดิมไม่ถูกต้อง กรุณากู้คืนข้อมูลใหม่")
	}
	private, err := readBounded(config.ClientKeyFile, 32<<10)
	if err != nil {
		return err
	}
	if len(certPEM) == 0 {
		certPEM, err = readBounded(config.ClientCertFile, 256<<10)
		if err != nil {
			return err
		}
	}
	if _, err := ValidateIdentity(certPEM, private); err != nil {
		return err
	}
	if rootsPEM == nil && config.HospitalCAFile != "" {
		rootsPEM, err = readBounded(config.HospitalCAFile, 256<<10)
		if err != nil {
			return err
		}
	}
	if err := validateHospitalCA(rootsPEM); err != nil {
		return err
	}
	if err := CheckDatabaseKey(m.options.DatabasePath, key); err != nil {
		return err
	}
	identity := filepath.Join(m.options.Directory, "identities", "repaired-"+backupID())
	if err := os.MkdirAll(identity, 0700); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(identity)
		}
	}()
	config.ClientCertFile = filepath.Join(identity, "client-chain.pem")
	config.ClientKeyFile = filepath.Join(identity, "client-key.pem")
	config.HospitalCAFile = ""
	if err := writePrivate(config.ClientCertFile, certPEM); err != nil {
		return err
	}
	if err := writePrivate(config.ClientKeyFile, private); err != nil {
		return err
	}
	if len(rootsPEM) > 0 {
		config.HospitalCAFile = filepath.Join(identity, "hospital-ca.pem")
		if err := writePrivate(config.HospitalCAFile, rootsPEM); err != nil {
			return err
		}
	}
	if err := m.validateStored(config, key); err != nil {
		return err
	}
	runtime, err := m.options.Build(config, key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return err
	}
	if err := persistJSON(filepath.Join(m.options.Directory, "runtime.json"), config); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return err
	}
	m.config, m.key = config, key
	m.generation++
	m.install(runtime)
	committed = true
	return nil
}

// resumeAfterRestoreRollback is used only after the journal has restored a
// consistent pre-activation generation. It must never run after active commit.
func (m *Manager) resumeAfterRestoreRollback(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = nil
	if m.closed {
		return errors.New("Gateway is closing")
	}
	var journal cutoverJournal
	if err := readBackupJSON(journalPath(m.options.Directory), &journal, 1<<20); err == nil && journal.Phase == "active" {
		m.fenced = true
		m.state = "recovery"
		m.reason = "พบข้อมูลกู้คืนที่เปิดใช้งานแล้ว กรุณาเริ่ม Gateway ใหม่เพื่อจบ journal"
		return errors.New("automatic pre-active rollback cannot cross an active journal")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists(filepath.Join(m.options.Directory, ".backup", "source-fence.json")) {
		m.fenced = true
		m.state = "recovery"
		m.reason = "ข้อมูลเดิมกู้คืนแล้ว แต่ยังมี fence ของการย้ายข้อมูล กรุณาตรวจสถานะปลายทางก่อนเปิดอีกครั้ง"
		return errors.New("an authoritative migration fence remains")
	}
	fail := func(err error) error {
		m.fenced = true
		m.state = "recovery"
		m.reason = "ข้อมูลเดิมกู้คืนแล้ว แต่ยังเปิดบริการไม่ได้ กรุณาตรวจ configuration, key และใบรับรองเดิม"
		return err
	}
	if !exists(filepath.Join(m.options.Directory, "runtime.json")) {
		m.fenced = false
		m.config = RuntimeConfig{}
		m.key = nil
		m.state = "setup"
		m.reason = ""
		if existingDatabase(m.options.DatabasePath) || exists(filepath.Join(m.options.Directory, "encryption.key")) {
			m.state = "recovery"
			m.reason = "ข้อมูลเดิมยังอยู่ กรุณากู้คืนการตั้งค่าและกุญแจเดิม"
		}
		return nil
	}
	config, key, err := LoadRuntime(m.options.Directory)
	if err != nil {
		return fail(err)
	}
	if err := m.validateStored(config, key); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	runtime, err := m.options.Build(config, key)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return fail(err)
	}
	m.config, m.key = config, key
	m.fenced = false
	m.generation++
	m.install(runtime)
	return nil
}

package bootstrap

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PasswordResetDurabilityWarning means the new password is already active and
// read back from disk, but the filesystem did not confirm durable persistence.
// Callers must report a successful reset with this warning, not retry using the
// old password or describe the reset as having failed.
type PasswordResetDurabilityWarning struct{}

func (*PasswordResetDurabilityWarning) Error() string {
	return "เปลี่ยนรหัสผ่านและเปิดใช้บัญชีใหม่แล้ว แต่ระบบจัดเก็บยังไม่ยืนยันการบันทึกถาวร กรุณาตรวจพื้นที่จัดเก็บและสำรอง config"
}

// ResetAdminPassword is reserved for the local server-owner control channel.
// It changes only the administrator verifier and hands off to a fresh runtime;
// the existing database, encryption key, username and TLS identity are retained.
func (m *Manager) ResetAdminPassword(ctx context.Context, password string) (string, error) {
	return m.resetAdminPassword(ctx, password, func(path string, cfg RuntimeConfig) error {
		return persistJSON(path, cfg)
	})
}

// The per-call persister isolates the atomic-rename/failed-directory-sync case
// without introducing a mutable production-wide persistence hook.
func (m *Manager) resetAdminPassword(ctx context.Context, password string, persist func(string, RuntimeConfig) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if password == "" || len(password) > 4096 {
		return "", errors.New("ระบุรหัสผ่านผู้ดูแลใหม่ที่ไม่ว่างและไม่เกิน 4096 ไบต์")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.closed {
		return "", errors.New("Gateway ปิดการทำงานแล้ว")
	}
	if m.current == nil || m.config.Version != 1 {
		return "", errors.New("คำสั่งนี้ใช้กับ Gateway ที่เปิดใช้งานผ่านเว็บแล้วเท่านั้น กรุณาใช้หน้า Setup สำหรับการติดตั้งครั้งแรก หรือเปลี่ยนรหัสใน environment ของโหมด legacy")
	}
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		return "", err
	}
	defer unlock()
	cfg, key, err := m.currentLocked()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(m.options.DatabasePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("ไม่พบฐานข้อมูลเดิมของ Gateway กรุณาตรวจ data volume หรือกู้คืน backup ระบบจะไม่สร้างฐานข้อมูลใหม่เพื่อรีเซ็ตรหัสผ่าน")
	}
	if err := CheckDatabaseKey(m.options.DatabasePath, key); err != nil {
		return "", err
	}
	credentials, err := passwordConfig(cfg.AdminUsername, password)
	if err != nil {
		return "", errors.New("เตรียมรหัสผ่านผู้ดูแลใหม่ไม่สำเร็จ")
	}
	cfg.PasswordSalt, cfg.PasswordHash, cfg.PasswordIterations = credentials.PasswordSalt, credentials.PasswordHash, credentials.PasswordIterations
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runtime, err := m.options.Build(cfg, key)
	closeCandidate := func() {
		if runtime.Close != nil {
			runtime.Close()
		}
	}
	if err != nil {
		closeCandidate()
		return "", fmt.Errorf("เตรียมระบบสำหรับรหัสผ่านใหม่ไม่สำเร็จ: %w", err)
	}
	if runtime.Handler == nil {
		closeCandidate()
		return "", errors.New("เตรียมระบบสำหรับรหัสผ่านใหม่ไม่สำเร็จ บัญชีเดิมยังใช้งานอยู่")
	}
	if err := ctx.Err(); err != nil {
		closeCandidate()
		return "", err
	}
	if err := persist(filepath.Join(m.options.Directory, "runtime.json"), cfg); err != nil {
		// atomicFile can successfully rename runtime.json, then fail to sync
		// its directory. Keep the live verifier coherent with a confirmed
		// committed file instead of returning with the old account active.
		if !passwordResetCommitted(m.options.Directory, cfg, key) {
			closeCandidate()
			return "", fmt.Errorf("บันทึกรหัสผ่านใหม่ไม่สำเร็จ บัญชีเดิมยังใช้งานอยู่: %w", err)
		}
		m.config = cfg
		m.install(runtime)
		return cfg.AdminUsername, &PasswordResetDurabilityWarning{}
	}
	m.config = cfg
	m.install(runtime)
	return cfg.AdminUsername, nil
}

func passwordResetCommitted(directory string, candidate RuntimeConfig, key []byte) bool {
	stored, storedKey, err := LoadRuntime(directory)
	if err != nil || subtle.ConstantTimeCompare(storedKey, key) != 1 {
		return false
	}
	want, wantErr := json.Marshal(candidate)
	got, gotErr := json.Marshal(stored)
	return wantErr == nil && gotErr == nil && bytes.Equal(want, got)
}

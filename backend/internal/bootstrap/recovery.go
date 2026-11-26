package bootstrap

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const recoverySchema = "erp-gateway-recovery.v1"
const maxRecoveryPayload = 2 << 20
const MaxRecoveryArchive = 4 << 20

var recoveryAAD = []byte("erp-gateway-recovery.v1/AES-256-GCM/PBKDF2-HMAC-SHA256/600000")

type recoveryEnvelope struct {
	Schema     string `json:"schema"`
	Cipher     string `json:"cipher"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// RecoveryPayload is a local-only decrypted representation. HTTP handlers must
// return only ExportRecovery's encrypted bytes, never this structure.
type RecoveryPayload struct {
	Version              int           `json:"version"`
	ExportedAt           int64         `json:"exported_at"`
	Config               RuntimeConfig `json:"config"`
	EncryptionKey        string        `json:"encryption_key"`
	ClientCertificatePEM string        `json:"client_certificate_pem"`
	ClientPrivateKeyPEM  string        `json:"client_private_key_pem"`
	HospitalCAPEM        string        `json:"hospital_ca_pem,omitempty"`
}

func recoveryPassphrase(passphrase string) error {
	if len(passphrase) < 16 || len(passphrase) > 4096 {
		return errors.New("รหัสผ่านไฟล์กู้คืนต้องยาว 16-4096 bytes")
	}
	return nil
}

func sealRecovery(ctx context.Context, payload RecoveryPayload, passphrase string) ([]byte, error) {
	if err := recoveryPassphrase(passphrase); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > maxRecoveryPayload {
		return nil, errors.New("recovery payload exceeds supported size")
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, passwordIterations, 32)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, raw, recoveryAAD)
	envelope := recoveryEnvelope{Schema: recoverySchema, Cipher: "AES-256-GCM", KDF: "PBKDF2-HMAC-SHA256", Iterations: passwordIterations, Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ciphertext)}
	return json.MarshalIndent(envelope, "", "  ")
}

func (m *Manager) ExportRecovery(ctx context.Context, passphrase string) ([]byte, error) {
	if err := recoveryPassphrase(passphrase); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	cfg, key, err := m.currentLocked()
	var cert, private, roots []byte
	if err == nil {
		err = m.validateStored(cfg, key)
	}
	if err == nil {
		cert, err = readBounded(cfg.ClientCertFile, 256<<10)
	}
	if err == nil {
		private, err = readBounded(cfg.ClientKeyFile, 32<<10)
	}
	if err == nil && cfg.HospitalCAFile != "" {
		roots, err = readBounded(cfg.HospitalCAFile, 256<<10)
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	payload := RecoveryPayload{Version: 1, ExportedAt: time.Now().Unix(), Config: cfg, EncryptionKey: base64.StdEncoding.EncodeToString(key), ClientCertificatePEM: string(cert), ClientPrivateKeyPEM: string(private), HospitalCAPEM: string(roots)}
	return sealRecovery(ctx, payload, passphrase)
}

func decodeRecoveryJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid trailing recovery data")
	}
	return nil
}

// DecryptRecovery is for local restoration/testing, not a web decryption route.
func DecryptRecovery(ctx context.Context, archive []byte, passphrase string) (RecoveryPayload, error) {
	var payload RecoveryPayload
	if err := recoveryPassphrase(passphrase); err != nil {
		return payload, err
	}
	if err := ctx.Err(); err != nil {
		return payload, err
	}
	if len(archive) == 0 || len(archive) > MaxRecoveryArchive {
		return payload, errors.New("recovery archive exceeds supported size")
	}
	var envelope recoveryEnvelope
	if decodeRecoveryJSON(archive, &envelope) != nil || envelope.Schema != recoverySchema || envelope.Cipher != "AES-256-GCM" || envelope.KDF != "PBKDF2-HMAC-SHA256" || envelope.Iterations != passwordIterations {
		return payload, errors.New("unsupported recovery archive format")
	}
	encoding := base64.StdEncoding.Strict()
	salt, e1 := encoding.DecodeString(envelope.Salt)
	nonce, e2 := encoding.DecodeString(envelope.Nonce)
	encrypted, e3 := encoding.DecodeString(envelope.Ciphertext)
	if e1 != nil || e2 != nil || e3 != nil || len(salt) != 16 || len(nonce) != 12 || len(encrypted) < 16 || len(encrypted) > maxRecoveryPayload+16 {
		return payload, errors.New("invalid recovery archive encoding")
	}
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, passwordIterations, 32)
	if err != nil {
		return payload, err
	}
	if err = ctx.Err(); err != nil {
		return payload, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return payload, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return payload, err
	}
	raw, err := gcm.Open(nil, nonce, encrypted, recoveryAAD)
	if err != nil {
		return payload, errors.New("รหัสผ่านไฟล์กู้คืนไม่ถูกต้อง หรือไฟล์เสียหาย")
	}
	if decodeRecoveryJSON(raw, &payload) != nil || payload.Version != 1 || payload.ExportedAt <= 0 {
		return RecoveryPayload{}, errors.New("invalid decrypted recovery payload")
	}
	return payload, nil
}

func validateRecoveryPayload(p RecoveryPayload, validatePolicy func([]string, []string) error) ([]byte, error) {
	if p.Version != 1 || p.Config.Version != 1 || p.Config.AdminUsername == "" || p.Config.AdminUsername != strings.TrimSpace(p.Config.AdminUsername) || len(p.Config.AdminUsername) > 100 || strings.ContainsAny(p.Config.AdminUsername, "\r\n\t") || p.Config.PasswordIterations != passwordIterations {
		return nil, errors.New("invalid recovery administrator configuration")
	}
	encoded, err := json.Marshal(p.Config)
	if err != nil || len(encoded) > 1<<20 {
		return nil, errors.New("recovery configuration exceeds supported size")
	}
	salt, e1 := base64.StdEncoding.Strict().DecodeString(p.Config.PasswordSalt)
	hash, e2 := base64.StdEncoding.Strict().DecodeString(p.Config.PasswordHash)
	if e1 != nil || e2 != nil || len(salt) != 16 || len(hash) != 32 {
		return nil, errors.New("invalid recovery administrator verifier")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(p.EncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid recovery encryption key")
	}
	if err = validatePublicSettings(publicSettings(p.Config), validatePolicy); err != nil {
		return nil, err
	}
	if _, err = ValidateIdentity([]byte(p.ClientCertificatePEM), []byte(p.ClientPrivateKeyPEM)); err != nil {
		return nil, err
	}
	if err = validateHospitalCA([]byte(p.HospitalCAPEM)); err != nil {
		return nil, err
	}
	return key, nil
}

// RestoreRecovery writes only fixed file names in a NEW private directory.
// A consistent DB backup must be restored separately; startup checks its key.
func RestoreRecovery(ctx context.Context, archive []byte, passphrase, destination string, validatePolicy func([]string, []string) error) error {
	payload, err := DecryptRecovery(ctx, archive, passphrase)
	if err != nil {
		return err
	}
	key, err := validateRecoveryPayload(payload, validatePolicy)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return errors.New("recovery destination parent must already exist")
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if strings.Contains(destination, string(filepath.Separator)+"erp"+string(filepath.Separator)) || strings.HasSuffix(destination, string(filepath.Separator)+"erp") {
		return errors.New("recovery configuration must remain outside ERP")
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return errors.New("recovery destination must be a new directory; existing files are never overwritten")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()
	cfg := payload.Config
	// Do not trust or preserve paths from the encrypted payload when restoring.
	cfg.ClientCertFile = filepath.Join(destination, "identity", "client-chain.pem")
	cfg.ClientKeyFile = filepath.Join(destination, "identity", "client-key.pem")
	cfg.HospitalCAFile = ""
	if err = atomicFile(filepath.Join(destination, "encryption.key"), []byte(base64.StdEncoding.EncodeToString(key)+"\n")); err != nil {
		return err
	}
	if err = atomicFile(cfg.ClientCertFile, []byte(payload.ClientCertificatePEM)); err != nil {
		return err
	}
	if err = atomicFile(cfg.ClientKeyFile, []byte(payload.ClientPrivateKeyPEM)); err != nil {
		return err
	}
	if payload.HospitalCAPEM != "" {
		cfg.HospitalCAFile = filepath.Join(destination, "identity", "hospital-ca.pem")
		if err = atomicFile(cfg.HospitalCAFile, []byte(payload.HospitalCAPEM)); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = persistJSON(filepath.Join(destination, "runtime.json"), cfg); err != nil {
		return err
	}
	complete = true
	return nil
}

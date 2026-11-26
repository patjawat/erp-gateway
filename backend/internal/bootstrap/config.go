// Package bootstrap provides a fail-closed first-run and recovery envelope for Gateway.
package bootstrap

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const passwordIterations = 600000

type RuntimeConfig struct {
	Version              int      `json:"version"`
	AdminUsername        string   `json:"admin_username"`
	PasswordSalt         string   `json:"password_salt"`
	PasswordHash         string   `json:"password_hash"`
	PasswordIterations   int      `json:"password_iterations"`
	PublicURL            string   `json:"public_url"`
	TrustedProxyCIDRs    []string `json:"trusted_proxy_cidrs"`
	ApprovedUpstreams    []string `json:"approved_upstreams"`
	PrivateUpstreamCIDRs []string `json:"private_upstream_cidrs"`
	ClientCertFile       string   `json:"client_cert_file"`
	ClientKeyFile        string   `json:"client_key_file"`
	HospitalCAFile       string   `json:"hospital_ca_file,omitempty"`
}

type Input struct {
	AdminUsername        string   `json:"admin_username"`
	AdminPassword        string   `json:"admin_password"`
	PublicURL            string   `json:"public_url"`
	TrustedProxyCIDRs    []string `json:"trusted_proxy_cidrs"`
	ApprovedUpstreams    []string `json:"approved_upstreams"`
	PrivateUpstreamCIDRs []string `json:"private_upstream_cidrs"`
	ClientCertificatePEM string   `json:"client_certificate_pem"`
	ClientPrivateKeyPEM  string   `json:"client_private_key_pem"`
	HospitalCAPEM        string   `json:"hospital_ca_pem"`
	EncryptionKey        string   `json:"encryption_key,omitempty"`
}

func (c RuntimeConfig) VerifyPassword(password string) bool {
	if c.PasswordIterations != passwordIterations || len(password) > 4096 {
		return false
	}
	salt, e1 := base64.StdEncoding.DecodeString(c.PasswordSalt)
	want, e2 := base64.StdEncoding.DecodeString(c.PasswordHash)
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, c.PasswordIterations, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func passwordConfig(username, password string) (RuntimeConfig, error) {
	c := RuntimeConfig{Version: 1, AdminUsername: username, PasswordIterations: passwordIterations}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return c, err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return c, err
	}
	c.PasswordSalt, c.PasswordHash = base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(hash)
	return c, nil
}

// LoadRuntime never invents a replacement key or repairs partial configuration.
func LoadRuntime(dir string) (RuntimeConfig, []byte, error) {
	var c RuntimeConfig
	body, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err != nil {
		return c, nil, err
	}
	if len(body) > 1<<20 || json.Unmarshal(body, &c) != nil || c.Version != 1 || c.AdminUsername == "" || c.PasswordIterations != passwordIterations {
		return c, nil, errors.New("invalid persistent Gateway configuration")
	}
	key, err := readEncryptionKey(dir)
	if err != nil {
		return c, nil, err
	}
	// The browser cannot choose a server path. Persistent identity files must be in this volume.
	for _, name := range []string{c.ClientCertFile, c.ClientKeyFile, c.HospitalCAFile} {
		if name != "" && !within(dir, name) {
			return c, nil, errors.New("identity file must be inside Gateway configuration volume")
		}
	}
	salt, e1 := base64.StdEncoding.DecodeString(c.PasswordSalt)
	hash, e2 := base64.StdEncoding.DecodeString(c.PasswordHash)
	if e1 != nil || e2 != nil || len(salt) != 16 || len(hash) != 32 {
		return c, nil, errors.New("invalid persisted administrator verifier")
	}
	return c, key, nil
}

func within(dir, name string) bool {
	root, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return false
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func readEncryptionKey(dir string) ([]byte, error) {
	body, err := os.ReadFile(filepath.Join(dir, "encryption.key"))
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("persistent encryption key is invalid; restore the original key")
	}
	return key, nil
}

func publicURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(value, "\\\r\n") {
		return nil, errors.New("ที่อยู่ Gateway ต้องเป็น HTTPS และไม่มี path, query หรือข้อมูลผู้ใช้")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("พอร์ต HTTPS ของ Gateway ต้องอยู่ระหว่าง 1-65535")
		}
	}
	return u, nil
}

func validateInput(in Input) error {
	if in.AdminUsername == "" || in.AdminUsername != strings.TrimSpace(in.AdminUsername) || len(in.AdminUsername) > 100 || strings.ContainsAny(in.AdminUsername, "\r\n\t") || len(in.AdminPassword) < 16 || len(in.AdminPassword) > 4096 {
		return errors.New("ระบุชื่อผู้ดูแลและรหัสผ่านอย่างน้อย 16 ตัวอักษร")
	}
	if _, err := publicURL(in.PublicURL); err != nil {
		return err
	}
	if len(in.TrustedProxyCIDRs) > 32 || len(in.ApprovedUpstreams) > 10000 || len(in.PrivateUpstreamCIDRs) > 128 {
		return errors.New("จำนวนการตั้งค่าเครือข่ายเกินขอบเขต")
	}
	for _, value := range in.TrustedProxyCIDRs {
		ip, network, err := net.ParseCIDR(value)
		ones, _ := networkSize(network)
		if err != nil || ip == nil || ones == 0 || ip.IsUnspecified() {
			return errors.New("ระบุ Trusted proxy เป็น CIDR เฉพาะเครื่องหรือเครือข่ายที่ดูแล")
		}
	}
	if _, err := ValidateIdentity([]byte(in.ClientCertificatePEM), []byte(in.ClientPrivateKeyPEM)); err != nil {
		return err
	}
	return validateHospitalCA([]byte(in.HospitalCAPEM))
}

func networkSize(n *net.IPNet) (int, int) {
	if n == nil {
		return 0, 0
	}
	return n.Mask.Size()
}

// ValidateIdentity verifies the imported key, purpose, dates and complete chain.
// Supplied roots are an explicit trust decision by the authenticated operator.
func ValidateIdentity(certPEM, keyPEM []byte) (tls.Certificate, error) {
	if len(certPEM) > 256<<10 || len(keyPEM) > 32<<10 {
		return tls.Certificate{}, errors.New("ไฟล์ใบรับรองหรือ private key ใหญ่เกินขอบเขต")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(pair.Certificate) == 0 {
		return pair, errors.New("ใบรับรองและ private key ไม่ตรงกัน หรือรูปแบบ PEM ไม่ถูกต้อง")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA {
		return pair, errors.New("ใบ Gateway ต้องเป็น leaf certificate สำหรับ clientAuth")
	}
	clientAuth := false
	for _, usage := range leaf.ExtKeyUsage {
		clientAuth = clientAuth || usage == x509.ExtKeyUsageClientAuth
	}
	if !clientAuth {
		return pair, errors.New("ใบ Gateway ต้องระบุ clientAuth โดยชัดเจน")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return pair, errors.New("อ่าน system trust store ไม่สำเร็จ")
	}
	intermediates := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return pair, errors.New("certificate chain มี issuer ที่ไม่ถูกต้อง")
		}
		if cert.CheckSignatureFrom(cert) == nil {
			roots.AddCert(cert)
		} else {
			intermediates.AddCert(cert)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: time.Now()}); err != nil {
		return pair, errors.New("ตรวจ chain, clientAuth หรืออายุใบรับรองไม่ผ่าน กรุณานำเข้า chain ครบถึง root ที่เชื่อถือ")
	}
	pair.Leaf = leaf
	return pair, nil
}

func validateHospitalCA(body []byte) error {
	if len(body) == 0 {
		return nil // Existing publicly trusted Hospital HTTPS endpoints use system roots.
	}
	if len(body) > 256<<10 {
		return errors.New("Hospital CA ใหญ่เกินขอบเขต")
	}
	count := 0
	for len(strings.TrimSpace(string(body))) > 0 {
		block, rest := pem.Decode(body)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("Hospital CA ต้องเป็น PEM certificate เท่านั้น")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || !time.Now().Before(cert.NotAfter) || time.Now().Before(cert.NotBefore) {
			return errors.New("Hospital CA ไม่ถูกต้องหรือหมดอายุ")
		}
		body, count = rest, count+1
	}
	if count == 0 {
		return errors.New("ไม่พบ Hospital CA ในไฟล์ PEM")
	}
	return nil
}

func exists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func existingDatabase(path string) bool {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if exists(path + suffix) {
			return true
		}
	}
	return false
}

// CheckDatabaseKey verifies the sentinel through a read-only SQLite connection.
// Unlike opening the operational store it cannot create tables, migrations or keys.
func CheckDatabaseKey(path string, key []byte) error {
	if !existingDatabase(path) {
		return nil
	}
	if len(key) != 32 {
		return errors.New("ต้องใช้ encryption key เดิมเพื่อปลดล็อกข้อมูล")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	u := &url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return errors.New("เปิดฐานข้อมูลเดิมแบบอ่านอย่างเดียวไม่สำเร็จ")
	}
	defer db.Close()
	var sealed []byte
	if err = db.QueryRow("SELECT value FROM secrets_check WHERE id=1").Scan(&sealed); err != nil {
		return errors.New("ตรวจข้อมูลเดิมไม่ได้ กรุณากู้คืนฐานข้อมูลและ encryption key จาก backup")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		return errors.New("ข้อมูลตรวจสอบ encryption key ไม่ถูกต้อง")
	}
	n := aead.NonceSize()
	plain, err := aead.Open(nil, sealed[:n], sealed[n:], []byte("store-check"))
	if err != nil || string(plain) != "erp-platform-gateway" {
		return errors.New("encryption key ไม่ตรงกับข้อมูลเดิม ระบบจะไม่สร้าง key ใหม่หรือแก้ฐานข้อมูล")
	}
	return nil
}

func atomicFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), ".pending-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmpName, name); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func token() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}

func persistJSON(name string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(name, append(body, '\n'))
}

func readBounded(name string, max int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, fmt.Errorf("invalid file %s", filepath.Base(name))
	}
	return os.ReadFile(name)
}

package gateway

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Shared across handler generations so a reload cannot race a pending CSR.
var identityOperation sync.Mutex

type certificateInfo struct {
	Subject       string `json:"subject"`
	Serial        string `json:"serial"`
	NotBefore     int64  `json:"not_before"`
	NotAfter      int64  `json:"not_after"`
	DaysRemaining int    `json:"days_remaining"`
	Status        string `json:"status"`
	Fingerprint   string `json:"fingerprint"`
}

func publicKeyFingerprint(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])
}
func (s *Server) gatewayCertificate() *certificateInfo {
	pair := s.config.GatewayClientCert
	if pair == nil || len(pair.Certificate) == 0 {
		return nil
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil
	}
	status := "valid"
	now := time.Now()
	days := int(c.NotAfter.Sub(now).Hours() / 24)
	if now.Before(c.NotBefore) {
		status = "not_yet_valid"
	} else if !now.Before(c.NotAfter) {
		status = "expired"
	} else if days <= 30 {
		status = "expiring"
	}
	return &certificateInfo{Subject: c.Subject.String(), Serial: c.SerialNumber.String(), NotBefore: c.NotBefore.Unix(), NotAfter: c.NotAfter.Unix(), DaysRemaining: days, Status: status, Fingerprint: publicKeyFingerprint(c.RawSubjectPublicKeyInfo)}
}

type certificateRequest struct {
	CSR         string `json:"csr_pem"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"created_at"`
}
type storedCertificateRequest struct {
	certificateRequest
	KeyPEM string `json:"private_key_pem"`
}

func (s *Server) pendingCertificate() (*storedCertificateRequest, error) {
	if s.config.IdentityDirectory == "" {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(s.config.IdentityDirectory, "pending.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, errors.New("invalid certificate request file")
	}
	var pending storedCertificateRequest
	if err = json.NewDecoder(f).Decode(&pending); err != nil {
		return nil, err
	}
	return &pending, nil
}
func (s *Server) adminCertificates(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	identityOperation.Lock()
	defer identityOperation.Unlock()
	pending, err := s.pendingCertificate()
	if err != nil {
		unavailable(w)
		return
	}
	var public *certificateRequest
	if pending != nil {
		public = &pending.certificateRequest
	}
	enabled := s.config.IdentityDirectory != "" && s.config.ActivateIdentity != nil
	writeData(w, map[string]any{"gateway": s.gatewayCertificate(), "issuer": map[string]string{"status": "not_configured", "message": "ลงนาม CSR ด้วย CA เดิม แล้วนำเข้าใบที่ลงนามแล้ว รักษา root CA แบบออฟไลน์"}, "capabilities": map[string]bool{"csr": enabled, "import": enabled, "renew": false}, "pending_request": public})
}
func (s *Server) adminCertificateCSR(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if s.config.IdentityDirectory == "" || s.config.ActivateIdentity == nil {
		fail(w, 409, "PERSISTED_SETUP_REQUIRED", "นำเข้า Gateway เดิมผ่านการตั้งค่าถาวรก่อนจัดการใบรับรองผ่านเว็บ")
		return
	}
	identityOperation.Lock()
	defer identityOperation.Unlock()
	// Retry returns the same CSR, rather than replacing an in-flight request.
	if pending, err := s.pendingCertificate(); err != nil {
		unavailable(w)
		return
	} else if pending != nil {
		writeData(w, pending.certificateRequest)
		return
	}
	var signer crypto.Signer
	subject := pkix.Name{CommonName: "ERP Mobile Gateway"}
	if pair := s.config.GatewayClientCert; pair != nil {
		var ok bool
		signer, ok = pair.PrivateKey.(crypto.Signer)
		if !ok {
			unavailable(w)
			return
		}
		if len(pair.Certificate) > 0 {
			c, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				unavailable(w)
				return
			}
			subject = c.Subject
		}
	} else {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			unavailable(w)
			return
		}
		signer = key
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: subject}, signer)
	if err != nil {
		unavailable(w)
		return
	}
	private, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		unavailable(w)
		return
	}
	pub, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		unavailable(w)
		return
	}
	pending := storedCertificateRequest{certificateRequest: certificateRequest{CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), Fingerprint: publicKeyFingerprint(pub), CreatedAt: time.Now().Unix()}, KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))}
	if err = os.MkdirAll(s.config.IdentityDirectory, 0700); err != nil {
		unavailable(w)
		return
	}
	b, _ := json.Marshal(pending)
	f, err := os.CreateTemp(s.config.IdentityDirectory, ".csr-*")
	if err != nil {
		unavailable(w)
		return
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(s.config.IdentityDirectory, "pending.json"))
	}
	if err != nil {
		unavailable(w)
		return
	}
	_, err = s.config.Store.db.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','certificate_csr','Gateway client identity; existing key retained',?)", time.Now().Unix())
	if err != nil {
		unavailable(w)
		return
	}
	writeData(w, pending.certificateRequest)
}

func validateClientChain(certPEM, keyPEM []byte, current *tls.Certificate) error {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || len(pair.Certificate) < 2 {
		return errors.New("ต้องมีใบ client และ chain จนถึง root CA ที่ลงนามถูกต้อง และตรงกับ CSR")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.IsCA {
		return errors.New("ใบรับรองต้องเป็น client leaf certificate")
	}
	clientAuth := false
	for _, eku := range leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			clientAuth = true
		}
	}
	if !clientAuth {
		return errors.New("ใบรับรองต้องมี clientAuth สำหรับ Gateway")
	}
	root, err := x509.ParseCertificate(pair.Certificate[len(pair.Certificate)-1])
	if err != nil || !root.IsCA || root.CheckSignatureFrom(root) != nil {
		return errors.New("แนบ chain ที่มี root CA ครบถ้วน")
	}
	if current != nil && len(current.Certificate) > 1 {
		old, e := x509.ParseCertificate(current.Certificate[len(current.Certificate)-1])
		if e == nil && old.IsCA && old.CheckSignatureFrom(old) == nil && !old.Equal(root) {
			return errors.New("การเปลี่ยน CA ต้องกระจาย trust ให้หน่วยงานก่อน ใช้ CA เดิมในการต่ออายุครั้งนี้")
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1 : len(pair.Certificate)-1] {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			return errors.New("chain ไม่ถูกต้อง")
		}
		intermediates.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: time.Now()})
	if err != nil {
		return errors.New("chain, อายุ หรือ purpose ของใบรับรองไม่ผ่านการตรวจสอบ")
	}
	return nil
}
func (s *Server) adminCertificateImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if s.config.IdentityDirectory == "" || s.config.ActivateIdentity == nil {
		fail(w, 409, "PERSISTED_SETUP_REQUIRED", "ตั้งค่าถาวรก่อนนำเข้าใบรับรอง")
		return
	}
	var input struct {
		CertificatePEM string `json:"certificate_pem"`
		HospitalCAPEM  string `json:"hospital_ca_pem"`
	}
	if !decodeLimit(w, r, &input, 2<<20) {
		return
	}
	identityOperation.Lock()
	defer identityOperation.Unlock()
	pending, err := s.pendingCertificate()
	if err != nil {
		unavailable(w)
		return
	}
	if pending == nil {
		fail(w, 409, "CSR_REQUIRED", "สร้าง CSR ก่อนนำเข้าใบที่ลงนามแล้ว")
		return
	}
	if err = validateClientChain([]byte(input.CertificatePEM), []byte(pending.KeyPEM), s.config.GatewayClientCert); err != nil {
		fail(w, 400, "INVALID_CERTIFICATE", err.Error())
		return
	}
	if input.HospitalCAPEM != "" {
		fail(w, 409, "TRUST_ROTATION_REQUIRED", "การเปลี่ยน Hospital CA ต้องตรวจแผน trust overlap แยกจากการต่ออายุ Gateway client")
		return
	}
	if err = s.config.ActivateIdentity(r.Context(), []byte(input.CertificatePEM), []byte(pending.KeyPEM), nil); err != nil {
		fail(w, 409, "CERTIFICATE_ACTIVATION_FAILED", "ยังเปิดใช้ใบใหม่ไม่ได้ ระบบยังคงใช้ใบเดิม กรุณาตรวจ chain และการตั้งค่า")
		return
	}
	_ = os.Remove(filepath.Join(s.config.IdentityDirectory, "pending.json"))
	_, _ = s.config.Store.db.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','certificate_activate','Gateway client certificate activated; SPKI retained',?)", time.Now().Unix())
	writeData(w, map[string]bool{"activated": true})
}

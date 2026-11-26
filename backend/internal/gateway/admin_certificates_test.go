package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testClientIdentity(t *testing.T) (tls.Certificate, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "offline test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "ERP Mobile Gateway"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	pair, err := tls.X509KeyPair(chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair, ca, caKey
}
func signTestCSR(t *testing.T, csr string, ca *x509.Certificate, key *ecdsa.PrivateKey, eku x509.ExtKeyUsage) string {
	t.Helper()
	block, _ := pem.Decode([]byte(csr))
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = request.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: request.Subject, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{eku}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, request.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...))
}
func TestCertificateCSRAndImportKeepKeyPrivateAndPinStable(t *testing.T) {
	r := rig(t)
	pair, ca, caKey := testClientIdentity(t)
	r.app.config.GatewayClientCert = &pair
	r.app.config.IdentityDirectory = t.TempDir()
	activations := 0
	r.app.config.ActivateIdentity = func(_ context.Context, cert, key, roots []byte) error {
		activations++
		if len(roots) != 0 {
			t.Fatal("unexpected trust replacement")
		}
		return validateClientChain(cert, key, &pair)
	}
	if w := r.request(t, "POST", "/admin/api/certificates/csr", map[string]any{}, "", false); w.Code != 401 {
		t.Fatal("CSR lacked admin auth")
	}
	w := r.request(t, "POST", "/admin/api/certificates/csr", map[string]any{}, "", true)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("PRIVATE KEY")) {
		t.Fatal(w.Code, w.Body.String())
	}
	var envelope struct {
		Data certificateRequest `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	if envelope.Data.Fingerprint != publicKeyFingerprint(leaf.RawSubjectPublicKeyInfo) {
		t.Fatal("CSR changed SPKI")
	}
	info, err := os.Stat(filepath.Join(r.app.config.IdentityDirectory, "pending.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private request file permissions", err)
	}
	retry := r.request(t, "POST", "/admin/api/certificates/csr", map[string]any{}, "", true)
	if !bytes.Equal(w.Body.Bytes(), retry.Body.Bytes()) {
		t.Fatal("CSR retry rotated the key")
	}
	bad := signTestCSR(t, envelope.Data.CSR, ca, caKey, x509.ExtKeyUsageServerAuth)
	if w = r.request(t, "POST", "/admin/api/certificates/import", map[string]string{"certificate_pem": bad}, "", true); w.Code != 400 || activations != 0 {
		t.Fatal("accepted serverAuth cert", w.Code)
	}
	_, otherCA, otherKey := testClientIdentity(t)
	rotated := signTestCSR(t, envelope.Data.CSR, otherCA, otherKey, x509.ExtKeyUsageClientAuth)
	if w = r.request(t, "POST", "/admin/api/certificates/import", map[string]string{"certificate_pem": rotated}, "", true); w.Code != 400 || activations != 0 {
		t.Fatal("silently changed CA", w.Code)
	}
	good := signTestCSR(t, envelope.Data.CSR, ca, caKey, x509.ExtKeyUsageClientAuth)
	w = r.request(t, "POST", "/admin/api/certificates/import", map[string]string{"certificate_pem": good}, "", true)
	if w.Code != 200 || activations != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = os.Stat(filepath.Join(r.app.config.IdentityDirectory, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("pending private material retained", err)
	}
}

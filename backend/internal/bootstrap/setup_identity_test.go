package bootstrap

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupCSRForIdentityTest(t *testing.T, m *Manager, cookie *http.Cookie, csrf string) string {
	t.Helper()
	w := request(t, m, "/setup/api/csr", map[string]any{}, cookie, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("PRIVATE KEY")) {
		t.Fatal("CSR response exposed a private key")
	}
	var result struct {
		Data struct {
			CSR string `json:"csr_pem"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Data.CSR == "" {
		t.Fatal("missing CSR")
	}
	return result.Data.CSR
}

func signPublicKeyForIdentityTest(t *testing.T, ca authority, publicKey any) string {
	t.Helper()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "Test Gateway"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca.cert, publicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return string(append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.pem...))
}

func signCSRForIdentityTest(t *testing.T, csrPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		t.Fatal("missing CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		t.Fatal("invalid CSR proof of possession")
	}
	return signPublicKeyForIdentityTest(t, testCA(t), csr.PublicKey)
}

func assertIdentitySetupUnchanged(t *testing.T, m *Manager, staged []byte) {
	t.Helper()
	if staged != nil {
		after, err := os.ReadFile(filepath.Join(m.options.Directory, "staged-client-key.pem"))
		if err != nil || !bytes.Equal(after, staged) {
			t.Fatal("validation changed the staged key")
		}
	}
	for _, name := range []string{"encryption.key", "runtime.json", "identities"} {
		if exists(filepath.Join(m.options.Directory, name)) {
			t.Fatalf("failed validation created %s", name)
		}
	}
	if m.current != nil {
		t.Fatal("validation installed a runtime")
	}
}

func TestSetupCSRReadinessAndActivationWithBlankPrivateKey(t *testing.T) {
	for _, blank := range []string{"", " \t\r\n "} {
		t.Run("key="+strings.ReplaceAll(blank, "\n", "newline"), func(t *testing.T) {
			m := managerForTest(t, dummyRuntime)
			cookie, csrf := setupSessionForTest(t, m)
			csr := setupCSRForIdentityTest(t, m, cookie, csrf)
			staged, err := os.ReadFile(filepath.Join(m.options.Directory, "staged-client-key.pem"))
			if err != nil {
				t.Fatal(err)
			}
			in := testInput(t)
			in.ClientPrivateKeyPEM, in.ClientCertificatePEM = blank, signCSRForIdentityTest(t, csr)
			if w := request(t, m, "/setup/api/validate", in, cookie, csrf); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			assertIdentitySetupUnchanged(t, m, staged)
			if w := request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			cfg, _, err := LoadRuntime(m.options.Directory)
			if err != nil {
				t.Fatal(err)
			}
			active, err := os.ReadFile(cfg.ClientKeyFile)
			if err != nil || !bytes.Equal(staged, active) {
				t.Fatal("activation did not preserve the staged key")
			}
		})
	}
}

func TestSetupCSRKeySurvivesRepeatAndRestart(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	cookie, csrf := setupSessionForTest(t, m)
	first := setupCSRForIdentityTest(t, m, cookie, csrf)
	staged, err := os.ReadFile(filepath.Join(m.options.Directory, "staged-client-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	_ = setupCSRForIdentityTest(t, m, cookie, csrf)
	assertIdentitySetupUnchanged(t, m, staged)
	m.Close()
	restarted, err := New(m.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	cookie, csrf = setupSessionForTest(t, restarted)
	_ = setupCSRForIdentityTest(t, restarted, cookie, csrf)
	assertIdentitySetupUnchanged(t, restarted, staged)
	in := testInput(t)
	in.ClientPrivateKeyPEM, in.ClientCertificatePEM = "", signCSRForIdentityTest(t, first)
	for _, path := range []string{"/setup/api/validate", "/setup/api/activate"} {
		if w := request(t, restarted, path, in, cookie, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestSetupReportsStagedKeyProblems(t *testing.T) {
	for _, test := range []struct {
		name, contents, message string
		status                  int
		directory               bool
	}{
		{name: "missing", message: "ไม่พบ private key ที่ Gateway สร้างไว้", status: 400},
		{name: "unreadable", directory: true, message: "อ่าน private key ที่ Gateway เก็บไว้ไม่ได้", status: 409},
		{name: "corrupt", contents: "broken-key", message: "private key ที่ Gateway เก็บไว้ไม่ถูกต้อง", status: 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := managerForTest(t, dummyRuntime)
			cookie, csrf := setupSessionForTest(t, m)
			name := filepath.Join(m.options.Directory, "staged-client-key.pem")
			if test.directory {
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			} else if test.contents != "" {
				if err := os.WriteFile(name, []byte(test.contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			in := testInput(t)
			in.ClientPrivateKeyPEM = ""
			for _, path := range []string{"/setup/api/validate", "/setup/api/activate"} {
				w := request(t, m, path, in, cookie, csrf)
				if w.Code != test.status || !strings.Contains(w.Body.String(), test.message) {
					t.Fatal(w.Code, w.Body.String())
				}
				var expected []byte
				if test.contents != "" {
					expected = []byte(test.contents)
				}
				assertIdentitySetupUnchanged(t, m, expected)
				info, err := os.Lstat(name)
				if test.directory && (err != nil || !info.IsDir()) {
					t.Fatal("unreadable key path was changed")
				}
				if test.name == "missing" && !os.IsNotExist(err) {
					t.Fatal("missing key was created")
				}
			}
		})
	}
}

func TestSetupInvalidCertificateLeavesStagedKeyUnchanged(t *testing.T) {
	for _, test := range []struct{ name, message string }{
		{"missing", "กรุณาระบุใบรับรอง client"},
		{"malformed", "PEM CERTIFICATE"},
		{"malformed-der", "PEM CERTIFICATE"},
		{"csr", "ไฟล์นี้เป็น CSR"},
		{"ca-only", "ใบแรกเป็นใบ CA"},
		{"ca-first", "ใบแรกเป็นใบ CA"},
		{"mismatch", "CSR ของ Gateway เครื่องนี้"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := managerForTest(t, dummyRuntime)
			cookie, csrf := setupSessionForTest(t, m)
			csr := setupCSRForIdentityTest(t, m, cookie, csrf)
			staged, err := os.ReadFile(filepath.Join(m.options.Directory, "staged-client-key.pem"))
			if err != nil {
				t.Fatal(err)
			}
			in := testInput(t)
			in.ClientPrivateKeyPEM = ""
			switch test.name {
			case "missing":
				in.ClientCertificatePEM = ""
			case "malformed":
				in.ClientCertificatePEM = "not a PEM certificate"
			case "malformed-der":
				in.ClientCertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid DER")}))
			case "csr":
				in.ClientCertificatePEM = csr
			case "ca-only":
				in.ClientCertificatePEM = string(testCA(t).pem)
			case "ca-first":
				ca := testCA(t)
				in.ClientCertificatePEM = string(ca.pem) + signCSRForIdentityTest(t, csr)
			}
			for _, path := range []string{"/setup/api/validate", "/setup/api/activate"} {
				w := request(t, m, path, in, cookie, csrf)
				if w.Code != 400 || !strings.Contains(w.Body.String(), test.message) {
					t.Fatal(w.Code, w.Body.String())
				}
				assertIdentitySetupUnchanged(t, m, staged)
			}
		})
	}
}

func TestInvalidIdentityReplacementPreservesActiveIdentity(t *testing.T) {
	m := managerForTest(t, dummyRuntime)
	cookie, csrf := setupSessionForTest(t, m)
	in := testInput(t)
	if w := request(t, m, "/setup/api/activate", in, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg, current := m.config, m.current
	before, err := os.ReadFile(filepath.Join(m.options.Directory, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cert := range []string{"", "invalid PEM", string(testCA(t).pem), testInput(t).ClientCertificatePEM} {
		if err := m.ReplaceIdentity(context.Background(), []byte(cert), []byte(in.ClientPrivateKeyPEM), nil); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		after, err := os.ReadFile(filepath.Join(m.options.Directory, "runtime.json"))
		if err != nil || !bytes.Equal(before, after) || m.current != current || m.config.ClientCertFile != cfg.ClientCertFile || m.config.ClientKeyFile != cfg.ClientKeyFile {
			t.Fatal("rejected replacement changed active identity")
		}
		activeCert, certErr := os.ReadFile(cfg.ClientCertFile)
		activeKey, keyErr := os.ReadFile(cfg.ClientKeyFile)
		if certErr != nil || keyErr != nil || string(activeCert) != in.ClientCertificatePEM || string(activeKey) != in.ClientPrivateKeyPEM {
			t.Fatal("rejected replacement changed active certificate or key files")
		}
	}
}

func TestValidateIdentityReportsInvalidPrivateKey(t *testing.T) {
	in := testInput(t)
	for _, test := range []struct{ name, key, message string }{
		{"missing", "", "กรุณาระบุ private key"},
		{"whitespace", " \r\n\t ", "กรุณาระบุ private key"},
		{"malformed", "invalid PEM", "private key ต้องเป็น PEM"},
		{"invalid-der", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid DER")})), "private key ต้องเป็น PEM"},
		{"certificate-in-key-field", in.ClientCertificatePEM, "private key ต้องเป็น PEM"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateIdentity([]byte(in.ClientCertificatePEM), []byte(test.key))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateIdentityPreservesSupportedPrivateKeyFormats(t *testing.T) {
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, ed25519Key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, kind string
		key        crypto.Signer
	}{
		{"ECDSA-PKCS8", "PRIVATE KEY", ecdsaKey},
		{"ECDSA-SEC1", "EC PRIVATE KEY", ecdsaKey},
		{"RSA-PKCS8", "PRIVATE KEY", rsaKey},
		{"RSA-PKCS1", "RSA PRIVATE KEY", rsaKey},
		{"Ed25519-PKCS8", "PRIVATE KEY", ed25519Key},
	} {
		t.Run(test.name, func(t *testing.T) {
			var der []byte
			var err error
			switch test.kind {
			case "EC PRIVATE KEY":
				der, err = x509.MarshalECPrivateKey(ecdsaKey)
			case "RSA PRIVATE KEY":
				der = x509.MarshalPKCS1PrivateKey(rsaKey)
			default:
				der, err = x509.MarshalPKCS8PrivateKey(test.key)
			}
			if err != nil {
				t.Fatal(err)
			}
			cert := signPublicKeyForIdentityTest(t, testCA(t), test.key.Public())
			if _, err := ValidateIdentity([]byte(cert), pem.EncodeToMemory(&pem.Block{Type: test.kind, Bytes: der})); err != nil {
				t.Fatal(err)
			}
		})
	}
}

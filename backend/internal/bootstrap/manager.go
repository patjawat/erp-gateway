package bootstrap

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"erp-platform/gateway/internal/webtransport"

	"golang.org/x/sys/unix"
)

const setupCookie = "erp_gateway_setup"

type Runtime struct {
	Handler http.Handler
	Start   func()
	Stop    func()
	Close   func()
}

type Options struct {
	Directory         string
	DatabasePath      string
	PublicSetupURL    string
	TrustedProxyCIDRs []string
	WebPolicy         webtransport.Policy
	SetupEnabled      *bool
	LocalSetupCIDRs   []string
	Build             func(RuntimeConfig, []byte) (Runtime, error)
	ValidatePolicy    func([]string, []string) error
	Assets            http.Handler
	Legacy            *Runtime
}

type running struct {
	Runtime
	requests sync.WaitGroup
}

type setupSession struct {
	csrf    string
	expires time.Time
	origin  string
}

type attempt struct {
	count int
	until time.Time
}

type Manager struct {
	options   Options
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	retired   sync.WaitGroup
	current   *running
	config    RuntimeConfig
	key       []byte
	state     string
	reason    string
	sessions  map[string]setupSession
	attempts  map[string]attempt
}

func New(options Options) (*Manager, error) {
	if options.Directory == "" || options.DatabasePath == "" || options.Build == nil {
		return nil, errors.New("bootstrap directory, database and runtime builder are required")
	}
	if err := os.MkdirAll(options.Directory, 0700); err != nil {
		return nil, err
	}
	if options.PublicSetupURL != "" {
		if _, err := options.WebPolicy.ValidateURL(options.PublicSetupURL); err != nil {
			return nil, fmt.Errorf("invalid GATEWAY_SETUP_PUBLIC_URL: %w", err)
		}
	}
	for _, value := range append(append([]string{}, options.TrustedProxyCIDRs...), options.LocalSetupCIDRs...) {
		ip, network, err := net.ParseCIDR(value)
		ones, _ := networkSize(network)
		if err != nil || ip.IsUnspecified() || ones == 0 {
			return nil, errors.New("invalid initial trusted proxy CIDR")
		}
	}
	m := &Manager{options: options, state: "setup", sessions: map[string]setupSession{}, attempts: map[string]attempt{}}
	if exists(filepath.Join(options.Directory, "runtime.json")) {
		cfg, key, err := LoadRuntime(options.Directory)
		if err == nil {
			err = m.validateStored(cfg, key)
		}
		if err == nil {
			var runtime Runtime
			runtime, err = options.Build(cfg, key)
			if err == nil && options.SetupEnabled != nil && cfg.WebAccess == nil {
				cfg, err = m.snapshotPolicy(cfg, key)
				if err != nil && runtime.Close != nil {
					runtime.Close()
				}
			}
			if err == nil {
				m.config, m.key = cfg, key
				m.install(runtime)
				return m, nil
			}
		}
		m.state, m.reason = "recovery", "อ่าน config, key หรือใบรับรองเดิมไม่ผ่าน กรุณากู้คืนหรือนำเข้าค่าเดิม โดยข้อมูลเดิมยังไม่ถูกแก้ไข"
		return m, nil
	}
	if options.Legacy != nil {
		m.install(*options.Legacy)
		return m, nil
	}
	if existingDatabase(options.DatabasePath) || exists(filepath.Join(options.Directory, "encryption.key")) || exists(filepath.Join(options.Directory, "identities")) {
		m.state, m.reason = "recovery", "พบข้อมูลเดิมหรือการตั้งค่าบางส่วน ต้องใช้ encryption key เดิมก่อนเปิดใช้งาน ระบบจะไม่สร้าง key ทับ"
	}
	return m, nil
}

func (m *Manager) validateStored(c RuntimeConfig, key []byte) error {
	policy, err := c.ManagementPolicy(m.options.WebPolicy)
	if err != nil {
		return err
	}
	if _, err := policy.ValidateURL(c.PublicURL); err != nil {
		return err
	}
	cert, err := readBounded(c.ClientCertFile, 256<<10)
	if err != nil {
		return err
	}
	private, err := readBounded(c.ClientKeyFile, 32<<10)
	if err != nil {
		return err
	}
	if _, err = ValidateIdentity(cert, private); err != nil {
		return err
	}
	if c.HospitalCAFile != "" {
		body, err := readBounded(c.HospitalCAFile, 256<<10)
		if err != nil {
			return err
		}
		if err = validateHospitalCA(body); err != nil {
			return err
		}
	}
	if m.options.ValidatePolicy != nil {
		if err := m.options.ValidatePolicy(c.ApprovedUpstreams, c.PrivateUpstreamCIDRs); err != nil {
			return err
		}
	}
	return CheckDatabaseKey(m.options.DatabasePath, key)
}

// snapshotPolicy adds only the resolved policy to a healthy older installation.
// It refuses concurrent config/key edits and never regenerates identity or data.
func (m *Manager) snapshotPolicy(cfg RuntimeConfig, key []byte) (RuntimeConfig, error) {
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		return cfg, err
	}
	defer unlock()
	current, currentKey, err := LoadRuntime(m.options.Directory)
	if err != nil {
		return cfg, err
	}
	want, _ := json.Marshal(cfg)
	got, _ := json.Marshal(current)
	if !bytes.Equal(want, got) || subtle.ConstantTimeCompare(key, currentKey) != 1 {
		return cfg, errors.New("configuration changed while resolving management policy")
	}
	policy, err := cfg.ManagementPolicy(m.options.WebPolicy)
	if err != nil {
		return cfg, err
	}
	settings := policy.Settings()
	cfg.WebAccess = &settings
	if err = persistJSON(filepath.Join(m.options.Directory, "runtime.json"), cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (m *Manager) setupEnabled() bool {
	return m.options.SetupEnabled == nil || *m.options.SetupEnabled
}

func (m *Manager) managementPolicy() webtransport.Policy {
	policy, err := m.config.ManagementPolicy(m.options.WebPolicy)
	if err != nil {
		return webtransport.Policy{}
	}
	return policy
}

func (m *Manager) install(runtime Runtime) {
	old := m.current
	if old != nil && old.Stop != nil {
		old.Stop()
	}
	m.current, m.state, m.reason = &running{Runtime: runtime}, "active", ""
	m.sessions = map[string]setupSession{}
	if runtime.Start != nil {
		runtime.Start()
	}
	if old != nil {
		// install is serialized with Close by m.mu (except initial construction).
		m.retired.Add(1)
		go func() {
			defer m.retired.Done()
			old.requests.Wait()
			if old.Close != nil {
				old.Close()
			}
		}()
	}
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		current := m.current
		m.current = nil
		m.mu.Unlock()
		if current != nil {
			if current.Stop != nil {
				current.Stop()
			}
			current.requests.Wait()
			if current.Close != nil {
				current.Close()
			}
		}
		m.retired.Wait()
	})
}

// Status is safe to expose through authenticated Admin; it never contains secrets.
func (m *Manager) Status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	policy := m.managementPolicy()
	return map[string]any{"state": m.state, "public_url": m.gatewayURL(), "lan_http_enabled": policy.LANHTTPEnabled(), "local_http_enabled": policy.LocalHTTPEnabled(), "setup_enabled": m.setupEnabled() && m.current == nil && !m.closed, "persistent": m.config.Version == 1, "issuer_configured": false}
}

// gatewayURL is read with m.mu held; the initial setup URL is useful before a
// runtime has been activated and never contains credentials.
func (m *Manager) gatewayURL() string {
	if m.config.PublicURL != "" {
		return m.config.PublicURL
	}
	return m.options.PublicSetupURL
}

func (m *Manager) Handler() http.Handler { return http.HandlerFunc(m.serveHTTP) }

func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		jsonError(w, 503, "GATEWAY_UNAVAILABLE", "Gateway ปิดการทำงานแล้ว")
		return
	}
	current := m.current
	if current != nil && !strings.HasPrefix(r.URL.Path, "/setup") {
		current.requests.Add(1)
		m.mu.Unlock()
		defer current.requests.Done()
		current.Handler.ServeHTTP(w, r)
		return
	}
	active := current != nil
	state := m.state
	gatewayURL := m.gatewayURL()
	policy := m.managementPolicy()
	m.mu.Unlock()
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		jsonData(w, map[string]any{"status": "ok", "state": state})
		return
	}
	if active {
		if policy.IsLANHTTPURL(gatewayURL) && !policy.AllowsLANRequest(r, gatewayURL) {
			jsonError(w, 403, "LOCAL_SETUP_REQUIRED", "เปิดหน้าตั้งค่าจาก IP และเครือข่ายภายในที่ผู้ดูแลกำหนดไว้")
			return
		}
		if r.URL.Path == "/setup/api/status" && r.Method == http.MethodGet {
			data := map[string]any{"state": "active", "authenticated": false, "existing_data": true, "lan_http_enabled": policy.LANHTTPEnabled(), "local_http_enabled": policy.LocalHTTPEnabled(), "setup_enabled": false}
			if m.options.SetupEnabled == nil {
				data["public_url"] = gatewayURL
			}
			jsonData(w, data)
		} else if strings.HasPrefix(r.URL.Path, "/setup/api/") {
			jsonError(w, 410, "SETUP_CLOSED", "ตั้งค่าแล้ว กรุณาเข้าสู่ระบบผู้ดูแล")
		} else {
			http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		}
		return
	}
	if !m.setupEnabled() {
		jsonError(w, 403, "SETUP_DISABLED", "ปิดการตั้งค่าครั้งแรกไว้ ตั้ง SETUP=true แล้วสร้าง container ใหม่เมื่อต้องการตั้งค่าหรือกู้คืน")
		return
	}
	if !m.allowedSetupRequest(r) {
		jsonError(w, 403, "LOCAL_SETUP_REQUIRED", "เปิดหน้าตั้งค่าผ่าน localhost/SSH tunnel หรือ HTTPS proxy ที่ผู้ดูแลกำหนดไว้")
		return
	}
	switch {
	case r.URL.Path == "/setup/api/status" && r.Method == http.MethodGet:
		m.mu.Lock()
		defer m.mu.Unlock()
		session, ok := m.session(r)
		data := map[string]any{"state": m.state, "reason": m.reason, "authenticated": ok, "existing_data": existingDatabase(m.options.DatabasePath), "code_command": "docker compose exec gateway /usr/local/bin/gateway setup-code", "lan_http_enabled": policy.LANHTTPEnabled(), "local_http_enabled": policy.LocalHTTPEnabled(), "setup_enabled": true}
		if m.options.SetupEnabled == nil {
			data["public_url"] = m.gatewayURL()
		}
		if ok {
			data["csrf_token"] = session.csrf
			if m.options.SetupEnabled != nil {
				data["public_url"] = session.origin
			}
		}
		jsonData(w, data)
	case r.URL.Path == "/setup/api/session" && r.Method == http.MethodPost:
		m.login(w, r)
	case r.URL.Path == "/setup/api/csr" && r.Method == http.MethodPost:
		m.createCSR(w, r)
	case (r.URL.Path == "/setup/api/validate" || r.URL.Path == "/setup/api/activate") && r.Method == http.MethodPost:
		m.configure(w, r, strings.HasSuffix(r.URL.Path, "/activate"))
	case strings.HasPrefix(r.URL.Path, "/setup/api/"):
		jsonError(w, 404, "NOT_FOUND", "ไม่พบคำสั่งตั้งค่า")
	case strings.HasPrefix(r.URL.Path, "/admin/") && r.URL.Path != "/admin/" && !strings.HasPrefix(r.URL.Path, "/admin/api/"):
		if m.options.Assets != nil {
			http.StripPrefix("/admin/", m.options.Assets).ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
	case r.URL.Path == "/setup/" || r.URL.Path == "/setup":
		if m.options.Assets != nil {
			copy := r.Clone(r.Context())
			copy.URL.Path = "/"
			m.options.Assets.ServeHTTP(w, copy)
		} else {
			http.NotFound(w, r)
		}
	case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/admin/api/"):
		jsonError(w, 503, "SETUP_REQUIRED", "Gateway ยังไม่พร้อมใช้งาน กรุณาตั้งค่าหรือกู้คืนระบบก่อน")
	default:
		http.Redirect(w, r, "/setup/", http.StatusSeeOther)
	}
}

func (m *Manager) allowedSetupRequest(r *http.Request) bool {
	if m.options.SetupEnabled != nil && m.options.PublicSetupURL == "" {
		_, ok := m.options.WebPolicy.BootstrapOrigin(r)
		return ok
	}
	if m.options.WebPolicy.IsLANHTTPURL(m.options.PublicSetupURL) {
		return m.options.WebPolicy.AllowsLANRequest(r, m.options.PublicSetupURL)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return false
	}
	requestHost := r.Host
	if h, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = h
	}
	requestHost = strings.Trim(requestHost, "[]")
	localPeer := peer.IsLoopback()
	for _, value := range m.options.LocalSetupCIDRs {
		_, network, err := net.ParseCIDR(value)
		if err == nil && network.Contains(peer) {
			localPeer = true
		}
	}
	if localPeer && (requestHost == "localhost" || (net.ParseIP(requestHost) != nil && net.ParseIP(requestHost).IsLoopback())) && r.Header.Get("X-Forwarded-Proto") == "" {
		return true
	}
	if m.options.PublicSetupURL == "" {
		return false
	}
	u, err := m.options.WebPolicy.ValidateURL(m.options.PublicSetupURL)
	if err != nil || r.Host != u.Host {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	for _, value := range m.options.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(value)
		if err == nil && network.Contains(peer) {
			return true
		}
	}
	return false
}

func (m *Manager) sameOrigin(r *http.Request) bool {
	if m.options.SetupEnabled != nil && m.options.PublicSetupURL == "" {
		origin, ok := m.options.WebPolicy.BootstrapOrigin(r)
		return ok && r.Header.Get("Origin") == origin
	}
	if m.options.WebPolicy.IsLANHTTPURL(m.options.PublicSetupURL) {
		return m.options.WebPolicy.AllowsLANOrigin(r, m.options.PublicSetupURL)
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return u.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback()))
}

func hashToken(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (m *Manager) session(r *http.Request) (setupSession, bool) {
	cookie, err := r.Cookie(setupCookie)
	if err != nil {
		return setupSession{}, false
	}
	s, ok := m.sessions[hashToken(cookie.Value)]
	if !ok || !time.Now().Before(s.expires) {
		return setupSession{}, false
	}
	if s.origin != "" {
		origin, valid := m.setupOrigin(r)
		if !valid || origin != s.origin {
			return setupSession{}, false
		}
	}
	return s, true
}

func (m *Manager) setupOrigin(r *http.Request) (string, bool) {
	if m.options.PublicSetupURL == "" {
		return m.options.WebPolicy.BootstrapOrigin(r)
	}
	if !m.allowedSetupRequest(r) {
		return "", false
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	u, err := m.options.WebPolicy.ValidateURL(scheme + "://" + r.Host)
	if err != nil {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

type codeRecord struct {
	Hash      string `json:"hash"`
	ExpiresAt int64  `json:"expires_at"`
}

func IssueSetupCode(directory string) (string, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	unlock, err := lockDirectory(directory)
	if err != nil {
		return "", err
	}
	defer unlock()
	code, err := token()
	if err != nil {
		return "", err
	}
	if err = persistJSON(filepath.Join(directory, "setup-code.json"), codeRecord{Hash: hashToken(code), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}); err != nil {
		return "", err
	}
	return code, nil
}

func (m *Manager) login(w http.ResponseWriter, r *http.Request) {
	if !m.sameOrigin(r) {
		jsonError(w, 403, "CSRF_REJECTED", "เปิดหน้าตั้งค่าจากที่อยู่เดียวกันแล้วลองใหม่")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in, 2048) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		jsonError(w, 503, "GATEWAY_UNAVAILABLE", "Gateway ปิดการทำงานแล้ว")
		return
	}
	if m.current != nil {
		jsonError(w, 410, "SETUP_CLOSED", "ตั้งค่าแล้ว กรุณาเข้าสู่ระบบผู้ดูแล")
		return
	}
	now := time.Now()
	for k, v := range m.attempts {
		if !now.Before(v.until) {
			delete(m.attempts, k)
		}
	}
	// A global ceiling also bounds memory and brute force across spoofed addresses.
	a := m.attempts["global"]
	if a.count >= 10 && now.Before(a.until) {
		w.Header().Set("Retry-After", "600")
		jsonError(w, 429, "RATE_LIMITED", "ลองรหัสบ่อยเกินไป รอ 10 นาทีแล้วลองใหม่")
		return
	}
	if !now.Before(a.until) {
		a = attempt{until: now.Add(10 * time.Minute)}
	}
	a.count++
	m.attempts["global"] = a
	body, err := readBounded(filepath.Join(m.options.Directory, "setup-code.json"), 2048)
	var code codeRecord
	if err != nil || json.Unmarshal(body, &code) != nil || code.ExpiresAt <= now.Unix() || len(in.Code) != 43 || subtle.ConstantTimeCompare([]byte(code.Hash), []byte(hashToken(in.Code))) != 1 {
		jsonError(w, 401, "INVALID_SETUP_CODE", "รหัสตั้งค่าไม่ถูกต้อง หมดอายุ หรือถูกใช้แล้ว ขอรหัสใหม่จากคำสั่งบนเครื่อง Gateway")
		return
	}
	origin := ""
	if m.options.SetupEnabled != nil {
		var valid bool
		origin, valid = m.setupOrigin(r)
		if !valid {
			jsonError(w, 403, "CSRF_REJECTED", "เปิดหน้าตั้งค่าจากที่อยู่เดียวกันแล้วลองใหม่")
			return
		}
	}
	sessionToken, err := token()
	if err != nil {
		jsonError(w, 503, "SETUP_UNAVAILABLE", "สร้าง session ไม่สำเร็จ")
		return
	}
	csrf, err := token()
	if err != nil {
		jsonError(w, 503, "SETUP_UNAVAILABLE", "สร้าง session ไม่สำเร็จ")
		return
	}
	if err = os.Remove(filepath.Join(m.options.Directory, "setup-code.json")); err != nil {
		jsonError(w, 503, "SETUP_UNAVAILABLE", "ใช้รหัสตั้งค่าไม่สำเร็จ")
		return
	}
	for k, v := range m.sessions {
		if !now.Before(v.expires) {
			delete(m.sessions, k)
		}
	}
	m.sessions[hashToken(sessionToken)] = setupSession{csrf: csrf, expires: now.Add(20 * time.Minute), origin: origin}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: sessionToken, Path: "/setup/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 1200})
	data := map[string]any{"authenticated": true, "csrf_token": csrf, "lan_http_enabled": m.options.WebPolicy.LANHTTPEnabled(), "local_http_enabled": m.options.WebPolicy.LocalHTTPEnabled(), "setup_enabled": true}
	if m.options.SetupEnabled != nil {
		data["public_url"] = origin
	} else {
		data["public_url"] = m.gatewayURL()
		if u, err := m.options.WebPolicy.ValidateURL(m.gatewayURL()); err == nil {
			data["public_url"] = u.Scheme + "://" + u.Host
		}
	}
	jsonData(w, data)
}

func (m *Manager) configure(w http.ResponseWriter, r *http.Request, activate bool) {
	if !m.sameOrigin(r) {
		jsonError(w, 403, "CSRF_REJECTED", "เปิดหน้าตั้งค่าจากที่อยู่เดียวกันแล้วลองใหม่")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		jsonError(w, 503, "GATEWAY_UNAVAILABLE", "Gateway ปิดการทำงานแล้ว")
		return
	}
	if m.current != nil {
		jsonError(w, 410, "SETUP_CLOSED", "ตั้งค่าแล้ว กรุณาเข้าสู่ระบบผู้ดูแล")
		return
	}
	session, ok := m.session(r)
	if !ok {
		jsonError(w, 401, "SETUP_LOGIN_REQUIRED", "กรุณายืนยันรหัสตั้งค่าอีกครั้ง")
		return
	}
	if subtle.ConstantTimeCompare([]byte(session.csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
		jsonError(w, 403, "CSRF_REJECTED", "โหลดหน้าตั้งค่าใหม่แล้วลองอีกครั้ง")
		return
	}
	var in Input
	if !readJSON(w, r, &in, 1<<20) {
		return
	}
	if in.ClientPrivateKeyPEM == "" {
		body, err := readBounded(filepath.Join(m.options.Directory, "staged-client-key.pem"), 32<<10)
		if err == nil {
			in.ClientPrivateKeyPEM = string(body)
		}
	}
	if err := validateInputWithPolicy(in, m.options.WebPolicy); err != nil {
		jsonError(w, 400, "INVALID_SETUP", err.Error())
		return
	}
	if m.options.ValidatePolicy != nil {
		if err := m.options.ValidatePolicy(in.ApprovedUpstreams, in.PrivateUpstreamCIDRs); err != nil {
			jsonError(w, 400, "INVALID_NETWORK_POLICY", "ปลายทางที่อนุญาตหรือ private CIDR ไม่ถูกต้อง")
			return
		}
	}
	key, err := m.inputKey(in, false)
	if err != nil {
		jsonError(w, 409, "RECOVERY_REQUIRED", err.Error())
		return
	}
	if len(key) != 0 {
		if err = CheckDatabaseKey(m.options.DatabasePath, key); err != nil {
			jsonError(w, 409, "KEY_MISMATCH", err.Error())
			return
		}
	}
	if !activate {
		transport := "ใช้ HTTPS เดิมของ Gateway"
		if m.options.WebPolicy.IsLANHTTPURL(in.PublicURL) {
			transport = "ใช้ HTTP เฉพาะเครือข่ายภายในที่อนุญาต ข้อมูลรับส่งไม่เข้ารหัส"
		}
		jsonData(w, map[string]any{"valid": true, "existing_data": existingDatabase(m.options.DatabasePath), "checks": []map[string]string{{"name": "admin", "status": "ready", "message": "พร้อมบันทึก password verifier"}, {"name": "https", "status": "ready", "message": transport}, {"name": "mtls", "status": "ready", "message": "ตรวจ key, chain, clientAuth และวันหมดอายุผ่าน"}, {"name": "hospital_connectivity", "status": "not_tested", "message": "ทดสอบกับหน่วยงานหลังเปิดใช้งาน ยังไม่ได้ตรวจ end-to-end"}}})
		return
	}
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		jsonError(w, 409, "SETUP_BUSY", "มีการตั้งค่าหรือออก setup code อยู่ กรุณาลองใหม่")
		return
	}
	defer unlock()
	key, err = m.inputKey(in, true)
	if err != nil {
		jsonError(w, 409, "RECOVERY_REQUIRED", err.Error())
		return
	}
	cfg, err := passwordConfig(in.AdminUsername, in.AdminPassword)
	if err != nil {
		jsonError(w, 503, "SETUP_UNAVAILABLE", "เตรียมบัญชีผู้ดูแลไม่สำเร็จ")
		return
	}
	cfg.PublicURL, cfg.TrustedProxyCIDRs, cfg.ApprovedUpstreams, cfg.PrivateUpstreamCIDRs = in.PublicURL, in.TrustedProxyCIDRs, in.ApprovedUpstreams, in.PrivateUpstreamCIDRs
	if m.options.SetupEnabled != nil {
		settings := m.options.WebPolicy.Settings()
		cfg.WebAccess = &settings
	}
	cfg, err = m.identityFiles(cfg, []byte(in.ClientCertificatePEM), []byte(in.ClientPrivateKeyPEM), []byte(in.HospitalCAPEM))
	if err != nil {
		jsonError(w, 503, "SETUP_UNAVAILABLE", "บันทึกใบรับรองไม่สำเร็จ")
		return
	}
	runtime, err := m.options.Build(cfg, key)
	if err != nil {
		jsonError(w, 409, "ACTIVATION_FAILED", "เปิด runtime ไม่สำเร็จ ข้อมูลเดิมยังคงอยู่ กรุณาตรวจ config และ encryption key")
		return
	}
	if err = persistJSON(filepath.Join(m.options.Directory, "runtime.json"), cfg); err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		jsonError(w, 503, "SETUP_UNAVAILABLE", "บันทึก config ไม่สำเร็จ กรุณาตรวจพื้นที่และสิทธิ์ไฟล์")
		return
	}
	m.config, m.key = cfg, key
	m.install(runtime)
	_ = os.Remove(filepath.Join(m.options.Directory, "setup-code.json"))
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Path: "/setup/", Value: "", HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	jsonData(w, map[string]any{"active": true, "redirect": "/admin/"})
}

func (m *Manager) inputKey(in Input, create bool) ([]byte, error) {
	key, err := readEncryptionKey(m.options.Directory)
	if err == nil {
		if in.EncryptionKey != "" {
			provided, e := base64.StdEncoding.DecodeString(in.EncryptionKey)
			if e != nil || subtle.ConstantTimeCompare(provided, key) != 1 {
				return nil, errors.New("encryption key ที่นำเข้าไม่ตรงกับ key ใน volume กรุณาตรวจ backup")
			}
		}
		return key, nil
	}
	if exists(filepath.Join(m.options.Directory, "encryption.key")) {
		return nil, errors.New("key ใน volume ไม่ถูกต้อง ต้องกู้คืนไฟล์เดิมก่อนดำเนินการ")
	}
	if in.EncryptionKey != "" {
		key, err = base64.StdEncoding.DecodeString(in.EncryptionKey)
		if err != nil || len(key) != 32 {
			return nil, errors.New("encryption key ต้องเป็น base64 ของข้อมูล 32 bytes")
		}
		if !existingDatabase(m.options.DatabasePath) {
			return nil, errors.New("นำเข้า encryption key ได้เมื่อมีฐานข้อมูลเดิมให้ตรวจสอบเท่านั้น")
		}
		if err = CheckDatabaseKey(m.options.DatabasePath, key); err != nil {
			return nil, err
		}
	} else if existingDatabase(m.options.DatabasePath) {
		return nil, errors.New("พบฐานข้อมูลเดิม ต้องนำเข้า encryption key เดิม ห้ามสร้างใหม่")
	} else if !create {
		return nil, nil
	} else {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
	}
	if create {
		if err = atomicFile(filepath.Join(m.options.Directory, "encryption.key"), []byte(base64.StdEncoding.EncodeToString(key)+"\n")); err != nil {
			return nil, err
		}
	}
	return key, nil
}

func (m *Manager) identityFiles(cfg RuntimeConfig, cert, key, roots []byte) (RuntimeConfig, error) {
	id, err := token()
	if err != nil {
		return cfg, err
	}
	dir := filepath.Join(m.options.Directory, "identities", id)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return cfg, err
	}
	cfg.ClientCertFile, cfg.ClientKeyFile = filepath.Join(dir, "client-chain.pem"), filepath.Join(dir, "client-key.pem")
	if err = atomicFile(cfg.ClientCertFile, cert); err != nil {
		return cfg, err
	}
	if err = atomicFile(cfg.ClientKeyFile, key); err != nil {
		return cfg, err
	}
	if len(roots) > 0 {
		cfg.HospitalCAFile = filepath.Join(dir, "hospital-ca.pem")
		err = atomicFile(cfg.HospitalCAFile, roots)
	} else {
		cfg.HospitalCAFile = ""
	}
	return cfg, err
}

// ReplaceIdentity keeps the previous config and live handler when staging fails.
// There is no root CA key in this process and no arbitrary signing endpoint.
func (m *Manager) ReplaceIdentity(ctx context.Context, cert, key, roots []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := ValidateIdentity(cert, key); err != nil {
		return err
	}
	if err := validateHospitalCA(roots); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.config.Version != 1 {
		return errors.New("identity reload requires persistent Gateway setup; import legacy settings first")
	}
	if roots == nil && m.config.HospitalCAFile != "" {
		var err error
		roots, err = readBounded(m.config.HospitalCAFile, 256<<10)
		if err != nil {
			return err
		}
	}
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := m.identityFiles(m.config, cert, key, roots)
	if err != nil {
		return err
	}
	runtime, err := m.options.Build(cfg, m.key)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err == nil {
		err = persistJSON(filepath.Join(m.options.Directory, "runtime.json"), cfg)
	}
	if err != nil {
		if runtime.Close != nil {
			runtime.Close()
		}
		return err
	}
	m.config = cfg
	m.install(runtime)
	return nil
}

func (m *Manager) createCSR(w http.ResponseWriter, r *http.Request) {
	if !m.sameOrigin(r) {
		jsonError(w, 403, "CSRF_REJECTED", "เปิดหน้าตั้งค่าจากที่อยู่เดียวกันแล้วลองใหม่")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		jsonError(w, 503, "GATEWAY_UNAVAILABLE", "Gateway ปิดการทำงานแล้ว")
		return
	}
	if m.current != nil {
		jsonError(w, 410, "SETUP_CLOSED", "ตั้งค่าแล้ว กรุณาเข้าสู่ระบบผู้ดูแล")
		return
	}
	s, ok := m.session(r)
	if !ok {
		jsonError(w, 401, "SETUP_LOGIN_REQUIRED", "กรุณายืนยันรหัสตั้งค่าอีกครั้ง")
		return
	}
	if subtle.ConstantTimeCompare([]byte(s.csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
		jsonError(w, 403, "CSRF_REJECTED", "โหลดหน้าใหม่แล้วลองอีกครั้ง")
		return
	}
	var in struct{}
	if !readJSON(w, r, &in, 2048) {
		return
	}
	unlock, err := lockDirectory(m.options.Directory)
	if err != nil {
		jsonError(w, 409, "SETUP_BUSY", "กำลังเตรียมการตั้งค่า กรุณาลองใหม่")
		return
	}
	defer unlock()
	name := filepath.Join(m.options.Directory, "staged-client-key.pem")
	var private *ecdsa.PrivateKey
	if exists(name) {
		body, e := readBounded(name, 32<<10)
		if e != nil {
			jsonError(w, 409, "KEY_RECOVERY_REQUIRED", "อ่าน private key ที่สร้างไว้ไม่ได้ กรุณากู้คืนไฟล์เดิม")
			return
		}
		block, _ := pem.Decode(body)
		if block == nil {
			jsonError(w, 409, "KEY_RECOVERY_REQUIRED", "private key เดิมไม่ถูกต้อง")
			return
		}
		parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		if e != nil {
			jsonError(w, 409, "KEY_RECOVERY_REQUIRED", "private key เดิมไม่ถูกต้อง")
			return
		}
		private, ok = parsed.(*ecdsa.PrivateKey)
		if !ok {
			jsonError(w, 409, "KEY_RECOVERY_REQUIRED", "private key เดิมไม่ใช่ P256")
			return
		}
	} else {
		private, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err == nil {
			var der []byte
			der, err = x509.MarshalPKCS8PrivateKey(private)
			if err == nil {
				err = atomicFile(name, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
			}
		}
		if err != nil {
			jsonError(w, 503, "KEY_CREATE_FAILED", "สร้างและเก็บ private key ไม่สำเร็จ")
			return
		}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ERP Mobile Gateway", Organization: []string{"ERP Mobile"}}}, private)
	if err != nil {
		jsonError(w, 503, "CSR_CREATE_FAILED", "สร้าง CSR ไม่สำเร็จ")
		return
	}
	spki, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		jsonError(w, 503, "CSR_CREATE_FAILED", "อ่าน public key ไม่สำเร็จ")
		return
	}
	fingerprint := sha256.Sum256(spki)
	jsonData(w, map[string]string{"csr_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), "fingerprint": hex.EncodeToString(fingerprint[:])})
}

func lockDirectory(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".bootstrap.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("Gateway configuration is busy")
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}

func readJSON(w http.ResponseWriter, r *http.Request, out any, max int64) bool {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		jsonError(w, 415, "JSON_REQUIRED", "ส่งข้อมูลเป็น application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		jsonError(w, 400, "INVALID_REQUEST", "รูปแบบข้อมูลไม่ถูกต้องหรือใหญ่เกินขอบเขต")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		jsonError(w, 400, "INVALID_REQUEST", "รูปแบบข้อมูลไม่ถูกต้อง")
		return false
	}
	return true
}
func jsonData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func jsonError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

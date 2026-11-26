package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"erp-platform/gateway/internal/admin"
	"erp-platform/gateway/internal/contracts"
	"erp-platform/gateway/internal/webtransport"
)

type Config struct {
	Store                 *Store
	AdminUsername         string
	AdminPassword         string
	AdminPasswordVerifier func(string) bool
	IdentityDirectory     string
	ActivateIdentity      func(context.Context, []byte, []byte, []byte) error
	ReadSettings          func() (RuntimeSettings, error)
	ApplySettings         func(context.Context, RuntimeSettings) error
	ExportRecovery        func(context.Context, string) ([]byte, error)
	BackupHandler         http.Handler
	Management            *ManagementState
	PublicURL             string
	WebPolicy             webtransport.Policy
	CookieSecure          bool
	AllowHTTPUpstream     bool
	TrustedProxyCIDRs     []string
	ApprovedUpstreams     []string
	PrivateUpstreamCIDRs  []string
	GatewayClientCert     *tls.Certificate
	HospitalServerRoots   *x509.CertPool
	Client                *http.Client
	NotificationSender    PushSender
}

type adminSession struct {
	csrf    string
	expires time.Time
}
type rateWindow struct {
	count int
	end   time.Time
}
type Server struct {
	config             Config
	client             *http.Client
	admins             map[string]adminSession
	rates              map[string]rateWindow
	mu                 sync.Mutex
	proxies            []*net.IPNet
	egress             *upstreamPolicy
	usageRefresh       chan struct{}
	management         *ManagementState
	managementRevision string
}

func New(config Config) (*Server, error) {
	if config.Store == nil {
		return nil, errors.New("gateway store is required")
	}
	if config.AdminUsername == "" || (config.AdminPasswordVerifier == nil && config.AdminPassword == "") {
		return nil, errors.New("admin username and a non-empty password are required")
	}
	if config.PublicURL != "" {
		if _, err := config.WebPolicy.ValidateURL(config.PublicURL); err != nil {
			return nil, err
		}
		config.CookieSecure = !config.WebPolicy.IsLANHTTPURL(config.PublicURL)
	}
	if config.GatewayClientCert != nil && config.AllowHTTPUpstream {
		return nil, errors.New("Gateway client identity requires HTTPS upstreams")
	}
	policy, err := newUpstreamPolicy(config.ApprovedUpstreams, config.PrivateUpstreamCIDRs, config.AllowHTTPUpstream)
	if err != nil {
		return nil, err
	}
	transport := policy.transport().(upstreamTransport)
	if config.GatewayClientCert != nil {
		transport.secure.TLSClientConfig.Certificates = []tls.Certificate{*config.GatewayClientCert}
		transport.secure.TLSClientConfig.RootCAs = config.HospitalServerRoots
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: transport}
	if config.Client != nil {
		*client = *config.Client
	}
	if client.Timeout <= 0 {
		client.Timeout = 20 * time.Second
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	s := &Server{config: config, client: client, admins: map[string]adminSession{}, rates: map[string]rateWindow{}, egress: policy, usageRefresh: make(chan struct{}, 1)}
	s.management = config.Management
	if s.management == nil {
		s.management = &ManagementState{}
	}
	s.managementRevision = managementFingerprint(policy, config.TrustedProxyCIDRs)
	for _, value := range config.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		s.proxies = append(s.proxies, network)
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.config.Store.db.PingContext(r.Context()); err != nil {
			fail(w, 503, "GATEWAY_UNAVAILABLE", "ระบบส่วนกลางไม่พร้อมใช้งาน")
			return
		}
		writeData(w, map[string]string{"status": "ok", "api_version": contracts.Version})
	})
	mux.HandleFunc("GET /api/hospitals", s.listHospitals)
	mux.HandleFunc("GET /api/hospitals/{code}/status", s.hospitalStatus)
	mux.HandleFunc("GET /api/hospitals/{code}/login-methods", s.loginMethods)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/external/start", s.externalStart)
	mux.HandleFunc("POST /api/auth/external/status", s.externalStatus)
	mux.HandleFunc("POST /api/auth/external/cancel", s.externalCancel)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("/api/notifications", s.notificationHTTP)
	mux.HandleFunc("GET /admin/api/notifications/summary", s.adminNotificationSummary)
	mux.HandleFunc("/api/notifications/", s.notificationHTTP)
	mux.HandleFunc("POST /api/notification-devices/register", s.notificationHTTP)
	mux.HandleFunc("POST /api/notification-devices/remove", s.notificationHTTP)
	mux.HandleFunc("GET /api/profile", s.profile)
	mux.HandleFunc("POST /api/usage/activity", s.usageActivity)
	mux.HandleFunc("GET /api/profile/avatar", s.avatar)
	mux.HandleFunc("/api/personnel", s.personnelAvatar)
	mux.HandleFunc("/api/personnel/", s.personnelAvatar)
	mux.HandleFunc("POST /api/profile/update", s.updateProfile)
	mux.HandleFunc("GET /api/profile/signature", s.profileSignature)
	mux.HandleFunc("GET /api/profile/personal", s.profilePersonal)
	mux.HandleFunc("GET /api/profile/records", s.profileRecordHistory)
	mux.HandleFunc("GET /api/attendance", s.attendanceHistory)
	mux.HandleFunc("/api/attendance/team", s.attendanceTeam)
	mux.HandleFunc("/api/attendance/team/", s.attendanceTeam)
	mux.HandleFunc("/api/attendance/", s.attendanceLive)
	mux.HandleFunc("GET /api/welfare", s.welfareHistory)
	mux.HandleFunc("GET /api/housing", s.housingHistory)
	mux.HandleFunc("GET /api/salary", s.salaryHistory)
	mux.HandleFunc("/api/documents", s.documents)
	mux.HandleFunc("/api/documents/", s.documents)
	mux.HandleFunc("GET /api/leaves", s.leaveHistory)
	mux.HandleFunc("POST /api/leaves", s.leave)
	mux.HandleFunc("/api/leaves/", s.leave)
	mux.HandleFunc("GET /api/repairs", s.repairHistory)
	mux.HandleFunc("POST /api/repairs", s.createRepair)
	mux.HandleFunc("GET /api/repairs/options", s.repairOptions)
	mux.HandleFunc("GET /api/repairs/assets", s.repairAssets)
	mux.HandleFunc("/api/repairs/", s.repairDetail)
	// A single subtree dispatcher avoids conflicting literal/method/wildcard
	// patterns (options and availability also occupy the {id} path position).
	mux.HandleFunc("/api/meetings", s.meeting)
	mux.HandleFunc("/api/meetings/", s.meeting)
	mux.HandleFunc("/api/vehicles", s.vehicle)
	mux.HandleFunc("/api/vehicles/options", s.vehicle)
	mux.HandleFunc("/api/vehicles/", s.vehicle)
	mux.HandleFunc("GET /depdrop/employee-by-id", s.directory)
	for path, method := range map[string]string{
		"/api/attendance": "GET",
		"/api/welfare":    "GET", "/api/housing": "GET", "/api/salary": "GET",
		"/api/profile/personal": "GET", "/api/profile/records": "GET",
		"/api/hospitals": "GET", "/api/hospitals/{code}/status": "GET", "/api/auth/login": "POST", "/api/auth/logout": "POST", "/api/profile": "GET", "/api/profile/avatar": "GET", "/api/leaves": "GET, POST", "/depdrop/employee-by-id": "GET",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", method)
			fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		})
	}
	for path, methods := range map[string]string{"/api/repairs": "GET, POST", "/api/repairs/options": "GET", "/api/repairs/assets": "GET"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", methods)
			fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		})
	}
	mux.HandleFunc("POST /admin/api/login", s.adminLogin)
	mux.HandleFunc("GET /admin/api/session", s.adminSession)
	mux.HandleFunc("POST /admin/api/logout", s.adminLogout)
	mux.HandleFunc("GET /admin/api/hospitals", s.adminHospitals)
	mux.HandleFunc("POST /admin/api/hospitals", s.adminAddHospital)
	mux.HandleFunc("PUT /admin/api/hospitals/{id}", s.adminUpdateHospital)
	mux.HandleFunc("GET /admin/api/hospitals/{id}/attachments", s.adminHospitalAttachments)
	mux.HandleFunc("POST /admin/api/hospitals/{id}/attachments", s.adminUploadHospitalAttachment)
	mux.HandleFunc("GET /admin/api/hospitals/{id}/attachments/{attachmentID}", s.adminDownloadHospitalAttachment)
	mux.HandleFunc("DELETE /admin/api/hospitals/{id}/attachments/{attachmentID}", s.adminDeleteHospitalAttachment)
	mux.HandleFunc("POST /admin/api/hospitals/{id}/{action}", s.adminAction)
	mux.HandleFunc("GET /admin/api/audit", s.adminAudit)
	mux.HandleFunc("GET /admin/api/dashboard", s.adminDashboard)
	mux.HandleFunc("POST /admin/api/usage/refresh", s.adminUsageRefresh)
	mux.HandleFunc("GET /admin/api/settings", s.adminSettings)
	mux.HandleFunc("PUT /admin/api/settings", s.adminApplySettings)
	mux.HandleFunc("POST /admin/api/recovery/export", s.adminExportRecovery)
	backupHandler := func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.adminAuth(w, r, r.Method != http.MethodGet && r.Method != http.MethodHead); !ok {
			return
		}
		if s.config.BackupHandler == nil {
			fail(w, 503, "BACKUP_UNAVAILABLE", "ระบบสำรองข้อมูลยังไม่พร้อม")
			return
		}
		s.config.BackupHandler.ServeHTTP(w, r)
	}
	mux.HandleFunc("/admin/api/backups", backupHandler)
	mux.HandleFunc("/admin/api/backups/", backupHandler)
	mux.HandleFunc("GET /admin/api/certificates", s.adminCertificates)
	mux.HandleFunc("POST /admin/api/certificates/csr", s.adminCertificateCSR)
	mux.HandleFunc("POST /admin/api/certificates/import", s.adminCertificateImport)
	s.registerUpdateRoutes(mux)
	mux.HandleFunc("/admin/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, 404, "NOT_FOUND", "ไม่พบรายการที่ร้องขอ")
	})
	mux.Handle("/admin/", http.StripPrefix("/admin/", admin.Handler()))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin/", http.StatusSeeOther) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, 404, "NOT_FOUND", "ไม่พบรายการที่ร้องขอ")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if (r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/")) && s.config.WebPolicy.IsLANHTTPURL(s.config.PublicURL) && !s.config.WebPolicy.AllowsLANRequest(r, s.config.PublicURL) {
			fail(w, http.StatusForbidden, "LAN_HTTP_FORBIDDEN", "เข้า HTTP ผ่าน IP และเครือข่าย LAN ที่ผู้ดูแลอนุญาตโดยตรง")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeData(w http.ResponseWriter, value any) { writeJSON(w, 200, map[string]any{"data": value}) }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": contracts.Failure{Code: code, Message: message}})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	return decodeLimit(w, r, value, 16*1024)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 415, "JSON_REQUIRED", "กรุณาส่งข้อมูลเป็น application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		fail(w, 400, "INVALID_REQUEST", "รูปแบบข้อมูลไม่ถูกต้อง")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, "INVALID_REQUEST", "รูปแบบข้อมูลไม่ถูกต้อง")
		return false
	}
	return true
}

func (s *Server) trusted(ip net.IP) bool {
	for _, network := range s.proxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	if s.trusted(ip) && len(r.Header.Get("X-Forwarded-For")) <= 1024 {
		chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(chain) - 1; i >= 0; i-- {
			candidate := net.ParseIP(strings.TrimSpace(chain[i]))
			if candidate == nil {
				break
			}
			ip = candidate
			if !s.trusted(ip) {
				break
			}
		}
	}
	return ip.String()
}
func (s *Server) allow(key string, limit int, period time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.rates {
		if !v.end.After(now) {
			delete(s.rates, k)
		}
	}
	v, ok := s.rates[key]
	if !ok {
		if len(s.rates) >= 10000 {
			return false
		}
		v = rateWindow{end: now.Add(period)}
	}
	if v.count >= limit {
		return false
	}
	v.count++
	s.rates[key] = v
	return true
}
func (s *Server) limit(w http.ResponseWriter, r *http.Request, key string, limit int, period time.Duration) bool {
	if s.allow(key, limit, period) {
		return true
	}
	w.Header().Set("Retry-After", fmt.Sprint(int(period.Seconds())))
	fail(w, 429, "RATE_LIMITED", "ลองใช้งานบ่อยเกินไป กรุณารอสักครู่แล้วลองใหม่")
	return false
}

var codePattern = regexp.MustCompile(`^[0-9]{5}$`)
var bearerPattern = regexp.MustCompile(`^(?i:Bearer) ([A-Za-z0-9_-]{43})$`)

func active(h Hospital) bool { return h.Enabled && h.ApprovalStatus == "approved" }
func (s *Server) listHospitals(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "list:"+s.clientIP(r), 120, time.Minute) {
		return
	}
	list, err := s.config.Store.hospitals(r.Context())
	if err != nil {
		unavailable(w)
		return
	}
	public := make([]contracts.Hospital, 0)
	for _, h := range list {
		if active(h) {
			public = append(public, h.Hospital)
		}
	}
	writeData(w, map[string]any{"hospitals": public})
}
func (s *Server) hospitalStatus(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "status:"+s.clientIP(r), 180, time.Minute) {
		return
	}
	h, err := s.config.Store.hospital(r.Context(), "code", r.PathValue("code"))
	if err != nil || !active(h) {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			unavailable(w)
			return
		}
		disabled(w)
		return
	}
	writeData(w, struct {
		contracts.Hospital
		Enabled bool `json:"enabled"`
	}{h.Hospital, true})
}

func (s *Server) validURL(value string) (*url.URL, error) {
	return parseUpstreamURL(value, s.config.AllowHTTPUpstream)
}

func (s *Server) upstream(ctx context.Context, h Hospital, method, path, token string, body []byte) (*http.Response, error) {
	base, err := s.egress.approvedURL(h.Code, h.APIURL)
	if err != nil {
		return nil, err
	}
	key, err := s.config.Store.decrypt("hospital:"+h.ID, h.key)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	if index := strings.Index(base.Path, "?"); index >= 0 {
		base.RawQuery = base.Path[index+1:]
		base.Path = base.Path[:index]
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Gateway-Key", key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return s.client.Do(req)
}
func readEnvelope(resp *http.Response, out any) error {
	return readEnvelopeLimit(resp, out, 1024*1024)
}

func readEnvelopeLimit(resp *http.Response, out any, limit int64) error {
	defer resp.Body.Close()
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "application/json" {
		return errors.New("non JSON upstream")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return errors.New("oversized upstream JSON")
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || len(envelope.Data) == 0 {
		return errors.New("missing upstream data")
	}
	return json.Unmarshal(envelope.Data, out)
}
func unavailable(w http.ResponseWriter) {
	fail(w, 503, "GATEWAY_UNAVAILABLE", "ระบบส่วนกลางไม่พร้อมใช้งาน กรุณาลองใหม่")
}
func disabled(w http.ResponseWriter) {
	fail(w, 403, "HOSPITAL_DISABLED", "หน่วยงานนี้ถูกระงับการใช้งานบนมือถือ กรุณาติดต่อผู้ดูแลระบบ")
}
func expired(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	fail(w, 401, "SESSION_EXPIRED", "เซสชันหมดอายุ กรุณาเข้าสู่ระบบอีกครั้ง")
}
func upstreamUnavailable(w http.ResponseWriter) {
	fail(w, 503, "ERP_UNAVAILABLE", "เชื่อมต่อหน่วยงานไม่ได้ กรุณาลองใหม่")
}
func (s *Server) relayError(w http.ResponseWriter, resp *http.Response, login bool) {
	defer resp.Body.Close()
	var envelope struct {
		Error contracts.Failure `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	_ = json.Unmarshal(data, &envelope)
	if envelope.Error.Code == "GATEWAY_UNAUTHORIZED" {
		upstreamUnavailable(w)
		return
	}
	switch resp.StatusCode {
	case 403:
		if envelope.Error.Code == "LOGIN_METHOD_DISABLED" {
			fail(w, 403, "LOGIN_METHOD_DISABLED", "หน่วยงานปิดช่องทางเข้าสู่ระบบนี้แล้ว กรุณาเลือกช่องทางอื่น")
		} else {
			upstreamUnavailable(w)
		}
	case 401:
		if login {
			fail(w, 401, "INVALID_CREDENTIALS", "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		} else {
			expired(w)
		}
	case 429:
		retry := resp.Header.Get("Retry-After")
		if len(retry) > 0 && len(retry) < 10 && regexp.MustCompile(`^[0-9]+$`).MatchString(retry) {
			w.Header().Set("Retry-After", retry)
		}
		fail(w, 429, "RATE_LIMITED", "ลองเข้าสู่ระบบบ่อยเกินไป กรุณารอสักครู่แล้วลองใหม่")
	case 404:
		fail(w, 404, "NOT_FOUND", "ไม่พบข้อมูลที่ร้องขอ")
	default:
		upstreamUnavailable(w)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "login-ip:"+s.clientIP(r), 30, 5*time.Minute) {
		return
	}
	var input contracts.Login
	if !decode(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if !codePattern.MatchString(input.HospitalCode) || input.Username == "" || len(input.Username) > 255 || len(input.Password) == 0 || len(input.Password) > 4096 {
		fail(w, 400, "INVALID_REQUEST", "กรุณาเลือกหน่วยงานและระบุชื่อผู้ใช้กับรหัสผ่าน")
		return
	}
	if !s.limit(w, r, "login-account:"+input.HospitalCode+":"+tokenHash(input.Username), 10, 5*time.Minute) {
		return
	}
	h, err := s.config.Store.hospital(r.Context(), "code", input.HospitalCode)
	if err != nil || !active(h) {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			unavailable(w)
			return
		}
		disabled(w)
		return
	}
	body, _ := json.Marshal(contracts.Login{Username: input.Username, Password: input.Password})
	resp, err := s.upstream(r.Context(), h, "POST", "/api/auth/login", "", body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayError(w, resp, true)
		return
	}
	var value contracts.Token
	if err = readEnvelope(resp, &value); err != nil {
		upstreamUnavailable(w)
		return
	}
	token, err := s.config.Store.createSession(r.Context(), h, value)
	if errors.Is(err, errDisabled) {
		disabled(w)
		return
	}
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if max := time.Now().Add(24 * time.Hour).Unix(); value.ExpiresAt > max {
		value.ExpiresAt = max
	}
	value.AccessToken = token
	value.TokenType = "Bearer"
	value.Scope = "mobile:read"
	writeData(w, value)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) (Hospital, string, bool) {
	match := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
	if match == nil {
		expired(w)
		return Hospital{}, "", false
	}
	v, err := s.config.Store.session(r.Context(), match[1])
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			unavailable(w)
		} else {
			expired(w)
		}
		return Hospital{}, "", false
	}
	h, err := s.config.Store.hospital(r.Context(), "id", v.hospitalID)
	if err != nil {
		unavailable(w)
		return h, "", false
	}
	if !active(h) {
		disabled(w)
		return h, "", false
	}
	if v.revoked || v.expires <= time.Now().Unix() {
		expired(w)
		return h, "", false
	}
	token, err := s.config.Store.decrypt("session:"+h.ID+":"+tokenHash(match[1]), v.upstream)
	if err != nil {
		unavailable(w)
		return h, "", false
	}
	return h, token, true
}
func (s *Server) stillActive(w http.ResponseWriter, r *http.Request, h Hospital) bool {
	current, err := s.config.Store.hospital(r.Context(), "id", h.ID)
	if err != nil {
		unavailable(w)
		return false
	}
	if !active(current) {
		disabled(w)
		return false
	}
	return true
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	match := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
	if match == nil {
		expired(w)
		return
	}
	if err := s.config.Store.revoke(r.Context(), match[1]); err != nil {
		unavailable(w)
		return
	}
	writeData(w, map[string]bool{"logged_out": true})
}
func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	s.proxyProfile(w, r, http.MethodGet, "/api/profile", nil)
}
func (s *Server) proxyProfile(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	resp, err := s.upstream(r.Context(), h, method, path, token, body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayProfileError(w, resp)
		return
	}
	var value contracts.Profile
	if err = readEnvelope(resp, &value); err != nil || len(value.ID) == 0 || string(value.ID) == "null" {
		upstreamUnavailable(w)
		return
	}
	centralToken := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))[1]
	session, err := s.config.Store.session(r.Context(), centralToken)
	if err != nil {
		unavailable(w)
		return
	}
	if identity(value.ID) != identity(json.RawMessage(session.userID)) || identity(value.ID) == "" {
		_ = s.config.Store.revoke(r.Context(), centralToken)
		expired(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	value.Hospital = &h.Hospital
	if value.SignatureURL != nil && *value.SignatureURL != "" && value.Employee != nil {
		path := "/api/profile/signature"
		value.SignatureURL = &path
	} else {
		value.SignatureURL = nil
	}
	if value.AvatarURL != nil && *value.AvatarURL != "" {
		path := "/api/profile/avatar"
		value.AvatarURL = &path
	} else {
		value.AvatarURL = nil
	}
	writeData(w, value)
}
func (s *Server) avatar(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	resp, err := s.upstream(r.Context(), h, "GET", "/api/profile/avatar", token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayError(w, resp, false)
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024+1))
	if err != nil || len(data) > 5*1024*1024 {
		upstreamUnavailable(w)
		return
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/gif" && contentType != "image/webp" {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (s *Server) directory(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	query := url.Values{}
	for _, key := range []string{"q", "exclude_emp_id", "page"} {
		if value := r.URL.Query().Get(key); len(value) <= 255 {
			query.Set(key, value)
		} else {
			fail(w, 400, "INVALID_REQUEST", "ข้อมูลค้นหาไม่ถูกต้อง")
			return
		}
	}
	resp, err := s.upstream(r.Context(), h, "GET", "/depdrop/employee-by-id?"+query.Encode(), token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.relayError(w, resp, false)
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 || !json.Valid(data) {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(tokenHash(a)), []byte(tokenHash(b))) == 1
}

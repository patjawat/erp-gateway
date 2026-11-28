package gateway

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const adminCookie = "erp_gateway_admin"

func (s *Server) cookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: token, Path: "/admin/", HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
func (s *Server) adminAuth(w http.ResponseWriter, r *http.Request, mutation bool) (adminSession, bool) {
	cookie, err := r.Cookie(adminCookie)
	if err != nil {
		fail(w, 401, "ADMIN_LOGIN_REQUIRED", "กรุณาเข้าสู่ระบบผู้ดูแล")
		return adminSession{}, false
	}
	s.mu.Lock()
	value, ok := s.admins[tokenHash(cookie.Value)]
	if ok && !value.expires.After(time.Now()) {
		delete(s.admins, tokenHash(cookie.Value))
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		fail(w, 401, "ADMIN_LOGIN_REQUIRED", "กรุณาเข้าสู่ระบบผู้ดูแล")
		return adminSession{}, false
	}
	if mutation && (!equal(r.Header.Get("X-CSRF-Token"), value.csrf) || !s.sameOrigin(r)) {
		fail(w, 403, "CSRF_REJECTED", "กรุณาโหลดหน้าใหม่แล้วลองอีกครั้ง")
		return adminSession{}, false
	}
	return value, true
}
func (s *Server) sameOrigin(r *http.Request) bool {
	if s.config.WebPolicy.IsLANHTTPURL(s.config.PublicURL) {
		return s.config.WebPolicy.AllowsLANOrigin(r, s.config.PublicURL)
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.User == nil && u.Host == r.Host && (u.Scheme == "https" || (!s.config.CookieSecure && u.Scheme == "http"))
}
func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		fail(w, 403, "CSRF_REJECTED", "ไม่อนุญาตให้เข้าสู่ระบบจากที่อยู่นี้")
		return
	}
	if !s.limit(w, r, "admin-login:"+s.clientIP(r), 10, 5*time.Minute) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	validPassword := equal(input.Password, s.config.AdminPassword)
	if s.config.AdminPasswordVerifier != nil {
		validPassword = s.config.AdminPasswordVerifier(input.Password)
	}
	if !equal(input.Username, s.config.AdminUsername) || !validPassword {
		fail(w, 401, "INVALID_CREDENTIALS", "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}
	token, err := randomToken()
	if err != nil {
		unavailable(w)
		return
	}
	csrf, err := randomToken()
	if err != nil {
		unavailable(w)
		return
	}
	s.mu.Lock()
	for k, v := range s.admins {
		if !v.expires.After(time.Now()) {
			delete(s.admins, k)
		}
	}
	if old, err := r.Cookie(adminCookie); err == nil {
		delete(s.admins, tokenHash(old.Value))
	}
	s.admins[tokenHash(token)] = adminSession{csrf: csrf, expires: time.Now().Add(8 * time.Hour)}
	s.mu.Unlock()
	s.cookie(w, token, 8*3600)
	writeData(w, map[string]string{"csrf_token": csrf})
}
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) {
	value, ok := s.adminAuth(w, r, false)
	if ok {
		writeData(w, map[string]string{"csrf_token": value.csrf})
	}
}
func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	cookie, _ := r.Cookie(adminCookie)
	s.mu.Lock()
	delete(s.admins, tokenHash(cookie.Value))
	s.mu.Unlock()
	s.cookie(w, "", -1)
	writeData(w, map[string]bool{"logged_out": true})
}
func (s *Server) adminHospitals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	s.adminHospitalPage(w, r)
}

type hospitalInput struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Province   string  `json:"province"`
	APIURL     string  `json:"api_url"`
	GatewayKey string  `json:"gateway_key"`
	StartDate  *string `json:"start_date"`
	ExpiryDate *string `json:"expiry_date"`
}

func (s *Server) hospitalInput(w http.ResponseWriter, r *http.Request, creating bool) (hospitalInput, bool) {
	var in hospitalInput
	if !decode(w, r, &in) {
		return in, false
	}
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.Province = strings.TrimSpace(in.Province)
	in.APIURL = strings.TrimRight(strings.TrimSpace(in.APIURL), "/")
	if !codePattern.MatchString(in.Code) || in.Name == "" || len(in.Name) > 500 || len(in.Province) > 255 || len(in.APIURL) > 2048 || ((creating || in.GatewayKey != "") && (len(in.GatewayKey) < 32 || len(in.GatewayKey) > 512 || strings.ContainsAny(in.GatewayKey, "\r\n"))) {
		fail(w, 400, "INVALID_REQUEST", "ตรวจสอบรหัสสถานพยาบาล 5 หลัก ชื่อ และ Gateway key อย่างน้อย 32 ตัวอักษร")
		return in, false
	}
	if _, err := s.egress.approvedURL(in.Code, in.APIURL); err != nil {
		fail(w, 400, "INVALID_API_URL", "ปลายทาง API ต้องตรงกับปลายทางที่อนุญาตสำหรับโรงพยาบาลนี้")
		return in, false
	}
	return in, true
}
func (s *Server) adminAddHospital(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if !s.beginManagement(w) {
		return
	}
	defer s.management.mu.Unlock()
	in, ok := s.hospitalInput(w, r, true)
	if !ok {
		return
	}
	if _, err := s.config.Store.hospital(r.Context(), "code", in.Code); err == nil {
		fail(w, 409, "DUPLICATE_HOSPITAL", "มีรหัสสถานพยาบาลนี้แล้ว")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		unavailable(w)
		return
	}
	h := Hospital{APIURL: in.APIURL}
	h.Code = in.Code
	h.Name = in.Name
	h.Province = in.Province
	h.StartDate, h.ExpiryDate, ok = hospitalDates(w, h, in)
	if !ok {
		return
	}
	h, err := s.config.Store.addHospital(r.Context(), h, in.GatewayKey)
	if err != nil {
		fail(w, 409, "CREATE_FAILED", "บันทึกหน่วยงานไม่สำเร็จ กรุณาตรวจสอบรหัสซ้ำแล้วลองใหม่")
		return
	}
	writeData(w, h)
}
func (s *Server) adminUpdateHospital(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if !s.beginManagement(w) {
		return
	}
	defer s.management.mu.Unlock()
	in, ok := s.hospitalInput(w, r, false)
	if !ok {
		return
	}
	tx, err := s.config.Store.db.BeginTx(r.Context(), nil)
	if err != nil {
		unavailable(w)
		return
	}
	defer tx.Rollback()
	h, err := scanHospital(tx.QueryRowContext(r.Context(), "SELECT "+hospitalColumns+" FROM hospitals WHERE id=?", r.PathValue("id")))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "NOT_FOUND", "ไม่พบหน่วยงาน")
		} else {
			unavailable(w)
		}
		return
	}
	if in.Code != h.Code {
		fail(w, 400, "IMMUTABLE_CODE", "ไม่สามารถเปลี่ยนรหัสสถานพยาบาลของหน่วยงานนี้")
		return
	}
	startDate, expiryDate, ok := hospitalDates(w, h, in)
	if !ok {
		return
	}
	configurationChanged := in.Name != h.Name || in.Province != h.Province || in.APIURL != h.APIURL || in.GatewayKey != ""
	key := h.key
	if in.GatewayKey != "" {
		key, err = s.config.Store.encrypt("hospital:"+h.ID, in.GatewayKey)
		if err != nil {
			unavailable(w)
			return
		}
	}
	reset := ""
	reason := "usage dates changed"
	if configurationChanged {
		reset = ",approval_status='pending',enabled=0,connection_status='unknown',checked_at=NULL"
		reason = "configuration changed; approval reset"
	}
	_, err = tx.ExecContext(r.Context(), "UPDATE hospitals SET name=?,province=?,api_url=?,gateway_key=?,start_date=?,expiry_date=?"+reset+" WHERE id=?", in.Name, in.Province, in.APIURL, key, startDate, expiryDate, h.ID)
	if err == nil && configurationChanged {
		_, err = tx.ExecContext(r.Context(), "UPDATE sessions SET revoked=1,erp_token=X'' WHERE hospital_id=?", h.ID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES(?,?,?,?,?)", h.ID, h.Code, "update", reason, time.Now().Unix())
	}
	if err != nil {
		unavailable(w)
		return
	}
	if err = tx.Commit(); err != nil {
		unavailable(w)
		return
	}
	h, err = s.config.Store.hospital(r.Context(), "id", h.ID)
	if err != nil {
		unavailable(w)
		return
	}
	writeData(w, h)
}
func (s *Server) adminAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if !s.beginManagement(w) {
		return
	}
	defer s.management.mu.Unlock()
	h, err := s.config.Store.hospital(r.Context(), "id", r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "NOT_FOUND", "ไม่พบหน่วยงาน")
		} else {
			unavailable(w)
		}
		return
	}
	action := r.PathValue("action")
	if action == "approve" || action == "enable" {
		if _, err := s.egress.approvedURL(h.Code, h.APIURL); err != nil {
			fail(w, 409, "DESTINATION_NOT_APPROVED", "ปลายทางของหน่วยงานยังไม่อยู่ในรายการที่อนุญาต เพิ่มปลายทางในตั้งค่าก่อนเปิดใช้งาน")
			return
		}
	}
	if action == "test" {
		resp, callErr := s.upstream(r.Context(), h, "GET", "/api/status", "", nil)
		online := false
		backendPath := ""
		if callErr == nil {
			if resp.StatusCode == 200 {
				var status backendStatus
				if readEnvelopeLimit(resp, &status, 8*1024) == nil {
					backendPath, callErr = status.directoryPath(h)
					online = callErr == nil
				}
			} else {
				resp.Body.Close()
			}
		}
		if err = s.config.Store.connection(r.Context(), h, online); err != nil {
			if errors.Is(err, errConfigChanged) {
				fail(w, 409, "STATE_CONFLICT", "ข้อมูลหน่วยงานเปลี่ยนระหว่างทดสอบ กรุณาทดสอบใหม่")
			} else {
				unavailable(w)
			}
			return
		}
		if online {
			s.directoryBackends.remember(directoryBackendFingerprint(h, s.managementRevision), backendPath)
		}
	} else {
		reason := ""
		if action == "suspend" {
			var in struct {
				Reason string `json:"reason"`
			}
			if !decode(w, r, &in) {
				return
			}
			reason = strings.TrimSpace(in.Reason)
			if reason == "" || len(reason) > 1000 {
				fail(w, 400, "REASON_REQUIRED", "กรุณาระบุเหตุผลการระงับ")
				return
			}
		}
		if action != "approve" && action != "enable" && action != "suspend" {
			fail(w, 404, "NOT_FOUND", "ไม่พบคำสั่งที่ร้องขอ")
			return
		}
		if err = s.config.Store.change(r.Context(), h.ID, action, reason); err != nil {
			fail(w, 409, "STATE_CONFLICT", "กรุณาทดสอบการเชื่อมต่อให้สำเร็จก่อนอนุมัติหรือเปิดใช้งาน")
			return
		}
	}
	h, err = s.config.Store.hospital(r.Context(), "id", h.ID)
	if err != nil {
		unavailable(w)
		return
	}
	writeData(w, h)
}
func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	s.adminAuditPage(w, r)
}

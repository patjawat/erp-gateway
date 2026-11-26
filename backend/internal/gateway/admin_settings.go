package gateway

import (
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// RuntimeSettings contains only administrator-editable public configuration.
// Password verifiers, encryption keys, credentials and identity paths are not
// accepted from this endpoint and are preserved by the bootstrap manager.
type RuntimeSettings struct {
	PublicURL            string   `json:"public_url"`
	TrustedProxyCIDRs    []string `json:"trusted_proxy_cidrs"`
	ApprovedUpstreams    []string `json:"approved_upstreams"`
	PrivateUpstreamCIDRs []string `json:"private_upstream_cidrs"`
}

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	if s.config.ReadSettings == nil || s.config.ApplySettings == nil {
		writeData(w, map[string]any{"configured": false, "editable": false, "lan_http_enabled": s.config.WebPolicy.LANHTTPEnabled(), "local_http_enabled": s.config.WebPolicy.LocalHTTPEnabled(), "settings": nil, "message": "Gateway ยังใช้ environment เดิม ย้ายเข้าการตั้งค่าถาวรตาม WEB_SETUP ก่อน โดยรักษา key และฐานข้อมูลเดิม"})
		return
	}
	settings, err := s.config.ReadSettings()
	if err != nil {
		unavailable(w)
		return
	}
	message := "ใช้ HTTPS เดิม การแก้ไขไม่เปลี่ยนรหัสผ่าน key หรือใบรับรอง และต้องเข้าสู่ระบบใหม่หลังบันทึก"
	if s.config.WebPolicy.IsLANHTTPURL(s.config.PublicURL) {
		message = "ใช้ HTTP เฉพาะ LAN ที่อนุญาต ข้อมูลระหว่างทางไม่เข้ารหัส ต้องเข้าสู่ระบบใหม่หลังบันทึก"
	}
	writeData(w, map[string]any{"configured": true, "editable": true, "lan_http_enabled": s.config.WebPolicy.LANHTTPEnabled(), "local_http_enabled": s.config.WebPolicy.LocalHTTPEnabled(), "settings": settings, "message": message})
}
func normalizedNetworks(values []string) map[string]bool {
	set := map[string]bool{}
	for _, v := range values {
		if p, err := netip.ParsePrefix(strings.TrimSpace(v)); err == nil {
			set[p.Masked().String()] = true
		}
	}
	return set
}
func (s *Server) adminApplySettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if s.config.ApplySettings == nil {
		fail(w, 409, "PERSISTED_SETUP_REQUIRED", "นำเข้าระบบเดิมผ่านการตั้งค่าถาวรก่อนแก้ไขผ่านเว็บ")
		return
	}
	if !s.limit(w, r, "settings-apply", 3, time.Minute) {
		return
	}
	var input RuntimeSettings
	if !decodeLimit(w, r, &input, 128<<10) {
		return
	}
	if len(input.PublicURL) > 2048 || len(input.ApprovedUpstreams) > 500 || len(input.PrivateUpstreamCIDRs) > 128 || len(input.TrustedProxyCIDRs) > 32 {
		fail(w, 400, "INVALID_SETTINGS", "ค่าที่ตั้งมีขนาดเกินขอบเขต กรุณาลดจำนวนปลายทางหรือเครือข่าย")
		return
	}
	policy, err := newUpstreamPolicy(input.ApprovedUpstreams, input.PrivateUpstreamCIDRs, false)
	if err != nil {
		fail(w, 400, "INVALID_SETTINGS", "รายการปลายทางหรือเครือข่ายไม่ถูกต้อง ใช้รหัส 5 หลัก=https://ปลายทาง และ CIDR ที่จำเป็น")
		return
	}
	if !s.beginManagement(w) {
		return
	}
	defer s.management.mu.Unlock()
	rows, err := s.config.Store.db.QueryContext(r.Context(), "SELECT "+hospitalColumns+" FROM hospitals WHERE enabled=1 AND approval_status='approved'")
	if err != nil {
		unavailable(w)
		return
	}
	activeSites := 0
	for rows.Next() {
		h, e := scanHospital(rows)
		if e != nil {
			rows.Close()
			unavailable(w)
			return
		}
		activeSites++
		if _, e = policy.approvedURL(h.Code, h.APIURL); e != nil {
			rows.Close()
			fail(w, 409, "ACTIVE_DESTINATION_REQUIRED", "รายการใหม่ตัดปลายทางของหน่วยงานที่เปิดใช้งานอยู่ พักหน่วยงานนั้นหรือคงปลายทางเดิมก่อนบันทึก")
			return
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		unavailable(w)
		return
	}
	if activeSites > 0 {
		next := normalizedNetworks(input.PrivateUpstreamCIDRs)
		for old := range normalizedNetworks(s.config.PrivateUpstreamCIDRs) {
			if !next[old] {
				fail(w, 409, "ACTIVE_NETWORK_REQUIRED", "คงเครือข่ายเดิมระหว่างมีหน่วยงานเปิดใช้งานอยู่ เพื่อลดการตัดการเชื่อมต่อโดยไม่ตั้งใจ")
				return
			}
		}
	}
	if err = s.config.ApplySettings(r.Context(), input); err != nil {
		fail(w, 400, "SETTINGS_REJECTED", err.Error())
		return
	}
	s.management.current.Store(&managementRevision{value: managementFingerprint(policy, input.TrustedProxyCIDRs)})
	_, _ = s.config.Store.db.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','settings_apply','Validated public runtime settings applied; credentials and identity retained',?)", time.Now().Unix())
	writeData(w, map[string]bool{"applied": true, "relogin_required": true})
}
func (s *Server) adminExportRecovery(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, true); !ok {
		return
	}
	if s.config.ExportRecovery == nil {
		fail(w, 409, "PERSISTED_SETUP_REQUIRED", "ย้ายเข้าการตั้งค่าถาวรก่อนสำรองข้อมูลกู้คืนผ่านเว็บ")
		return
	}
	if !s.limit(w, r, "recovery-export", 3, time.Minute) {
		return
	}
	var input struct {
		Passphrase string `json:"passphrase"`
	}
	if !decodeLimit(w, r, &input, 8<<10) {
		return
	}
	if len(input.Passphrase) < 16 || len(input.Passphrase) > 4096 {
		fail(w, 422, "PASSPHRASE_REQUIRED", "ตั้งรหัสสำรอง 16 ถึง 4096 ตัวอักษร และเก็บแยกจากไฟล์สำรอง")
		return
	}
	body, err := s.config.ExportRecovery(r.Context(), input.Passphrase)
	input.Passphrase = ""
	if err != nil {
		fail(w, 409, "RECOVERY_EXPORT_FAILED", "ยังสร้างไฟล์กู้คืนไม่ได้ ตรวจสิทธิ์ไฟล์และการตั้งค่าถาวรแล้วลองใหม่")
		return
	}
	if len(body) == 0 || len(body) > 2<<20 {
		unavailable(w)
		return
	}
	if _, err = s.config.Store.db.ExecContext(r.Context(), "INSERT INTO audit(hospital_id,hospital_code,action,reason,created_at) VALUES('','','recovery_export','Encrypted config/identity export requested; database backup is separate',?)", time.Now().Unix()); err != nil {
		unavailable(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="gateway-recovery-`+time.Now().UTC().Format("20060102T150405Z")+`.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

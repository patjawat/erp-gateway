package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) loginMethods(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "login-policy:"+s.clientIP(r), 90, time.Minute) {
		return
	}
	code := r.PathValue("code")
	if !codePattern.MatchString(code) {
		fail(w, 400, "INVALID_HOSPITAL_CODE", "กรุณาระบุรหัสหน่วยงาน 5 หลัก")
		return
	}
	h, err := s.config.Store.hospital(r.Context(), "code", code)
	if err != nil || !active(h) {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			unavailable(w)
			return
		}
		disabled(w)
		return
	}
	resp, err := s.upstream(r.Context(), h, http.MethodGet, "/api/auth/login-methods", "", nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != http.StatusOK {
		s.relayError(w, resp, false)
		return
	}
	// Decode required booleans as pointers: a partial or malformed response must
	// never turn into an apparently valid default policy on the phone.
	var raw struct {
		HospitalCode string `json:"hospital_code"`
		LoginMethods struct {
			Password   *bool `json:"password"`
			ProviderID *bool `json:"provider_id"`
			ThaID      *bool `json:"thaid"`
		} `json:"login_methods"`
		ExternalAuth json.RawMessage `json:"external_auth"`
	}
	if readEnvelope(resp, &raw) != nil || raw.HospitalCode != h.Code || raw.LoginMethods.Password == nil || raw.LoginMethods.ProviderID == nil || raw.LoginMethods.ThaID == nil {
		upstreamUnavailable(w)
		return
	}
	var external map[string]struct {
		Available *bool  `json:"available"`
		Message   string `json:"message"`
	}
	if json.Unmarshal(raw.ExternalAuth, &external) != nil || external["provider_id"].Available == nil || external["thaid"].Available == nil {
		upstreamUnavailable(w)
		return
	}
	availability := make(map[string]contracts.ExternalAuthAvailability, 2)
	for _, provider := range []string{"provider_id", "thaid"} {
		value := external[provider]
		availability[provider] = contracts.ExternalAuthAvailability{Available: *value.Available}
		if !*value.Available {
			// Keep private endpoint and credential details out of public responses.
			availability[provider] = contracts.ExternalAuthAvailability{Available: false, Message: "ช่องทางนี้ยังไม่พร้อมใช้งาน กรุณาติดต่อผู้ดูแลหน่วยงาน"}
		}
	}
	writeData(w, struct {
		Hospital     contracts.Hospital                            `json:"hospital"`
		LoginMethods contracts.LoginMethods                        `json:"login_methods"`
		ExternalAuth map[string]contracts.ExternalAuthAvailability `json:"external_auth"`
	}{h.Hospital, contracts.LoginMethods{Password: *raw.LoginMethods.Password, ProviderID: *raw.LoginMethods.ProviderID, ThaID: *raw.LoginMethods.ThaID}, availability})
}

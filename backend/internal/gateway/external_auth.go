package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"erp-platform/gateway/internal/contracts"
)

var externalFlowValuePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var externalFlowVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func (s *Server) externalStart(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "external-start:"+s.clientIP(r), 300, 5*time.Minute) {
		return
	}
	var input contracts.ExternalStart
	if !decode(w, r, &input) {
		return
	}
	if !codePattern.MatchString(input.HospitalCode) || input.Provider != "provider_id" && input.Provider != "thaid" || !externalFlowValuePattern.MatchString(input.CodeChallenge) {
		fail(w, 400, "INVALID_REQUEST", "ข้อมูลยืนยันตัวตนไม่ถูกต้อง")
		return
	}
	h, ok := s.externalHospital(w, r, input.HospitalCode)
	if !ok {
		return
	}
	body, _ := json.Marshal(input)
	resp, err := s.upstream(r.Context(), h, http.MethodPost, "/api/auth/external/start", "", body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.externalError(w, resp)
		return
	}
	var flow contracts.ExternalFlow
	if readEnvelope(resp, &flow) != nil || !externalFlowValuePattern.MatchString(flow.FlowID) || flow.ExpiresAt <= time.Now().Unix() || flow.ExpiresAt > time.Now().Add(11*time.Minute).Unix() || flow.PollInterval < 2 || flow.PollInterval > 30 {
		upstreamUnavailable(w)
		return
	}
	// This URL comes from the authenticated hospital API, not from mobile input.
	// Enforce HTTPS nevertheless so a misconfigured provider cannot downgrade it.
	authorization, parseErr := url.Parse(flow.AuthorizationURL)
	if parseErr != nil || len(flow.AuthorizationURL) > 16*1024 || authorization.Scheme != "https" || authorization.Host == "" || authorization.User != nil || authorization.Fragment != "" || authorization.Opaque != "" || strings.ContainsAny(flow.AuthorizationURL, "\\\r\n") {
		upstreamUnavailable(w)
		return
	}
	writeData(w, flow)
}

func (s *Server) externalHospital(w http.ResponseWriter, r *http.Request, code string) (Hospital, bool) {
	h, err := s.config.Store.hospital(r.Context(), "code", code)
	if err != nil || !active(h) {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			unavailable(w)
		} else {
			disabled(w)
		}
		return Hospital{}, false
	}
	return h, true
}

func (s *Server) externalDeviceRequest(w http.ResponseWriter, r *http.Request) (Hospital, contracts.ExternalFlowRequest, bool) {
	var input contracts.ExternalFlowRequest
	if !decode(w, r, &input) {
		return Hospital{}, input, false
	}
	if !codePattern.MatchString(input.HospitalCode) || !externalFlowValuePattern.MatchString(input.FlowID) || !externalFlowVerifierPattern.MatchString(input.CodeVerifier) {
		fail(w, 400, "INVALID_REQUEST", "ข้อมูลยืนยันตัวตนไม่ถูกต้อง")
		return Hospital{}, input, false
	}
	h, ok := s.externalHospital(w, r, input.HospitalCode)
	return h, input, ok
}

func (s *Server) externalStatus(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "external-poll:"+s.clientIP(r), 12000, time.Minute) {
		return
	}
	h, input, ok := s.externalDeviceRequest(w, r)
	if !ok {
		return
	}
	if !s.limit(w, r, "external-poll-flow:"+input.HospitalCode+":"+input.FlowID, 45, time.Minute) {
		return
	}
	body, _ := json.Marshal(input)
	resp, err := s.upstream(r.Context(), h, http.MethodPost, "/api/auth/external/status", "", body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.externalError(w, resp)
		return
	}
	var result contracts.ExternalResult
	if readEnvelope(resp, &result) != nil {
		upstreamUnavailable(w)
		return
	}
	if result.Status == "pending" {
		// Pending never relays a malformed upstream token or identity.
		writeData(w, map[string]string{"status": "pending"})
		return
	}
	if result.Status != "complete" {
		upstreamUnavailable(w)
		return
	}
	token, err := s.config.Store.createSession(r.Context(), h, result.Token)
	if errors.Is(err, errDisabled) {
		disabled(w)
		return
	}
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if max := time.Now().Add(24 * time.Hour).Unix(); result.ExpiresAt > max {
		result.ExpiresAt = max
	}
	result.AccessToken = token
	result.TokenType = "Bearer"
	result.Scope = "mobile:read"
	writeData(w, result)
}

func (s *Server) externalCancel(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "external-cancel:"+s.clientIP(r), 120, time.Minute) {
		return
	}
	h, input, ok := s.externalDeviceRequest(w, r)
	if !ok {
		return
	}
	body, _ := json.Marshal(input)
	resp, err := s.upstream(r.Context(), h, http.MethodPost, "/api/auth/external/cancel", "", body)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode != 200 {
		s.externalError(w, resp)
		return
	}
	var value struct {
		Status string `json:"status"`
	}
	if readEnvelope(resp, &value) != nil || value.Status != "cancelled" {
		upstreamUnavailable(w)
		return
	}
	writeData(w, value)
}

func (s *Server) externalError(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	var envelope struct {
		Error contracts.Failure `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	_ = json.Unmarshal(data, &envelope)
	messages := map[string]string{
		"EXTERNAL_AUTH_EXPIRED":     "คำขอยืนยันตัวตนหมดอายุ กรุณาเริ่มใหม่",
		"EXTERNAL_AUTH_CANCELLED":   "ยกเลิกการยืนยันตัวตนแล้ว",
		"EXTERNAL_AUTH_FAILED":      "ยืนยันตัวตนไม่สำเร็จ กรุณาลองใหม่",
		"EXTERNAL_AUTH_UNAVAILABLE": "หน่วยงานยังไม่พร้อมให้ใช้งานช่องทางนี้ กรุณาติดต่อผู้ดูแลระบบ",
		"LOGIN_POLICY_UNAVAILABLE":  "ตรวจสอบวิธีเข้าสู่ระบบไม่ได้ กรุณาลองใหม่",
		"LOGIN_METHOD_DISABLED":     "หน่วยงานปิดช่องทางเข้าสู่ระบบนี้แล้ว กรุณาเลือกวิธีอื่น",
		"RATE_LIMITED":              "กรุณารอสักครู่แล้วลองใหม่",
	}
	message, ok := messages[envelope.Error.Code]
	if !ok || resp.StatusCode < 400 || resp.StatusCode > 599 {
		upstreamUnavailable(w)
		return
	}
	fail(w, resp.StatusCode, envelope.Error.Code, message)
}

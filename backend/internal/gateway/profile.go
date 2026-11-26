package gateway

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		fail(w, 415, "UNSUPPORTED_MEDIA_TYPE", "กรุณาส่งข้อมูลเป็น application/json")
		return
	}
	var input contracts.ProfileUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, contracts.MaxProfileUpdateJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || contracts.ValidateProfileUpdate(&input) != nil {
		fail(w, 422, "PROFILE_INVALID", "กรุณาตรวจสอบข้อมูลโปรไฟล์และลายเซ็น")
		return
	}
	body, _ := json.Marshal(input)
	s.proxyProfile(w, r, http.MethodPost, "/api/profile/update", body)
}

func (s *Server) profileSignature(w http.ResponseWriter, r *http.Request) {
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	response, err := s.upstream(r.Context(), h, http.MethodGet, "/api/profile/signature", token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayProfileError(w, response)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, contracts.MaxProfileSignatureBytes+1))
	media := http.DetectContentType(body)
	if err != nil || len(body) == 0 || len(body) > contracts.MaxProfileSignatureBytes || (media != "image/png" && media != "image/jpeg") {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (s *Server) relayProfileError(w http.ResponseWriter, response *http.Response) {
	defer response.Body.Close()
	var envelope struct {
		Error contracts.Failure `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	_ = json.Unmarshal(body, &envelope)
	// Fixed messages prevent an upstream error from reflecting private SQL details.
	switch {
	case envelope.Error.Code == "GATEWAY_UNAUTHORIZED":
		upstreamUnavailable(w)
	case response.StatusCode == 401:
		expired(w)
	case response.StatusCode == 403 && envelope.Error.Code == "EMPLOYEE_REQUIRED":
		fail(w, 403, "EMPLOYEE_REQUIRED", "บัญชีนี้ยังไม่มีข้อมูลบุคลากร")
	case response.StatusCode == 404:
		fail(w, 404, "SIGNATURE_NOT_FOUND", "ยังไม่มีลายเซ็นที่บันทึกไว้")
	case response.StatusCode == 422 && envelope.Error.Code == "PROFILE_INVALID":
		fail(w, 422, "PROFILE_INVALID", "กรุณาตรวจสอบข้อมูลโปรไฟล์และลายเซ็น")
	case response.StatusCode == 409 && envelope.Error.Code == "PROFILE_CONTACT_CONFLICT":
		fail(w, 409, "PROFILE_CONTACT_CONFLICT", "หมายเลขโทรศัพท์นี้ถูกใช้งานแล้ว กรุณาตรวจสอบข้อมูล")
	case response.StatusCode == 503 && envelope.Error.Code == "PROFILE_READ_ONLY":
		fail(w, 503, "PROFILE_READ_ONLY", "หน่วยงานยังไม่เปิดการบันทึกโปรไฟล์ผ่านมือถือ")
	case response.StatusCode == 503 && envelope.Error.Code == "PROFILE_SIGNATURE_DISABLED":
		fail(w, 503, "PROFILE_SIGNATURE_DISABLED", "หน่วยงานยังไม่พร้อมบันทึกลายเซ็นผ่านมือถือ กรุณาติดต่อผู้ดูแลระบบ")
	default:
		upstreamUnavailable(w)
	}
}

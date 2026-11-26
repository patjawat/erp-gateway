package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"

	"erp-platform/gateway/internal/contracts"
)

var attendancePhotoRoute = regexp.MustCompile(`^/api/attendance/records/[0-9]{1,20}/photo$`)
var attendanceNumericID = regexp.MustCompile(`^[0-9]{1,20}$`)

func (s *Server) attendanceLive(w http.ResponseWriter, r *http.Request) {
	if isAttendanceApprovalPath(r.URL.Path) {
		s.attendanceApproval(w, r)
		return
	}
	path := r.URL.Path
	method := "POST"
	if path == "/api/attendance/context" || attendancePhotoRoute.MatchString(path) {
		method = "GET"
	} else if path != "/api/attendance/location-check" && path != "/api/attendance/photos" && path != "/api/attendance/records" {
		fail(w, 404, "NOT_FOUND", "ไม่พบข้อมูลที่ร้องขอ")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "INVALID_REQUEST", "ตัวเลือกการลงเวลาไม่ถูกต้อง")
		return
	}
	// Upload and punch limits also bound repeated submissions before the hospital.
	if !s.limit(w, r, "attendance:"+h.ID+":"+token, 60, time.Minute) {
		return
	}
	var body []byte
	contentType := "application/json"
	switch path {
	case "/api/attendance/location-check":
		var v contracts.AttendanceLocationRequest
		if !decodeLimit(w, r, &v, 16384) {
			return
		}
		if contracts.ValidateAttendanceLocation(v) != nil {
			fail(w, 400, "INVALID_REQUEST", "พิกัดการลงเวลาไม่ถูกต้อง")
			return
		}
		body, _ = json.Marshal(v)
	case "/api/attendance/records":
		var v contracts.AttendancePunchRequest
		if !decodeLimit(w, r, &v, 16384) {
			return
		}
		if contracts.ValidateAttendancePunch(v) != nil {
			fail(w, 400, "INVALID_REQUEST", "ข้อมูลการลงเวลาไม่ถูกต้อง")
			return
		}
		body, _ = json.Marshal(v)
	case "/api/attendance/photos":
		var err error
		body, contentType, err = attendanceMultipart(w, r)
		if err != nil {
			fail(w, 400, "INVALID_REQUEST", "กรุณาส่งรูปภาพหนึ่งรูป ขนาดไม่เกิน 8 MB")
			return
		}
	}
	var response *http.Response
	var err error
	if path == "/api/attendance/photos" {
		response, err = s.attendanceUploadUpstream(r, h, token, body, contentType)
	} else {
		response, err = s.upstream(r.Context(), h, method, path, token, body)
	}
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		s.relayAttendanceError(w, response)
		return
	}
	if attendancePhotoRoute.MatchString(path) {
		s.attendancePhotoResponse(w, r, h, response)
		return
	}
	var value any
	valid := false
	switch path {
	case "/api/attendance/context":
		var v contracts.AttendanceContext
		valid = readEnvelopeLimit(response, &v, 65536) == nil && validAttendanceContext(v)
		value = v
	case "/api/attendance/location-check":
		var v contracts.AttendancePosition
		valid = readEnvelopeLimit(response, &v, 16384) == nil && validAttendancePosition(v)
		value = v
	case "/api/attendance/records":
		var v contracts.AttendanceReceipt
		valid = readEnvelopeLimit(response, &v, 16384) == nil && validAttendanceReceipt(v)
		value = v
	case "/api/attendance/photos":
		var v contracts.AttendancePhotoUploadResult
		valid = readEnvelopeLimit(response, &v, 4096) == nil && contracts.ValidAttendancePhotoID(v.PhotoID)
		value = v
	}
	if !valid {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	writeData(w, value)
}

func attendanceMultipart(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, contracts.MaxAttendanceMultipartBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, "", err
	}
	part, err := reader.NextPart()
	if err != nil {
		return nil, "", err
	}
	defer part.Close()
	if part.FormName() != "file" || part.FileName() == "" {
		return nil, "", http.ErrMissingFile
	}
	data, err := io.ReadAll(io.LimitReader(part, contracts.MaxAttendancePhotoBytes+1))
	if err != nil || len(data) == 0 || len(data) > contracts.MaxAttendancePhotoBytes {
		return nil, "", http.ErrContentLength
	}
	media := http.DetectContentType(data)
	if media != "image/jpeg" && media != "image/png" && media != "image/webp" {
		return nil, "", http.ErrNotSupported
	}
	if _, err = reader.NextPart(); err != io.EOF {
		return nil, "", http.ErrNotSupported
	}
	// Rebuild a single part: filenames, paths, headers and additional fields are not relayed.
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	file, err := writer.CreateFormFile("file", "photo")
	if err != nil {
		return nil, "", err
	}
	if _, err = file.Write(data); err != nil {
		return nil, "", err
	}
	if err = writer.Close(); err != nil {
		return nil, "", err
	}
	return out.Bytes(), writer.FormDataContentType(), nil
}

func (s *Server) attendanceUploadUpstream(r *http.Request, h Hospital, token string, body []byte, contentType string) (*http.Response, error) {
	base, err := s.validURL(h.APIURL)
	if err != nil {
		return nil, err
	}
	key, err := s.config.Store.decrypt("hospital:"+h.ID, h.key)
	if err != nil {
		return nil, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/attendance/photos"
	req, err := http.NewRequestWithContext(r.Context(), "POST", base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Gateway-Key", key)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	return s.client.Do(req)
}
func validAttendanceAction(s string) bool { return s == "in" || s == "out" || s == "complete" }
func validAttendanceTime(s string) bool   { _, err := time.Parse(time.RFC3339, s); return err == nil }
func validAttendanceReceipt(v contracts.AttendanceReceipt) bool {
	return attendanceNumericID.MatchString(v.ID) && validAttendanceTime(v.RecordedAt) && (v.CheckType == "in" || v.CheckType == "out" || v.CheckType == "scan") && attendanceStatus(v.Status) && validAttendanceAction(v.NextAction) && len(v.LocationName) <= 2048
}
func validAttendanceContext(v contracts.AttendanceContext) bool {
	if !validAttendanceTime(v.ServerTime) || !validAttendanceAction(v.NextAction) || (v.Latest != nil && !validAttendanceReceipt(*v.Latest)) {
		return false
	}
	if !contracts.ValidAttendanceLocationType(v.LocationType) || !contracts.ValidAttendanceWorkMode(v.WorkMode) {
		return false
	}
	for _, at := range []*string{v.CheckIn, v.CheckOut} {
		if at != nil && !validAttendanceTime(*at) {
			return false
		}
	}
	if v.ScheduledEnd != nil && !validAttendanceTime(*v.ScheduledEnd) {
		return false
	}
	if v.CheckoutReasonRequired && (!v.CheckoutReasonSupported || v.NextAction != "out") {
		return false
	}
	return true
}
func validAttendancePosition(v contracts.AttendancePosition) bool {
	return attendanceNumericID.MatchString(v.LocationID) && v.LocationName != "" && len(v.LocationName) <= 2048 && !math.IsNaN(v.DistanceM) && !math.IsInf(v.DistanceM, 0) && v.DistanceM >= 0 && !math.IsNaN(v.RadiusM) && !math.IsInf(v.RadiusM, 0) && v.RadiusM > 0 && v.RadiusM <= 100000 && v.RequiresPhoto == !v.Inside && v.RequiresReason == !v.Inside
}
func (s *Server) attendancePhotoResponse(w http.ResponseWriter, r *http.Request, h Hospital, resp *http.Response) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, contracts.MaxAttendancePhotoBytes+1))
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || len(data) == 0 || len(data) > contracts.MaxAttendancePhotoBytes || media != "image/jpeg" || http.DetectContentType(data) != "image/jpeg" {
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (s *Server) relayAttendanceError(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	var env struct {
		Error contracts.Failure `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if len(data) > 16384 || json.Unmarshal(data, &env) != nil {
		upstreamUnavailable(w)
		return
	}
	if env.Error.Code == "GATEWAY_UNAUTHORIZED" {
		upstreamUnavailable(w)
		return
	}
	if resp.StatusCode == 401 {
		expired(w)
		return
	}
	if resp.StatusCode == 429 {
		fail(w, 429, "RATE_LIMITED", "ส่งคำขอบ่อยเกินไป กรุณารอสักครู่")
		return
	}
	messages := map[string]string{
		"ATTENDANCE_APPROVAL_DISABLED":        "ยังไม่เปิดการยืนยันลงเวลาผ่านมือถือ",
		"ATTENDANCE_APPROVAL_CHANGED":         "รายการเปลี่ยนแปลงหรือพิจารณาแล้ว กรุณาโหลดใหม่",
		"ATTENDANCE_APPROVAL_FORBIDDEN":       "ไม่มีสิทธิ์ยืนยันรายการนี้",
		"ATTENDANCE_APPROVAL_NOT_FOUND":       "ไม่พบรายการ",
		"ATTENDANCE_DISABLED":                 "ยังไม่เปิดการลงเวลาผ่านมือถือ",
		"ATTENDANCE_LOCATION_UNAVAILABLE":     "ยังไม่มีจุดลงเวลาที่พร้อมใช้งาน กรุณาติดต่อผู้ดูแลระบบ",
		"ATTENDANCE_LOCATION_REQUIRED":        "กรุณาเปิดตำแหน่งแล้วลองใหม่",
		"ATTENDANCE_REASON_REQUIRED":          "อยู่นอกพื้นที่ กรุณาระบุเหตุผล",
		"ATTENDANCE_CHECKOUT_REASON_REQUIRED": "เลยเวลาสิ้นสุดงาน กรุณาเลือกเหตุผลลงเวลาออก",
		"ATTENDANCE_PHOTO_REQUIRED":           "ลงเวลานอกพื้นที่ กรุณาถ่ายรูปยืนยันตัวตน",
		"ATTENDANCE_PHOTO_INVALID":            "รูปยืนยันไม่ถูกต้องหรือหมดอายุ กรุณาถ่ายใหม่",
		"ATTENDANCE_STATE_CHANGED":            "สถานะลงเวลาเปลี่ยนแล้ว กรุณาตรวจสอบแล้วลองใหม่",
		"ATTENDANCE_COMPLETE":                 "บันทึกเวลาเข้าและออกครบแล้ว",
		"ATTENDANCE_REQUEST_CONFLICT":         "คำขอนี้มีข้อมูลเปลี่ยนแปลง กรุณาตรวจสอบรายการที่บันทึกแล้ว",
		"ATTENDANCE_TOO_SOON":                 "เพิ่งบันทึกลงเวลา กรุณารอให้พ้น 2 นาทีจากรายการก่อนหน้าแล้วลองใหม่",
		"ATTENDANCE_LOCATION_STALE":           "ตำแหน่งหมดอายุ กรุณาตรวจตำแหน่งใหม่",
		"ATTENDANCE_LOCATION_INACCURATE":      "ตำแหน่งยังไม่แม่นยำ กรุณาตรวจตำแหน่งใหม่",
		"INVALID_REQUEST":                     "ข้อมูลการลงเวลาไม่ถูกต้อง",
		"NOT_FOUND":                           "ไม่พบข้อมูลที่ร้องขอ",
	}
	code := env.Error.Code
	status := resp.StatusCode
	expected := 422
	switch code {
	case "ATTENDANCE_DISABLED", "ATTENDANCE_LOCATION_UNAVAILABLE", "ATTENDANCE_APPROVAL_DISABLED":
		expected = 503
	case "ATTENDANCE_STATE_CHANGED", "ATTENDANCE_COMPLETE", "ATTENDANCE_REQUEST_CONFLICT", "ATTENDANCE_APPROVAL_CHANGED", "ATTENDANCE_TOO_SOON":
		expected = 409
	case "NOT_FOUND", "ATTENDANCE_APPROVAL_NOT_FOUND":
		expected = 404
	case "ATTENDANCE_APPROVAL_FORBIDDEN":
		expected = 403
	case "INVALID_REQUEST":
		if status == 400 {
			expected = 400
		}
	}
	if msg, ok := messages[code]; ok && status == expected {
		fail(w, status, code, msg)
		return
	}
	if status == 403 {
		fail(w, 403, "FORBIDDEN", "ไม่มีสิทธิ์ทำรายการนี้")
		return
	}
	upstreamUnavailable(w)
}

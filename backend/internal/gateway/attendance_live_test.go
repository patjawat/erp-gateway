package gateway

import (
	"bytes"
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttendanceLiveRoutesBoundToSession(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	b := r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta := r.login(t, a.Code)
	tb := r.login(t, b.Code)
	previous := r.app.client.Transport
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/attendance/") {
			return previous.RoundTrip(req)
		}
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) {
			t.Fatal("wrong credential")
		}
		value := contracts.AttendanceContext{ServerTime: "2026-11-18T12:00:00+07:00", NextAction: "in", WriteEnabled: true}
		data, _ := json.Marshal(map[string]any{"data": value})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	for _, token := range []string{ta, tb} {
		if w := r.request(t, "GET", "/api/attendance/context", nil, token, false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/attendance/context?emp_id=4", "/api/attendance/context?hospital_code=12448"} {
		if w := r.request(t, "GET", path, nil, ta, false); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/attendance/context", nil, "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := r.request(t, "POST", "/api/attendance/context", nil, ta, false); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if calls != 2 {
		t.Fatal("invalid request reached upstream", calls)
	}
	bad := map[string]any{"latitude": 0, "longitude": 0, "accuracy_m": 5, "captured_at": "2026-11-18T12:00:00+07:00", "emp_id": 99}
	if w := r.request(t, "POST", "/api/attendance/location-check", bad, ta, false); w.Code != 400 {
		t.Fatal("unknown input accepted", w.Code)
	}
}
func TestAttendanceLiveValidatesResponses(t *testing.T) {
	if validAttendanceContext(contracts.AttendanceContext{ServerTime: "bad", NextAction: "in"}) {
		t.Fatal("bad time")
	}
	if validAttendanceReceipt(contracts.AttendanceReceipt{ID: "../1", RecordedAt: "2026-11-18T12:00:00Z", CheckType: "in", Status: "approved", NextAction: "out"}) {
		t.Fatal("bad ID")
	}
	if validAttendancePosition(contracts.AttendancePosition{LocationID: "1", LocationName: "A", RadiusM: 100, Inside: false}) {
		t.Fatal("outside bypass flags")
	}
}
func TestAttendanceMultipartRejectsExtraFields(t *testing.T) {
	for _, extra := range []bool{false, true} {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		p, _ := writer.CreateFormFile("file", "../../x.jpg")
		p.Write([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 0, 0, 0})
		if extra {
			writer.WriteField("emp_id", "99")
		}
		writer.Close()
		req := httptest.NewRequest("POST", "/", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		data, typ, err := attendanceMultipart(httptest.NewRecorder(), req)
		if extra && err == nil {
			t.Fatal("extra allowed")
		}
		if !extra && (err != nil || len(data) == 0 || !strings.HasPrefix(typ, "multipart/form-data")) {
			t.Fatal(err)
		}
	}
}

func TestAttendanceConflictReasonsPreserved(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	for _, code := range []string{"ATTENDANCE_TOO_SOON", "ATTENDANCE_STATE_CHANGED", "ATTENDANCE_COMPLETE", "ATTENDANCE_REQUEST_CONFLICT"} {
		r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
			if !strings.HasPrefix(req.URL.Path, "/api/attendance/") {
				return previous.RoundTrip(req)
			}
			data, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": "internal upstream details"}})
			return &http.Response{StatusCode: 409, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
		})
		w := r.request(t, "GET", "/api/attendance/context", nil, token, false)
		if w.Code != 409 || !strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), "internal upstream") {
			t.Fatalf("%s %d %s", code, w.Code, w.Body)
		}
		if code == "ATTENDANCE_TOO_SOON" && (!strings.Contains(w.Body.String(), "2 นาที") || strings.Contains(w.Body.String(), "ข้อมูลเปลี่ยนแปลง")) {
			t.Fatal(w.Body.String())
		}
	}
}

func TestAttendanceCheckoutReasonsForwardedOnlyForCheckout(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	var received []string
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/attendance/records" {
			return previous.RoundTrip(req)
		}
		if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[a.Code] {
			t.Fatal("checkout sent without its authenticated session")
		}
		var input contracts.AttendancePunchRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.ExpectedAction != "out" {
			t.Fatal("invalid checkout reached upstream")
		}
		received = append(received, input.CheckoutReason)
		data, _ := json.Marshal(map[string]any{"data": contracts.AttendanceReceipt{ID: "101", RecordedAt: "2026-11-19T00:00:00+07:00", CheckType: "out", Status: "pending", NextAction: "complete"}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})
	coordinate, accuracy := 16.0, 5.0
	input := contracts.AttendancePunchRequest{
		AttendanceLocationRequest: contracts.AttendanceLocationRequest{Latitude: &coordinate, Longitude: &coordinate, AccuracyM: &accuracy, CapturedAt: "2026-11-19T00:00:00+07:00"},
		RequestID:                 "checkoutreason01",
		ExpectedAction:            "out",
	}
	for index, reason := range []string{"", "night_shift", "afternoon_shift", "overtime", "forgot_checkout"} {
		input.CheckoutReason = reason
		w := r.request(t, "POST", "/api/attendance/records", input, token, false)
		if w.Code != 200 || len(received) != index+1 || received[index] != reason {
			t.Fatalf("reason %q not forwarded: %d %s, received=%v", reason, w.Code, w.Body, received)
		}
	}
	calls := len(received)
	for _, reason := range []string{"other", "night_shift", "afternoon_shift", "overtime", "forgot_checkout"} {
		input.CheckoutReason = reason
		input.ExpectedAction = "in"
		if reason == "other" {
			input.ExpectedAction = "out"
		}
		if w := r.request(t, "POST", "/api/attendance/records", input, token, false); w.Code != 400 {
			t.Fatalf("invalid checkout reason/action accepted: %s/%s: %d %s", input.ExpectedAction, reason, w.Code, w.Body)
		}
	}
	if len(received) != calls {
		t.Fatal("invalid checkout reason reached upstream")
	}
}

func TestAttendanceCheckoutContextCompatibilityAndValidation(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	var upstream string
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/attendance/context" {
			return previous.RoundTrip(req)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(upstream))}, nil
	})
	for _, tc := range []struct {
		name, nextAction, extra string
		valid                   bool
	}{
		{"legacy", "out", "", true},
		{"supported", "out", `,"scheduled_end":"2026-11-18T16:30:00+07:00","checkout_reason_required":true,"checkout_reason_supported":true`, true},
		{"invalid_scheduled_end", "out", `,"scheduled_end":"16:30","checkout_reason_supported":true`, false},
		{"required_but_unsupported", "out", `,"checkout_reason_required":true`, false},
		{"required_for_checkin", "in", `,"checkout_reason_required":true,"checkout_reason_supported":true`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream = `{"data":{"server_time":"2026-11-19T00:00:00+07:00","next_action":"` + tc.nextAction + `","write_enabled":true` + tc.extra + `}}`
			w := r.request(t, "GET", "/api/attendance/context", nil, token, false)
			if !tc.valid {
				if w.Code != 503 || !strings.Contains(w.Body.String(), "ERP_UNAVAILABLE") {
					t.Fatalf("invalid checkout context accepted: %d %s", w.Code, w.Body)
				}
				return
			}
			if w.Code != 200 {
				t.Fatalf("valid checkout context rejected: %d %s", w.Code, w.Body)
			}
			var response struct {
				Data contracts.AttendanceContext `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			v := response.Data
			if tc.name == "legacy" && (v.ScheduledEnd != nil || v.CheckoutReasonRequired || v.CheckoutReasonSupported) {
				t.Fatalf("legacy response unexpectedly requires checkout reasons: %+v", v)
			}
			if tc.name == "supported" && (v.ScheduledEnd == nil || *v.ScheduledEnd != "2026-11-18T16:30:00+07:00" || !v.CheckoutReasonRequired || !v.CheckoutReasonSupported) {
				t.Fatalf("checkout context fields lost at gateway: %+v", v)
			}
		})
	}
}

func TestAttendanceCheckoutReasonRequiredErrorIsSafe(t *testing.T) {
	r := rig(t)
	for _, status := range []int{422, 409} {
		body := `{"error":{"code":"ATTENDANCE_CHECKOUT_REASON_REQUIRED","message":"private employee and database details"}}`
		response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
		w := httptest.NewRecorder()
		r.app.relayAttendanceError(w, response)
		if status != 422 {
			if w.Code != 503 || !strings.Contains(w.Body.String(), "ERP_UNAVAILABLE") {
				t.Fatalf("unexpected upstream status relayed: %d %s", w.Code, w.Body)
			}
			continue
		}
		var envelope struct {
			Error contracts.Failure `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if w.Code != 422 || envelope.Error.Code != "ATTENDANCE_CHECKOUT_REASON_REQUIRED" || envelope.Error.Message != "เลยเวลาสิ้นสุดงาน กรุณาเลือกเหตุผลลงเวลาออก" || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("checkout reason error not safely preserved: %d %s", w.Code, w.Body)
		}
	}
}

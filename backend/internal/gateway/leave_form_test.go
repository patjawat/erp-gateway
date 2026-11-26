package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func gatewayLeaveInput() contracts.LeaveInput {
	return contracts.LeaveInput{RequestID: "leave_request_000001", LeaveTypeID: "LT4", StartDate: "2030-11-18", EndDate: "2030-11-18", StartTime: "08:30", EndTime: "12:00", Reason: "พักผ่อน", TravelScope: "ในประเทศ", Comments: "ติดต่อทางโทรศัพท์", ReplacementEmployeeID: "42"}
}

func TestLeaveFormGatewayKeepsTenantCredentialsAndReviewedPayload(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("Cookie") != "" {
			t.Fatal("leave used another tenant's credentials")
		}
		response := httptest.NewRecorder()
		switch request.URL.Path {
		case "/api/leaves/options":
			entitlement, used, remaining := 10.0, 2.5, 7.5
			writeData(response, contracts.LeaveOptions{Types: []contracts.LeaveType{{ID: "LT4", Name: "Leave " + code}}, Balances: []contracts.LeaveBalance{{LeaveTypeID: "LT4", LeaveType: "Leave " + code, EntitlementDays: &entitlement, UsedDays: &used, RemainingDays: &remaining}}, CanSubmit: true, FiscalYear: 2574})
		case "/api/leaves/preview", "/api/leaves":
			var input contracts.LeaveInput
			if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&input) != nil || !reflect.DeepEqual(input, gatewayLeaveInput()) {
				t.Fatal("reviewed leave payload changed in transit")
			}
			if request.URL.Path == "/api/leaves/preview" {
				writeData(response, contracts.LeavePreview{
					TotalDays: .5, Period: "ครึ่งวัน " + code, FiscalYear: 2574,
					CalendarDays: 1, HolidaysDeducted: true,
					Approvals: []contracts.LeaveApproval{{Level: 1, Title: "หัวหน้า", EmployeeID: "42", EmployeeName: "ผู้อนุมัติ " + code}},
				})
				break
			}
			fallthrough
		case "/api/leaves/101":
			writeData(response, contracts.Leave{ID: "101", LeaveTypeID: "LT4", LeaveType: "Leave " + code, StartDate: "2030-11-18", EndDate: "2030-11-18", TotalDays: .5, Status: "Pending", StatusLabel: "รออนุมัติ", ReplacementEmployeeID: "42", ReplacementEmployeeName: "ผู้ปฏิบัติงานแทน", VerificationSteps: []contracts.LeaveVerificationStep{
				{ID: "201", Level: 1, Title: "หัวหน้า", EmployeeID: "42", EmployeeName: "ผู้ตรวจสอบ " + code, Status: "Pass", StatusLabel: "เห็นชอบ", ReviewedAt: "2030-11-17T09:30:00+07:00", Comment: "ตรวจสอบแล้ว"},
				{ID: "202", Level: 2, Title: "งานการลา", Role: "leave", Status: "Pending", StatusLabel: "รอตรวจสอบ"},
			}, DaySummary: &contracts.LeaveDaySummary{CalendarDays: 1, TotalDays: .5}})
		default:
			t.Fatalf("unexpected leave upstream %s", request.URL.Path)
		}
		return response.Result(), nil
	})
	for _, tenant := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		for _, route := range []struct {
			method, path string
			body         any
		}{{"GET", "/api/leaves/options", nil}, {"POST", "/api/leaves/preview", gatewayLeaveInput()}, {"POST", "/api/leaves", gatewayLeaveInput()}, {"GET", "/api/leaves/101", nil}} {
			response := r.request(t, route.method, route.path, route.body, tenant.token, false)
			if response.Code != 200 || !strings.Contains(response.Body.String(), tenant.code) || strings.Contains(response.Body.String(), tenant.other) {
				t.Fatalf("wrong leave tenant %d %s", response.Code, response.Body.String())
			}
			if route.path == "/api/leaves/options" {
				var envelope struct {
					Data contracts.LeaveOptions `json:"data"`
				}
				if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Data.Balances) != 1 {
					t.Fatalf("missing leave summary: %s", response.Body.String())
				}
				balance := envelope.Data.Balances[0]
				if strings.Contains(response.Body.String(), `"color"`) {
					t.Fatalf("ERP leave colors must not be forwarded: %s", response.Body.String())
				}
				if balance.EntitlementDays == nil || *balance.EntitlementDays != 10 || balance.UsedDays == nil || *balance.UsedDays != 2.5 || balance.RemainingDays == nil || *balance.RemainingDays != 7.5 {
					t.Fatalf("leave summary changed in transit: %s", response.Body.String())
				}
			}
			if route.path == "/api/leaves/101" {
				var envelope struct {
					Data contracts.Leave `json:"data"`
				}
				if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Data.VerificationSteps) != 2 || envelope.Data.VerificationSteps[0].EmployeeName != "ผู้ตรวจสอบ "+tenant.code || envelope.Data.VerificationSteps[0].Comment != "ตรวจสอบแล้ว" || envelope.Data.VerificationSteps[1].Role != "leave" {
					t.Fatalf("saved verification timeline changed in transit: %s", response.Body.String())
				}
				if envelope.Data.DaySummary == nil || envelope.Data.DaySummary.CalendarDays != 1 || envelope.Data.DaySummary.TotalDays != .5 || envelope.Data.DaySummary.HolidaysDeducted != nil {
					t.Fatalf("saved day summary changed in transit: %s", response.Body.String())
				}
			}
		}
	}
	before := calls
	for _, route := range []struct {
		method, path string
		body         any
		status       int
	}{{"GET", "/api/leaves/options?emp_id=99", nil, 400}, {"POST", "/api/leaves?hospital_code=12448", gatewayLeaveInput(), 400}, {"POST", "/api/leaves", map[string]string{"emp_id": "99"}, 400}, {"POST", "/api/leaves/options", nil, 405}, {"GET", "/api/leaves/preview", nil, 405}, {"GET", "/api/leaves/not-an-id", nil, 404}, {"DELETE", "/api/leaves/101", nil, 405}} {
		if response := r.request(t, route.method, route.path, route.body, ta, false); response.Code != route.status {
			t.Fatalf("invalid route %s %s got %d: %s", route.method, route.path, response.Code, response.Body.String())
		}
	}
	if response := r.request(t, "POST", "/api/leaves", gatewayLeaveInput(), "", false); response.Code != 401 {
		t.Fatal("unauthenticated leave submission was accepted")
	}
	if calls != before {
		t.Fatal("invalid request reached the hospital API")
	}
	r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test"}, "", true)
	if response := r.request(t, "POST", "/api/leaves", gatewayLeaveInput(), ta, false); response.Code != 403 || calls != before {
		t.Fatal("suspended hospital submitted a leave request")
	}
}

func TestLeaveGatewayValidatesOptionalBalanceNumbers(t *testing.T) {
	options := func() contracts.LeaveOptions {
		return contracts.LeaveOptions{
			Types:      []contracts.LeaveType{{ID: "LT4", Name: "ลาพักผ่อน"}},
			Balances:   []contracts.LeaveBalance{{LeaveTypeID: "LT4", LeaveType: "ลาพักผ่อน"}},
			FiscalYear: 2570,
		}
	}
	legacy := options()
	if !validLeaveOutput(&legacy) {
		t.Fatal("legacy options with missing optional summary fields rejected")
	}
	for _, field := range []string{"entitlement", "used", "remaining"} {
		for _, number := range []float64{-0.5, math.NaN(), math.Inf(1), math.Inf(-1)} {
			v := options()
			switch field {
			case "entitlement":
				v.Balances[0].EntitlementDays = &number
			case "used":
				v.Balances[0].UsedDays = &number
			case "remaining":
				v.Balances[0].RemainingDays = &number
			}
			if validLeaveOutput(&v) {
				t.Fatalf("invalid %s days accepted: %v", field, number)
			}
		}
	}
	zero, half := 0.0, 0.5
	valid := options()
	valid.Balances[0].EntitlementDays = &zero
	valid.Balances[0].UsedDays = &half
	valid.Balances[0].RemainingDays = &zero
	if !validLeaveOutput(&valid) {
		t.Fatal("zero entitlement and fractional overuse rejected")
	}
}

func TestLeaveDetailGatewayAcceptsLegacyRowsAndValidatesVerification(t *testing.T) {
	valid := func() contracts.Leave {
		return contracts.Leave{
			ID: "101", StartDate: "2030-11-18", EndDate: "2030-11-18",
			VerificationSteps: []contracts.LeaveVerificationStep{
				{ID: "201", Level: 1, EmployeeID: "42", Status: "Pass"},
				{ID: "202", Level: 1, Status: "75"},
				{ID: "203", Level: 2, Role: "leave", Status: "Pending"},
			},
		}
	}
	for _, steps := range [][]contracts.LeaveVerificationStep{nil, {}, valid().VerificationSteps} {
		v := valid()
		v.VerificationSteps = steps
		if !validLeaveOutput(&v) {
			t.Fatal("legacy detail with missing type/zero days or valid saved steps was rejected")
		}
	}
	for _, change := range []struct {
		name  string
		apply func(*contracts.Leave)
	}{
		{"invalid leave ID", func(v *contracts.Leave) { v.ID = "../101" }},
		{"missing start date", func(v *contracts.Leave) { v.StartDate = "" }},
		{"missing end date", func(v *contracts.Leave) { v.EndDate = "" }},
		{"negative days", func(v *contracts.Leave) { v.TotalDays = -1 }},
		{"nonfinite days", func(v *contracts.Leave) { v.TotalDays = math.Inf(1) }},
		{"NaN days", func(v *contracts.Leave) { v.TotalDays = math.NaN() }},
		{"too many steps", func(v *contracts.Leave) { v.VerificationSteps = make([]contracts.LeaveVerificationStep, 101) }},
		{"invalid step ID", func(v *contracts.Leave) { v.VerificationSteps[0].ID = "bad" }},
		{"invalid employee ID", func(v *contracts.Leave) { v.VerificationSteps[0].EmployeeID = "bad" }},
		{"negative level", func(v *contracts.Leave) { v.VerificationSteps[0].Level = -1 }},
		{"out of order levels", func(v *contracts.Leave) { v.VerificationSteps[2].Level = 0 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			v := valid()
			change.apply(&v)
			if validLeaveOutput(&v) {
				t.Fatal("malformed upstream leave detail was accepted")
			}
		})
	}
}

func TestLeaveGatewaySanitizesUpstreamErrorsAndPreservesFieldKeys(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, test := range []struct {
		status, expected int
		code             string
	}{{403, 403, "READ_ONLY"}, {404, 404, "NOT_FOUND"}, {409, 409, "LEAVE_OVERLAP"}, {409, 409, "REQUEST_ID_CONFLICT"}, {422, 422, "VALIDATION_FAILED"}, {503, 503, "APPROVAL_UNAVAILABLE"}, {500, 503, "PRIVATE_DATABASE_ERROR"}, {403, 503, "GATEWAY_UNAUTHORIZED"}} {
		r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			response := httptest.NewRecorder()
			writeJSON(response, test.status, map[string]any{"error": map[string]any{"code": test.code, "message": "private SQL password", "fields": map[string]string{"end_date": "private query", "private_key": "private secret"}}})
			return response.Result(), nil
		})
		response := r.request(t, "POST", "/api/leaves", gatewayLeaveInput(), token, false)
		if response.Code != test.expected || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("unsafe leave error: %d %s", response.Code, response.Body.String())
		}
		if test.status == 422 && !strings.Contains(response.Body.String(), `"end_date"`) {
			t.Fatal("leave field recovery key was discarded")
		}
	}
}

func TestLeavePreviewGatewayRejectsInconsistentSummaryData(t *testing.T) {
	valid := func() contracts.LeavePreview {
		return contracts.LeavePreview{
			TotalDays: 2.5, Period: "ลาเต็มวัน", FiscalYear: 2570,
			CalendarDays: 6, WeekendDays: 2, HolidayDays: 1, HolidaysDeducted: true,
			Approvals: []contracts.LeaveApproval{
				{Level: 1, Title: "หัวหน้า", EmployeeID: "42", EmployeeName: "ผู้อนุมัติจริง"},
				{Level: 2, Title: "บุคลากร", Role: "leave"},
			},
		}
	}
	good := valid()
	if !validLeaveOutput(&good) {
		t.Fatal("valid server-calculated summary rejected")
	}
	for _, change := range []struct {
		name  string
		apply func(*contracts.LeavePreview)
	}{
		{"negative weekends", func(v *contracts.LeavePreview) { v.WeekendDays = -1 }},
		{"overlapping holiday counts", func(v *contracts.LeavePreview) { v.HolidayDays = 5 }},
		{"total includes excluded holidays", func(v *contracts.LeavePreview) { v.TotalDays = 4 }},
		{"missing approval route", func(v *contracts.LeavePreview) { v.Approvals = nil }},
		{"out of order approvals", func(v *contracts.LeavePreview) { v.Approvals[1].Level = 1 }},
		{"missing approver name", func(v *contracts.LeavePreview) { v.Approvals[0].EmployeeName = "" }},
		{"unsupported role", func(v *contracts.LeavePreview) { v.Approvals[1].Role = "admin" }},
		{"invented role employee", func(v *contracts.LeavePreview) { v.Approvals[1].EmployeeName = "สมมติ" }},
		{"auto approval with unresolved role", func(v *contracts.LeavePreview) { v.AutoApproved = true }},
	} {
		t.Run(change.name, func(t *testing.T) {
			v := valid()
			change.apply(&v)
			if validLeaveOutput(&v) {
				t.Fatal("invalid upstream summary accepted")
			}
		})
	}
	shift := valid()
	shift.HolidaysDeducted, shift.TotalDays = false, 5.5
	if !validLeaveOutput(&shift) {
		t.Fatal("shift employee holiday information should not reduce leave days")
	}
}

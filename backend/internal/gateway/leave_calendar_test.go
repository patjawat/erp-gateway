package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"erp-platform/gateway/internal/contracts"
)

func TestLeaveCalendarGatewayTenantScopeAndStrictValidation(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	malformed := false
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("calendar used wrong tenant credentials")
		}
		w := httptest.NewRecorder()
		if strings.HasSuffix(request.URL.Path, "/day") {
			if request.URL.Query().Get("per_page") != "30" {
				t.Fatal("page bound not forwarded")
			}
			date := "2026-11-02"
			if malformed {
				date = "2026-11-03"
			}
			writeData(w, contracts.LeaveCalendarPage{Date: date, DepartmentID: "3", Department: "Department " + code, Items: []contracts.LeaveCalendarEntry{}, Page: 1, PerPage: 30})
		} else {
			if request.URL.RawQuery != "month=2026-11" {
				t.Fatal(request.URL.RawQuery)
			}
			value := contracts.LeaveCalendar{Month: "2026-11", DepartmentID: "3", Department: "Department " + code, Days: []contracts.LeaveCalendarDay{}}
			for d := 1; d <= 30; d++ {
				value.Days = append(value.Days, contracts.LeaveCalendarDay{Date: time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Holidays: []string{}})
			}
			if malformed {
				value.Days = value.Days[:29]
			}
			writeData(w, value)
		}
		return w.Result(), nil
	})
	for _, tenant := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		for _, path := range []string{"/api/leaves/calendar?month=2026-11", "/api/leaves/calendar/day?date=2026-11-02"} {
			w := r.request(t, http.MethodGet, path, nil, tenant.token, false)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Department "+tenant.code) || strings.Contains(w.Body.String(), tenant.other) || !json.Valid(w.Body.Bytes()) {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	before := calls
	for _, path := range []string{"/api/leaves/calendar?month=2026-11&department_id=1", "/api/leaves/calendar?month=2026-11&employee_id=99", "/api/leaves/calendar?month=2026-11&hospital_code=12448", "/api/leaves/calendar/day?date=2026-11-02&per_page=51", "/api/leaves/calendar?month=2026-11&includes_subdepartments=true", "/api/leaves/calendar/day?date=2026-11-02&scope_key=" + strings.Repeat("a", 64)} {
		if w := r.request(t, http.MethodGet, path, nil, ta, false); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	if w := r.request(t, http.MethodGet, "/api/leaves/calendar?month=2026-11", nil, "", false); w.Code != 401 || calls != before {
		t.Fatal("unauthenticated/invalid query reached local API")
	}
	malformed = true
	for _, path := range []string{"/api/leaves/calendar?month=2026-11", "/api/leaves/calendar/day?date=2026-11-02"} {
		if w := r.request(t, http.MethodGet, path, nil, ta, false); w.Code != 503 {
			t.Fatal("invalid upstream accepted", w.Code)
		}
	}
}

func TestLeaveCalendarGatewayPreservesVerifiedScopeAndDepartmentMetadata(t *testing.T) {
	r := rig(t)
	hospital := r.add(t, "11447")
	r.enable(t, hospital)
	token := r.login(t, hospital.Code)
	key := strings.Repeat("a", 64)
	missingKey, badDepartment := false, false
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		scopeKey := key
		if missingKey {
			scopeKey = ""
		}
		if strings.HasSuffix(request.URL.Path, "/day") {
			department := "หน่วยงานย่อย"
			if badDepartment {
				department = ""
			}
			writeData(w, contracts.LeaveCalendarPage{Date: "2026-11-02", DepartmentID: "3", Department: "แผนกหลัก", ScopeKey: scopeKey, IncludesSubdepartments: true, Items: []contracts.LeaveCalendarEntry{{ID: "81", EmployeeID: "17", EmployeeName: "บุคลากรทดสอบ", DepartmentID: "12", Department: department, StartDate: "2026-11-02", EndDate: "2026-11-02", Period: "ลาเต็มวัน", Status: "Approve", Approved: true}}, Page: 1, PerPage: 30, TotalCount: 1})
		} else {
			value := contracts.LeaveCalendar{Month: "2026-11", DepartmentID: "3", Department: "แผนกหลัก", ScopeKey: scopeKey, IncludesSubdepartments: true, Days: []contracts.LeaveCalendarDay{}}
			for d := 1; d <= 30; d++ {
				value.Days = append(value.Days, contracts.LeaveCalendarDay{Date: time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Holidays: []string{}})
			}
			writeData(w, value)
		}
		return w.Result(), nil
	})
	month := r.request(t, http.MethodGet, "/api/leaves/calendar?month=2026-11", nil, token, false)
	var m struct {
		Data contracts.LeaveCalendar `json:"data"`
	}
	if month.Code != 200 || json.Unmarshal(month.Body.Bytes(), &m) != nil || m.Data.ScopeKey != key || !m.Data.IncludesSubdepartments {
		t.Fatal("verified month scope was lost", month.Code, month.Body.String())
	}
	day := r.request(t, http.MethodGet, "/api/leaves/calendar/day?date=2026-11-02", nil, token, false)
	var p struct {
		Data contracts.LeaveCalendarPage `json:"data"`
	}
	if day.Code != 200 || json.Unmarshal(day.Body.Bytes(), &p) != nil || p.Data.ScopeKey != key || !p.Data.IncludesSubdepartments || p.Data.Items[0].DepartmentID != "12" || p.Data.Items[0].Department != "หน่วยงานย่อย" {
		t.Fatal("verified child-unit metadata was lost", day.Code, day.Body.String())
	}
	missingKey = true
	for _, path := range []string{"/api/leaves/calendar?month=2026-11", "/api/leaves/calendar/day?date=2026-11-02"} {
		if w := r.request(t, http.MethodGet, path, nil, token, false); w.Code != 503 {
			t.Fatal("unversioned expanded scope accepted", w.Code)
		}
	}
	missingKey = false
	badDepartment = true
	if w := r.request(t, http.MethodGet, "/api/leaves/calendar/day?date=2026-11-02", nil, token, false); w.Code != 503 {
		t.Fatal("unpaired department metadata accepted", w.Code)
	}
}

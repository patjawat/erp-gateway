package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestLeaveCalendarGatewayGroupsAndExpansionPreserveTenantAndMetadata(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	calls, malformed := 0, ""
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("grouped calendar used wrong tenant credentials")
		}
		w := httptest.NewRecorder()
		if strings.HasSuffix(request.URL.Path, "/departments") {
			if request.URL.RawQuery != "date=2026-11-02&page=1&per_page=30" {
				t.Fatal("summary selection not forwarded canonically", request.URL.RawQuery)
			}
			v := contracts.LeaveCalendarDepartmentsPage{Date: "2026-11-02", DepartmentID: "3", Department: "Department " + code, ScopeKey: strings.Repeat("a", 64), IncludesSubdepartments: true, TotalPeople: 3, TotalCount: 2, Page: 1, PerPage: 30,
				Items: []contracts.LeaveCalendarDepartment{{DepartmentID: "3", Department: "Own unit", TotalPeople: 2, TotalRecords: 3}, {DepartmentID: "12", Department: "Controlled unit", TotalPeople: 1, TotalRecords: 1, CanViewDetails: true}}}
			switch malformed {
			case "scope":
				v.ScopeKey = ""
			case "counts":
				v.Items[0].TotalRecords = 1
			case "duplicate":
				v.Items[1].DepartmentID = "3"
			case "page":
				v.HasMore = true
			}
			writeData(w, v)
		} else {
			if request.URL.RawQuery != "date=2026-11-02&department_id=12&page=1&per_page=30" {
				t.Fatal("expansion selection not forwarded canonically", request.URL.RawQuery)
			}
			v := contracts.LeaveCalendarPage{Date: "2026-11-02", DepartmentID: "3", Department: "Department " + code, ScopeKey: strings.Repeat("a", 64), IncludesSubdepartments: true, SelectedDepartmentID: "12", TotalCount: 1, Page: 1, PerPage: 30,
				Items: []contracts.LeaveCalendarEntry{{ID: "81", EmployeeID: "17", EmployeeName: "บุคลากรทดสอบ", DepartmentID: "12", Department: "Controlled unit", LeaveTypeID: "LT4", LeaveType: "ลาพักผ่อน", StartDate: "2026-11-02", EndDate: "2026-11-02", Period: "ลาเต็มวัน", Status: "Approve", Approved: true}}}
			switch malformed {
			case "echo":
				v.SelectedDepartmentID = "13"
			case "row":
				v.Items[0].DepartmentID = "13"
			}
			writeData(w, v)
		}
		return w.Result(), nil
	})
	groupsPath := "/api/leaves/calendar/departments?date=2026-11-02"
	dayPath := "/api/leaves/calendar/day?date=2026-11-02&department_id=12"
	for _, tenant := range []struct{ token, code, other string }{{ta, a.Code, b.Code}, {tb, b.Code, a.Code}} {
		w := r.request(t, http.MethodGet, groupsPath, nil, tenant.token, false)
		var groups struct {
			Data contracts.LeaveCalendarDepartmentsPage `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &groups) != nil || groups.Data.TotalPeople != 3 || groups.Data.TotalCount != 2 || len(groups.Data.Items) != 2 || groups.Data.Items[0].CanViewDetails || !groups.Data.Items[1].CanViewDetails || !strings.Contains(w.Body.String(), "Department "+tenant.code) || strings.Contains(w.Body.String(), tenant.other) {
			t.Fatal("verified group aggregates/capabilities lost", w.Code, w.Body.String())
		}
		w = r.request(t, http.MethodGet, dayPath, nil, tenant.token, false)
		var day struct {
			Data contracts.LeaveCalendarPage `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &day) != nil || day.Data.SelectedDepartmentID != "12" || len(day.Data.Items) != 1 || day.Data.Items[0].LeaveType != "ลาพักผ่อน" || day.Data.Items[0].LeaveTypeID != "LT4" {
			t.Fatal("verified expansion type/selector lost", w.Code, w.Body.String())
		}
	}
	before := calls
	for _, path := range []string{groupsPath + "&department_id=12", groupsPath + "&employee_id=17", groupsPath + "&hospital_code=12448", groupsPath + "&per_page=51", dayPath + "&scope_key=abc", dayPath + "&department_id=13"} {
		if w := r.request(t, http.MethodGet, path, nil, ta, false); w.Code != 400 {
			t.Fatal("unsafe grouped selection accepted", path, w.Code)
		}
	}
	if w := r.request(t, http.MethodGet, groupsPath, nil, "", false); w.Code != 401 || calls != before {
		t.Fatal("unauthenticated/invalid card request reached upstream")
	}
	for _, broken := range []string{"scope", "counts", "duplicate", "page"} {
		malformed = broken
		if w := r.request(t, http.MethodGet, groupsPath, nil, ta, false); w.Code != 503 {
			t.Fatal("malformed summary accepted", broken, w.Code)
		}
	}
	for _, broken := range []string{"echo", "row"} {
		malformed = broken
		if w := r.request(t, http.MethodGet, dayPath, nil, ta, false); w.Code != 503 {
			t.Fatal("wrong department expansion accepted", broken, w.Code)
		}
	}
}

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func approvalGatewayFixture(code string) contracts.LeaveApprovalDetail {
	return contracts.LeaveApprovalDetail{
		LeaveApprovalItem: contracts.LeaveApprovalItem{ID: "101", ApprovalID: "201", RequesterName: "ผู้ขอลา " + code, Department: "หน่วยงาน", LeaveType: "ลาพักผ่อน", StartDate: "2026-11-24", EndDate: "2026-11-25", TotalDays: 2, Status: "Pending", StatusLabel: "รอพิจารณา", ApprovalStatus: "Pending", Level: 1, Title: "หัวหน้า", Label: "เห็นชอบ", Revision: strings.Repeat("a", 64), CanDecide: true},
		Leave:             contracts.Leave{ID: "101", LeaveType: "ลาพักผ่อน", StartDate: "2026-11-24", EndDate: "2026-11-25", TotalDays: 2, Status: "Pending", Reason: "พักผ่อน", VerificationSteps: []contracts.LeaveVerificationStep{}},
	}
}

func TestLeaveApprovalGatewayUsesOnlySessionTenantAndReviewedStep(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	tokens := []string{r.login(t, a.Code), r.login(t, b.Code)}
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || req.Header.Get("Cookie") != "" {
			t.Fatal("approval crossed tenant credentials")
		}
		w := httptest.NewRecorder()
		item := approvalGatewayFixture(code)
		switch req.URL.Path {
		case "/api/leaves/approvals":
			if req.URL.RawQuery != "page=1" {
				t.Fatal("gateway did not bound the first page")
			}
			writeData(w, contracts.LeaveApprovalList{Items: []contracts.LeaveApprovalItem{item.LeaveApprovalItem}, Page: 1, PerPage: 20, TotalCount: 1})
		case "/api/leaves/approvals/summary":
			writeData(w, contracts.LeaveApprovalSummary{PendingCount: 1, WriteEnabled: true})
		case "/api/leaves/approvals/101":
			writeData(w, item)
		case "/api/leaves/approvals/101/decision":
			var input contracts.LeaveApprovalDecision
			if req.Method != "POST" || json.NewDecoder(req.Body).Decode(&input) != nil || input.ApprovalID != "201" || input.Revision != strings.Repeat("a", 64) || input.Decision != "approve" {
				t.Fatal("reviewed step changed in transit")
			}
			item.ApprovalStatus, item.CanDecide = "Pass", false
			item.Status, item.Leave.Status = "Checking1_pass", "Checking1_pass"
			writeData(w, item)
		case "/api/leaves/approvals/101/attachments/" + strings.Repeat("a", 32):
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4\nfixture\n%%EOF"))
		default:
			t.Fatalf("unexpected approval route %s", req.URL.Path)
		}
		return w.Result(), nil
	})
	for _, token := range tokens {
		for _, path := range []string{"/api/leaves/approvals", "/api/leaves/approvals/summary", "/api/leaves/approvals/101", "/api/leaves/approvals/101/attachments/" + strings.Repeat("a", 32)} {
			response := r.request(t, "GET", path, nil, token, false)
			if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store, private" {
				t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
			}
		}
		response := r.request(t, "POST", "/api/leaves/approvals/101/decision", contracts.LeaveApprovalDecision{ApprovalID: "201", Decision: "approve", Revision: strings.Repeat("a", 64)}, token, false)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"approval_status":"Pass"`) {
			t.Fatalf("approval response: %d %s", response.Code, response.Body.String())
		}
	}
	before := calls
	for _, test := range []struct {
		method, path string
		body         any
		status       int
	}{
		{"GET", "/api/leaves/approvals?employee_id=42", nil, 400},
		{"GET", "/api/leaves/approvals?page=1&hospital=other", nil, 400},
		{"GET", "/api/leaves/approvals/101?user_id=99", nil, 400},
		{"POST", "/api/leaves/approvals/101", nil, 405},
		{"GET", "/api/leaves/approvals/101/decision", nil, 405},
		{"GET", "/api/leaves/approvals/not-an-id", nil, 404},
		{"POST", "/api/leaves/approvals/101/decision", map[string]any{"approval_id": "201", "decision": "approve", "revision": strings.Repeat("a", 64), "employee_id": "99"}, 400},
		{"POST", "/api/leaves/approvals/101/decision", contracts.LeaveApprovalDecision{ApprovalID: "201", Decision: "reject", Revision: strings.Repeat("a", 64)}, 400},
	} {
		response := r.request(t, test.method, test.path, test.body, tokens[0], false)
		if response.Code != test.status {
			t.Fatalf("%s %s: got %d want %d %s", test.method, test.path, response.Code, test.status, response.Body.String())
		}
	}
	if calls != before {
		t.Fatal("invalid authority selectors reached Local API")
	}
}

func TestLeaveApprovalGatewayRejectsMalformedUpstreamAndSanitizesConflict(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, change := range []func(*contracts.LeaveApprovalDetail){
		func(v *contracts.LeaveApprovalDetail) { v.ID = "102" },
		func(v *contracts.LeaveApprovalDetail) { v.Revision = "" },
		func(v *contracts.LeaveApprovalDetail) { v.Leave.ID = "102" },
		func(v *contracts.LeaveApprovalDetail) { v.Leave.Status = "Reject" },
		func(v *contracts.LeaveApprovalDetail) { v.TotalDays = 9 },
	} {
		r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			item := approvalGatewayFixture(h.Code)
			change(&item)
			w := httptest.NewRecorder()
			writeData(w, item)
			return w.Result(), nil
		})
		if response := r.request(t, "GET", "/api/leaves/approvals/101", nil, token, false); response.Code != 503 {
			t.Fatalf("malformed upstream accepted: %d %s", response.Code, response.Body.String())
		}
	}
	for _, test := range []struct {
		status, want int
		code         string
	}{{409, 409, "LEAVE_APPROVAL_CHANGED"}, {403, 403, "LEAVE_APPROVAL_DISABLED"}, {403, 503, "GATEWAY_UNAUTHORIZED"}, {500, 503, "PRIVATE_DATABASE_ERROR"}} {
		r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			writeJSON(w, test.status, map[string]any{"error": map[string]string{"code": test.code, "message": "private SQL password"}})
			return w.Result(), nil
		})
		response := r.request(t, "POST", "/api/leaves/approvals/101/decision", contracts.LeaveApprovalDecision{ApprovalID: "201", Decision: "approve", Revision: strings.Repeat("a", 64)}, token, false)
		if response.Code != test.want || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("unsafe approval failure: %d %s", response.Code, response.Body.String())
		}
	}
}

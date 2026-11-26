package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentWorkflowGatewayScopeAndGuardrails(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	previous := r.app.client.Transport
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/comments") {
			return previous.RoundTrip(req)
		}
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] {
			t.Fatal("tenant token mismatch")
		}
		var payload any = contracts.DocumentComments{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentComment{{ID: "5", Text: code, Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}}}
		if strings.HasSuffix(req.URL.Path, "/comments/5") {
			payload = contracts.DocumentComment{ID: "5", Text: code, Revision: strings.Repeat("a", 64), Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}
		}
		status := 200
		if req.Method != "GET" {
			status = 503
			payload = map[string]any{"error": map[string]string{"code": "WORKFLOW_NOT_READY", "message": "private backend detail"}}
		} else {
			payload = map[string]any{"data": payload}
		}
		raw, _ := json.Marshal(payload)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, tenant := range []struct{ token, code string }{{ta, a.Code}, {tb, b.Code}} {
		w := r.request(t, "GET", "/api/documents/1/comments", nil, tenant.token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tenant.code) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := r.request(t, "GET", "/api/documents/1/comments", nil, "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := r.request(t, "GET", "/api/documents/1/comment-options?emp_id=99", nil, ta, false); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := r.request(t, "POST", "/api/documents/1/comments", contracts.DocumentCommentInput{Text: "test", RequestID: "request-fixture-001"}, ta, false); w.Code != 503 || strings.Contains(w.Body.String(), "private backend") {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	if w := r.request(t, "GET", "/api/documents/1/comments/5", nil, ta, false); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestDocumentWorkflowGatewayForwardsDurableDelete(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	previous := r.app.client.Transport
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/documents/1/comments/5" {
			return previous.RoundTrip(req)
		}
		var in contracts.DocumentCommentInput
		if e := json.NewDecoder(req.Body).Decode(&in); e != nil || in.OperationVersion != 1 || in.RequestID != "durable-delete-fixture" || in.Revision != strings.Repeat("a", 64) {
			t.Fatal(in, e)
		}
		raw, _ := json.Marshal(map[string]any{"data": contracts.DocumentCommentResult{ID: "5", Deleted: true, NotificationStatus: "not_requested"}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	in := contracts.DocumentCommentInput{RequestID: "durable-delete-fixture", Revision: strings.Repeat("a", 64), OperationVersion: 1}
	w := r.request(t, "DELETE", "/api/documents/1/comments/5", in, token, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestDocumentWorkflowGatewayRejectsForgedPermissions(t *testing.T) {
	v := &contracts.DocumentComments{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentComment{{ID: "1", IsOwn: false, CanEdit: true, Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}}}
	if validDocumentWorkflow(v) {
		t.Fatal("foreign edit flag accepted")
	}
	if validDocumentWorkflow(&contracts.DocumentCommentResult{ID: "1", NotificationStatus: "sent"}) {
		t.Fatal("invented sent state")
	}
}

func TestDocumentWorkflowGatewayForwardsBoundedPages(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	previous := r.app.client.Transport
	calls := 0
	wrongPage := false
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/documents/1/") {
			return previous.RoundTrip(req)
		}
		calls++
		if req.URL.Query().Get("page") != "2" || req.URL.Query().Get("per_page") != "20" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] {
			t.Fatal(req.URL)
		}
		page := 2
		if wrongPage {
			page = 1
		}
		var data any = contracts.DocumentComments{Page: page, PerPage: 20, TotalCount: 21, HasMore: page*20 < 21, Items: []contracts.DocumentComment{{ID: "21", Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}}}
		if strings.HasSuffix(req.URL.Path, "/timeline") {
			data = contracts.DocumentTimeline{Page: page, PerPage: 20, TotalCount: 21, HasMore: page*20 < 21, Items: []contracts.DocumentTimelineItem{{ID: "21", Type: "comment"}}}
		}
		raw, _ := json.Marshal(map[string]any{"data": data})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, kind := range []string{"comments", "timeline"} {
		w := r.request(t, "GET", "/api/documents/1/"+kind+"?page=2&per_page=20", nil, token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"total_count":21`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, bad := range []string{"page=0", "per_page=1000", "page=1&page=2", "emp_id=3"} {
		w := r.request(t, "GET", "/api/documents/1/comments?"+bad, nil, token, false)
		if w.Code != 400 {
			t.Fatal(w.Code, bad)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	wrongPage = true
	if w := r.request(t, "GET", "/api/documents/1/comments?page=2&per_page=20", nil, token, false); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

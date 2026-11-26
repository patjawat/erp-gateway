package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentNativeGatewayTenantPaginationAndMutationGuards(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	previous := r.app.client.Transport
	calls := 0
	wrongPage := false
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/documents/1/") {
			return previous.RoundTrip(req)
		}
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] {
			t.Fatal("tenant token mismatch")
		}
		var v any
		switch req.Method {
		case "GET":
			if req.URL.Query().Get("page") != "2" || req.URL.Query().Get("per_page") != "20" {
				t.Fatal(req.URL)
			}
			page := 2
			if wrongPage {
				page = 1
			}
			v = contracts.DocumentNativePage{Page: page, PerPage: 20, TotalCount: 21, HasMore: page == 1, Items: []contracts.DocumentNativeItem{{ID: "21", Name: code}}}
		case "DELETE":
			var in contracts.DocumentNativeInput
			if e := json.NewDecoder(req.Body).Decode(&in); e != nil || len(in.Revision) != 64 {
				t.Fatal(in, e)
			}
			v = contracts.DocumentNativeResult{ID: "21", Deleted: true, NotificationStatus: "not_requested"}
		}
		raw, _ := json.Marshal(map[string]any{"data": v})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, tenant := range []struct{ token, code string }{{ta, a.Code}, {tb, b.Code}} {
		w := r.request(t, "GET", "/api/documents/1/templates?page=2&per_page=20", nil, tenant.token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tenant.code) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := r.request(t, "GET", "/api/documents/1/templates?page=2&per_page=20", nil, "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, q := range []string{"employee_id=9", "hospital=9", "page=0", "per_page=1000", "page=1&page=2"} {
		if w := r.request(t, "GET", "/api/documents/1/templates?"+q, nil, ta, false); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if w := r.request(t, "DELETE", "/api/documents/1/templates/21", contracts.DocumentNativeInput{RequestID: "native-delete-001"}, ta, false); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	if w := r.request(t, "DELETE", "/api/documents/1/templates/21", contracts.DocumentNativeInput{RequestID: "native-delete-001", Revision: strings.Repeat("a", 64)}, ta, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	wrongPage = true
	if w := r.request(t, "GET", "/api/documents/1/templates?page=2&per_page=20", nil, ta, false); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestDocumentNativeGatewayRejectsForgedCapabilitiesAndPrimary(t *testing.T) {
	if validDocumentNative(&contracts.DocumentWorkspace{CanRequestApproval: true}, contracts.DocumentNativeRoute{}) {
		t.Fatal("approval without writer accepted")
	}
	if validDocumentNative(&contracts.DocumentNativeResult{ID: "1", NotificationStatus: "sent"}, contracts.DocumentNativeRoute{}) {
		t.Fatal("invented notification accepted")
	}
	d := &contracts.DocumentDetail{Document: contracts.Document{ID: "1", RouteID: "2"}, Attachments: []contracts.DocumentAttachment{}, PrimaryAttachment: &contracts.DocumentAttachment{ID: "../../private", MimeType: "application/pdf", IsPrimary: true}}
	if validDocuments(d) {
		t.Fatal("unvalidated primary metadata accepted")
	}
}

package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentForwardingHistoryGatewayKeepsBoundedSelectionAndRevision(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	previous := r.app.client.Transport
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/documents/101/forwarding-history" {
			return previous.RoundTrip(req)
		}
		calls++
		if req.Method != "GET" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || req.URL.Query().Get("page") != "2" || req.URL.Query().Get("per_page") != "20" || req.URL.Query().Get("q") != "Staff" {
			t.Fatal("forwarding selection or tenant scope lost", req.URL)
		}
		data := contracts.DocumentNativePage{
			Page: 2, PerPage: 20, TotalCount: 21,
			Items: []contracts.DocumentNativeItem{{ID: "5", Name: "Staff", Kind: "employee", CanDelete: true, Revision: strings.Repeat("a", 64)}},
		}
		raw, _ := json.Marshal(map[string]any{"data": data})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	w := r.request(t, "GET", "/api/documents/101/forwarding-history?page=2&per_page=20&q=Staff", nil, token, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total_count":21`) || !strings.Contains(w.Body.String(), `"can_delete":true`) || !strings.Contains(w.Body.String(), strings.Repeat("a", 64)) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.request(t, "DELETE", "/api/documents/101/forwarding-history/5", nil, token, false); w.Code != 400 {
		t.Fatal("history unexpectedly became a mutation endpoint", w.Code)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestDocumentTaskSuggestionsCannotAppearOnUnrelatedCollections(t *testing.T) {
	page := &contracts.DocumentNativePage{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentNativeItem{{ID: "10", Kind: "unit", IsSuggested: true}}}
	route := contracts.DocumentNativeRoute{Kind: "task-options", Selection: contracts.DocumentNativeSelection{Page: 1, PerPage: 20}}
	if !validDocumentNative(page, route) {
		t.Fatal("explicit task unit suggestion rejected")
	}
	route.Kind = "templates"
	if validDocumentNative(page, route) {
		t.Fatal("suggestion on unrelated collection accepted")
	}
	route.Kind = "task-options"
	page.Items[0].Kind = "employee"
	if validDocumentNative(page, route) {
		t.Fatal("individual employee invented as a suggested unit")
	}
}

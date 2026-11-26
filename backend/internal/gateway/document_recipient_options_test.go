package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentRecipientMetadataIsOptionalAndBounded(t *testing.T) {
	for _, text := range []string{"", "ตัวอย่าง", strings.Repeat("x", 2000)} {
		options := &contracts.DocumentCommentOptions{Page: 1, Items: []contracts.DocumentRecipient{{ID: "2", Name: "Full name", GivenName: text, PositionName: text}}, SuggestedEmployeeIDs: []string{}}
		if !validDocumentWorkflow(options) {
			t.Fatal("valid optional metadata rejected", len(text))
		}
	}
	for _, item := range []contracts.DocumentRecipient{
		{ID: "2", GivenName: strings.Repeat("x", 2001)},
		{ID: "2", PositionName: strings.Repeat("x", 2001)},
	} {
		if validDocumentRecipients([]contracts.DocumentRecipient{item}) {
			t.Fatal("unbounded employee metadata accepted")
		}
	}
	raw, err := json.Marshal(contracts.DocumentRecipient{ID: "2", Name: "Legacy"})
	if err != nil || string(raw) != `{"id":"2","name":"Legacy"}` {
		t.Fatal("legacy contract changed", err)
	}
}

func TestDocumentRecipientMetadataGatewayRelaysBoundedPublicPage(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	previous := r.app.client.Transport
	oversized := false
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/documents/1/comment-options" {
			return previous.RoundTrip(req)
		}
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || req.URL.Query().Get("page") != "1" || req.URL.Query().Get("kind") != "employee" {
			t.Fatal("employee options lost tenant scope or bounded selection")
		}
		position := "Role"
		if oversized {
			position = strings.Repeat("x", 2001)
		}
		data := map[string]any{"page": 1, "items": []map[string]any{{"id": "2", "name": "Full", "given_name": "Given", "position_name": position, "data_json": map[string]string{"private": "SECRET"}}}, "suggested_employee_ids": []string{}}
		raw, _ := json.Marshal(map[string]any{"data": data})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	w := r.request(t, "GET", "/api/documents/1/comment-options?page=1&kind=employee", nil, token, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "data_json") || !strings.Contains(w.Body.String(), `"position_name":"Role"`) || !strings.Contains(w.Body.String(), `"given_name":"Given"`) {
		t.Fatal("gateway public metadata relay failed", w.Code)
	}
	oversized = true
	if w = r.request(t, "GET", "/api/documents/1/comment-options?page=1&kind=employee", nil, token, false); w.Code != 503 {
		t.Fatal("gateway accepted oversized position metadata", w.Code)
	}
}

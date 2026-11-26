package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentsGatewayTenantAndValidation(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	previous := r.app.client.Transport
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/documents") {
			return previous.RoundTrip(req)
		}
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) {
			t.Fatal("wrong credentials")
		}
		v := contracts.DocumentHistory{Items: []contracts.Document{{ID: "1", RouteID: "2", Title: code, Bookmark: code == a.Code}}, Page: 1, PerPage: 20, TotalCount: 1, Summary: contracts.DocumentSummary{All: 1, Unread: 1}}
		raw, _ := json.Marshal(map[string]any{"data": v})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, v := range []struct{ token, code string }{{ta, a.Code}, {tb, b.Code}} {
		w := r.request(t, "GET", "/api/documents", nil, v.token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), v.code) {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Data contracts.DocumentHistory `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data.Items) != 1 || response.Data.Items[0].Bookmark != (v.code == a.Code) {
			t.Fatal("gateway dropped or crossed tenant bookmark state")
		}
	}
	for _, path := range []string{"/api/documents?employee=3", "/api/documents?hospital_code=12448", "/api/documents/1?employee=3"} {
		if w := r.request(t, "GET", path, nil, ta, false); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/documents", nil, "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := r.request(t, "GET", "/api/documents/1/read", nil, ta, false); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if calls != 2 {
		t.Fatal("invalid requests reached upstream", calls)
	}
	r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "fixture"}, "", true)
	if w := r.request(t, "GET", "/api/documents", nil, ta, false); w.Code != 403 || calls != 2 {
		t.Fatal("suspension not enforced")
	}
}
func TestDocumentsGatewayMalformedAndFileBounds(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	payload := "<html>private debug</html>"
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.URL.Path, "/api/documents") {
			return previous.RoundTrip(req)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})
	if w := r.request(t, "GET", "/api/documents", nil, token, false); w.Code != 503 || strings.Contains(w.Body.String(), "private debug") {
		t.Fatal("malformed leak", w.Code)
	}
	for _, p := range []string{"<html>bad</html>", "%PDF-1.7\n" + strings.Repeat("x", contracts.MaxDocumentBytes)} {
		payload = p
		if w := r.request(t, "GET", "/api/documents/1/attachments/2", nil, token, false); w.Code != 503 {
			t.Fatal("bad binary", w.Code)
		}
	}
	payload = "%PDF-1.7\nfixture"
	w := r.request(t, "GET", "/api/documents/1/attachments/2", nil, token, false)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || w.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("valid PDF", w.Code, w.Header())
	}
}

func TestDocumentReceiptDateSurvivesGateway(t *testing.T) {
	r := rig(t)
	hospital := r.add(t, "11447")
	r.enable(t, hospital)
	token := r.login(t, hospital.Code)
	previous := r.app.client.Transport
	receipt := "2026-11-20"
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/documents/1" {
			return previous.RoundTrip(req)
		}
		value := contracts.DocumentDetail{
			Document:     contracts.Document{ID: "1", RouteID: "2", Date: "2026-11-17"},
			ReceivedDate: receipt,
			Attachments:  []contracts.DocumentAttachment{},
		}
		raw, _ := json.Marshal(map[string]any{"data": value})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, receipt = range []string{"2026-11-20", ""} {
		response := r.request(t, "GET", "/api/documents/1", nil, token, false)
		var payload struct {
			Data contracts.DocumentDetail `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Data.ReceivedDate != receipt || payload.Data.Date != "2026-11-17" {
			t.Fatalf("receipt/book dates were dropped or substituted: %d %s", response.Code, response.Body.String())
		}
	}
}

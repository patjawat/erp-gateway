package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleDestinationGatewayValidatesTenantPayloadAndCatalogue(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.Path != "/api/vehicles/destinations" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("destination request escaped tenant session")
		}
		w := httptest.NewRecorder()
		if request.Method == http.MethodPost {
			var input contracts.VehicleDestinationInput
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.Name != "New" {
				t.Fatalf("untrimmed/wrong input %#v", input)
			}
			writeData(w, map[string]any{"id": "mobile-1", "name": "New", "private": "secret"})
		} else {
			if request.URL.Query().Get("q") != "New" {
				t.Fatalf("wrong query %s", request.URL.RawQuery)
			}
			writeData(w, map[string]any{"items": []any{map[string]any{"id": "mobile-1", "name": "New", "private": "secret"}}, "can_write": true, "private": "secret"})
		}
		return w.Result(), nil
	})
	for _, item := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/vehicles/destinations?q=%20New%20", ""},
		{http.MethodPost, "/api/vehicles/destinations", `{"name":" New "}`},
	} {
		got := r.request(t, item.method, item.path, json.RawMessage(item.body), token, false)
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"name":"New"`) || strings.Contains(got.Body.String(), "secret") || strings.Contains(got.Body.String(), "private") {
			t.Fatalf("unsafe response %d %s", got.Code, got.Body.String())
		}
	}
	before := calls
	for _, item := range []struct {
		method, path, body, token string
		status                    int
	}{
		{http.MethodGet, "/api/vehicles/destinations", "", "", 401},
		{http.MethodPost, "/api/vehicles/destinations", `{"name":"New"}`, "", 401},
		{http.MethodPut, "/api/vehicles/destinations", "", token, 405},
		{http.MethodGet, "/api/vehicles/destinations?q=a&q=b", "", token, 400},
		{http.MethodPost, "/api/vehicles/destinations?q=other", `{"name":"New"}`, token, 400},
		{http.MethodPost, "/api/vehicles/destinations", `{"name":" ","emp_id":"99"}`, token, 400},
		{http.MethodPost, "/api/vehicles/destinations", `{"name":" "}`, token, 422},
	} {
		got := r.request(t, item.method, item.path, json.RawMessage(item.body), item.token, false)
		if got.Code != item.status {
			t.Fatalf("validation %s %s %d %s", item.method, item.path, got.Code, got.Body.String())
		}
	}
	if calls != before {
		t.Fatal("invalid destination request reached upstream")
	}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeData(w, map[string]any{"items": []any{map[string]any{"id": "1", "name": ""}}, "can_write": true})
		return w.Result(), nil
	})
	if got := r.request(t, http.MethodGet, "/api/vehicles/destinations?q=New", nil, token, false); got.Code != 503 {
		t.Fatalf("malformed upstream catalogue accepted %d", got.Code)
	}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		fail(w, 403, "READ_ONLY", "SQL secrets")
		return w.Result(), nil
	})
	if got := r.request(t, http.MethodPost, "/api/vehicles/destinations", map[string]string{"name": "New"}, token, false); got.Code != 403 || strings.Contains(got.Body.String(), "secrets") || !strings.Contains(got.Body.String(), "READ_ONLY") {
		t.Fatalf("read-only error not preserved safely %d %s", got.Code, got.Body.String())
	}
}

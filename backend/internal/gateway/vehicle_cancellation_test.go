package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleCancellationGatewayUsesTenantSessionAndRejectsAuthorityFields(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		if request.Method != "POST" || request.URL.Host != "11447.example.invalid" || request.URL.Path != "/api/vehicles/1/cancel" || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" || string(body) != "{}" {
			t.Fatalf("unscoped cancellation: %s %s body=%s", request.Method, request.URL, string(body))
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleBooking{ID: "1", Status: "cancelled", RequesterID: "17"})
		return w.Result(), nil
	})
	for _, test := range []struct {
		method, path, token string
		body                any
		status              int
	}{
		{"POST", "/api/vehicles/1/cancel", "", map[string]any{}, 401},
		{"GET", "/api/vehicles/1/cancel", token, nil, 405},
		{"POST", "/api/vehicles/1/cancel", token, map[string]string{"employee_id": "17"}, 400},
		{"POST", "/api/vehicles/1/cancel", token, map[string]string{"status": "cancelled"}, 400},
		{"POST", "/api/vehicles/1/cancel?employee_id=17", token, map[string]any{}, 400},
		{"POST", "/api/vehicles/1/cancel", token, json.RawMessage("null"), 400},
	} {
		got := r.request(t, test.method, test.path, test.body, test.token, false)
		if got.Code != test.status {
			t.Fatalf("%s %s => %d %s", test.method, test.path, got.Code, got.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("invalid request reached upstream %d times", calls)
	}
	got := r.request(t, "POST", "/api/vehicles/1/cancel", map[string]any{}, token, false)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"can_cancel":false`) || calls != 1 {
		t.Fatalf("valid cancellation %d %s calls=%d", got.Code, got.Body.String(), calls)
	}
}

func TestVehicleCancellationGatewayRejectsUnexpectedOutputAndRelaysConflictSafely(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, booking := range []contracts.VehicleBooking{
		{ID: "2", Status: "cancelled"}, {ID: "1", Status: "pending"}, {ID: "1", Status: "cancelled", CanCancel: true},
		{ID: "1", Status: "cancelled", VehicleImageURL: "https://untrusted.invalid/photo"},
		{ID: "1", Status: "cancelled", VehicleInput: contracts.VehicleInput{Travelers: []contracts.VehicleTraveler{{Name: "unbounded legacy list"}}}},
	} {
		r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			writeData(w, booking)
			return w.Result(), nil
		})
		if got := r.request(t, "POST", "/api/vehicles/1/cancel", map[string]any{}, token, false); got.Code != 503 {
			t.Fatalf("accepted untrusted cancellation %#v: %d %s", booking, got.Code, got.Body.String())
		}
	}
	r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "VEHICLE_CANCELLATION_CHANGED", "message": "private database contents"}})
		return w.Result(), nil
	})
	got := r.request(t, "POST", "/api/vehicles/1/cancel", map[string]any{}, token, false)
	if got.Code != 409 || !strings.Contains(got.Body.String(), "VEHICLE_CANCELLATION_CHANGED") || strings.Contains(got.Body.String(), "private") {
		t.Fatalf("unsafe conflict %d %s", got.Code, got.Body.String())
	}
}

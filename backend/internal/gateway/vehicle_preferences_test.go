package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleGatewayForwardsPreferenceIDsAndCanonicalSnapshots(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	input := contracts.VehicleInput{RequestID: "vehicle-preference-gateway-01", Location: "ปลายทาง", Reason: "ประชุม", Phone: "1234", StartDate: "2099-01-01", EndDate: "2099-01-01", StartTime: "09:00", EndTime: "10:00", Passengers: 1, Travelers: []contracts.VehicleTraveler{{EmployeeID: "17", Name: "ผู้ขอ"}}, RequestedVehicleID: "1", RequestedDriverID: "18"}
	raw, _ := json.Marshal(input)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.Path != "/api/vehicles" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] {
			t.Fatal("preferences escaped selected hospital session")
		}
		var sent contracts.VehicleInput
		if json.NewDecoder(request.Body).Decode(&sent) != nil || sent.RequestedVehicleID != "1" || sent.RequestedDriverID != "18" {
			t.Fatalf("preference IDs not relayed %#v", sent)
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleBooking{VehicleInput: sent, ID: "14", Code: "CAR-14", Status: "pending", RequestedVehicleName: "รถตู้จริง", RequestedLicensePlate: "กก-123", RequestedDriverName: "พขร. จริง"})
		return w.Result(), nil
	})
	got := r.request(t, http.MethodPost, "/api/vehicles", input, token, false)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"requested_vehicle_name":"รถตู้จริง"`) || !strings.Contains(got.Body.String(), `"requested_driver_name":"พขร. จริง"`) {
		t.Fatalf("preference snapshots dropped %d %s", got.Code, got.Body.String())
	}
	for _, extra := range []string{`"license_plate":"spoof"`, `"driver_id":"18"`, `"requested_vehicle_name":"spoof"`, `"requested_driver_name":"spoof"`} {
		body := strings.TrimSuffix(string(raw), "}") + "," + extra + "}"
		if got := r.request(t, http.MethodPost, "/api/vehicles", json.RawMessage(body), token, false); got.Code != 400 {
			t.Fatalf("accepted caller allocation/snapshot %s => %d", extra, got.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("spoofed snapshots reached upstream: %d calls", calls)
	}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "fields": map[string]string{"requested_vehicle_id": "private SQL error", "requested_driver_id": "private employee detail", "private": "secret"}}})
		return w.Result(), nil
	})
	got = r.request(t, http.MethodPost, "/api/vehicles", input, token, false)
	if got.Code != 422 || !strings.Contains(got.Body.String(), `"requested_vehicle_id"`) || !strings.Contains(got.Body.String(), `"requested_driver_id"`) || strings.Contains(got.Body.String(), "private") || strings.Contains(got.Body.String(), "secret") {
		t.Fatalf("preference errors not sanitized %d %s", got.Code, got.Body.String())
	}
}

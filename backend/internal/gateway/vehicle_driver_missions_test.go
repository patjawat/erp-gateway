package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func gatewayMileage(value float64) *float64 { return &value }

func TestDriverMissionGatewayRequiresSessionAndSupportedMethods(t *testing.T) {
	r := rig(t)
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatal("unauthorized upstream call")
		return nil, nil
	})
	for _, test := range []struct {
		method, path string
		code         int
		allow        string
	}{
		{http.MethodGet, "/api/vehicles/drivers/18/missions", 401, ""},
		{http.MethodPut, "/api/vehicles/drivers/18/missions/1", 401, ""},
		{http.MethodPost, "/api/vehicles/drivers/18/missions", 405, "GET"},
		{http.MethodGet, "/api/vehicles/drivers/18/missions/1", 401, ""},
	} {
		got := r.request(t, test.method, test.path, nil, "", false)
		if got.Code != test.code || got.Header().Get("Allow") != test.allow {
			t.Fatalf("%s %s => %d", test.method, test.path, got.Code)
		}
	}
}

func TestDriverMissionGatewayForwardsHospitalSessionAndMileage(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	input := contracts.VehicleDriverMissionInput{MileageStart: gatewayMileage(100), MileageEnd: gatewayMileage(125.5)}
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("mission escaped selected hospital session")
		}
		w := httptest.NewRecorder()
		item := contracts.VehicleDriverMission{ID: "9", BookingID: "1", DriverID: "18", Code: "CAR-1", MileageStart: gatewayMileage(100)}
		if request.Method == http.MethodGet {
			if request.URL.Path != "/api/vehicles/drivers/18/missions" || request.URL.RawQuery != "page=2&per_page=1" {
				t.Fatalf("mission selection changed %s", request.URL)
			}
			writeData(w, contracts.VehicleDriverMissions{Items: []contracts.VehicleDriverMission{item}, Page: 2, PerPage: 1, TotalCount: 2})
		} else {
			var sent contracts.VehicleDriverMissionInput
			if request.Method != http.MethodPut || request.URL.Path != "/api/vehicles/drivers/18/missions/9" || json.NewDecoder(request.Body).Decode(&sent) != nil || sent.MileageStart == nil || *sent.MileageStart != 100 || sent.MileageEnd == nil || *sent.MileageEnd != 125.5 {
				t.Fatalf("wrong mileage request %#v", sent)
			}
			item.MileageEnd = sent.MileageEnd
			writeData(w, item)
		}
		return w.Result(), nil
	})
	if got := r.request(t, http.MethodGet, "/api/vehicles/drivers/18/missions?page=2&per_page=1", nil, token, false); got.Code != 200 || !strings.Contains(got.Body.String(), `"total_count":2`) {
		t.Fatalf("list %d %s", got.Code, got.Body.String())
	}
	if got := r.request(t, http.MethodPut, "/api/vehicles/drivers/18/missions/9", input, token, false); got.Code != 200 || !strings.Contains(got.Body.String(), `"mileage_end":125.5`) {
		t.Fatalf("save %d %s", got.Code, got.Body.String())
	}
	for _, test := range []struct {
		path string
		body any
		code int
	}{
		{"/api/vehicles/drivers/18/missions/9", json.RawMessage(`{"mileage_start":100,"mileage_end":125,"driver_id":"19"}`), 400},
		{"/api/vehicles/drivers/18/missions/9", json.RawMessage(`{"mileage_start":100}`), 422},
		{"/api/vehicles/drivers/18/missions/9", json.RawMessage(`{"mileage_start":100,"mileage_end":90}`), 422},
		{"/api/vehicles/drivers/18/missions/9?page=2", input, 400},
	} {
		if got := r.request(t, http.MethodPut, test.path, test.body, token, false); got.Code != test.code {
			t.Fatalf("invalid input => %d %s", got.Code, got.Body.String())
		}
	}
	if got := r.request(t, http.MethodGet, "/api/vehicles/drivers/18/missions?date=2026-11-19", nil, token, false); got.Code != 400 {
		t.Fatalf("accepted date filter %d", got.Code)
	}
	if calls != 2 {
		t.Fatalf("invalid request reached upstream: %d calls", calls)
	}
}

func TestDriverMissionGatewayRejectsInconsistentRepliesAndSanitizesErrors(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	input := contracts.VehicleDriverMissionInput{MileageStart: gatewayMileage(100), MileageEnd: gatewayMileage(125)}
	item := contracts.VehicleDriverMission{ID: "9", BookingID: "1", DriverID: "18", MileageStart: gatewayMileage(100), MileageEnd: gatewayMileage(125)}
	for _, mutate := range []func(*contracts.VehicleDriverMission){
		func(v *contracts.VehicleDriverMission) { v.ID = "10" },
		func(v *contracts.VehicleDriverMission) { v.DriverID = "19" },
		func(v *contracts.VehicleDriverMission) { v.MileageEnd = gatewayMileage(130) },
		func(v *contracts.VehicleDriverMission) { v.CanEdit = true },
	} {
		bad := item
		mutate(&bad)
		r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			writeData(w, bad)
			return w.Result(), nil
		})
		if got := r.request(t, http.MethodPut, "/api/vehicles/drivers/18/missions/9", input, token, false); got.Code != 503 {
			t.Fatalf("accepted inconsistent save %d %s", got.Code, got.Body.String())
		}
	}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleDriverMissions{Items: []contracts.VehicleDriverMission{item}, Page: 1, PerPage: 50, TotalCount: 1})
		return w.Result(), nil
	})
	if got := r.request(t, http.MethodGet, "/api/vehicles/drivers/18/missions", nil, token, false); got.Code != 503 {
		t.Fatalf("completed mission appeared pending %d", got.Code)
	}
	for _, test := range []struct {
		code   string
		status int
	}{{"VEHICLE_MISSION_CHANGED", 409}, {"FORBIDDEN", 403}, {"READ_ONLY", 403}, {"VEHICLE_NOT_FOUND", 404}} {
		r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			writeJSON(w, test.status, map[string]any{"error": map[string]any{"code": test.code, "message": "private SQL error"}})
			return w.Result(), nil
		})
		if got := r.request(t, http.MethodPut, "/api/vehicles/drivers/18/missions/9", input, token, false); got.Code != test.status || !strings.Contains(got.Body.String(), test.code) || strings.Contains(got.Body.String(), "private") {
			t.Fatalf("bad relayed error %d %s", got.Code, got.Body.String())
		}
	}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "fields": map[string]string{"mileage_end": "private SQL", "internal": "secret"}}})
		return w.Result(), nil
	})
	if got := r.request(t, http.MethodPut, "/api/vehicles/drivers/18/missions/9", input, token, false); got.Code != 422 || !strings.Contains(got.Body.String(), "mileage_end") || strings.Contains(got.Body.String(), "private") || strings.Contains(got.Body.String(), "internal") {
		t.Fatalf("bad validation error %d %s", got.Code, got.Body.String())
	}
}

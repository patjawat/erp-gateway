package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleHomeGatewayPreservesFilterAndCentralCounts(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	query := "scope=all&vehicle_type=ambulance&date=2026-11-22&status=approved&exclude_cancelled=true&include_type_counts=true&page=1&per_page=20"
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.RawQuery != query || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("home filter or tenant authorization changed")
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleHistory{Items: []contracts.VehicleBooking{{ID: "12", Status: "approved", StatusLabel: "อนุมัติแล้ว รอจัดสรร", VehicleInput: contracts.VehicleInput{VehicleType: "ambulance", RequestMode: "immediate"}}}, Page: 1, PerPage: 20, TotalCount: 1, TypeCounts: &contracts.VehicleTypeCounts{All: 3, Official: 2, Ambulance: 1}})
		return w.Result(), nil
	})
	got := r.request(t, http.MethodGet, "/api/vehicles?"+query, nil, token, false)
	var reply struct {
		Data contracts.VehicleHistory `json:"data"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &reply) != nil || reply.Data.TypeCounts == nil || *reply.Data.TypeCounts != (contracts.VehicleTypeCounts{All: 3, Official: 2, Ambulance: 1}) || reply.Data.TotalCount != 1 {
		t.Fatalf("home envelope %d %s", got.Code, got.Body.String())
	}
	for _, raw := range []string{"status=Pass", "status=all", "include_type_counts=1", "vehicle_type=central&status=bad", "status=pending&status=completed"} {
		got = r.request(t, http.MethodGet, "/api/vehicles?"+raw, nil, token, false)
		if got.Code != 400 {
			t.Fatalf("invalid home query %q reached upstream: %d", raw, got.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("invalid home filters called upstream %d times", calls)
	}
}

func TestVehicleHomeGatewayRejectsInvalidOrInconsistentSummaries(t *testing.T) {
	for _, test := range []struct {
		name, query string
		counts      *contracts.VehicleTypeCounts
		total       int
		status      string
	}{
		{"missing requested counts", "vehicle_type=central&include_type_counts=true", nil, 1, "pending"},
		{"negative", "vehicle_type=central&include_type_counts=true", &contracts.VehicleTypeCounts{All: -1}, 1, "pending"},
		{"partition mismatch", "vehicle_type=central&include_type_counts=true", &contracts.VehicleTypeCounts{All: 3, Official: 1, Ambulance: 1}, 3, "pending"},
		{"central total mismatch", "vehicle_type=central&include_type_counts=true", &contracts.VehicleTypeCounts{All: 2, Official: 1, Ambulance: 1}, 1, "pending"},
		{"official total mismatch", "vehicle_type=official&include_type_counts=true", &contracts.VehicleTypeCounts{All: 3, Official: 2, Ambulance: 1}, 1, "pending"},
		{"ambulance total mismatch", "vehicle_type=ambulance&include_type_counts=true", &contracts.VehicleTypeCounts{All: 3, Official: 1, Ambulance: 2}, 1, "pending"},
		{"status mismatch", "vehicle_type=central&status=approved", nil, 1, "assigned"},
		{"cancelled excluded", "vehicle_type=central&exclude_cancelled=true", nil, 1, "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := rig(t)
			h := r.add(t, "11447")
			r.enable(t, h)
			token := r.login(t, h.Code)
			r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				writeData(w, contracts.VehicleHistory{Items: []contracts.VehicleBooking{{ID: "12", Status: test.status}}, Page: 1, PerPage: 20, TotalCount: test.total, TypeCounts: test.counts})
				return w.Result(), nil
			})
			got := r.request(t, http.MethodGet, "/api/vehicles?"+test.query, nil, token, false)
			if got.Code != 503 {
				t.Fatalf("invalid home summary escaped gateway: %d %s", got.Code, got.Body.String())
			}
		})
	}
}

func TestVehicleHomeGatewayLaterPagesDoNotRequireTypeCounts(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	query := "scope=all&vehicle_type=central&date=2026-11-22&status=pending&page=2&per_page=1&exclude_cancelled=true"
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.RawQuery != query {
			t.Fatal("pagination filters changed")
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleHistory{Items: []contracts.VehicleBooking{{ID: "12", Status: "pending"}}, Page: 2, PerPage: 1, TotalCount: 2})
		return w.Result(), nil
	})
	got := r.request(t, http.MethodGet, "/api/vehicles?"+query, nil, token, false)
	if got.Code != 200 || strings.Contains(got.Body.String(), "type_counts") {
		t.Fatalf("later page requires or invents aggregate: %d %s", got.Code, got.Body.String())
	}
}

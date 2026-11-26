package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleHistoryPeriodGatewayPreservesTenantAndFilters(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	query := "scope=mine&vehicle_type=all&date_from=2026-11-01&date_to=2026-11-30&page=2&per_page=20"
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.Path != "/api/vehicles" || request.URL.RawQuery != query || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("history filters or tenant authorization changed")
		}
		w := httptest.NewRecorder()
		selection, _ := contracts.VehicleQuery(request.URL.RawQuery, false)
		writeData(w, contracts.VehicleHistory{Items: []contracts.VehicleBooking{{ID: "21"}}, Page: selection.Page, PerPage: selection.PerPage, TotalCount: 21, HasMore: selection.Page*selection.PerPage < 21})
		return w.Result(), nil
	})
	if got := r.request(t, http.MethodGet, "/api/vehicles?"+query, nil, "", false); got.Code != 401 {
		t.Fatalf("unauthenticated period: %d", got.Code)
	}
	got := r.request(t, http.MethodGet, "/api/vehicles?"+query, nil, token, false)
	var reply struct {
		Data contracts.VehicleHistory `json:"data"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &reply) != nil || reply.Data.Page != 2 || reply.Data.TotalCount != 21 || reply.Data.PerPage != 20 || reply.Data.HasMore {
		t.Fatalf("period reply %d %s", got.Code, got.Body.String())
	}
	query = "scope=all&vehicle_type=all&date=2026-11-19&page=1&per_page=20&exclude_cancelled=true"
	if got := r.request(t, http.MethodGet, "/api/vehicles?"+query, nil, token, false); got.Code != 200 || !strings.Contains(got.Body.String(), `"has_more":true`) {
		t.Fatalf("schedule selection %d %s", got.Code, got.Body.String())
	}
	for _, path := range []string{
		"/api/vehicles?date_from=2026-11-01",
		"/api/vehicles?date_from=2026-11-30&date_to=2026-11-01",
		"/api/vehicles?date_from=2026-11-01&date_to=2026-11-30&date=2026-11-19",
		"/api/vehicles/options?date_from=2026-11-01&date_to=2026-11-30",
		"/api/vehicles?exclude_cancelled=1",
		"/api/vehicles/options?exclude_cancelled=true",
	} {
		if got := r.request(t, http.MethodGet, path, nil, token, false); got.Code != 400 {
			t.Fatalf("invalid period %s: %d %s", path, got.Code, got.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("invalid/unauthenticated period reached upstream: %d calls", calls)
	}
}

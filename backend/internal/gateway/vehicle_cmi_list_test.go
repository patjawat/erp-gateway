package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCMIListGatewayKeepsHospitalFilterAndRejectsUnfilteredRows(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	status := "expired"
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.RawQuery != "status=expired&page=1&per_page=20" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] {
			t.Fatal("filter or hospital session changed")
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleCMIPage{Items: []contracts.VehicleDirectoryItem{{ID: "1", Name: "expired car", CMIExpiryDate: "2026-11-19", CMIStatus: status}}, Page: 1, PerPage: 20, TotalCount: 1})
		return w.Result(), nil
	})
	path := "/api/vehicles/cmi?status=expired&page=1&per_page=20"
	if got := r.request(t, http.MethodGet, path, nil, "", false); got.Code != 401 || calls != 0 {
		t.Fatal("unauthenticated request reached upstream")
	}
	if got := r.request(t, http.MethodGet, path, nil, token, false); got.Code != 200 {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	status = "expiring"
	if got := r.request(t, http.MethodGet, path, nil, token, false); got.Code != 503 {
		t.Fatalf("unfiltered response accepted %d", got.Code)
	}
}

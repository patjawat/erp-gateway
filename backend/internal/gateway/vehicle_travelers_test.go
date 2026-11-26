package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVehicleTravelerGatewayScopesPagesAndValidatesReplies(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	wrongBooking := false
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.URL.Path != "/api/vehicles/1/travelers" || request.URL.RawQuery != "page=2&per_page=20" {
			t.Fatalf("request escaped hospital/page %s", request.URL)
		}
		out := contracts.VehicleTravelers{BookingID: "1", Items: []contracts.VehicleBookingTraveler{{ID: "21", VehicleTraveler: contracts.VehicleTraveler{EmployeeID: "18", Name: "คนเดินทาง"}, Position: "พนักงานขับรถ"}}, Page: 2, PerPage: 20, TotalCount: 21}
		if wrongBooking {
			out.BookingID = "2"
		}
		w := httptest.NewRecorder()
		writeData(w, out)
		return w.Result(), nil
	})
	path := "/api/vehicles/1/travelers?page=2&per_page=20"
	if got := r.request(t, http.MethodGet, path, nil, "", false); got.Code != 401 {
		t.Fatalf("no session %d", got.Code)
	}
	if got := r.request(t, http.MethodGet, path, nil, token, false); got.Code != 200 || !strings.Contains(got.Body.String(), "พนักงานขับรถ") {
		t.Fatalf("page %d %s", got.Code, got.Body.String())
	}
	if got := r.request(t, http.MethodGet, "/api/vehicles/1/travelers?per_page=100", nil, token, false); got.Code != 400 {
		t.Fatalf("unbounded %d", got.Code)
	}
	wrongBooking = true
	if got := r.request(t, http.MethodGet, path, nil, token, false); got.Code != 503 || strings.Contains(got.Body.String(), "คนเดินทาง") {
		t.Fatalf("forged scope %d %s", got.Code, got.Body.String())
	}
	if calls != 2 {
		t.Fatalf("unexpected upstream calls: %d", calls)
	}
}

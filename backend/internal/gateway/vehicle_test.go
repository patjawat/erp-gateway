package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleRoutesRequireHospitalSessionAndSupportedMethods(t *testing.T) {
	r := rig(t)
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unauthenticated route reached upstream: %s", request.URL.Path)
		return nil, nil
	})
	for _, v := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, "/api/vehicles", 401, ""},
		{http.MethodPost, "/api/vehicles", 401, ""},
		{http.MethodGet, "/api/vehicles/options", 401, ""},
		{http.MethodGet, "/api/vehicles/1", 401, ""},
		{http.MethodGet, "/api/vehicles/photos/vehicle/1", 401, ""},
		{http.MethodGet, "/api/vehicles/photos/driver/18", 401, ""},
		{http.MethodPut, "/api/vehicles", 405, "GET, POST"},
		{http.MethodPost, "/api/vehicles/options", 405, "GET"},
		{http.MethodPost, "/api/vehicles/1", 405, "GET"},
		{http.MethodPost, "/api/vehicles/photos/vehicle/1", 405, "GET"},
	} {
		got := r.request(t, v.method, v.path, nil, "", false)
		if got.Code != v.status || got.Header().Get("Allow") != v.allow {
			t.Fatalf("%s %s => %d %s", v.method, v.path, got.Code, got.Body.String())
		}
	}
}

func TestVehicleGatewayDetailsAndPhotosStayWithinTenant(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	photo := "GIF89a-real-photo"
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "11447.example.invalid" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) || request.Header.Get("Cookie") != "" {
			t.Fatal("photo/detail escaped tenant session")
		}
		w := httptest.NewRecorder()
		if request.URL.Path == "/api/vehicles/1" {
			// Legacy summaries may lack code/reason/location; the detail endpoint
			// must preserve the same read compatibility as the history endpoint.
			writeData(w, contracts.VehicleBooking{ID: "1", VehicleImageURL: "/api/vehicles/photos/vehicle/1", ContactPhone: "1234", RequesterDepartment: "ฝ่ายงาน", DriverPhone: "5678", DriverPosition: "พนักงานขับรถ ชำนาญงาน"})
		} else {
			w.Header().Set("Content-Type", "image/gif")
			_, _ = io.WriteString(w, photo)
		}
		return w.Result(), nil
	})
	if got := r.request(t, "GET", "/api/vehicles/1", nil, token, false); got.Code != 200 {
		t.Fatalf("legacy detail %d %s", got.Code, got.Body.String())
	} else if !strings.Contains(got.Body.String(), `"contact_phone":"1234"`) || !strings.Contains(got.Body.String(), `"requester_department":"ฝ่ายงาน"`) || !strings.Contains(got.Body.String(), `"driver_phone":"5678"`) || !strings.Contains(got.Body.String(), `"driver_position":"พนักงานขับรถ ชำนาญงาน"`) {
		t.Fatal("gateway dropped booking contact details")
	}
	if got := r.request(t, "GET", "/api/vehicles/photos/vehicle/1", nil, token, false); got.Code != 200 || got.Header().Get("Content-Type") != "image/gif" || got.Body.String() != photo {
		t.Fatalf("photo %d %s", got.Code, got.Body.String())
	}
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		_, _ = io.WriteString(w, "<svg><script>unsafe</script></svg>")
		return w.Result(), nil
	})
	if got := r.request(t, "GET", "/api/vehicles/photos/driver/18", nil, token, false); got.Code != 503 || strings.Contains(got.Body.String(), "script") {
		t.Fatalf("unsafe upstream image %d %s", got.Code, got.Body.String())
	}
}

func TestVehicleGatewayRejectsUntrustedImageURLsAndPreservesLegacyHistory(t *testing.T) {
	history := contracts.VehicleHistory{Items: []contracts.VehicleBooking{{ID: "1"}}, Page: 1, PerPage: 50, TotalCount: 1}
	if !validVehicleOutput(&history) {
		t.Fatal("legacy history rejected")
	}
	history.Items[0].DriverImageURL = "https://example.invalid/private.png"
	if validVehicleOutput(&history) {
		t.Fatal("external image URL accepted")
	}
	options := contracts.VehicleOptions{Vehicles: []contracts.VehicleDirectoryItem{{ID: "1", ImageURL: "/api/vehicles/photos/driver/18"}}, Drivers: []contracts.VehicleDirectoryItem{}}
	if validVehicleOutput(&options) {
		t.Fatal("vehicle photo accepted driver route")
	}
}

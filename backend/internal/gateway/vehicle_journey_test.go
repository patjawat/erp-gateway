package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleFormOptionsGatewayAuthTenantAndCatalogueValidation(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	options := contracts.VehicleFormOptions{VehicleTypes: []contracts.VehicleFormChoice{{ID: "official", Name: "รถทั่วไป"}, {ID: "personal", Name: "รถยนต์ส่วนตัว"}, {ID: "ambulance", Name: "รถพยาบาล"}}, ReferTypes: []contracts.VehicleFormChoice{{ID: "EMS", Name: "ภารกิจจริง"}}, Urgencies: []contracts.VehicleFormChoice{{ID: "actual-code", Name: "ระดับจริง"}}, CanWrite: true}
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.Path != "/api/vehicles/form-options" || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] {
			t.Fatal("form options escaped selected hospital session")
		}
		w := httptest.NewRecorder()
		writeData(w, options)
		return w.Result(), nil
	})
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{http.MethodGet, "/api/vehicles/form-options", "", 401},
		{http.MethodPost, "/api/vehicles/form-options", token, 405},
		{http.MethodGet, "/api/vehicles/form-options?x=1", token, 400},
		{http.MethodGet, "/api/vehicles/form-options", token, 200},
	} {
		got := r.request(t, tc.method, tc.path, nil, tc.token, false)
		if got.Code != tc.want {
			t.Fatalf("%s %s => %d %s", tc.method, tc.path, got.Code, got.Body.String())
		}
		if tc.want == 200 && (!strings.Contains(got.Body.String(), `"id":"actual-code"`) || !strings.Contains(got.Body.String(), `"id":"EMS"`)) {
			t.Fatal("actual catalogue codes were changed")
		}
	}
	if calls != 1 {
		t.Fatalf("invalid form requests reached upstream: %d", calls)
	}
	options.VehicleTypes[1].ID = "untrusted-fleet"
	if got := r.request(t, http.MethodGet, "/api/vehicles/form-options", nil, token, false); got.Code != 503 || strings.Contains(got.Body.String(), "untrusted-fleet") {
		t.Fatalf("untrusted catalogue relayed %d %s", got.Code, got.Body.String())
	}
}

func TestVehicleImmediateGatewayForwardsImmutableIntakeAndSanitizesErrors(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	input := contracts.VehicleInput{RequestID: "vehicle-immediate-gateway-01", VehicleType: "ambulance", RequestMode: "immediate", ReferType: "EMS", Urgency: "actual-code", Pickup: "จุดเกิดเหตุ", ContactName: "ผู้แจ้ง", Phone: "1234", Reason: "อุบัติเหตุ"}
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		var sent contracts.VehicleInput
		if request.URL.Host != "11447.example.invalid" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || json.NewDecoder(request.Body).Decode(&sent) != nil || sent.VehicleType != "ambulance" || sent.RequestMode != "immediate" || sent.ReferType != "EMS" || sent.Urgency != "actual-code" || sent.ContactName != "ผู้แจ้ง" || sent.StartDate != "" || sent.StartTime != "" || sent.Location != "" || sent.RequestedVehicleID != "" || sent.RequestedDriverID != "" || len(sent.Travelers) != 0 {
			t.Fatalf("intake payload changed %#v", sent)
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.VehicleBooking{VehicleInput: sent, ID: "14", Code: "CAR-14", Status: "pending", StatusLabel: "รอเจ้าหน้าที่จัดสรร"})
		return w.Result(), nil
	})
	got := r.request(t, http.MethodPost, "/api/vehicles", input, token, false)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"status_label":"รอเจ้าหน้าที่จัดสรร"`) {
		t.Fatalf("intake rejected %d %s", got.Code, got.Body.String())
	}
	input.RequestedDriverID = "18"
	if got = r.request(t, http.MethodPost, "/api/vehicles", input, token, false); got.Code != 422 || calls != 1 {
		t.Fatalf("immediate preference reached local API %d calls=%d", got.Code, calls)
	}
	input.RequestedDriverID = ""
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		writeJSON(w, 422, map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "fields": map[string]string{"refer_type": "private SQL", "urgency": "private patient detail", "contact_name": "private name", "patient_hn": "private hn", "private": "secret"}}})
		return w.Result(), nil
	})
	got = r.request(t, http.MethodPost, "/api/vehicles", input, token, false)
	if got.Code != 422 || strings.Contains(got.Body.String(), "private") || strings.Contains(got.Body.String(), "secret") {
		t.Fatalf("intake errors leaked %d %s", got.Code, got.Body.String())
	}
	for _, key := range []string{"refer_type", "urgency", "contact_name", "patient_hn"} {
		if !strings.Contains(got.Body.String(), `"`+key+`"`) {
			t.Fatalf("validation field dropped %s", got.Body.String())
		}
	}
}

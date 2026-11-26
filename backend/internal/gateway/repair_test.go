package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func validRepairRequest() contracts.RepairCreate {
	return contracts.RepairCreate{RequestID: "12345678-1234-4234-a234-123456789abc", Title: "เปิดเครื่องไม่ติด", DeviceTypeID: "COM", RepairGroup: "2", Location: "ห้องทำงาน", Phone: "1234", Urgency: "medium"}
}

func TestRepairsAreRoutedToSessionHospitalAndValidated(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	tokenA, tokenB := r.login(t, a.Code), r.login(t, b.Code)
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || request.Header.Get("Cookie") != "" {
			t.Fatalf("wrong tenant credentials forwarded to repair API")
		}
		w := httptest.NewRecorder()
		item := contracts.Repair{ID: "1", Title: "Repair " + code, Status: "pending"}
		switch request.URL.Path {
		case "/api/repairs":
			if request.Method == http.MethodPost {
				var value contracts.RepairCreate
				if json.NewDecoder(request.Body).Decode(&value) != nil || value.Title != "เปิดเครื่องไม่ติด" || value.RequestID == "" || request.URL.RawQuery != "" {
					t.Fatal("invalid forwarded repair create")
				}
				writeData(w, item)
			} else {
				if request.URL.Query().Get("page") != "2" || request.URL.Query().Get("per_page") != "10" {
					t.Fatal("repair paging lost")
				}
				writeData(w, contracts.RepairHistory{Items: []contracts.Repair{item}, Page: 2, PerPage: 10, TotalCount: 11})
			}
		case "/api/repairs/options":
			writeData(w, contracts.RepairOptions{DeviceTypes: []contracts.RepairOption{{Code: "COM", Title: code}}, RepairGroups: []contracts.RepairOption{}, Urgencies: []contracts.RepairOption{}, CreateEnabled: true})
		case "/api/repairs/assets":
			if request.URL.Query().Get("q") != "เครื่อง" || request.URL.Query().Get("repair_group") != "2" {
				t.Fatal("asset query lost")
			}
			writeData(w, contracts.RepairAssets{Items: []contracts.RepairAsset{{Code: code, Name: "เครื่อง"}}})
		default:
			t.Fatalf("unexpected upstream route %s", request.URL.Path)
		}
		return w.Result(), nil
	})
	for _, tenant := range []struct{ token, code, other string }{{tokenA, a.Code, b.Code}, {tokenB, b.Code, a.Code}} {
		for _, path := range []string{"/api/repairs?page=2&per_page=10", "/api/repairs/options", "/api/repairs/assets?q=%E0%B9%80%E0%B8%84%E0%B8%A3%E0%B8%B7%E0%B9%88%E0%B8%AD%E0%B8%87&repair_group=2"} {
			w := r.request(t, "GET", path, nil, tenant.token, false)
			if w.Code != 200 || !strings.Contains(w.Body.String(), tenant.code) || strings.Contains(w.Body.String(), tenant.other) || w.Header().Get("Cache-Control") != "no-store, private" {
				t.Fatalf("wrong tenant repair data: %d %s", w.Code, w.Body.String())
			}
		}
		w := r.request(t, "POST", "/api/repairs", validRepairRequest(), tenant.token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tenant.code) || strings.Contains(w.Body.String(), tenant.other) {
			t.Fatalf("wrong tenant repair submission: %d %s", w.Code, w.Body.String())
		}
	}
	before := calls
	for _, path := range []string{"/api/repairs?emp_id=99", "/api/repairs?hospital_code=12448", "/api/repairs?page=1&page=2", "/api/repairs?per_page=101", "/api/repairs/options?user_id=99", "/api/repairs/assets?device_type_id=COM", "/api/repairs/assets?repair_group=4", "/api/repairs/assets?q=a&q=b"} {
		if w := r.request(t, "GET", path, nil, tokenA, false); w.Code != 400 {
			t.Fatalf("invalid selection reached repair API %s: %d", path, w.Code)
		}
	}
	bad := validRepairRequest()
	bad.Urgency = "unknown"
	if w := r.request(t, "POST", "/api/repairs", bad, tokenA, false); w.Code != 400 {
		t.Fatal("invalid create accepted", w.Code)
	}
	if w := r.request(t, "POST", "/api/repairs", map[string]string{"emp_id": "99"}, tokenA, false); w.Code != 400 {
		t.Fatal("ownership field accepted", w.Code)
	}
	if w := r.request(t, "POST", "/api/repairs", validRepairRequest(), "", false); w.Code != 401 {
		t.Fatal("unauthenticated create accepted", w.Code)
	}
	if w := r.request(t, "DELETE", "/api/repairs", nil, tokenA, false); w.Code != 405 || w.Header().Get("Allow") != "GET, POST" {
		t.Fatal("unsupported repair method accepted", w.Code)
	}
	if calls != before {
		t.Fatal("invalid repair request reached upstream")
	}
	w := r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test"}, "", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.request(t, "POST", "/api/repairs", validRepairRequest(), tokenA, false); w.Code != 403 || calls != before {
		t.Fatal("suspended hospital submitted a repair", w.Code)
	}
}

func TestRepairGatewaySanitizesKnownAndUnknownUpstreamErrors(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	for _, test := range []struct {
		upstreamStatus int
		code           string
		expected       int
	}{
		{400, "INVALID_REQUEST", 400}, {403, "EMPLOYEE_REQUIRED", 403},
		{409, "ASSET_UNAVAILABLE", 409}, {503, "REPAIR_DISABLED", 503},
		{503, "ERP_UNAVAILABLE", 503}, {500, "private-error", 503},
		{403, "GATEWAY_UNAUTHORIZED", 503}, {401, "SESSION_EXPIRED", 401},
	} {
		r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			fail(w, test.upstreamStatus, test.code, "private database password")
			return w.Result(), nil
		})
		w := r.request(t, "POST", "/api/repairs", validRepairRequest(), token, false)
		if w.Code != test.expected || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("unsafe repair failure %s: %d %s", test.code, w.Code, w.Body.String())
		}
	}
}

func TestRepairGatewayAcceptsAllowedUnicodePayloadAndFullHistory(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	request := validRepairRequest()
	request.Note = strings.Repeat("🔧", 4000)
	request.Location = strings.Repeat("ห", 1000)
	r.app.client.Transport = transportFunc(func(upstream *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		item := contracts.Repair{ID: "1", Title: request.Title, Note: request.Note, Location: request.Location}
		if upstream.Method == http.MethodPost {
			var value contracts.RepairCreate
			if json.NewDecoder(upstream.Body).Decode(&value) != nil || value.Note != request.Note || value.Location != request.Location {
				t.Fatal("valid Unicode payload was truncated")
			}
			writeData(w, item)
		} else {
			items := make([]contracts.Repair, 100)
			for i := range items {
				items[i] = item
			}
			writeData(w, contracts.RepairHistory{Items: items, Page: 1, PerPage: 100, TotalCount: 100})
		}
		return w.Result(), nil
	})
	if w := r.request(t, "POST", "/api/repairs", request, token, false); w.Code != 200 {
		t.Fatalf("valid large repair rejected: %d %s", w.Code, w.Body.String())
	}
	if w := r.request(t, "GET", "/api/repairs?per_page=100", nil, token, false); w.Code != 200 {
		t.Fatalf("valid full history rejected: %d", w.Code)
	}
}

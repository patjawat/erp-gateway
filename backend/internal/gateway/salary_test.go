package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestSalaryGatewayTenantBindingAndValidation(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	tokenA, tokenB := r.login(t, a.Code), r.login(t, b.Code)
	previous := r.app.client.Transport
	calls := 0
	malformed := false
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/salary" {
			return previous.RoundTrip(req)
		}
		calls++
		code := strings.TrimSuffix(req.URL.Host, ".example.invalid")
		if req.Header.Get("Authorization") != "Bearer "+r.localTokens[code] || req.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || req.URL.RawQuery != "page=1&page_size=20" {
			t.Fatal("incorrect salary tenant credentials", req.URL)
		}
		v := contracts.SalaryHistory{Items: []contracts.SalaryRecord{{ID: "1", PeriodCode: "2026-11", PeriodType: "salary", PeriodTypeLabel: "เงินเดือน",
			DateStart: "2026-11-01", DateEnd: "2026-11-30", Earnings: []contracts.SalaryLine{{Name: "ERP " + code, Amount: "123.01"}}, Deductions: []contracts.SalaryLine{}}}, Page: 1, PageSize: 20, Total: 1}
		if malformed {
			v.Items[0].Earnings[0].Amount = "NaN"
		}
		raw, _ := json.Marshal(map[string]any{"data": v})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, v := range []struct{ token, own, other string }{{tokenA, a.Code, b.Code}, {tokenB, b.Code, a.Code}} {
		w := r.request(t, "GET", "/api/salary", nil, v.token, false)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ERP "+v.own) || strings.Contains(w.Body.String(), v.other) || w.Header().Get("Cache-Control") != "no-store, private" {
			t.Fatal("incorrect tenant salary", w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"employee_id=99", "hospital_code=12448", "page=0", "page_size=51", "page=1&page=2", "page=%zz"} {
		if w := r.request(t, "GET", "/api/salary?"+query, nil, tokenA, false); w.Code != 400 || calls != 2 {
			t.Fatal("invalid salary query reached upstream", query, w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/salary", nil, "", false); w.Code != 401 || calls != 2 {
		t.Fatal("anonymous salary reached upstream", w.Code)
	}
	if w := r.request(t, "POST", "/api/salary", nil, tokenA, false); w.Code != 405 || calls != 2 {
		t.Fatal("salary accepted writes", w.Code)
	}
	malformed = true
	if w := r.request(t, "GET", "/api/salary", nil, tokenA, false); w.Code != 503 || strings.Contains(w.Body.String(), "NaN") {
		t.Fatal("malformed payroll escaped validation", w.Code)
	}
	r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "salary fixture"}, "", true)
	if w := r.request(t, "GET", "/api/salary", nil, tokenA, false); w.Code != 403 || calls != 3 {
		t.Fatal("suspended salary query reached upstream", w.Code)
	}
}

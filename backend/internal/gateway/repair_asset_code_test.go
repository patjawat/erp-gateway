package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestRepairExactAssetGatewayPreservesTenantAndRejectsSubstring(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	returnedCode := "COM-01"
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		query := request.URL.Query()
		if request.URL.Host != "11447.example.invalid" || query.Get("code") != "COM-01" || query.Has("q") || query.Get("page") != "1" || query.Get("per_page") != "2" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) {
			t.Fatal("exact lookup lost session hospital or code")
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.RepairAssets{Items: []contracts.RepairAsset{{Code: returnedCode, Name: "equipment"}}, Page: 1, PerPage: 2})
		return w.Result(), nil
	})
	path := "/api/repairs/assets?code=COM-01&page=1&per_page=2"
	if got := r.request(t, "GET", path, nil, "", false); got.Code != 401 || calls != 0 {
		t.Fatal("anonymous exact lookup reached local API", got.Code, calls)
	}
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	returnedCode = "PREFIX-COM-01"
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 503 {
		t.Fatal("substring asset accepted", got.Code)
	}
	if got := r.request(t, "GET", path+"&hospital_code=other", nil, token, false); got.Code != 400 || calls != 2 {
		t.Fatal("hospital override reached local API", got.Code, calls)
	}
}

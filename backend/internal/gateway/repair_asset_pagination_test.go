package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestRepairAssetPaginationGatewayForwardsTenantAndRejectsWrongPage(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	page := 2
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.URL.Query().Get("page") != "2" || request.URL.Query().Get("per_page") != "20" || request.URL.Query().Get("repair_group") != "2" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("X-Gateway-Key") != strings.Repeat(h.Code, 8) {
			t.Fatal("asset page escaped tenant or lost filters")
		}
		w := httptest.NewRecorder()
		writeData(w, contracts.RepairAssets{Items: []contracts.RepairAsset{{Code: "PAGE-20", Name: "equipment"}}, Page: page, PerPage: 20})
		return w.Result(), nil
	})
	path := "/api/repairs/assets?page=2&per_page=20&q=equipment&repair_group=2"
	if got := r.request(t, "GET", path, nil, "", false); got.Code != 401 {
		t.Fatal(got.Code)
	}
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	page = 1
	if got := r.request(t, "GET", path, nil, token, false); got.Code != 503 {
		t.Fatal("wrong page accepted", got.Code)
	}
	if got := r.request(t, "GET", "/api/repairs/assets?page=0", nil, token, false); got.Code != 400 || calls != 2 {
		t.Fatal("invalid page forwarded", got.Code, calls)
	}
}

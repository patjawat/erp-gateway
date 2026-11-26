package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDocumentPeriodsForwardWithTenantSearchYearStatusAndBoundedPage(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	previous := r.app.client.Transport
	forwarded := []string{}
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/documents" {
			return previous.RoundTrip(req)
		}
		query := req.URL.Query()
		forwarded = append(forwarded, query.Get("period"))
		if req.Method != "GET" || req.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || query.Get("q") != "period report" || query.Get("year") != "2569" || query.Get("status") != "unread" || query.Get("page") != "2" || query.Get("per_page") != "20" {
			t.Fatal("period selection or tenant credentials changed", req.URL)
		}
		data := contracts.DocumentHistory{Items: []contracts.Document{}, Page: 2, PerPage: 20, TotalCount: 0, Summary: contracts.DocumentSummary{}}
		raw, _ := json.Marshal(map[string]any{"data": data})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	for _, period := range []string{"this_week", "this_month", "last_month", "this_year"} {
		w := r.request(t, "GET", "/api/documents?q=period+report&year=2569&period="+period+"&status=unread&page=2&per_page=20", nil, token, false)
		if w.Code != 200 || forwarded[len(forwarded)-1] != period {
			t.Fatal(period, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"period=all", "period=", "period=this_week&period=this_month", "period=this_month&per_page=101", "period=this_year&employee_id=3"} {
		if w := r.request(t, "GET", "/api/documents?"+query, nil, token, false); w.Code != 400 {
			t.Fatal("invalid period reached upstream", query, w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/documents/101?period=this_week", nil, token, false); w.Code != 400 {
		t.Fatal("inbox-only period accepted on detail", w.Code)
	}
	if len(forwarded) != 4 {
		t.Fatal("invalid period request was forwarded", forwarded)
	}
}

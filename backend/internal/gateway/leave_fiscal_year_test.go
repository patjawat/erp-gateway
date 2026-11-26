package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestSavedLeaveFiscalYearRelayAndValidation(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	var savedYear *int
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		item := contracts.Leave{
			ID: "81", FiscalYear: savedYear, StartDate: "2026-11-18", EndDate: "2026-11-18",
			Reason: "private leave details",
		}
		w := httptest.NewRecorder()
		switch request.URL.Path {
		case "/api/leaves":
			writeData(w, contracts.LeaveHistory{Items: []contracts.Leave{item}, Page: 1, PerPage: 50, TotalCount: 1})
		case "/api/leaves/81":
			writeData(w, item)
		default:
			t.Fatalf("unexpected leave fiscal year route: %s", request.URL.Path)
		}
		return w.Result(), nil
	})
	for _, test := range []struct {
		name    string
		year    int
		missing bool
		valid   bool
	}{
		{"legacy missing year", 0, true, true},
		{"minimum year", 2443, false, true},
		{"saved year", 2570, false, true},
		{"maximum year", 2744, false, true},
		{"zero year", 0, false, false},
		{"Gregorian year", 2026, false, false},
		{"year below range", 2442, false, false},
		{"year above range", 2745, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			savedYear = &test.year
			if test.missing {
				savedYear = nil
			}
			for _, path := range []string{"/api/leaves", "/api/leaves/81"} {
				w := r.request(t, http.MethodGet, path, nil, token, false)
				if !test.valid {
					if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "private leave details") {
						t.Fatalf("invalid saved year escaped %s: %d %s", path, w.Code, w.Body.String())
					}
					continue
				}
				if w.Code != http.StatusOK {
					t.Fatalf("valid saved year rejected %s: %d %s", path, w.Code, w.Body.String())
				}
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				var item contracts.Leave
				if path == "/api/leaves" {
					var history contracts.LeaveHistory
					if err := json.Unmarshal(envelope.Data, &history); err != nil || len(history.Items) != 1 {
						t.Fatalf("invalid fiscal year history envelope: %v", err)
					}
					item = history.Items[0]
				} else if err := json.Unmarshal(envelope.Data, &item); err != nil {
					t.Fatal(err)
				}
				if test.missing {
					if item.FiscalYear != nil {
						t.Fatal("missing saved fiscal year was fabricated")
					}
				} else if item.FiscalYear == nil || *item.FiscalYear != test.year {
					t.Fatal("saved fiscal year was changed in transit")
				}
			}
		})
	}
}

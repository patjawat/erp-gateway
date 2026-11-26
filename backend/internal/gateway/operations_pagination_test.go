package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestOperationsPaginationRelaysTypedMetadataAndFixedPaths(t *testing.T) {
	for _, family := range []string{"usage", "destinations", "options", "timeline", "attachments", "repair_detail"} {
		t.Run(family, func(t *testing.T) {
			r := rig(t)
			h := r.add(t, "11447")
			r.enable(t, h)
			token := r.login(t, h.Code)
			path, output := operationsPageFixture(family)
			r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.RequestURI() != path || request.Method != "GET" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("Cookie") != "" {
					t.Fatalf("wrong scoped request %s", request.URL)
				}
				w := httptest.NewRecorder()
				writeData(w, output)
				return w.Result(), nil
			})
			got := r.request(t, "GET", path, nil, token, false)
			if got.Code != 200 || !strings.Contains(got.Body.String(), `"total_count":3`) || strings.Contains(got.Body.String(), "private-secret") {
				t.Fatalf("page metadata lost or unsafe %d %s", got.Code, got.Body.String())
			}
		})
	}
}

func operationsPageFixture(family string) (string, map[string]any) {
	meta := map[string]any{"page": 2, "per_page": 2, "total_count": 3, "has_more": false, "private": "private-secret"}
	switch family {
	case "usage":
		meta["date"], meta["checked_at"] = "2099-10-01", "2099-10-01T00:00:00Z"
		meta["items"] = []any{map[string]any{"room_id": "A", "room_name": "Room", "title": "Meeting", "start_date": "2099-10-01", "end_date": "2099-10-01", "start_time": "09:00", "end_time": "10:00", "status": "pending", "status_label": "Pending"}}
		return "/api/meetings/usage?date=2099-10-01&page=2&per_page=2", meta
	case "destinations":
		meta["items"] = []any{map[string]any{"id": "D", "name": "Destination"}}
		return "/api/vehicles/destinations?page=2&per_page=2&q=Destination", meta
	case "options":
		item := map[string]any{"id": "1", "name": "Car", "detail": "AB", "status": "available", "status_label": "Available", "image_url": ""}
		meta["items"] = []any{item}
		drivers := map[string]any{"items": []any{}, "page": 2, "per_page": 2, "total_count": 0, "has_more": false}
		return "/api/vehicles/options?page=2&per_page=2", map[string]any{"vehicles": []any{item}, "drivers": []any{}, "vehicles_page": meta, "drivers_page": drivers, "cmi_summary": map[string]int{"expired_count": 3, "missing_count": 0, "expiring_count": 0}}
	case "timeline":
		meta["items"] = []any{map[string]any{"id": "3", "title": "Event", "status": "pending", "created_at": ""}}
		return "/api/repairs/1/timeline?page=2&per_page=2", meta
	case "attachments":
		meta["items"] = []any{map[string]any{"id": "3", "file_name": "photo.png", "mime_type": "", "download_url": "/api/repairs/1/attachments/3"}}
		return "/api/repairs/1/attachments?page=2&per_page=2", meta
	default:
		items := []any{map[string]any{"id": "3", "title": "Event", "status": "pending", "created_at": ""}, map[string]any{"id": "4", "title": "Later", "status": "pending", "created_at": ""}, map[string]any{"id": "5", "title": "Last", "status": "pending", "created_at": ""}}
		first := map[string]any{"items": items, "page": 1, "per_page": 50, "total_count": 3, "has_more": false}
		return "/api/repairs/1", map[string]any{"record": map[string]string{"id": "1", "title": "Repair"}, "timeline": items, "timeline_page": first, "attachments": []any{}}
	}
}

func TestOperationsPaginationRejectsInvalidQueriesBeforeUpstream(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid query reached upstream")
		return nil, nil
	})
	for _, base := range []string{"/api/meetings/usage?date=2099-10-01&", "/api/vehicles/destinations?", "/api/vehicles/options?", "/api/repairs/1/timeline?", "/api/repairs/1/attachments?"} {
		for _, query := range []string{"page=0", "page=01", "per_page=101", "page=1&page=2", "page=1000001", "emp_id=99"} {
			if got := r.request(t, "GET", base+query, nil, token, false); got.Code != 400 {
				t.Fatalf("invalid query %s%s returned %d", base, query, got.Code)
			}
		}
	}
}

func TestOperationsPaginationRejectsMalformedMetadata(t *testing.T) {
	for _, family := range []string{"usage", "destinations", "options", "timeline", "attachments"} {
		for _, corruption := range []string{"page", "total", "more", "missing"} {
			t.Run(family+"_"+corruption, func(t *testing.T) {
				r := rig(t)
				h := r.add(t, "11447")
				r.enable(t, h)
				token := r.login(t, h.Code)
				path, payload := operationsPageFixture(family)
				meta := payload
				if family == "options" {
					meta = payload["vehicles_page"].(map[string]any)
				}
				switch corruption {
				case "page":
					meta["page"] = 1
				case "total":
					meta["total_count"] = 4
				case "more":
					meta["has_more"] = true
				case "missing":
					delete(meta, "page")
					delete(meta, "per_page")
					delete(meta, "total_count")
					delete(meta, "has_more")
				}
				r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
					w := httptest.NewRecorder()
					writeData(w, payload)
					return w.Result(), nil
				})
				if got := r.request(t, "GET", path, nil, token, false); got.Code != 503 {
					t.Fatalf("malformed page accepted %d %s", got.Code, got.Body.String())
				}
			})
		}
	}
}

func TestOperationsPaginationRejectsOverflowingCMISummary(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	total := 1
	value := &contracts.VehicleOptions{Vehicles: []contracts.VehicleDirectoryItem{}, Drivers: []contracts.VehicleDirectoryItem{}, VehiclesPage: &contracts.ReadPage[contracts.VehicleDirectoryItem]{ReadPageMetadata: &contracts.ReadPageMetadata{TotalCount: &total}}, CMISummary: &contracts.VehicleCMISummary{ExpiringCount: maximum, ExpiredCount: maximum, MissingCount: 1}}
	if validVehicleOutput(value) {
		t.Fatal("overflowing policy aggregate accepted")
	}
}

func TestOperationsPaginationRejectsMismatchedDuplicateArrays(t *testing.T) {
	for _, family := range []string{"options", "repair_detail"} {
		t.Run(family, func(t *testing.T) {
			r := rig(t)
			h := r.add(t, "11447")
			r.enable(t, h)
			token := r.login(t, h.Code)
			path, payload := operationsPageFixture(family)
			if family == "options" {
				payload["vehicles"] = []any{map[string]any{"id": "99", "name": "Different", "status": "available", "image_url": ""}}
			} else {
				payload["timeline"] = []any{}
			}
			r.app.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				writeData(w, payload)
				return w.Result(), nil
			})
			if got := r.request(t, "GET", path, nil, token, false); got.Code != 503 {
				t.Fatalf("contradictory arrays accepted %d %s", got.Code, got.Body.String())
			}
		})
	}
}

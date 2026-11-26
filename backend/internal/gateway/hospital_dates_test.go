package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func dateHospitalInput(h Hospital) map[string]string {
	return map[string]string{"code": h.Code, "name": h.Name, "province": h.Province, "api_url": h.APIURL, "gateway_key": ""}
}

func dateHospitalResponse(t *testing.T, response *httptest.ResponseRecorder) Hospital {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data Hospital `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// The operator owns customer renewal discussions; dates never suspend a site.
func TestHospitalExpiryNeedsExplicitOperatorSuspension(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	existingSession := r.login(t, h.Code)
	body := dateHospitalInput(h)
	body["start_date"] = "2000-01-01"
	body["expiry_date"] = "2001-01-01"
	dateHospitalResponse(t, r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, body, "", true))
	if w := r.request(t, "GET", "/api/profile", nil, existingSession, false); w.Code != 200 {
		t.Fatalf("recording expiry must keep existing sessions usable: %d %s", w.Code, w.Body.String())
	}
	newSession := r.login(t, h.Code)
	if w := r.request(t, "POST", "/admin/api/hospitals/"+h.ID+"/suspend", map[string]string{"reason": "operator decision"}, "", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := r.request(t, "GET", "/api/profile", nil, newSession, false); w.Code != 403 {
		t.Fatalf("explicit suspension must stop access: %d %s", w.Code, w.Body.String())
	}
}

func TestHospitalDatesCreateListUpdateAndPersistence(t *testing.T) {
	r := rig(t)
	body := map[string]string{
		"code": "11447", "name": "Hospital dates", "province": "Test",
		"api_url": "https://11447.example.invalid", "gateway_key": strings.Repeat("11447", 8),
		"start_date": "2026-01-01", "expiry_date": "2026-12-31",
	}
	h := dateHospitalResponse(t, r.request(t, "POST", "/admin/api/hospitals", body, "", true))
	if h.StartDate != body["start_date"] || h.ExpiryDate != body["expiry_date"] {
		t.Fatalf("created dates = %q, %q", h.StartDate, h.ExpiryDate)
	}
	response := r.request(t, "GET", "/admin/api/hospitals", nil, "", true)
	var list struct {
		Data struct {
			Hospitals []Hospital `json:"hospitals"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list.Data.Hospitals) != 1 {
		t.Fatalf("list status %d: %s", response.Code, response.Body.String())
	}
	if listed := list.Data.Hospitals[0]; listed.StartDate != h.StartDate || listed.ExpiryDate != h.ExpiryDate {
		t.Fatalf("listed dates = %q, %q", listed.StartDate, listed.ExpiryDate)
	}

	// Existing clients omit date fields; an ordinary update must retain them.
	update := dateHospitalInput(h)
	update["name"] = "Corrected hospital name"
	h = dateHospitalResponse(t, r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, update, "", true))
	if h.StartDate != "2026-01-01" || h.ExpiryDate != "2026-12-31" {
		t.Fatalf("omitted dates were lost: %q, %q", h.StartDate, h.ExpiryDate)
	}
	update = dateHospitalInput(h)
	update["expiry_date"] = "2027-12-31"
	h = dateHospitalResponse(t, r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, update, "", true))
	if h.StartDate != "2026-01-01" || h.ExpiryDate != "2027-12-31" {
		t.Fatalf("partial date update = %q, %q", h.StartDate, h.ExpiryDate)
	}
	if err := r.app.config.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(r.dbPath, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.hospital(context.Background(), "id", h.ID)
	if err != nil || saved.StartDate != h.StartDate || saved.ExpiryDate != h.ExpiryDate {
		t.Fatalf("persisted dates = %q, %q, error %v", saved.StartDate, saved.ExpiryDate, err)
	}
}

func TestHospitalDateOnlyEditsPreserveAccessAndCanClearDates(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	before, err := r.app.config.Store.hospital(context.Background(), "id", h.ID)
	if err != nil {
		t.Fatal(err)
	}
	update := dateHospitalInput(h)
	// Dates are administrative records, not an automatic access restriction.
	update["start_date"] = "2020-01-01"
	update["expiry_date"] = "2020-12-31"
	h = dateHospitalResponse(t, r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, update, "", true))
	if !h.Enabled || h.ApprovalStatus != "approved" || h.ConnectionStatus != before.ConnectionStatus || h.CheckedAt == nil || before.CheckedAt == nil || *h.CheckedAt != *before.CheckedAt {
		t.Fatalf("date-only edit reset hospital state: %+v", h)
	}
	if response := r.request(t, "GET", "/api/profile", nil, token, false); response.Code != 200 {
		t.Fatalf("date-only edit revoked session: %d %s", response.Code, response.Body.String())
	}
	update = dateHospitalInput(h)
	update["start_date"] = ""
	update["expiry_date"] = ""
	h = dateHospitalResponse(t, r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, update, "", true))
	if h.StartDate != "" || h.ExpiryDate != "" || !h.Enabled || h.ApprovalStatus != "approved" {
		t.Fatalf("cleared dates/state: %+v", h)
	}
	if response := r.request(t, "GET", "/api/profile", nil, token, false); response.Code != 200 {
		t.Fatalf("clearing dates revoked session: %d %s", response.Code, response.Body.String())
	}
}

func TestHospitalDatesRejectInvalidCalendarDatesAndRanges(t *testing.T) {
	r := rig(t)
	for _, dates := range []struct{ start, expiry string }{
		{"2026-2-01", ""}, {"2026-02-30", ""}, {"0000-01-01", ""},
		{"2026-01-01T00:00:00Z", ""}, {"", "2025-02-29"}, {"2026-12-31", "2026-01-01"},
	} {
		t.Run(dates.start+"_"+dates.expiry, func(t *testing.T) {
			body := map[string]string{
				"code": "11447", "name": "Hospital", "province": "Test",
				"api_url": "https://11447.example.invalid", "gateway_key": strings.Repeat("11447", 8),
				"start_date": dates.start, "expiry_date": dates.expiry,
			}
			response := r.request(t, "POST", "/admin/api/hospitals", body, "", true)
			if response.Code != 400 || !strings.Contains(response.Body.String(), "INVALID_HOSPITAL_DATES") {
				t.Fatalf("invalid dates accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
	h := r.add(t, "11447")
	update := dateHospitalInput(h)
	update["start_date"] = "2024-02-29"
	update["expiry_date"] = "2024-02-29"
	h = dateHospitalResponse(t, r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, update, "", true))
	for _, change := range []map[string]string{{"start_date": "2024-03-01"}, {"expiry_date": "2024-02-28"}, {"expiry_date": "2026-02-30"}} {
		update = dateHospitalInput(h)
		for key, value := range change {
			update[key] = value
		}
		response := r.request(t, "PUT", "/admin/api/hospitals/"+h.ID, update, "", true)
		if response.Code != 400 || !strings.Contains(response.Body.String(), "INVALID_HOSPITAL_DATES") {
			t.Fatalf("invalid partial update accepted: %d %s", response.Code, response.Body.String())
		}
	}
	saved, err := r.app.config.Store.hospital(context.Background(), "id", h.ID)
	if err != nil || saved.StartDate != "2024-02-29" || saved.ExpiryDate != "2024-02-29" {
		t.Fatalf("invalid update changed stored dates: %+v, error %v", saved, err)
	}
}

func TestHospitalMetadataMigrationPreservesLegacyHospitals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE hospitals (
 id TEXT PRIMARY KEY, code TEXT NOT NULL UNIQUE, name TEXT NOT NULL, province TEXT NOT NULL,
 api_url TEXT NOT NULL, gateway_key BLOB NOT NULL, approval_status TEXT NOT NULL DEFAULT 'pending',
 enabled INTEGER NOT NULL DEFAULT 0, connection_status TEXT NOT NULL DEFAULT 'unknown',
 checked_at INTEGER, created_at INTEGER NOT NULL);
INSERT INTO hospitals VALUES ('legacy-id','11447','Legacy Hospital','Test','https://11447.example.invalid',X'010203','approved',1,'online',12345,12300);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		store, err := OpenStore(path, bytes.Repeat([]byte{7}, 32))
		if err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
		h, err := store.hospital(context.Background(), "id", "legacy-id")
		if err != nil || h.Code != "11447" || h.Name != "Legacy Hospital" || h.StartDate != "" || h.ExpiryDate != "" || !h.Enabled || h.ApprovalStatus != "approved" || h.ConnectionStatus != "online" || h.CheckedAt == nil || *h.CheckedAt != 12345 || !bytes.Equal(h.key, []byte{1, 2, 3}) {
			store.Close()
			t.Fatalf("legacy hospital changed on pass %d: %+v, error %v", pass, h, err)
		}
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM hospital_attachments").Scan(&count); err != nil {
			store.Close()
			t.Fatal("attachments schema missing:", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

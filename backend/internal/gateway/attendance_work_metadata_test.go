package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestAttendanceWorkMetadataContextValidatedAndPreserved(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	var upstream string
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/attendance/context" {
			return previous.RoundTrip(req)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(upstream))}, nil
	})
	for _, tc := range []struct {
		name, extra            string
		valid, supported       bool
		locationType, workMode string
	}{
		{"legacy", "", true, false, "", ""},
		{"new_unknown", `,"checkout_metadata_supported":true`, true, true, "", ""},
		{"new_prefill", `,"checkout_metadata_supported":true,"location_type":"OFF SITE","work_mode":"WFH"`, true, true, "OFF SITE", "WFH"},
		{"partial_metadata", `,"checkout_metadata_supported":true,"work_mode":"WFH"`, true, true, "", "WFH"},
		{"invalid_location", `,"location_type":"unknown"`, false, false, "", ""},
		{"invalid_mode", `,"work_mode":"unknown"`, false, false, "", ""},
		{"wrong_location_type", `,"location_type":17`, false, false, "", ""},
		{"wrong_mode_type", `,"work_mode":true`, false, false, "", ""},
		{"wrong_capability_type", `,"checkout_metadata_supported":"yes"`, false, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream = `{"data":{"server_time":"2026-11-18T16:30:00+07:00","next_action":"out","write_enabled":true` + tc.extra + `}}`
			w := r.request(t, "GET", "/api/attendance/context", nil, token, false)
			if !tc.valid {
				if w.Code != 503 {
					t.Fatalf("invalid context accepted %d %s", w.Code, w.Body)
				}
				return
			}
			var response struct {
				Data contracts.AttendanceContext `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Data.CheckoutMetadataSupported != tc.supported || response.Data.LocationType != tc.locationType || response.Data.WorkMode != tc.workMode {
				t.Fatalf("metadata context lost %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestAttendanceConfirmedWorkMetadataForwardedOnlyForCheckout(t *testing.T) {
	r := rig(t)
	a := r.add(t, "11447")
	r.enable(t, a)
	token := r.login(t, a.Code)
	previous := r.app.client.Transport
	calls := 0
	r.app.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/attendance/records" {
			return previous.RoundTrip(req)
		}
		calls++
		var body contracts.AttendancePunchRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || !body.CheckoutMetadataConfirmed || body.LocationType != "ON SITE" || body.WorkMode != "Office" || body.ExpectedAction != "out" {
			t.Fatalf("confirmed intent lost %#v %v", body, err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"id":"101","recorded_at":"2026-11-18T16:30:00+07:00","check_type":"out","status":"approved","next_action":"complete"}}`))}, nil
	})
	coordinate, accuracy := 16.0, 5.0
	input := contracts.AttendancePunchRequest{AttendanceLocationRequest: contracts.AttendanceLocationRequest{Latitude: &coordinate, Longitude: &coordinate, AccuracyM: &accuracy, CapturedAt: "2026-11-18T16:30:00+07:00"}, RequestID: "confirmed-metadata-request-001", ExpectedAction: "out", LocationType: "ON SITE", WorkMode: "Office", CheckoutMetadataConfirmed: true}
	if w := r.request(t, "POST", "/api/attendance/records", input, token, false); w.Code != 200 || calls != 1 {
		t.Fatalf("confirmed checkout failed %d %s", w.Code, w.Body)
	}
	input.ExpectedAction = "in"
	if w := r.request(t, "POST", "/api/attendance/records", input, token, false); w.Code != 400 || calls != 1 {
		t.Fatalf("confirmation accepted on checkin %d %s", w.Code, w.Body)
	}
}

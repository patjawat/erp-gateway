package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestMeetingSubtreeRoutesReservedPathsAndMethods(t *testing.T) {
	// Construct the complete Gateway mux so literal options/availability routes
	// cannot regress to conflicting all-method patterns beside the ID routes.
	r := rig(t)
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unauthenticated or invalid route reached upstream: %s %s", request.Method, request.URL.Path)
		return nil, nil
	})
	for _, test := range []struct {
		method, path string
		status       int
		code, allow  string
	}{
		{http.MethodGet, "/api/meetings", 401, "SESSION_EXPIRED", ""},
		{http.MethodPost, "/api/meetings", 401, "SESSION_EXPIRED", ""},
		{http.MethodGet, "/api/meetings/options", 401, "SESSION_EXPIRED", ""},
		{http.MethodGet, "/api/meetings/availability", 401, "SESSION_EXPIRED", ""},
		{http.MethodGet, "/api/meetings/1", 401, "SESSION_EXPIRED", ""},
		{http.MethodPut, "/api/meetings/1", 401, "SESSION_EXPIRED", ""},
		{http.MethodPost, "/api/meetings/1/cancel", 401, "SESSION_EXPIRED", ""},
		{http.MethodPost, "/api/meetings/options", 405, "METHOD_NOT_ALLOWED", "GET"},
		{http.MethodPut, "/api/meetings/options", 405, "METHOD_NOT_ALLOWED", "GET"},
		{http.MethodPost, "/api/meetings/availability", 405, "METHOD_NOT_ALLOWED", "GET"},
		{http.MethodPut, "/api/meetings/availability", 405, "METHOD_NOT_ALLOWED", "GET"},
		{http.MethodOptions, "/api/meetings/options", 405, "METHOD_NOT_ALLOWED", "GET"},
		{http.MethodDelete, "/api/meetings", 405, "METHOD_NOT_ALLOWED", "GET, POST"},
		{http.MethodPost, "/api/meetings/1", 405, "METHOD_NOT_ALLOWED", "GET, PUT"},
		{http.MethodGet, "/api/meetings/1/cancel", 405, "METHOD_NOT_ALLOWED", "POST"},
		{http.MethodGet, "/api/meetings/", 404, "NOT_FOUND", ""},
		{http.MethodGet, "/api/meetings/unknown", 404, "NOT_FOUND", ""},
		{http.MethodPost, "/api/meetings/options/cancel", 404, "NOT_FOUND", ""},
		{http.MethodGet, "/api/meetings/1/unknown", 404, "NOT_FOUND", ""},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := r.request(t, test.method, test.path, nil, "", false)
			var envelope struct {
				Error contracts.Failure `json:"error"`
			}
			if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Code != test.code || response.Header().Get("Allow") != test.allow {
				t.Fatalf("status=%d Allow=%q body=%s", response.Code, response.Header().Get("Allow"), response.Body.String())
			}
		})
	}
}

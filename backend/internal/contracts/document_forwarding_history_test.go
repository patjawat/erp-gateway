package contracts

import "testing"

func TestDocumentForwardingHistoryIsBoundedAndReadOnly(t *testing.T) {
	const path = "/api/documents/101/forwarding-history"
	for _, raw := range []string{"", "page=2&per_page=20&q=Staff"} {
		route, matched, err := ParseNativeDocumentRoute(path, "GET", raw)
		if err != nil || !matched || route.Kind != "forwarding-history" || route.Target != "" || route.Selection.PerPage != 20 {
			t.Fatalf("valid forwarding history: %+v %v", route, err)
		}
		if raw != "" && (route.Selection.Page != 2 || route.Selection.Query != "Staff") {
			t.Fatalf("selection lost: %+v", route.Selection)
		}
	}
	for _, tc := range []struct{ path, method, query string }{
		{path, "POST", ""},
		{path, "PUT", ""},
		{path, "DELETE", ""},
		{path + "/2", "GET", ""},
		{path, "GET", "page=0"},
		{path, "GET", "per_page=101"},
		{path, "GET", "page=1&page=2"},
		{path, "GET", "employee_id=3"},
	} {
		if _, matched, err := ParseNativeDocumentRoute(tc.path, tc.method, tc.query); !matched || err == nil {
			t.Fatalf("accepted invalid history: %+v", tc)
		}
	}
	// Deletions retain the existing route endpoint and its revision contract.
	if route, matched, err := ParseNativeDocumentRoute("/api/documents/101/routes/2", "DELETE", ""); !matched || err != nil || route.Target != "2" {
		t.Fatalf("route deletion changed: %+v %v", route, err)
	}
}

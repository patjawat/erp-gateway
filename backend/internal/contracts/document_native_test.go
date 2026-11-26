package contracts

import "testing"

func TestDocumentNativeRouteValidation(t *testing.T) {
	for _, tc := range []struct{ path, method, query string }{
		{"/api/documents/1/attachments", "GET", "page=2&per_page=20&q=word"},
		{"/api/documents/1/files/2", "GET", "variant=original&download=1"},
		{"/api/documents/1/templates/2", "DELETE", ""},
		{"/api/documents/1/operations/request-id-native-001", "GET", ""},
		{"/api/documents/1/task-people", "GET", "unit_id=2&page=1"},
	} {
		if _, matched, e := ParseNativeDocumentRoute(tc.path, tc.method, tc.query); !matched || e != nil {
			t.Fatal(tc, e)
		}
	}
	for _, q := range []string{"page=0", "page=1&page=2", "per_page=101", "emp_id=2", "hospital=2", "unit_id=2"} {
		if _, _, e := ParseNativeDocumentRoute("/api/documents/1/templates", "GET", q); e == nil {
			t.Fatal(q)
		}
	}
	if _, matched, e := ParseNativeDocumentRoute("/api/documents/1/attachments/2", "GET", ""); matched || e != nil {
		t.Fatal("legacy file route intercepted")
	}
	if _, _, e := ParseNativeDocumentRoute("/api/documents/1/files/2", "GET", "variant=../../secret"); e == nil {
		t.Fatal("invalid variant accepted")
	}
}

func TestDocumentNativeMutationValidation(t *testing.T) {
	b := true
	if !ValidDocumentNativeInput("bookmark", "PUT", DocumentNativeInput{Bookmark: &b}) {
		t.Fatal("explicit bookmark rejected")
	}
	if ValidDocumentNativeInput("bookmark", "PUT", DocumentNativeInput{}) {
		t.Fatal("implicit toggle accepted")
	}
	if ValidDocumentNativeInput("templates", "POST", DocumentNativeInput{Text: "text", RequestID: "short"}) {
		t.Fatal("invalid idempotency key accepted")
	}
	if ValidDocumentNativeInput("templates", "POST", DocumentNativeInput{Text: "text", TargetID: "2", RequestID: "native-template-create-001"}) {
		t.Fatal("extraneous target accepted")
	}
	if ValidDocumentNativeInput("routes", "DELETE", DocumentNativeInput{RequestID: "native-route-delete-001"}) {
		t.Fatal("delete without revision accepted")
	}
	if DocumentNativeBytesMatch("application/pdf", []byte("<html>not a PDF</html>")) {
		t.Fatal("forged binary accepted")
	}
	if !DocumentNativeBytesMatch("application/octet-stream", []byte("<html>stored attachment</html>")) {
		t.Fatal("generic stored file rejected")
	}
	if DocumentNativeBytesMatch("application/octet-stream", nil) {
		t.Fatal("empty generic download accepted")
	}
	if DocumentNativeBytesMatch("text/html", []byte("<html>active MIME</html>")) {
		t.Fatal("active content MIME accepted")
	}
}

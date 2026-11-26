package contracts

import (
	"strings"
	"testing"
)

func TestDocumentWorkflowValidation(t *testing.T) {
	in := DocumentCommentInput{Text: "test", RequestID: "12345678-1234-1234-1234-123456789012"}
	if !ValidateDocumentComment(in) {
		t.Fatal("valid rejected")
	}
	in.Text = strings.Repeat("ก", 5001)
	if ValidateDocumentComment(in) {
		t.Fatal("oversize")
	}
	for _, q := range []string{"emp_id=2", "kind=all", "page=0", "comment_id=../2", "q=a&q=b"} {
		if _, e := DocumentOptionsQuery(q); e == nil {
			t.Fatal(q)
		}
	}
}

func TestDocumentWorkflowPageQuery(t *testing.T) {
	selection, err := DocumentWorkflowQuery("")
	if err != nil || selection.Page != 1 || selection.PerPage != 20 {
		t.Fatal(selection, err)
	}
	selection, err = DocumentWorkflowQuery("page=3&per_page=20")
	if err != nil || selection.Page != 3 || selection.PerPage != 20 {
		t.Fatal(selection, err)
	}
	for _, raw := range []string{"page=0", "page=100001", "per_page=0", "per_page=101", "page=1&page=2", "emp_id=2", "page=", "per_page=1.5"} {
		if _, err := DocumentWorkflowQuery(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

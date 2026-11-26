package gateway

import (
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestDocumentCommentDepartmentMetadataIsOptionalAndBounded(t *testing.T) {
	comment := contracts.DocumentComment{
		ID: "1", AuthorName: "Author", AuthorDepartmentName: "Department",
		Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{},
	}
	page := func() *contracts.DocumentComments {
		return &contracts.DocumentComments{Items: []contracts.DocumentComment{comment}, Page: 1, PerPage: 20, TotalCount: 1}
	}
	for _, name := range []string{"Department", ""} {
		comment.AuthorDepartmentName = name
		if !validDocumentWorkflow(&comment) || !validDocumentWorkflow(page()) {
			t.Fatal("valid optional author department rejected")
		}
	}
	comment.AuthorDepartmentName = strings.Repeat("x", 2001)
	if validDocumentWorkflow(&comment) || validDocumentWorkflow(page()) {
		t.Fatal("oversized author department accepted")
	}
}

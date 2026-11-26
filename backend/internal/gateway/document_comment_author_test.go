package gateway

import (
	"erp-platform/gateway/internal/contracts"
	"testing"
)

func TestDocumentCommentAuthorPhotoIDsAreOptionalAndValidated(t *testing.T) {
	for _, employee := range []string{"", "2", "123456"} {
		comment := contracts.DocumentComment{ID: "901", AuthorEmployeeID: employee, Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}
		page := &contracts.DocumentComments{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentComment{comment}}
		if !validDocumentWorkflow(&comment) || !validDocumentWorkflow(page) {
			t.Fatal("valid/legacy optional photo identity rejected", employee)
		}
	}
	for _, employee := range []string{"0", "-2", "2.5", "../../private", "user-20"} {
		comment := contracts.DocumentComment{ID: "901", AuthorEmployeeID: employee, Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}
		page := &contracts.DocumentComments{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentComment{comment}}
		if validDocumentWorkflow(&comment) || validDocumentWorkflow(page) {
			t.Fatal("invalid author photo identity accepted", employee)
		}
	}
}

package gateway

import (
	"encoding/json"
	"erp-platform/gateway/internal/contracts"
	"strings"
	"testing"
)

func TestDocumentGivenNamesAreOptionalBoundedResponseFields(t *testing.T) {
	for _, name := range []string{"", "นางวันดี", "นายพิทักษ์", strings.Repeat("x", 2000)} {
		comment := contracts.DocumentComment{ID: "1", AuthorGivenName: name, Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}
		comments := &contracts.DocumentComments{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentComment{comment}}
		timeline := &contracts.DocumentTimeline{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentTimelineItem{{ID: "1", ActorGivenName: name}}}
		native := &contracts.DocumentNativePage{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentNativeItem{{ID: "1", GivenName: name}}}
		if !validDocumentWorkflow(&comment) || !validDocumentWorkflow(comments) || !validDocumentWorkflow(timeline) || !validDocumentNative(native, contracts.DocumentNativeRoute{Selection: contracts.DocumentNativeSelection{Page: 1, PerPage: 20}}) {
			t.Fatal("valid optional given name rejected", len(name))
		}
	}
	tooLong := strings.Repeat("x", 2001)
	comment := contracts.DocumentComment{ID: "1", AuthorGivenName: tooLong, Employees: []contracts.DocumentRecipient{}, Departments: []contracts.DocumentRecipient{}}
	comments := &contracts.DocumentComments{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentComment{comment}}
	timeline := &contracts.DocumentTimeline{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentTimelineItem{{ID: "1", ActorGivenName: tooLong}}}
	native := &contracts.DocumentNativePage{Page: 1, PerPage: 20, TotalCount: 1, Items: []contracts.DocumentNativeItem{{ID: "1", GivenName: tooLong}}}
	if validDocumentWorkflow(&comment) || validDocumentWorkflow(comments) || validDocumentWorkflow(timeline) || validDocumentNative(native, contracts.DocumentNativeRoute{Selection: contracts.DocumentNativeSelection{Page: 1, PerPage: 20}}) {
		t.Fatal("unbounded given-name response accepted")
	}
	for _, value := range []any{
		contracts.DocumentComment{ID: "1"},
		contracts.DocumentTimelineItem{ID: "1"},
		contracts.DocumentNativeItem{ID: "1"},
	} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), "given_name") {
			t.Fatal("absent metadata changed a legacy response", string(raw), err)
		}
	}
}

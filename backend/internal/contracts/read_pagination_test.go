package contracts

import (
	"encoding/json"
	"testing"
)

func TestReadPaginationStrictCanonicalBounds(t *testing.T) {
	for _, raw := range []string{"page=0", "page=01", "page=1000001", "per_page=101", "page=1&page=2", "per_page=1&per_page=2", "page=%ZZ", "page[]=2", "q=a&q=b"} {
		selection, rest, err := ReadPagination(raw)
		if err == nil {
			if _, bad := rest["page[]"]; bad {
				continue
			} // domain allowlist rejects the foreign key
			t.Fatalf("accepted %q %#v %#v", raw, selection, rest)
		}
	}
	selection, rest, err := ReadPagination("page=2&per_page=2&q=term")
	if err != nil || selection.Page != 2 || selection.PerPage != 2 || !selection.Explicit || rest.Encode() != "q=term" {
		t.Fatalf("wrong bounded selection %#v %#v %v", selection, rest, err)
	}
}

func TestReadPaginationRequiresCompleteMetadataAndCountConsistency(t *testing.T) {
	selection := ReadPageSelection{Page: 2, PerPage: 2, Explicit: true}
	for _, raw := range []string{
		`{"page":2,"per_page":2,"total_count":3,"has_more":false}`,
		`{"page":2,"per_page":2,"total_count":0,"has_more":false}`,
	} {
		var metadata ReadPageMetadata
		if json.Unmarshal([]byte(raw), &metadata) != nil {
			t.Fatal("fixture")
		}
		length := 1
		if *metadata.TotalCount == 0 {
			length = 0
		}
		if !ValidReadPage(&metadata, length, selection) {
			t.Fatalf("valid page rejected %s", raw)
		}
	}
	for _, raw := range []string{`{"page":2,"per_page":2}`, `{"page":2,"per_page":2,"total_count":3}`, `{"page":2,"per_page":2,"has_more":false}`, `{"page":2,"per_page":2,"total_count":4,"has_more":false}`, `{"page":2,"per_page":2,"total_count":3,"has_more":true}`, `{"page":2,"per_page":null,"total_count":3,"has_more":false}`} {
		var metadata ReadPageMetadata
		if json.Unmarshal([]byte(raw), &metadata) != nil || ValidReadPage(&metadata, 1, selection) {
			t.Fatalf("invalid page accepted %s", raw)
		}
	}
	if ValidReadPage(nil, 0, selection) || !ValidReadPage(nil, 0, ReadPageSelection{Page: 1, PerPage: 50}) {
		t.Fatal("legacy optional metadata behavior changed")
	}
}

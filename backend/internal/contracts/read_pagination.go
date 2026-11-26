package contracts

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
)

// ReadPageMetadata is optional on legacy responses. Present fields describe
// one bounded server page and are checked against the request by Gateway.
type ReadPageMetadata struct {
	Page       *int  `json:"page"`
	PerPage    *int  `json:"per_page"`
	TotalCount *int  `json:"total_count"`
	HasMore    *bool `json:"has_more"`
}
type ReadPage[T any] struct {
	Items []T `json:"items"`
	*ReadPageMetadata
}
type ReadPageSelection struct {
	Page, PerPage int
	Explicit      bool
}

var readPageInteger = regexp.MustCompile(`^[1-9][0-9]{0,6}$`)

// Remove only canonical page selectors; the caller retains its domain parser.
func ReadPagination(raw string) (ReadPageSelection, url.Values, error) {
	selection := ReadPageSelection{Page: 1, PerPage: 50}
	values, err := url.ParseQuery(raw)
	invalid := errors.New("invalid read pagination")
	if err != nil || len(raw) > 2048 {
		return selection, nil, invalid
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return selection, nil, invalid
		}
		if key != "page" && key != "per_page" {
			continue
		}
		if !readPageInteger.MatchString(entries[0]) {
			return selection, nil, invalid
		}
		n, err := strconv.Atoi(entries[0])
		if err != nil || key == "page" && n > 1000000 || key == "per_page" && n > 100 {
			return selection, nil, invalid
		}
		selection.Explicit = true
		if key == "page" {
			selection.Page = n
		} else {
			selection.PerPage = n
		}
		values.Del(key)
	}
	return selection, values, nil
}

func ValidReadPage(metadata *ReadPageMetadata, length int, selection ReadPageSelection) bool {
	if metadata == nil {
		return !selection.Explicit
	}
	if metadata.Page == nil || metadata.PerPage == nil || metadata.TotalCount == nil || metadata.HasMore == nil {
		return false
	}
	page, perPage, total := *metadata.Page, *metadata.PerPage, *metadata.TotalCount
	if page != selection.Page || perPage != selection.PerPage || page < 1 || page > 1000000 || perPage < 1 || perPage > 100 || total < 0 {
		return false
	}
	expected := total - (page-1)*perPage
	if expected < 0 {
		expected = 0
	}
	if expected > perPage {
		expected = perPage
	}
	return length == expected && *metadata.HasMore == (page*perPage < total)
}

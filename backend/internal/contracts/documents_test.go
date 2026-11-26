package contracts

import "testing"

func TestDocumentQuery(t *testing.T) {
	for _, q := range []string{"employee=2", "hospital=3", "page=0", "per_page=101", "status=unknown", "year=2026", "q=a&q=b"} {
		if _, e := DocumentQuery(q); e == nil {
			t.Fatal(q)
		}
	}
	if s, e := DocumentQuery("q=R100&year=2569&status=bookmarked&page=2&per_page=20"); e != nil || s.Status != "bookmarked" || s.Page != 2 {
		t.Fatal(s, e)
	}
	if s, e := DocumentQuery("q=R100&year=2569&status=unread&page=2&per_page=20"); e != nil || s.Year != 2569 {
		t.Fatal(s, e)
	}
}

package contracts

import "testing"

func TestRepairAssetPaginationValidationAndLegacyDefaults(t *testing.T) {
	for _, test := range []struct {
		raw        string
		page, size int
	}{
		{"q=printer&repair_group=2", 1, 50},
		{"q=printer&repair_group=2&page=3&per_page=20", 3, 20},
	} {
		query, err := RepairAssetQuery(test.raw)
		if err != nil {
			t.Fatal(err)
		}
		page, size, err := RepairAssetPagination(query)
		if err != nil || page != test.page || size != test.size {
			t.Fatalf("%s: %d/%d %v", test.raw, page, size, err)
		}
	}
	for _, raw := range []string{"page=0", "page=1000001", "page=one", "per_page=0", "per_page=101", "page=1&page=2", "emp_id=17", "device_type_id=COM"} {
		if _, err := RepairAssetQuery(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

package contracts

import "testing"

func TestProfileRecordsQueryBoundsAndOwnershipSelectors(t *testing.T) {
	for _, raw := range []string{"section=position&emp_id=18", "section=position&section=kpi", "section=position&page=0", "section=position&page=100001", "section=position&per_page=51", "section=position&parent_id=1", "section=jd_sections", "section=jd_items&parent_id=0", "section=jd_items&parent_id=-1", "section=unknown", "section=position&page="} {
		if _, err := ProfileRecordsQuery(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"section=position", "section=jd_sections&parent_id=17&page=2&per_page=10"} {
		q, err := ProfileRecordsQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := ProfileRecordsQuery(q.Query()); err != nil || again != q {
			t.Fatal("query does not roundtrip")
		}
	}
}

package contracts

import "testing"

func TestSalaryQueryRejectsIdentityOverridesAndInvalidPagination(t *testing.T) {
	for _, raw := range []string{"employee_id=99", "hospital_code=12448", "page=0", "page=10001", "page=01", "page=-1", "page_size=51", "page_size=0", "page=1&page=2", "page=%zz"} {
		if _, err := SalaryQuery(raw); err == nil {
			t.Errorf("invalid query accepted: %s", raw)
		}
	}
	if got, err := SalaryQuery(""); err != nil || got != (SalarySelection{Page: 1, PageSize: 20}) {
		t.Fatalf("default pagination: %#v %v", got, err)
	}
}

func TestSalaryDecimalNeverRoundsOrUsesFloatingPoint(t *testing.T) {
	for input, expected := range map[string]string{"0": "0.00", "-0.0": "0.00", "0001.2": "1.20", "-100.01": "-100.01", "9999999999999.99": "9999999999999.99"} {
		if actual, ok := SalaryDecimal(input); !ok || actual != expected {
			t.Errorf("%q -> %q %t", input, actual, ok)
		}
	}
	for _, input := range []string{"", "NaN", "1e5", "1.001", "+1.00", "10000000000000.00", " 1.00", "null"} {
		if _, ok := SalaryDecimal(input); ok {
			t.Errorf("invalid amount accepted: %q", input)
		}
	}
}

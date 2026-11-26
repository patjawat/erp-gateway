package contracts

import (
	"strings"
	"testing"
)

func TestRepairRequestValidation(t *testing.T) {
	valid := RepairCreate{RequestID: "12345678-1234-4234-a234-123456789abc", Title: " เครื่องไม่ทำงาน ", DeviceTypeID: "COM", RepairGroup: "2", Location: " ห้องทำงาน ", Phone: "1234", Urgency: "medium"}
	if err := ValidateRepairCreate(&valid); err != nil || valid.Title != "เครื่องไม่ทำงาน" || valid.Location != "ห้องทำงาน" {
		t.Fatalf("valid Thai repair rejected or not normalized: %#v %v", valid, err)
	}
	for _, date := range []string{"2000-01-01", "9999-01-01 "} {
		value := valid
		value.RequestRepairDate = date
		if err := ValidateRepairCreate(&value); err != nil {
			t.Fatalf("valid date rejected before idempotency lookup: %v", err)
		}
	}
	for _, mutate := range []func(*RepairCreate){
		func(v *RepairCreate) { v.RequestID = "short" },
		func(v *RepairCreate) { v.Title = strings.Repeat("ก", 256) },
		func(v *RepairCreate) { v.DeviceTypeID = "" },
		func(v *RepairCreate) { v.RepairGroup = "4" },
		func(v *RepairCreate) { v.Phone = "" },
		func(v *RepairCreate) { v.Location = "\x00" },
		func(v *RepairCreate) { v.Urgency = "normal" },
		func(v *RepairCreate) { v.RequestRepairDate = "2026-02-30" },
	} {
		value := valid
		mutate(&value)
		if err := ValidateRepairCreate(&value); err == nil {
			t.Fatalf("invalid repair accepted: %#v", value)
		}
	}
}

func TestRepairQueryValidation(t *testing.T) {
	query, err := RepairAssetQuery("q=+%E0%B8%81+&repair_group=2")
	if err != nil || query.Get("q") != "ก" || query.Get("repair_group") != "2" {
		t.Fatalf("valid query rejected: %v %v", query, err)
	}
	for _, query := range []string{"hospital_code=11447", "emp_id=9", "q=a&q=b", "q=%zz", "repair_group=4", "q=" + strings.Repeat("a", 101)} {
		if _, err := RepairAssetQuery(query); err == nil {
			t.Fatalf("invalid query accepted: %s", query)
		}
	}
}

package contracts

import (
	"net/url"
	"strings"
	"testing"
)

func TestRepairExactAssetCodeQuery(t *testing.T) {
	code := "  ครุภัณฑ์/COM-01 &%  "
	query, err := RepairAssetQuery(url.Values{"code": {code}, "page": {"1"}, "per_page": {"2"}}.Encode())
	if err != nil || query.Get("code") != strings.TrimSpace(code) || query.Has("q") {
		t.Fatalf("exact code normalization: %v %v", query, err)
	}
	for _, raw := range []string{
		"code=", "code=+", "code=A&code=B", "code=A&q=A", "code=A&emp_id=17", "code=A&hospital_code=11447",
		url.Values{"code": {strings.Repeat("ก", 101)}}.Encode(), "code=A%00B",
	} {
		if _, err := RepairAssetQuery(raw); err == nil {
			t.Fatalf("accepted invalid exact lookup %q", raw)
		}
	}
}

func TestRepairProblemAndSeparateNoteValidation(t *testing.T) {
	create := RepairCreate{RequestID: "test-request-000001", Title: "สั้น", DeviceTypeID: "printer", RepairGroup: "2", Location: "ห้อง1", Phone: "1234", Urgency: "medium", Problem: " อาการเต็ม ", Note: " ติดต่อก่อน "}
	if err := ValidateRepairCreate(&create); err != nil || create.Problem != "อาการเต็ม" || create.Note != "ติดต่อก่อน" {
		t.Fatalf("separate fields: %#v %v", create, err)
	}
	create.Problem = strings.Repeat("ก", 4001)
	if err := ValidateRepairCreate(&create); err == nil {
		t.Fatal("unbounded full problem accepted")
	}
	edit := RepairEdit{Title: "สั้น", Location: "ห้อง1", Phone: "1234", Urgency: "medium", Problem: "อาการเต็ม", Note: "ติดต่อก่อน"}
	if err := ValidateRepairEdit(&edit); err != nil {
		t.Fatal(err)
	}
	edit.Problem += "\x00"
	if err := ValidateRepairEdit(&edit); err == nil {
		t.Fatal("invalid full problem accepted on edit")
	}
}

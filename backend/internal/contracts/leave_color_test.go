package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLeaveOptionsDiscardsLegacyERPColors(t *testing.T) {
	var options LeaveOptions
	err := json.Unmarshal([]byte(`{"types":[{"id":"LT4","name":"Vacation","color":"#FF0000"}],"balances":[{"leave_type_id":"LT4","leave_type":"Vacation","color":"#FF0000","remaining_days":7.5}],"can_submit":true,"fiscal_year":2570}`), &options)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(options)
	if err != nil || strings.Contains(string(body), `"color"`) {
		t.Fatalf("ERP colors survived: %s %v", body, err)
	}
	if options.Types[0].ID != "LT4" || *options.Balances[0].RemainingDays != 7.5 || !options.CanSubmit {
		t.Fatalf("leave options changed: %#v", options)
	}
}

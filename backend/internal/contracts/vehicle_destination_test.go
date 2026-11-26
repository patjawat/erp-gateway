package contracts

import (
	"net/url"
	"strings"
	"testing"
)

func TestVehicleDestinationValidationAndQueries(t *testing.T) {
	if kind, _, ok := VehicleRoute("/api/vehicles/destinations"); !ok || kind != "destinations" {
		t.Fatal("destination route not recognized")
	}
	for _, name := range []string{"", "  ", strings.Repeat("ก", 256), "place\x00name", "place\tname", "place\nname"} {
		input := VehicleDestinationInput{Name: name}
		if len(ValidateVehicleDestinationInput(&input)) == 0 {
			t.Fatalf("accepted destination %q", name)
		}
	}
	input := VehicleDestinationInput{Name: "  โรงพยาบาล  "}
	if len(ValidateVehicleDestinationInput(&input)) != 0 || input.Name != "โรงพยาบาล" {
		t.Fatalf("valid Thai name rejected %#v", input)
	}
	input.Name = strings.Repeat("ก", 255)
	if len(ValidateVehicleDestinationInput(&input)) != 0 {
		t.Fatal("max valid Unicode length rejected")
	}
	query := url.Values{"q": {"  " + input.Name + " "}}.Encode()
	if got, err := VehicleDestinationQuery(query); err != nil || got != input.Name {
		t.Fatalf("Unicode query failed %v", err)
	}
	for _, raw := range []string{"q=one&q=two", "q=%00", "q=one%0Atwo", "scope=all", "q=%zz"} {
		if _, err := VehicleDestinationQuery(raw); err == nil {
			t.Fatalf("accepted query %s", raw)
		}
	}
	if _, err := VehicleDestinationQuery(""); err != nil {
		t.Fatal("blank query rejected")
	}
	if ValidVehicleDestinations(nil) || ValidVehicleDestinations([]VehicleDestination{{ID: "1", Name: "A"}, {ID: "1", Name: "B"}}) || ValidVehicleDestination(VehicleDestination{ID: "1", Name: " "}) {
		t.Fatal("malformed directory accepted")
	}
}

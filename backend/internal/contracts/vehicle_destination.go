package contracts

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

type VehicleDestination struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type VehicleDestinationList struct {
	Items    []VehicleDestination `json:"items"`
	CanWrite bool                 `json:"can_write"`
}

type VehicleDestinationInput struct {
	Name string `json:"name"`
}

func VehicleDestinationText(value string, required bool) bool {
	return meetingText(value, 255, required) && strings.IndexFunc(value, unicode.IsControl) < 0 && (!required || strings.TrimSpace(value) != "")
}

func ValidateVehicleDestinationInput(input *VehicleDestinationInput) map[string]string {
	fields := map[string]string{}
	if input == nil {
		fields["name"] = "กรุณาระบุชื่อจุดหมายปลายทาง"
		return fields
	}
	input.Name = strings.TrimSpace(input.Name)
	if !VehicleDestinationText(input.Name, true) {
		fields["name"] = "กรุณาระบุชื่อจุดหมายปลายทางไม่เกิน 255 ตัวอักษร"
	}
	return fields
}

func VehicleDestinationQuery(raw string) (string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 4096 {
		return "", errors.New("invalid destination query")
	}
	for key, items := range values {
		if key != "q" || len(items) != 1 {
			return "", errors.New("invalid destination query")
		}
	}
	query := strings.TrimSpace(values.Get("q"))
	if !VehicleDestinationText(query, false) {
		return "", errors.New("invalid destination query")
	}
	return query, nil
}

func ValidVehicleDestination(item VehicleDestination) bool {
	return VehicleDestinationText(item.ID, true) && VehicleDestinationText(item.Name, true)
}

func ValidVehicleDestinations(items []VehicleDestination) bool {
	if items == nil || len(items) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !ValidVehicleDestination(item) || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

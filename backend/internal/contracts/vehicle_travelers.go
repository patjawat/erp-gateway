package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type VehicleBookingTraveler struct {
	ID string `json:"id"`
	VehicleTraveler
	Position string `json:"position"`
}

type VehicleTravelers struct {
	BookingID  string                   `json:"booking_id"`
	Items      []VehicleBookingTraveler `json:"items"`
	Page       int                      `json:"page"`
	PerPage    int                      `json:"per_page"`
	TotalCount int                      `json:"total_count"`
	HasMore    bool                     `json:"has_more"`
}

func VehicleTravelersRoute(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/vehicles/"), "/")
	if strings.HasPrefix(path, "/api/vehicles/") && len(parts) == 2 && parts[1] == "travelers" && ValidMeetingID(parts[0]) {
		return parts[0], true
	}
	return "", false
}

func VehiclePagedTravelersDetail(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	q, err := url.ParseQuery(raw)
	if err != nil || len(q) != 1 || len(q["travelers"]) != 1 || q.Get("travelers") != "paged" {
		return false, errors.New("invalid detail selection")
	}
	return true, nil
}

func VehicleTravelersQuery(raw string) (VehicleSelection, error) {
	out, err := VehicleDriverMissionsQuery(raw)
	q, _ := url.ParseQuery(raw)
	if _, exists := q["per_page"]; !exists {
		out.PerPage = 20
	}
	if err != nil || out.PerPage > 20 {
		return out, errors.New("invalid traveler page")
	}
	return out, nil
}

func ValidVehicleTravelers(out *VehicleTravelers, id string, page, perPage int) bool {
	if out.BookingID != id || !ValidMeetingID(id) || out.Items == nil || out.Page != page || out.PerPage != perPage || page < 1 || perPage < 1 || perPage > 20 || out.TotalCount < 0 || out.TotalCount > 1000 || out.HasMore != (page*perPage < out.TotalCount) {
		return false
	}
	expected := out.TotalCount - (page-1)*perPage
	if expected < 0 {
		expected = 0
	}
	if expected > perPage {
		expected = perPage
	}
	if len(out.Items) != expected {
		return false
	}
	for index, item := range out.Items {
		if item.ID != strconv.Itoa((page-1)*perPage+index+1) || item.Name == "" || len(item.Name) > 1000 || len(item.Position) > 1000 || len(item.Department) > 1000 || (item.EmployeeID != "" && !ValidMeetingID(item.EmployeeID)) || (item.IsExternal && item.EmployeeID != "") {
			return false
		}
	}
	return true
}

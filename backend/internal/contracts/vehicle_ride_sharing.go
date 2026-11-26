package contracts

import "strings"

type VehicleRideRequest struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Position   string `json:"position"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}
type VehicleRideSharing struct {
	SummaryOnly bool                 `json:"summary_only"`
	BookingID   string               `json:"booking_id"`
	IsOwner     bool                 `json:"is_owner"`
	TripOpen    bool                 `json:"trip_open"`
	CanRequest  bool                 `json:"can_request"`
	CanReview   bool                 `json:"can_review"`
	OwnRequest  *VehicleRideRequest  `json:"own_request,omitempty"`
	Items       []VehicleRideRequest `json:"items"`
	Page        int                  `json:"page"`
	PerPage     int                  `json:"per_page"`
	TotalCount  int                  `json:"total_count"`
	HasMore     bool                 `json:"has_more"`
}
type VehicleRideInput struct {
	RequestID string `json:"request_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
}

func VehicleRideRoute(path string) (booking, request string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/vehicles/"), "/")
	if !strings.HasPrefix(path, "/api/vehicles/") || (len(parts) != 2 && len(parts) != 3) || !ValidMeetingID(parts[0]) || parts[1] != "ride-sharing" {
		return
	}
	if len(parts) == 3 {
		if !vehicleMissionRequestPattern.MatchString(parts[2]) {
			return
		}
		request = parts[2]
	}
	return parts[0], request, true
}
func ValidVehicleRideRequest(v *VehicleRideRequest) bool {
	return v != nil && vehicleMissionRequestPattern.MatchString(v.ID) && ValidMeetingID(v.EmployeeID) && v.Name != "" && len(v.Name) <= 1000 && len(v.Position) <= 1000 && (v.Status == "pending" || v.Status == "approved" || v.Status == "rejected") && len(v.CreatedAt) <= 32
}
func ValidVehicleRideSharing(v *VehicleRideSharing, id string, page, perPage int) bool {
	if v.BookingID != id || !ValidMeetingID(id) || v.Items == nil || v.Page != page || v.PerPage != perPage || page < 1 || perPage < 1 || perPage > 20 || v.TotalCount < 0 || v.TotalCount > 1000 || v.HasMore != (page*perPage < v.TotalCount) || (v.CanRequest && (v.IsOwner || !v.TripOpen || v.OwnRequest != nil)) || (v.CanReview && !v.IsOwner) || (v.OwnRequest != nil && !ValidVehicleRideRequest(v.OwnRequest)) {
		return false
	}
	expected := v.TotalCount - (page-1)*perPage
	if expected < 0 {
		expected = 0
	}
	if expected > perPage {
		expected = perPage
	}
	if v.SummaryOnly {
		if page != 1 {
			return false
		}
		expected = 0
	}
	if len(v.Items) != expected || (!v.IsOwner && (v.TotalCount != 0 || len(v.Items) != 0)) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if !ValidVehicleRideRequest(&item) || item.Status != "pending" || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

package contracts

import (
	"errors"
	"math"
	"net/url"
	"strings"
)

type VehicleAssetHistoryItem struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Code             string   `json:"code"`
	StatusLabel      string   `json:"status_label"`
	StartDate        string   `json:"start_date"`
	EndDate          string   `json:"end_date"`
	CreatedAt        string   `json:"created_at"`
	Remark           string   `json:"remark"`
	Operator         string   `json:"operator"`
	Amount           *float64 `json:"amount"`
	CTPLCompany      string   `json:"ctpl_company"`
	CTPLStart        string   `json:"ctpl_start"`
	CTPLEnd          string   `json:"ctpl_end"`
	InsuranceCompany string   `json:"insurance_company"`
	InsuranceStart   string   `json:"insurance_start"`
	InsuranceEnd     string   `json:"insurance_end"`
}
type VehicleAssetHistory struct {
	AssetID    string                    `json:"asset_id"`
	Section    string                    `json:"section"`
	Items      []VehicleAssetHistoryItem `json:"items"`
	Page       int                       `json:"page"`
	PerPage    int                       `json:"per_page"`
	TotalCount int                       `json:"total_count"`
	HasMore    bool                      `json:"has_more"`
}

func VehicleAssetHistoryRoute(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/vehicles/assets/"), "/")
	if strings.HasPrefix(path, "/api/vehicles/assets/") && len(parts) == 2 && parts[1] == "history" && ValidMeetingID(parts[0]) {
		return parts[0], true
	}
	return "", false
}
func VehicleAssetHistoryQuery(raw string) (string, VehicleSelection, error) {
	q, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 2048 || len(q["section"]) != 1 {
		return "", VehicleSelection{}, errors.New("invalid asset history selection")
	}
	section := q.Get("section")
	if section != "tax" && section != "repairs" && section != "pm" {
		return "", VehicleSelection{}, errors.New("invalid asset history section")
	}
	q.Del("section")
	selection, err := VehicleTravelersQuery(q.Encode())
	return section, selection, err
}
func ValidVehicleAssetHistory(out *VehicleAssetHistory, id, section string, page, perPage int) bool {
	if out.AssetID != id || !ValidMeetingID(id) || out.Section != section || out.Items == nil || out.Page != page || out.PerPage != perPage || page < 1 || perPage < 1 || perPage > 20 || out.TotalCount < 0 || out.HasMore != (page*perPage < out.TotalCount) {
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
	seen := map[string]bool{}
	for _, item := range out.Items {
		if !ValidMeetingID(item.ID) || seen[item.ID] || item.Title == "" || len(item.Title) > 4000 || len(item.Remark) > 16000 || len(item.Operator) > 1000 || len(item.CTPLCompany) > 2000 || len(item.InsuranceCompany) > 2000 || (item.Amount != nil && (math.IsNaN(*item.Amount) || math.IsInf(*item.Amount, 0) || *item.Amount < 0 || *item.Amount > 1e12)) {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

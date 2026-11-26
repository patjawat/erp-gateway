package contracts

import (
	"errors"
	"net/url"
	"strconv"
	"time"
)

type VehicleCMISelection struct {
	Page, PerPage int
	Status, Date  string
}

type VehicleCMIPage struct {
	Items      []VehicleDirectoryItem `json:"items"`
	Page       int                    `json:"page"`
	PerPage    int                    `json:"per_page"`
	TotalCount int                    `json:"total_count"`
	HasMore    bool                   `json:"has_more"`
}

func VehicleCMIQuery(raw string) (VehicleCMISelection, error) {
	q := VehicleCMISelection{Page: 1, PerPage: 20}
	values, err := url.ParseQuery(raw)
	invalid := errors.New("invalid vehicle policy filter")
	if err != nil || len(raw) > 1024 {
		return q, invalid
	}
	for key, list := range values {
		if len(list) != 1 {
			return q, invalid
		}
		value := list[0]
		switch key {
		case "status":
			q.Status = value
		case "date":
			d, e := time.Parse(time.DateOnly, value)
			if e != nil || d.Format(time.DateOnly) != value || d.Year() < 1900 || d.Year() > 2200 {
				return q, invalid
			}
			q.Date = value
		case "page", "per_page":
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 || (key == "per_page" && n > 100) || (key == "page" && n > 100000) {
				return q, invalid
			}
			if key == "page" {
				q.Page = n
			} else {
				q.PerPage = n
			}
		default:
			return q, invalid
		}
	}
	if q.Status != "expired" && q.Status != "expiring" && q.Status != "missing" {
		return q, invalid
	}
	return q, nil
}

func ValidVehicleCMIPage(v *VehicleCMIPage, q VehicleCMISelection) bool {
	if v == nil || v.Items == nil || v.Page != q.Page || v.PerPage != q.PerPage || v.TotalCount < 0 || len(v.Items) > v.PerPage || v.HasMore != (v.Page*v.PerPage < v.TotalCount) {
		return false
	}
	seen := map[string]bool{}
	expected := v.TotalCount - (q.Page-1)*q.PerPage
	if expected < 0 {
		expected = 0
	}
	if expected > q.PerPage {
		expected = q.PerPage
	}
	if len(v.Items) != expected {
		return false
	}
	for _, item := range v.Items {
		if !ValidMeetingID(item.ID) || seen[item.ID] || item.Name == "" || item.CMIStatus != q.Status || (item.CMIExpiryDate == "" && q.Status != "missing") || !ValidVehicleCMI(item) || !ValidVehiclePhotoURL(item.ImageURL, "vehicle") {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

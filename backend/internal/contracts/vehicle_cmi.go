package contracts

import "time"

func ValidVehicleCMI(v VehicleDirectoryItem) bool {
	if v.CMIExpiryDate == "" {
		return v.CMIStatus == "" || v.CMIStatus == "missing"
	}
	d, err := time.Parse(time.DateOnly, v.CMIExpiryDate)
	if err != nil || d.Format(time.DateOnly) != v.CMIExpiryDate {
		return false
	}
	return v.CMIStatus == "" || v.CMIStatus == "expiring" || v.CMIStatus == "expired"
}

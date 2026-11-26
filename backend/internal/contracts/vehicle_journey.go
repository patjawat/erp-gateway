package contracts

import (
	"strings"
	"unicode"
)

type VehicleFormChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type VehicleFormOptions struct {
	VehicleTypes []VehicleFormChoice `json:"vehicle_types"`
	ReferTypes   []VehicleFormChoice `json:"refer_types"`
	Urgencies    []VehicleFormChoice `json:"urgencies"`
	CanWrite     bool                `json:"can_write"`
}

func EffectiveVehicleType(value string) string {
	if value == "ambulance" || value == "personal" {
		return value
	}
	return "official"
}

func validateVehicleJourney(v *VehicleInput, fields map[string]string) {
	for _, field := range []struct {
		value *string
		key   string
		max   int
	}{
		{&v.VehicleType, "vehicle_type", 32}, {&v.RequestMode, "request_mode", 32},
		{&v.ReferType, "refer_type", 128}, {&v.Urgency, "urgency", 128},
		{&v.ContactName, "contact_name", 255}, {&v.PatientName, "patient_name", 255},
		{&v.PatientHN, "patient_hn", 100}, {&v.PatientAge, "patient_age", 100},
		{&v.PatientSymptom, "patient_symptom", 4000},
		{&v.PersonalLicensePlate, "personal_license_plate", 255},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if !meetingText(*field.value, field.max, false) {
			fields[field.key] = "กรุณาระบุข้อมูลให้ถูกต้อง"
		}
	}
	if v.VehicleType != "" && v.VehicleType != "official" && v.VehicleType != "ambulance" && v.VehicleType != "personal" {
		fields["vehicle_type"] = "ประเภทการขอใช้รถไม่ถูกต้อง"
	}
	if v.VehicleType == "personal" {
		if !meetingText(v.PersonalLicensePlate, 255, true) || strings.IndexFunc(v.PersonalLicensePlate, unicode.IsControl) >= 0 {
			fields["personal_license_plate"] = "กรุณาระบุทะเบียนรถยนต์ส่วนตัว"
		}
		if v.RequestedVehicleID != "" {
			fields["requested_vehicle_id"] = "รถยนต์ส่วนตัวไม่ต้องเลือกรถราชการ"
		}
		if v.RequestedDriverID != "" {
			fields["requested_driver_id"] = "รถยนต์ส่วนตัวใช้ผู้ขอเป็นผู้ขับ"
		}
	} else if v.PersonalLicensePlate != "" {
		fields["personal_license_plate"] = "ระบุทะเบียนส่วนตัวได้เฉพาะรถยนต์ส่วนตัว"
	}
	if v.RequestMode != "" && v.RequestMode != "scheduled" && v.RequestMode != "immediate" {
		fields["request_mode"] = "รูปแบบการขอใช้รถไม่ถูกต้อง"
	}
	if v.VehicleType == "ambulance" {
		if v.ReferType == "" {
			fields["refer_type"] = "กรุณาเลือกประเภทภารกิจ"
		}
		if v.Urgency == "" {
			fields["urgency"] = "กรุณาเลือกความเร่งด่วน"
		}
		if v.GoType != 0 && v.GoType != 1 {
			fields["go_type"] = "กรุณาตรวจสอบรูปแบบการเดินทางของรถพยาบาล"
		}
	} else if v.ReferType != "" || v.PatientName != "" || v.PatientHN != "" || v.PatientAge != "" || v.PatientSymptom != "" {
		fields["vehicle_type"] = "ข้อมูลผู้ป่วยและภารกิจใช้กับคำขอรถพยาบาลเท่านั้น"
	}
	if v.RequestMode == "immediate" {
		if v.VehicleType != "ambulance" {
			fields["request_mode"] = "การขอรถทันทีใช้กับรถพยาบาลเท่านั้น"
		}
		if v.ContactName == "" {
			fields["contact_name"] = "กรุณาระบุผู้ติดต่อ"
		}
		for _, field := range []struct {
			value string
			key   string
		}{
			{v.StartDate, "start_date"}, {v.EndDate, "end_date"}, {v.StartTime, "start_time"}, {v.EndTime, "end_time"},
		} {
			if field.value != "" {
				fields[field.key] = "การขอรถทันทีใช้เวลารับคำขอจากระบบ"
			}
		}
		if v.RequestedVehicleID != "" {
			fields["requested_vehicle_id"] = "เจ้าหน้าที่จัดสรรรถภายหลังรับคำขอ"
		}
		if v.RequestedDriverID != "" {
			fields["requested_driver_id"] = "เจ้าหน้าที่จัดสรร พขร. ภายหลังรับคำขอ"
		}
		if v.Passengers < 0 {
			fields["passengers"] = "จำนวนผู้เดินทางไม่ถูกต้อง"
		}
	}
}

func ValidVehicleFormOptions(value *VehicleFormOptions) bool {
	if value == nil || value.VehicleTypes == nil || value.ReferTypes == nil || value.Urgencies == nil {
		return false
	}
	if len(value.VehicleTypes) != 3 {
		return false
	}
	for _, item := range value.VehicleTypes {
		if item.ID != "official" && item.ID != "ambulance" && item.ID != "personal" {
			return false
		}
	}
	for _, list := range [][]VehicleFormChoice{value.VehicleTypes, value.ReferTypes, value.Urgencies} {
		if len(list) > 100 {
			return false
		}
		seen := map[string]bool{}
		for _, item := range list {
			if !meetingText(item.ID, 128, true) || !meetingText(item.Name, 255, true) || seen[item.ID] {
				return false
			}
			seen[item.ID] = true
		}
	}
	return true
}

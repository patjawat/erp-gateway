package contracts

import (
	"errors"
	"net/url"
	"strconv"
)

// Profile fields are explicitly projected; HR JSON and internal identifiers are
// never returned wholesale. Growing child collections have their own pages.
type ProfileField struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	Kind     string `json:"kind,omitempty"`
	EndValue string `json:"end_value,omitempty"`
}

type ProfilePersonal struct {
	Fields []ProfileField `json:"fields"`
}
type ProfileRecordLink struct {
	Section  string `json:"section"`
	Title    string `json:"title"`
	ParentID string `json:"parent_id"`
}
type ProfileRecord struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Fields   []ProfileField      `json:"fields"`
	Children []ProfileRecordLink `json:"children"`
}
type ProfileRecords struct {
	Items      []ProfileRecord `json:"items"`
	Section    string          `json:"section"`
	ParentID   string          `json:"parent_id"`
	Page       int             `json:"page"`
	PerPage    int             `json:"per_page"`
	TotalCount int             `json:"total_count"`
	HasMore    bool            `json:"has_more"`
}
type ProfileRecordSelection struct {
	Section, ParentID string
	Page, PerPage     int
}

func ProfileRecordSection(section string) (nested, valid bool) {
	switch section {
	case "position", "job_description_history", "kpi", "position_manage", "license", "develop", "internal_training", "training_roadmap", "idp", "education", "scholarships":
		return false, true
	case "jd_sections", "jd_items", "kpi_items", "kpi_entries", "roadmap_phases", "roadmap_activities", "roadmap_milestones", "roadmap_results", "idp_goals", "idp_activities":
		return true, true
	}
	return false, false
}

func ProfileRecordsQuery(raw string) (ProfileRecordSelection, error) {
	v := ProfileRecordSelection{Page: 1, PerPage: 20}
	q, err := url.ParseQuery(raw)
	bad := errors.New("invalid profile records selection")
	if err != nil {
		return v, bad
	}
	for key, values := range q {
		if len(values) != 1 {
			return v, bad
		}
		switch key {
		case "section", "parent_id", "page", "per_page":
		default:
			return v, bad
		}
	}
	v.Section, v.ParentID = q.Get("section"), q.Get("parent_id")
	nested, valid := ProfileRecordSection(v.Section)
	if !valid || nested != (v.ParentID != "") {
		return v, bad
	}
	if nested {
		id, e := strconv.ParseUint(v.ParentID, 10, 64)
		if e != nil || id == 0 {
			return v, bad
		}
	}
	for key, target := range map[string]*int{"page": &v.Page, "per_page": &v.PerPage} {
		if values, exists := q[key]; exists {
			n, e := strconv.Atoi(values[0])
			if e != nil {
				return v, bad
			}
			*target = n
		}
	}
	if v.Page < 1 || v.Page > 100000 || v.PerPage < 1 || v.PerPage > 50 {
		return v, bad
	}
	return v, nil
}
func (v ProfileRecordSelection) Query() string {
	q := url.Values{"section": {v.Section}, "page": {strconv.Itoa(v.Page)}, "per_page": {strconv.Itoa(v.PerPage)}}
	if v.ParentID != "" {
		q.Set("parent_id", v.ParentID)
	}
	return q.Encode()
}
func ValidProfileRecords(v ProfileRecords, q ProfileRecordSelection) bool {
	if v.Section != q.Section || v.ParentID != q.ParentID || v.Page != q.Page || v.PerPage != q.PerPage || v.TotalCount < 0 || len(v.Items) > q.PerPage {
		return false
	}
	remaining := v.TotalCount - (q.Page-1)*q.PerPage
	if remaining < 0 {
		remaining = 0
	}
	if remaining > q.PerPage {
		remaining = q.PerPage
	}
	if len(v.Items) != remaining || v.HasMore != (q.Page*q.PerPage < v.TotalCount) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if item.ID == "" || seen[item.ID] || len(item.Fields) > 40 || len(item.Children) > 4 {
			return false
		}
		seen[item.ID] = true
		for _, link := range item.Children {
			nested, ok := ProfileRecordSection(link.Section)
			if !ok || !nested || link.ParentID != item.ID {
				return false
			}
		}
	}
	return true
}

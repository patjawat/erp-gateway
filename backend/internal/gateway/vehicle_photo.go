package gateway

import (
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) vehiclePhoto(w http.ResponseWriter, r *http.Request, h Hospital, token string) {
	response, e := s.upstream(r.Context(), h, http.MethodGet, r.URL.Path, token, nil)
	if e != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != 200 {
		if response.StatusCode == 401 || response.StatusCode == 429 {
			s.relayError(w, response, false)
			return
		}
		defer response.Body.Close()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&env)
		if response.StatusCode == 404 && env.Error.Code == "VEHICLE_PHOTO_NOT_FOUND" {
			fail(w, 404, "VEHICLE_PHOTO_NOT_FOUND", "ไม่พบรูปภาพนี้")
			return
		}
		if response.StatusCode == 503 && env.Error.Code == "VEHICLE_PHOTOS_DISABLED" {
			fail(w, 503, "VEHICLE_PHOTOS_DISABLED", "ยังไม่เปิดการดูรูปผ่านมือถือ")
			return
		}
		upstreamUnavailable(w)
		return
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 5*1024*1024+1))
	if e != nil || len(body) == 0 || len(body) > 5*1024*1024 {
		upstreamUnavailable(w)
		return
	}
	mime := http.DetectContentType(body)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		upstreamUnavailable(w)
		return
	}
	if !s.stillActive(w, r, h) {
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

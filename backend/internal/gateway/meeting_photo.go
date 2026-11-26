package gateway

import (
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) meetingRoomPhoto(w http.ResponseWriter, r *http.Request, h Hospital, token string) {
	s.meetingPhoto(w, r, h, token, "MEETING_ROOM_PHOTO_NOT_FOUND", "MEETING_ROOM_PHOTOS_DISABLED")
}

func (s *Server) meetingPhoto(w http.ResponseWriter, r *http.Request, h Hospital, token, missingCode, disabledCode string) {
	response, err := s.upstream(r.Context(), h, http.MethodGet, r.URL.Path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
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
		if response.StatusCode == 404 && env.Error.Code == missingCode {
			fail(w, 404, env.Error.Code, "ไม่พบภาพประกอบนี้")
			return
		}
		if response.StatusCode == 503 && env.Error.Code == disabledCode {
			fail(w, 503, env.Error.Code, "ยังไม่เปิดการดูภาพประกอบผ่านมือถือ")
			return
		}
		upstreamUnavailable(w)
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 5*1024*1024+1))
	if err != nil || len(body) == 0 || len(body) > 5*1024*1024 {
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
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

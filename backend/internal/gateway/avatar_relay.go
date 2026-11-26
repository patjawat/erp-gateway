package gateway

import (
	"io"
	"net/http"
)

func (s *Server) relayAvatarImage(w http.ResponseWriter, r *http.Request, h Hospital, token, path string) {
	response, err := s.upstream(r.Context(), h, http.MethodGet, path, token, nil)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	if response.StatusCode != http.StatusOK {
		s.relayError(w, response, false)
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
	// A request may finish after logout or hospital suspension. Recheck before
	// disclosing image bytes, using the same session that chose the upstream.
	current, currentToken, active := s.authorize(w, r)
	if !active {
		return
	}
	if current.ID != h.ID || currentToken != token {
		expired(w)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

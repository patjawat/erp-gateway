package gateway

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// This is a transport ceiling, independent of each ERP service's file/field
// policies. Responses are streamed with a fixed-size buffer, not accumulated.
const businessRequestLimit int64 = 32 * 1024 * 1024

// relayBusiness intercepts only authenticated ERP-module installations. The old
// DTO handlers remain compatibility code for Go installations; they must not be
// extended for new ERP business fields or endpoints.
func (s *Server) relayBusiness(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodConnect || r.Method == http.MethodTrace {
		fail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "ไม่รองรับ method ที่ร้องขอ")
		return true
	}
	alias := r.URL.Path == "/depdrop/employee-by-id"
	if !alias && !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	if !safeBusinessPath(r) {
		fail(w, 400, "INVALID_REQUEST", "เส้นทางคำขอไม่ถูกต้อง")
		return true
	}
	if !alias {
		first := strings.ToLower(strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/"), "/", 2)[0])
		switch first {
		case "auth", "hospitals", "notification-devices":
			// These namespaces terminate at Gateway, including unknown operations.
			return false
		case "status", "version", "management", "notification-outbox", "internal", "admin", "gateway", "updater", "healthz":
			fail(w, 404, "NOT_FOUND", "ไม่พบรายการที่ร้องขอ")
			return true
		}
	}
	// Preserve existing Go method/route rejection before authentication. No
	// business request is sent by a compatibility handler without authorize().
	match := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
	if match == nil {
		return false
	}
	session, err := s.config.Store.session(r.Context(), match[1])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			expired(w)
		} else {
			unavailable(w)
		}
		return true
	}
	h, err := s.config.Store.hospital(r.Context(), "id", session.hospitalID)
	if err != nil {
		unavailable(w)
		return true
	}
	if !active(h) {
		disabled(w)
		return true
	}
	if session.revoked || session.expires <= time.Now().Unix() {
		expired(w)
		return true
	}
	discoveredConfig := directoryBackendFingerprint(h, s.managementRevision)
	backend, err := s.directoryUpstreamPath(r.Context(), h)
	if err != nil {
		upstreamUnavailable(w)
		return true
	}
	if backend == goDirectoryPath {
		return false
	}
	h, token, ok := s.authorize(w, r)
	if !ok {
		return true
	}
	if directoryBackendFingerprint(h, s.managementRevision) != discoveredConfig {
		upstreamUnavailable(w)
		return true
	}
	if !s.businessRateLimit(w, r, h, token) {
		return true
	}
	upstreamPath := r.URL.Path
	if alias {
		upstreamPath = erpDirectoryPath
	}
	s.serveBusinessRelay(w, r, h, token, upstreamPath)
	return true
}

func safeBusinessPath(r *http.Request) bool {
	p := r.URL.Path
	if strings.ContainsAny(p, "\\%\x00\r\n") || path.Clean(p) != strings.TrimSuffix(p, "/") {
		return false
	}
	encoded := strings.ToLower(r.URL.RawPath)
	return !strings.Contains(encoded, "%2f") && !strings.Contains(encoded, "%5c")
}

func (s *Server) businessRateLimit(w http.ResponseWriter, r *http.Request, h Hospital, token string) bool {
	if !s.limit(w, r, "business:"+h.ID+":"+tokenHash(token), 600, time.Minute) {
		return false
	}
	// Retain the existing connection-abuse budgets without parsing domain data.
	p := r.URL.Path
	switch {
	case p == "/api/usage/activity":
		return s.limit(w, r, "usage:"+h.ID+":"+tokenHash(token), 30, time.Hour)
	case p == "/api/attendance/approvals" || strings.HasPrefix(p, "/api/attendance/approvals/"):
		return s.limit(w, r, "attendance-review:"+h.ID+":"+token, 60, time.Minute)
	case strings.HasPrefix(p, "/api/attendance/") && !strings.HasPrefix(p, "/api/attendance/team"):
		return s.limit(w, r, "attendance:"+h.ID+":"+token, 60, time.Minute)
	case isLeaveApprovalPath(p):
		return s.limit(w, r, "leave-review:"+h.ID+":"+tokenHash(token), 90, time.Minute)
	default:
		return true
	}
}

func (s *Server) serveBusinessRelay(w http.ResponseWriter, r *http.Request, h Hospital, token, upstreamPath string) {
	base, err := s.egress.approvedURL(h.Code, h.APIURL)
	if err != nil {
		upstreamUnavailable(w)
		return
	}
	key, err := s.config.Store.decrypt("hospital:"+h.ID, h.key)
	if err != nil {
		unavailable(w)
		return
	}
	if r.ContentLength > businessRequestLimit {
		fail(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "คำขอมีขนาดใหญ่เกินกำหนด")
		return
	}
	prefix, escapedPrefix := strings.TrimRight(base.Path, "/"), strings.TrimRight(base.EscapedPath(), "/")
	base.Path = prefix + upstreamPath
	if r.URL.RawPath != "" && upstreamPath == r.URL.Path {
		base.RawPath = escapedPrefix + r.URL.RawPath
	}
	base.RawQuery, base.ForceQuery = r.URL.RawQuery, r.URL.ForceQuery
	body := r.Body
	if body != nil {
		body = http.MaxBytesReader(w, body, businessRequestLimit)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base.String(), body)
	if err != nil {
		fail(w, 400, "INVALID_REQUEST", "คำขอไม่ถูกต้อง")
		return
	}
	req.ContentLength = r.ContentLength
	req.Header = relayHeaders(r.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Gateway-Key", key)
	// Prevent Go's transport from transparently decompressing a response; byte
	// representation and Content-Encoding belong to the ERP response.
	if req.Header.Get("Accept-Encoding") == "" {
		req.Header.Set("Accept-Encoding", "identity")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "คำขอมีขนาดใหญ่เกินกำหนด")
		} else {
			upstreamUnavailable(w)
		}
		return
	}
	defer resp.Body.Close()
	// Check revocation/configuration changes before releasing the first byte.
	current, currentToken, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if current.ID != h.ID || current.APIURL != h.APIURL || !bytes.Equal(current.key, h.key) || currentToken != token {
		expired(w)
		return
	}
	for name, values := range relayHeaders(resp.Header) {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return
	}
	if _, err := io.CopyBuffer(w, resp.Body, make([]byte, 32*1024)); err != nil {
		// Headers may already be sent. Abort the stream instead of turning an
		// incomplete success into valid-looking JSON or appending an error body.
		panic(http.ErrAbortHandler)
	}
}

func relayHeaders(source http.Header) http.Header {
	result := source.Clone()
	for _, line := range source.Values("Connection") {
		for _, value := range strings.Split(line, ",") {
			result.Del(strings.TrimSpace(value))
		}
	}
	for name := range result {
		lower := strings.ToLower(name)
		switch lower {
		case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "authorization", "cookie", "set-cookie", "forwarded", "host", "x-http-method-override", "x-method-override", "x-original-url", "x-original-uri", "x-rewrite-url":
			result.Del(name)
		default:
			if strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-gateway-") || strings.HasPrefix(lower, "x-erp-") || strings.HasPrefix(lower, "x-ssl-") || strings.HasPrefix(lower, "x-client-cert") || strings.HasPrefix(lower, "x-accel-") {
				result.Del(name)
			}
		}
	}
	return result
}

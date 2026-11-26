package gateway

import (
	"net/http"
	"time"
)

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminAuth(w, r, false); !ok {
		return
	}
	summary, err := scanHospitalSummary(s.config.Store.db.QueryRowContext(r.Context(), "SELECT "+hospitalSummarySQL+" FROM hospitals"))
	if err != nil {
		unavailable(w)
		return
	}
	pendingCount := 0
	identityOperation.Lock()
	pending, pendingErr := s.pendingCertificate()
	identityOperation.Unlock()
	if pendingErr != nil {
		unavailable(w)
		return
	}
	if pending != nil {
		pendingCount = 1
	}
	usage, err := s.config.Store.usageSummary(r.Context(), summary.Total)
	if err != nil {
		unavailable(w)
		return
	}
	// Session rows and notification devices are deliberately not used as people
	// or installation analytics: they have different retention and semantics.
	writeData(w, map[string]any{
		"as_of": time.Now().Unix(), "hospitals": summary,
		"usage":        usage,
		"certificates": map[string]any{"gateway": s.gatewayCertificate(), "pending_requests": pendingCount, "issuer_status": "not_configured"},
		"setup":        map[string]any{"configured": true, "https_mode": "existing_proxy"},
	})
}

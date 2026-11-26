package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

// ManagementState is shared by operational handler generations. A candidate
// does not publish its revision until the settings transaction actually commits.
type ManagementState struct {
	mu      sync.Mutex
	current atomic.Pointer[managementRevision]
}
type managementRevision struct{ value string }

func managementFingerprint(p *upstreamPolicy, proxies []string) string {
	private := make([]string, 0, len(p.private))
	for _, v := range p.private {
		private = append(private, v.Masked().String())
	}
	sort.Strings(private)
	trusted := make([]string, 0, len(proxies))
	for v := range normalizedNetworks(proxies) {
		trusted = append(trusted, v)
	}
	sort.Strings(trusted)
	body, _ := json.Marshal(struct {
		Bases            map[string]string
		Private, Proxies []string
	}{p.bases, private, trusted})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (s *Server) beginManagement(w http.ResponseWriter) bool {
	s.management.mu.Lock()
	s.management.current.CompareAndSwap(nil, &managementRevision{value: s.managementRevision})
	current := s.management.current.Load()
	if current == nil || current.value != s.managementRevision {
		s.management.mu.Unlock()
		fail(w, 409, "STALE_RUNTIME", "การตั้งค่าระบบเปลี่ยนแล้ว กรุณาโหลดหน้าใหม่และเข้าสู่ระบบอีกครั้ง")
		return false
	}
	return true
}

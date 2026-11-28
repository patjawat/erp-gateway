package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"erp-platform/gateway/internal/contracts"
)

const (
	directoryBackendTTL         = time.Minute
	directoryBackendCacheLimit  = 256
	directoryBackendFlightLimit = 64
	goDirectoryPath             = "/depdrop/employee-by-id"
	erpDirectoryPath            = "/api/personnel"
)

var errDirectoryBackend = errors.New("hospital directory backend is unavailable")

type directoryBackendEntry struct {
	path    string
	expires time.Time
}

type directoryBackendFlight struct {
	done chan struct{}
	path string
	err  error
}

type backendStatus struct {
	HospitalCode string          `json:"hospital_code"`
	APIVersion   string          `json:"api_version"`
	ERPReachable bool            `json:"erp_reachable"`
	Backend      json.RawMessage `json:"backend"`
}

func (status backendStatus) directoryPath(h Hospital) (string, error) {
	if status.HospitalCode != h.Code || status.APIVersion != contracts.Version || !status.ERPReachable {
		return "", errDirectoryBackend
	}
	if len(status.Backend) == 0 {
		return goDirectoryPath, nil
	}
	var backend string
	if json.Unmarshal(status.Backend, &backend) != nil || backend != "erp_api_module" {
		return "", errDirectoryBackend
	}
	return erpDirectoryPath, nil
}

// The cache belongs to one immutable runtime generation. Its keys additionally
// bind the hospital/configuration snapshot, so a key or destination change can
// never reuse a previous backend decision. Neither tokens nor plaintext keys
// are retained here. Expired/evicted entries do not authorize a fallback.
type directoryBackendCache struct {
	mu      sync.Mutex
	entries map[string]directoryBackendEntry
	flights map[string]*directoryBackendFlight
	clock   func() time.Time
}

func (c *directoryBackendCache) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// A successful administrator connection test already authenticated this same
// bounded status protocol. Reuse it for business routing without another probe.
// The cache key still binds the exact destination/key/configuration generation.
func (c *directoryBackendCache) remember(key, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]directoryBackendEntry)
		c.flights = make(map[string]*directoryBackendFlight)
	}
	now := c.now()
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
		}
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= directoryBackendCacheLimit {
		var oldest string
		var earliest time.Time
		for existing, entry := range c.entries {
			if oldest == "" || entry.expires.Before(earliest) {
				oldest, earliest = existing, entry.expires
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = directoryBackendEntry{path: path, expires: now.Add(directoryBackendTTL)}
}

func (c *directoryBackendCache) resolve(ctx context.Context, key string, fetch func(context.Context) (string, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]directoryBackendEntry)
		c.flights = make(map[string]*directoryBackendFlight)
	}
	now := c.now()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.path, nil
	}
	if pending := c.flights[key]; pending != nil {
		c.mu.Unlock()
		select {
		case <-pending.done:
			return pending.path, pending.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if len(c.flights) >= directoryBackendFlightLimit {
		c.mu.Unlock()
		return "", errDirectoryBackend
	}
	pending := &directoryBackendFlight{done: make(chan struct{})}
	c.flights[key] = pending
	c.mu.Unlock()

	path, err := fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.flights, key)
	if err == nil {
		now = c.now()
		for existing, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, existing)
			}
		}
		if len(c.entries) >= directoryBackendCacheLimit {
			var oldest string
			var earliest time.Time
			for existing, entry := range c.entries {
				if oldest == "" || entry.expires.Before(earliest) {
					oldest, earliest = existing, entry.expires
				}
			}
			delete(c.entries, oldest)
		}
		c.entries[key] = directoryBackendEntry{path: path, expires: now.Add(directoryBackendTTL)}
	}
	pending.path, pending.err = path, err
	close(pending.done)
	return path, err
}

func directoryBackendFingerprint(h Hospital, revision string) string {
	body, _ := json.Marshal(struct {
		ID, Code, URL, Name, Province, Revision string
		EncryptedKey                            []byte
	}{h.ID, h.Code, h.APIURL, h.Name, h.Province, revision, h.key})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (s *Server) directoryUpstreamPath(ctx context.Context, h Hospital) (string, error) {
	// Validate even before a cache hit: cached metadata is not an egress grant.
	if _, err := s.egress.approvedURL(h.Code, h.APIURL); err != nil {
		return "", errDirectoryBackend
	}
	key := directoryBackendFingerprint(h, s.managementRevision)
	return s.directoryBackends.resolve(ctx, key, func(ctx context.Context) (string, error) {
		// Installation identity/key authenticate this response. Never forward the
		// Mobile bearer or any public directory query into capability discovery.
		resp, err := s.upstream(ctx, h, http.MethodGet, "/api/status", "", nil)
		if err != nil {
			return "", errDirectoryBackend
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", errDirectoryBackend
		}
		var status backendStatus
		if readEnvelopeLimit(resp, &status, 8*1024) != nil {
			return "", errDirectoryBackend
		}
		// This is deliberately a fixed path, not a path/URL supplied by a site.
		return status.directoryPath(h)
	})
}

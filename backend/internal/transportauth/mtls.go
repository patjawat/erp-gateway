// Package transportauth configures the service-to-service TLS identities.
package transportauth

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

type Pins map[[sha256.Size]byte]struct{}

// ParsePins accepts a small overlap set for intentional key rotation.
func ParsePins(value string) (Pins, error) {
	pins := make(Pins)
	for _, entry := range strings.Split(value, ",") {
		raw, err := hex.DecodeString(strings.TrimSpace(entry))
		if err != nil || len(raw) != sha256.Size || len(pins) >= 4 {
			return nil, errors.New("expected 1 to 4 Gateway SHA-256 SPKI hex fingerprints")
		}
		pins[[sha256.Size]byte(raw)] = struct{}{}
	}
	return pins, nil
}

func (pins Pins) MatchesVerifiedPeer(state *tls.ConnectionState) bool {
	if state == nil || !state.HandshakeComplete || len(state.PeerCertificates) == 0 {
		return false
	}
	peer := state.PeerCertificates[0]
	for _, chain := range state.VerifiedChains {
		if len(chain) != 0 && chain[0].Equal(peer) {
			_, ok := pins[sha256.Sum256(peer.RawSubjectPublicKeyInfo)]
			return ok
		}
	}
	return false
}

func LoadRoots(path string, includeSystem bool) (*x509.CertPool, error) {
	const limit = 1 << 20
	if path == "" {
		return nil, errors.New("required CA PEM file path missing")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open CA PEM file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("CA PEM file must be a regular file at most 1 MiB")
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(body) > limit {
		return nil, errors.New("cannot read CA PEM file")
	}
	roots := x509.NewCertPool()
	if includeSystem {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return nil, errors.New("cannot load system trust store")
		}
	}
	if !roots.AppendCertsFromPEM(body) {
		return nil, errors.New("CA PEM file contains no certificates")
	}
	return roots, nil
}

func LoadPair(certPath, keyPath string) (tls.Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, errors.New("cannot load matching TLS certificate and private key")
	}
	return pair, nil
}

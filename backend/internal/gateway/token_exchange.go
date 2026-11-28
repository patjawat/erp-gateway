package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"erp-platform/gateway/internal/contracts"
)

// Only the security token protocol is interpreted here. Other data members
// (including an optional profile) and outer metadata remain opaque JSON.
type tokenExchangeEnvelope struct {
	outer map[string]json.RawMessage
	data  map[string]json.RawMessage
}

func readTokenExchange(resp *http.Response) (contracts.Token, tokenExchangeEnvelope, error) {
	defer resp.Body.Close()
	var token contracts.Token
	var envelope tokenExchangeEnvelope
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "application/json" {
		return token, envelope, errors.New("non JSON token response")
	}
	const limit = 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) > limit {
		return token, envelope, errors.New("invalid token response size")
	}
	if json.Unmarshal(raw, &envelope.outer) != nil || envelope.outer == nil ||
		json.Unmarshal(envelope.outer["data"], &envelope.data) != nil || envelope.data == nil ||
		json.Unmarshal(envelope.outer["data"], &token) != nil {
		return token, envelope, errors.New("invalid token response")
	}
	return token, envelope, nil
}

func (envelope tokenExchangeEnvelope) write(w http.ResponseWriter, token contracts.Token) {
	for name, value := range map[string]any{"access_token": token.AccessToken, "token_type": token.TokenType, "expires_at": token.ExpiresAt, "scope": token.Scope} {
		envelope.data[name], _ = json.Marshal(value)
	}
	envelope.outer["data"], _ = json.Marshal(envelope.data)
	writeJSON(w, http.StatusOK, envelope.outer)
}

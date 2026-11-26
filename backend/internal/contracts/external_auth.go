package contracts

// ExternalStart binds an authorization flow to the device's S256 verifier.
// Neither an ERP credential nor an identity claim is accepted from the device.
type ExternalStart struct {
	HospitalCode  string `json:"hospital_code,omitempty"`
	Provider      string `json:"provider"`
	CodeChallenge string `json:"code_challenge"`
}

type ExternalFlowRequest struct {
	HospitalCode string `json:"hospital_code,omitempty"`
	FlowID       string `json:"flow_id"`
	CodeVerifier string `json:"code_verifier"`
}

type ExternalFlow struct {
	FlowID           string `json:"flow_id"`
	AuthorizationURL string `json:"authorization_url"`
	ExpiresAt        int64  `json:"expires_at"`
	PollInterval     int    `json:"poll_interval"`
}

type ExternalResult struct {
	Status string `json:"status"`
	Token
}

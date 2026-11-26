package contracts

// LoginMethods contains the effective ERP login policy, never client defaults.
type LoginMethods struct {
	Password   bool `json:"password"`
	ProviderID bool `json:"provider_id"`
	ThaID      bool `json:"thaid"`
}

func (m LoginMethods) Enabled(method string) bool {
	switch method {
	case "password":
		return m.Password
	case "provider_id":
		return m.ProviderID
	case "thaid":
		return m.ThaID
	default:
		return false
	}
}

type ExternalAuthAvailability struct {
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
}

type LoginPolicy struct {
	HospitalCode string                              `json:"hospital_code"`
	LoginMethods LoginMethods                        `json:"login_methods"`
	ExternalAuth map[string]ExternalAuthAvailability `json:"external_auth"`
}

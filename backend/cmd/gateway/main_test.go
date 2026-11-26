package main

import (
	"os"
	"strings"
	"testing"
)

func unsetSetup(t *testing.T) {
	t.Helper()
	t.Setenv("SETUP", "")
	if err := os.Unsetenv("SETUP"); err != nil {
		t.Fatal(err)
	}
}

func TestWebPolicyEnvironmentRequiresExplicitLANOptIn(t *testing.T) {
	unsetSetup(t)
	t.Setenv("GATEWAY_ALLOW_LAN_HTTP", "")
	t.Setenv("GATEWAY_LAN_HTTP_CIDRS", "")
	policy, err := webPolicyFromEnvironment()
	if err != nil || policy.LANHTTPEnabled() {
		t.Fatal("HTTPS must remain the default", err)
	}
	if _, err := policy.ValidateURL("http://10.10.8.127:8080"); err == nil {
		t.Fatal("default policy accepted HTTP")
	}
	t.Setenv("GATEWAY_ALLOW_LAN_HTTP", "true")
	if _, err := webPolicyFromEnvironment(); err == nil || !strings.Contains(err.Error(), "GATEWAY_LAN_HTTP_CIDRS") {
		t.Fatal("enabled LAN mode must require an allowlist", err)
	}
	t.Setenv("GATEWAY_LAN_HTTP_CIDRS", "10.10.8.0/24")
	policy, err = webPolicyFromEnvironment()
	if err != nil || !policy.LANHTTPEnabled() {
		t.Fatal("explicit LAN mode was not enabled", err)
	}
	if _, err := policy.ValidateURL("http://10.10.8.127:8080"); err != nil {
		t.Fatal(err)
	}
}

func TestWebPolicyEnvironmentErrorsNameVariables(t *testing.T) {
	unsetSetup(t)
	t.Setenv("GATEWAY_ALLOW_LAN_HTTP", "invalid-example-input")
	t.Setenv("GATEWAY_LAN_HTTP_CIDRS", "")
	if _, err := webPolicyFromEnvironment(); err == nil || !strings.Contains(err.Error(), "GATEWAY_ALLOW_LAN_HTTP") || strings.Contains(err.Error(), "invalid-example-input") {
		t.Fatal("invalid opt-in should name the variable without echoing its value", err)
	}
	t.Setenv("GATEWAY_ALLOW_LAN_HTTP", "true")
	t.Setenv("GATEWAY_LAN_HTTP_CIDRS", "0.0.0.0/0")
	if _, err := webPolicyFromEnvironment(); err == nil || !strings.Contains(err.Error(), "GATEWAY_LAN_HTTP_CIDRS") {
		t.Fatal("invalid allowlist should name the variable", err)
	}
}

func TestSetupEnvironmentPresenceAndValidation(t *testing.T) {
	unsetSetup(t)
	if enabled, err := setupFromEnvironment(); err != nil || enabled != nil {
		t.Fatal("missing SETUP must retain legacy behavior", enabled, err)
	}
	for _, value := range []string{"true", "false"} {
		t.Setenv("SETUP", value)
		enabled, err := setupFromEnvironment()
		if err != nil || enabled == nil || *enabled != (value == "true") {
			t.Fatal("incorrect SETUP switch", value, enabled, err)
		}
	}
	for _, value := range []string{"", "invalid-example-input"} {
		t.Setenv("SETUP", value)
		if _, err := setupFromEnvironment(); err == nil || !strings.Contains(err.Error(), "SETUP") || strings.Contains(err.Error(), "invalid-example-input") {
			t.Fatal("invalid explicit switch must name SETUP without echoing input", err)
		}
	}
}

func TestSimpleSetupPolicySurvivesSwitchOff(t *testing.T) {
	t.Setenv("GATEWAY_ALLOW_LAN_HTTP", "false")
	t.Setenv("GATEWAY_LAN_HTTP_CIDRS", "")
	for _, value := range []string{"true", "false"} {
		t.Setenv("SETUP", value)
		policy, err := webPolicyFromEnvironment()
		if err != nil || !policy.LANHTTPEnabled() || !policy.LocalHTTPEnabled() {
			t.Fatal("both switch values must support managed transport", value, err)
		}
		for _, address := range []string{"http://10.10.8.127:8080", "http://localhost:8080", "http://127.0.0.1:8080"} {
			if _, err := policy.ValidateURL(address); err != nil {
				t.Fatal(value, address, err)
			}
		}
		if _, err := policy.ValidateURL("http://203.0.113.4:8080"); err == nil {
			t.Fatal("managed transport accepted public HTTP", value)
		}
	}
}

func TestSimpleSetupPreservesManualLANAllowlist(t *testing.T) {
	t.Setenv("SETUP", "true")
	t.Setenv("GATEWAY_ALLOW_LAN_HTTP", "true")
	t.Setenv("GATEWAY_LAN_HTTP_CIDRS", "10.10.8.0/24")
	policy, err := webPolicyFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	settings := policy.Settings()
	if !settings.AllowLANHTTP || settings.AllowLocalHTTP || len(settings.LANHTTPCIDRs) != 1 || settings.LANHTTPCIDRs[0] != "10.10.8.0/24" {
		t.Fatalf("explicit allowlist was widened: %+v", settings)
	}
}

func TestSetupCodeAvailability(t *testing.T) {
	yes, no := true, false
	for _, tt := range []struct {
		name       string
		enabled    *bool
		configured bool
		allowed    bool
	}{
		{"legacy new", nil, false, true},
		{"enabled new", &yes, false, true},
		{"disabled new", &no, false, false},
		{"legacy active", nil, true, false},
		{"enabled active", &yes, true, false},
		{"disabled active", &no, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := setupCodeAllowed(tt.enabled, tt.configured)
			if (err == nil) != tt.allowed {
				t.Fatal("incorrect code issuance gate", err)
			}
			if tt.configured && !strings.Contains(err.Error(), "configured") {
				t.Fatal("healthy configured system must take precedence", err)
			}
		})
	}
}

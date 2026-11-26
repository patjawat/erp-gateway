package main

import (
	"strings"
	"testing"
)

func TestWebPolicyEnvironmentRequiresExplicitLANOptIn(t *testing.T) {
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

package gateway

import (
	"net/http"
	"testing"
)

func TestLegacyAdminPasswordAcceptsNonEmptyWithoutMinimum(t *testing.T) {
	r := rig(t)
	for _, password := range []string{"1", " ", "a!"} {
		config := r.app.config
		config.AdminPassword, config.AdminPasswordVerifier = password, nil
		app, err := New(config)
		if err != nil {
			t.Fatal("legacy Gateway rejected non-empty password", err)
		}
		r.handler = app.Handler()
		response := r.request(t, http.MethodPost, "/admin/api/login", map[string]string{"username": config.AdminUsername, "password": password}, "", false)
		if response.Code != http.StatusOK {
			t.Fatal("legacy short password login failed", response.Code)
		}
	}
	config := r.app.config
	config.AdminPassword, config.AdminPasswordVerifier = "", nil
	if _, err := New(config); err == nil {
		t.Fatal("legacy Gateway accepted an empty administrator password")
	}
}

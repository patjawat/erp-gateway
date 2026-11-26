package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPersonnelAvatarUsesSessionHospitalAndSafeImage(t *testing.T) {
	r := rig(t)
	a, b := r.add(t, "11447"), r.add(t, "12448")
	r.enable(t, a)
	r.enable(t, b)
	ta, tb := r.login(t, a.Code), r.login(t, b.Code)
	body, status := "GIF89a personnel fixture", http.StatusOK
	calls := 0
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		code := strings.TrimSuffix(request.URL.Host, ".example.invalid")
		if request.Method != http.MethodGet || request.URL.Path != "/api/personnel/42/avatar" || request.URL.RawQuery != "" || request.Header.Get("Cookie") != "" || request.Header.Get("X-Gateway-Key") != strings.Repeat(code, 8) || request.Header.Get("Authorization") != "Bearer "+r.localTokens[code] {
			t.Fatal("personnel image forwarded outside the authenticated hospital")
		}
		w := httptest.NewRecorder()
		if status != http.StatusOK {
			fail(w, status, "NOT_FOUND", "private personnel storage path")
		} else {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.WriteString(body)
		}
		return w.Result(), nil
	})
	for _, token := range []string{ta, tb} {
		w := r.request(t, "GET", "/api/personnel/42/avatar", nil, token, false)
		if w.Code != http.StatusOK || w.Body.String() != body || w.Header().Get("Content-Type") != "image/gif" || w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("personnel image unavailable or unsafe: %d %s", w.Code, w.Body.String())
		}
	}
	for _, invalid := range []string{"<script>private</script>", "", "GIF89a" + strings.Repeat("x", 5*1024*1024)} {
		body = invalid
		if w := r.request(t, "GET", "/api/personnel/42/avatar", nil, ta, false); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("unsafe personnel binary escaped: %d", w.Code)
		}
	}
	for _, code := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusFound} {
		status = code
		w := r.request(t, "GET", "/api/personnel/42/avatar", nil, ta, false)
		want := http.StatusServiceUnavailable
		if code == http.StatusNotFound {
			want = code
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("personnel upstream error was not sanitized: %d", w.Code)
		}
	}
	before := calls
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/personnel", 404},
		{"GET", "/api/personnel/", 404},
		{"GET", "/api/personnel/0/avatar", 404},
		{"GET", "/api/personnel/not-an-id/avatar", 404},
		{"GET", "/api/personnel/00042/avatar", 404},
		{"GET", "/api/personnel/42/avatar/extra", 404},
		{"GET", "/api/personnel/42/avatar?hospital_code=12448", 400},
		{"GET", "/api/personnel/42/avatar?file=private.jpg", 400},
		{"POST", "/api/personnel/42/avatar", 405},
		{"HEAD", "/api/personnel/42/avatar", 405},
	} {
		if w := r.request(t, test.method, test.path, nil, ta, false); w.Code != test.status {
			t.Fatalf("invalid personnel route %s returned %d", test.path, w.Code)
		}
	}
	if w := r.request(t, "GET", "/api/personnel/42/avatar", nil, "", false); w.Code != 401 {
		t.Fatal("anonymous personnel photo request accepted")
	}
	if calls != before {
		t.Fatal("invalid personnel photo request reached hospital API")
	}
}

func TestPersonnelAvatarDropsImagesAfterLogoutOrSuspension(t *testing.T) {
	for _, suspend := range []bool{false, true} {
		t.Run(map[bool]string{false: "logout", true: "suspension"}[suspend], func(t *testing.T) {
			r := rig(t)
			a := r.add(t, "11447")
			r.enable(t, a)
			token := r.login(t, a.Code)
			r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
				if suspend {
					if w := r.request(t, "POST", "/admin/api/hospitals/"+a.ID+"/suspend", map[string]string{"reason": "test"}, "", true); w.Code != 200 {
						t.Fatal("could not suspend fixture hospital")
					}
				} else if err := r.app.config.Store.revoke(request.Context(), token); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				_, _ = w.WriteString("GIF89a private personnel image")
				return w.Result(), nil
			})
			w := r.request(t, "GET", "/api/personnel/42/avatar", nil, token, false)
			want := http.StatusUnauthorized
			if suspend {
				want = http.StatusForbidden
			}
			if w.Code != want || strings.Contains(w.Body.String(), "private personnel") {
				t.Fatalf("personnel image survived session change: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

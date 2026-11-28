package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erp-platform/gateway/internal/contracts"
)

func TestVehicleMissionGatewayStagesAndReadsOnlyCanonicalReceipt(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	var data bytes.Buffer
	if png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))) != nil {
		t.Fatal("image fixture")
	}
	upload := contracts.RepairUpload{RequestID: "receipt-upload-0001", FileName: "ค่าใช้จ่าย.png", DataBase64: base64.StdEncoding.EncodeToString(data.Bytes())}
	id := strings.Repeat("a", 64)
	path := "/api/vehicles/drivers/18/missions/1/receipts"
	receipt := contracts.VehicleMissionReceipt{ID: id, FileName: upload.FileName, MimeType: "image/png", Size: data.Len(), URL: path + "/" + id}
	calls := 0
	badURL := false
	r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "11447.example.invalid" || request.Header.Get("Authorization") != "Bearer "+r.localTokens[h.Code] || request.Header.Get("Cookie") != "" {
			t.Fatal("receipt crossed hospital/session boundary")
		}
		w := httptest.NewRecorder()
		if request.Method == "POST" {
			var sent contracts.RepairUpload
			if request.URL.Path != path || json.NewDecoder(request.Body).Decode(&sent) != nil || sent != upload {
				t.Fatal("upload changed")
			}
			value := receipt
			if badURL {
				value.URL = "https://other.invalid/private"
			}
			writeData(w, value)
		} else {
			if request.Method != "GET" || request.URL.Path != receipt.URL {
				t.Fatal("unexpected image URL")
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(data.Bytes())
		}
		return w.Result(), nil
	})
	if got := r.request(t, "POST", path, upload, token, false); got.Code != 200 || !strings.Contains(got.Body.String(), id) {
		t.Fatalf("upload %d %s", got.Code, got.Body)
	}
	if got := r.request(t, "GET", receipt.URL, nil, token, false); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data.Bytes()) || got.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("image %d %s", got.Code, got.Body)
	}
	badURL = true
	if got := r.request(t, "POST", path, upload, token, false); got.Code != 503 {
		t.Fatalf("external response URL %d", got.Code)
	}
	before := calls
	for _, test := range []struct {
		method, path, token string
		code                int
	}{{"GET", receipt.URL, "", 401}, {"POST", path + "?url=http://other.invalid", token, 400}, {"DELETE", receipt.URL, token, 405}, {"GET", path + "/..%2Fsecret", token, 400}} {
		if got := r.request(t, test.method, test.path, nil, test.token, false); got.Code != test.code {
			t.Fatalf("invalid route %s: %d", test.path, got.Code)
		}
	}
	if calls != before {
		t.Fatal("invalid receipt request reached upstream")
	}
}

func TestVehicleMissionGatewayConfirmsEveryOptionalFieldAndReceipt(t *testing.T) {
	r := rig(t)
	h := r.add(t, "11447")
	r.enable(t, h)
	token := r.login(t, h.Code)
	status := "Custom"
	id := strings.Repeat("a", 64)
	input := contracts.VehicleDriverMissionInput{MileageStart: gatewayMileage(0), MileageEnd: gatewayMileage(10), DistanceKM: gatewayMileage(3), OilPrice: gatewayMileage(0), OilLiter: gatewayMileage(2.5), Status: &status, RequestID: "request_with_underscore_0001", ReceiptIDs: []string{id}}
	output := contracts.VehicleDriverMission{ID: "1", BookingID: "2", DriverID: "18", MileageStart: input.MileageStart, MileageEnd: input.MileageEnd, DistanceKM: input.DistanceKM, OilPrice: input.OilPrice, OilLiter: input.OilLiter, Status: status, Receipts: []contracts.VehicleMissionReceipt{{ID: id, FileName: "receipt.png", MimeType: "image/png", Size: 10, URL: contracts.VehicleMissionReceiptURL("18", "1", id)}}}
	for _, bad := range []string{"", "distance", "fuel", "status", "receipt"} {
		r.app.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			var sent contracts.VehicleDriverMissionInput
			if json.NewDecoder(request.Body).Decode(&sent) != nil || sent.RequestID != input.RequestID || sent.OilPrice == nil || *sent.OilPrice != 0 || sent.Status == nil || *sent.Status != status || len(sent.ReceiptIDs) != 1 {
				t.Fatal("optional save fields lost")
			}
			value := output
			switch bad {
			case "distance":
				value.DistanceKM = nil
			case "fuel":
				value.OilPrice = gatewayMileage(2)
			case "status":
				value.Status = "Other"
			case "receipt":
				value.Receipts = nil
			}
			w := httptest.NewRecorder()
			writeData(w, value)
			return w.Result(), nil
		})
		want := 503
		if bad == "" {
			want = 200
		}
		if got := r.request(t, "PUT", "/api/vehicles/drivers/18/missions/1", input, token, false); got.Code != want {
			t.Fatalf("confirmation %s => %d %s", bad, got.Code, got.Body)
		}
	}
}

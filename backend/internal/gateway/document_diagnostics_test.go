package gateway

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestDocumentDiagnosticsNeverLogUntrustedErrorPayload(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	documentGatewayDiagnostic("upstream", 503, "secret-token\nprivate document body")
	if !strings.Contains(output.String(), "code=UNRECOGNIZED") || strings.Contains(output.String(), "secret-token") || strings.Contains(output.String(), "private document") {
		t.Fatal("diagnostic leaked upstream payload")
	}
}

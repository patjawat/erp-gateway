package gateway

import (
	"log"
	"regexp"
)

var documentDiagnosticCode = regexp.MustCompile(`^[A-Z_]{1,64}$`)

// Only stage, status and a bounded machine code are logged. Never log the
// request URL/query, headers, tokens, upstream body or document metadata.
func documentGatewayDiagnostic(stage string, status int, code string) {
	if !documentDiagnosticCode.MatchString(code) {
		code = "UNRECOGNIZED"
	}
	log.Printf("document_failure component=gateway stage=%s status=%d code=%s", stage, status, code)
}

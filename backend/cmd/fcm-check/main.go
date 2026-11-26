package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"

	"erp-platform/gateway/internal/gateway"
)

func main() {
	flags := flag.NewFlagSet("fcm-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	credentials := flags.String("credentials", "", "Path to the private service-account JSON")
	project := flags.String("expected-project", gateway.DefaultFCMProject, "Expected Firebase project ID")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 || *credentials == "" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "error": "FCM_CHECK_INVALID_ARGUMENTS"})
		os.Exit(1)
	}
	result, err := gateway.CheckFCMCredentials(context.Background(), *credentials, *project)
	if err != nil {
		output := map[string]any{"ok": false, "error": "FCM_CHECK_FAILED"}
		var checked *gateway.FCMCheckError
		if errors.As(err, &checked) {
			output["error"] = checked.Code
			if checked.HTTPStatus != 0 {
				output["http_status"] = checked.HTTPStatus
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(output)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		OK bool `json:"ok"`
		gateway.FCMCheckResult
	}{OK: true, FCMCheckResult: result})
}

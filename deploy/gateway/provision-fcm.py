#!/usr/bin/env python3
"""Provision an FCM key for the nonroot Gateway; never start its worker."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def docker(arguments, *, payload=None):
    result = subprocess.run(
        ["docker", *arguments],
        input=payload,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=60,
        check=False,
    )
    if result.returncode:
        # Provider or container diagnostics must never echo credential contents.
        raise RuntimeError("Docker credential provisioning or permission check failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--credentials", required=True, type=Path)
    parser.add_argument("--volume", default="erp-mobile-gateway-fcm")
    parser.add_argument("--helper-image", default="busybox:1.37.0")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}", args.volume):
        raise ValueError("Invalid credential volume name")
    with args.credentials.open("rb") as source:
        raw = source.read(65537)
    if len(raw) > 65536:
        raise ValueError("Credential file exceeds the supported size")
    credential = json.loads(raw)
    if (
        not isinstance(credential, dict)
        or credential.get("type") != "service_account"
        or credential.get("project_id") != "hospital-erp-44fbd"
        or credential.get("token_uri") != "https://oauth2.googleapis.com/token"
        or not credential.get("private_key")
        or not credential.get("client_email")
    ):
        raise ValueError("Expected a hospital-erp-44fbd service account credential")
    os.chmod(args.credentials, 0o600)
    docker(["volume", "create", args.volume])
    mount = f"type=volume,source={args.volume},target=/credentials"
    docker(
        [
            "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
            "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE",
            "--cap-add", "FOWNER", "--security-opt", "no-new-privileges:true",
            "--mount", mount, "-i", args.helper_image, "sh", "-eu", "-c",
            """umask 077
temporary=$(mktemp /credentials/.firebase.XXXXXX)
trap 'rm -f "$temporary"' EXIT HUP INT TERM
cat > "$temporary"
test "$(wc -c < "$temporary")" -eq "$1"
chmod 400 "$temporary"
chown 65532:65532 "$temporary"
mv -f "$temporary" /credentials/firebase-service-account.json
chmod 700 /credentials
chown 65532:65532 /credentials
""",
            "fcm-provision", str(len(raw)),
        ],
        payload=raw,
    )
    docker(
        [
            "run", "--rm", "--network", "none", "--read-only",
            "--user", "65532:65532", "--cap-drop", "ALL",
            "--security-opt", "no-new-privileges:true",
            "--mount", mount + ",readonly", args.helper_image, "sh", "-eu", "-c",
            "test -r /credentials/firebase-service-account.json && "
            "test $(stat -c %a /credentials/firebase-service-account.json) = 400 && "
            "test $(stat -c %u /credentials/firebase-service-account.json) = 65532",
        ]
    )
    print(json.dumps({
        "project_id": "hospital-erp-44fbd",
        "credential_volume": args.volume,
        "gateway_user_can_read": True,
        "host_mode": "0600",
        "volume_mode": "0400",
        "worker_started": False,
    }))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired):
        print("FCM credential provisioning failed; check the private file and Docker.", file=sys.stderr)
        sys.exit(1)

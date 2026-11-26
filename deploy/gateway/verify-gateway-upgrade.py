#!/usr/bin/env python3
"""Fail closed before an installer can discard a running Gateway's identity/data.

Docker/config JSON is read privately in memory. Never print an environment value,
certificate, private key, credential, or the Docker diagnostics containing them.
"""
import hmac
import json
import subprocess
import sys


class UpgradeRejected(ValueError):
    pass


def check_upgrade(existing, resolved, legacy_tls):
    labels = existing.get("Config", {}).get("Labels", {}) or {}
    if labels.get("com.docker.compose.project") != "erp-mobile-gateway" or labels.get("com.docker.compose.service") != "gateway":
        raise UpgradeRejected("existing container is not the expected Gateway project/service")
    service = resolved.get("services", {}).get("gateway", {})
    before = {}
    for item in existing.get("Config", {}).get("Env", []) or []:
        if "=" in item:
            key, value = item.split("=", 1)
            before[key] = value
    after = service.get("environment", {}) or {}
    client_paths = ("/run/gateway-tls/client-chain.pem", "/run/gateway-tls/client-key.pem")
    old_mounts = existing.get("Mounts", []) or []
    if any(m.get("Destination") in client_paths for m in old_mounts) and not legacy_tls:
        raise UpgradeRejected("existing Gateway uses client TLS binds from its old base Compose; add --legacy-tls and preserve its original --env-file")
    protected = (
        "GATEWAY_DATABASE_PATH", "GATEWAY_CONFIG_DIR",
        "GATEWAY_ENCRYPTION_KEY", "GATEWAY_ADMIN_USERNAME", "GATEWAY_ADMIN_PASSWORD",
        "GATEWAY_CLIENT_TLS_CERT_FILE", "GATEWAY_CLIENT_TLS_KEY_FILE",
        "GATEWAY_HOSPITAL_SERVER_CA_FILE", "GATEWAY_APPROVED_UPSTREAMS",
        "GATEWAY_PRIVATE_UPSTREAM_CIDRS", "GATEWAY_TRUSTED_PROXY_CIDRS", "FCM_CREDENTIALS_FILE",
        "GATEWAY_UPDATE_REPOSITORIES", "GATEWAY_UPDATE_SIGNING_PUBLIC_KEY",
        "GATEWAY_RELEASE_PUBLISH_TOKEN_SHA256", "GATEWAY_SETUP_PUBLIC_URL",
    )
    for key in protected:
        value = before.get(key, "")
        replacement = after.get(key, "")
        if value and not hmac.compare_digest(str(value).encode(), str(replacement or "").encode()):
            raise UpgradeRejected(f"existing {key} would change or disappear; use the original environment file and required overlays, then migrate explicitly through recovery/settings")
    expected = {v.get("target"): v for v in service.get("volumes", []) if isinstance(v, dict)}
    named = resolved.get("volumes", {}) or {}
    for mount in old_mounts:
        if mount.get("Type") == "tmpfs":
            continue
        target = mount.get("Destination")
        candidate = expected.get(target)
        if candidate is None or candidate.get("type") != mount.get("Type"):
            raise UpgradeRejected("an existing Gateway data, identity or credential mount would be omitted; preserve its overlay or review migration manually")
        if bool(candidate.get("read_only", False)) == bool(mount.get("RW", False)):
            raise UpgradeRejected("an existing Gateway mount's read/write policy would change; review migration manually")
        source = candidate.get("source")
        if mount.get("Type") == "volume":
            source = (named.get(source) or {}).get("name")
            actual = mount.get("Name")
        elif mount.get("Type") == "bind":
            actual = mount.get("Source")
        else:
            raise UpgradeRejected("existing Gateway uses an unsupported mount type; review migration manually")
        if not source or source != actual:
            raise UpgradeRejected("an existing Gateway volume or credential bind source would change; preserve the original volume/path")
    if not any(m.get("Destination") == "/data" for m in old_mounts):
        raise UpgradeRejected("existing Gateway has no verifiable persistent /data mount; review database migration manually")


def private_json(command):
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=True)
    if len(result.stdout) > 16 * 1024 * 1024:
        raise UpgradeRejected("Gateway inventory exceeds the preflight limit")
    return json.loads(result.stdout)


def main(argv):
    if len(argv) < 5 or argv[2] not in ("0", "1"):
        raise UpgradeRejected("invalid upgrade-preflight invocation")
    context, container, legacy = argv[:3]
    existing = private_json(["docker", "--context", context, "container", "inspect", container, "--format", "{{json .}}"])
    resolved = private_json(argv[3:] + ["config", "--format", "json"])
    check_upgrade(existing, resolved, legacy == "1")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except UpgradeRejected as error:
        sys.exit(f"Gateway upgrade refused: {error}")
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, KeyError, AttributeError):
        sys.exit("Gateway upgrade refused: cannot inspect the existing private runtime inventory safely")

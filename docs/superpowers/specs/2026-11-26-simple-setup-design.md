# Single-switch Gateway Setup

The user approved replacing normal installation's coupled network variables with `SETUP=true` / `SETUP=false`. New installs must work through LAN/VPN without configuring a public setup URL, client CIDRs or Docker bridge/proxy settings first.

## Behavior

- Root Compose exposes port 8080 on all IPv4 interfaces by default, retaining optional port/bind overrides for advanced deployments. Its normal environment example contains only `SETUP=true`.
- `SETUP=true` permits first-run/recovery Setup from direct private/loopback connections with a valid private-IP or localhost HTTP origin. The short-lived, one-time server-issued Setup code remains mandatory. Public HTTP and untrusted forwarded headers are rejected.
- Before authentication, a request origin is only a proposed address. After code verification, the Setup session is bound to its canonical origin and returns that address for operator confirmation in the form. All mutations retain CSRF and same-origin checks.
- `SETUP=false` disables unconfigured/recovery Setup and issuing setup codes, with a clear disabled response; health checks continue. A healthy active configuration always wins: Setup stays closed even with true, while Admin/Mobile keep working with false.
- Persist the management transport policy alongside the existing public URL in runtime.json. HTTP/HTTPS cookies and management admission use this saved policy, independently of SETUP on subsequent starts. Preserve all credentials, identities, encryption keys, database state and volume names.
- Support localhost HTTP as well as private-IP HTTP in the new managed mode. Preserve the existing strict/manual webtransport API behavior for legacy callers.
- Existing explicit LAN CIDRs/proxy settings remain advanced overrides. For an older healthy runtime without saved policy, migrate only the resolved policy snapshot under the existing lock/atomic-write rules after successful validation/build; retain its exact CIDRs when supplied. Never widen an explicit persisted allowlist.
- HTTPS through a proxy remains available through advanced web settings/legacy bootstrap overrides. Do not infer trust from X-Forwarded headers. Gateway-to-API HTTPS/mTLS remains unchanged.

## Boundaries

This is Gateway-only. Do not edit ERP, live .env, Docker volumes, private CA files, customer services or host firewall/Nginx. Do not introduce dependencies, simulators or Docker control. The switch does not reset passwords or reinstall an active system.

## Acceptance

With only SETUP=true, a new private/localhost browser can unlock Setup, confirm the suggested URL, activate and log into Admin. Restart with SETUP=false preserves access and data. Both values cannot reopen healthy active Setup. Missing/wrong code, missing CSRF, changed session origin, public HTTP, forged forwarded headers and malformed saved policy are denied. Existing HTTPS/manual LAN behavior remains tested. Restore validates transport policy rather than trusting arbitrary archive settings.

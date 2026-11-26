# Explicit LAN HTTP for Setup and Admin

The user selected HTTP access to both Setup and Admin at `http://10.10.8.127:8080`, accepting that traffic is unencrypted. The existing HTTPS-only default prevents this and an HTTP setup URL currently aborts startup.

## Approaches

- HTTPS with a private IP certificate preserves encryption but requires certificate installation/trust. The user chose the following alternative.
- Explicit LAN HTTP permits direct access without SSL, with private IP URLs, exact Host/Origin checks, and an operator-supplied client CIDR allowlist. Plaintext credentials are an accepted limitation.

## Design

- Add `GATEWAY_ALLOW_LAN_HTTP=false` and `GATEWAY_LAN_HTTP_CIDRS=`. Enabling requires a nonempty, canonical, private-network allowlist. Default behavior remains HTTPS.
- A shared `internal/webtransport` policy validates gateway URLs and direct LAN requests. HTTP URLs require a literal private IP, no credentials/path/query/fragment, and a valid port. Allowlisted direct peers and an exact configured Host are required for LAN Setup and Admin. Forwarded headers do not grant LAN access.
- Persist the public URL using the existing config format. Environment opt-in remains mandatory on restart and recovery. Do not modify database, identity, encryption key, or volume names.
- HTTP Admin cookies are HttpOnly, SameSite=Strict and non-Secure only for the enabled HTTP URL. CSRF, one-time setup codes, authentication and rate limits remain active.
- Backend metadata exposes read-only `lan_http_enabled` to Setup and Settings. Existing forms show the enabled mode and accept private-IP HTTP only when this capability is enabled. Preserve the existing design.
- Upstream API validation and HTTPS/mTLS remain unchanged. ERP, deployment on the user's server, and Nginx changes are outside this implementation.

## Acceptance

Explicitly enabled, allowlisted clients can complete Setup, activate, log in, change settings and restart using HTTP on the configured IP/port. Disabled mode, public HTTP URLs, spoofed forwarded headers, unlisted peers, incorrect Host/Origin and missing CSRF credentials remain denied. Recovery validates the same policy. Existing HTTPS tests remain passing.

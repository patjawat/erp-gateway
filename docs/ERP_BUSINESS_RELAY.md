# ERP API business transport

An authenticated `/api/status` response with `data.backend="erp_api_module"`
selects the opaque business relay for that hospital's single registered upstream.
The status must match the hospital code, API version `1` and reachable state.
Discovery is bounded, coalesced and cached for one minute under the exact
hospital/destination/key/configuration fingerprint. Successful administrator
connection tests seed the same cache. Unknown markers and failed discovery do
not authorize another implementation or a fallback request.

Business traffic follows Mobile → Gateway → ERP API → Gateway → Mobile.
Gateway replaces the Mobile bearer with the session's encrypted upstream token
and authenticates the connection using its existing Gateway key and TLS/mTLS
transport. ERP owns business schemas, field validation, RBAC, pagination,
commands, idempotency and workflow. Adding a business endpoint, field, selector,
pagination property or business error does not require a Gateway DTO change.

The relay preserves method, path escaping, raw query (including repeated keys),
opaque request bytes, upstream HTTP status, opaque response bytes and applicable
end-to-end headers. Multipart parts are not reconstructed. HEAD and Range use the
upstream's semantics. Responses stream through a 32 KiB copy buffer; an upstream
read failure aborts the HTTP response instead of appending a JSON error to a
partial file. Client cancellation reaches the upstream request. Gateway retains
its existing 20-second HTTP client timeout and a transport request ceiling of
32 MiB, without interpreting domain-specific upload rules.

Gateway has a generic ERP business request budget of 600 requests per minute per
hospital/session. Existing attendance (60/minute), attendance approvals
(60/minute), leave approvals (90/minute), and activity ingestion (30/hour) abuse
budgets also remain. These are connection controls; ERP validates the business
operation and its fields.

Hop-by-hop headers and headers named by `Connection` are removed. Client cookies,
authorization, proxy/forwarded identity assertions, ERP/Gateway secret headers
and method/path override headers are never trusted or forwarded. Gateway sets its
own bearer and connection key. ERP cookies and private identity headers are not
returned to Mobile. CONNECT and TRACE are rejected. Redirects are returned to
the caller without following them or sending credentials to another host.

The following terminate at Gateway rather than the business relay:

- `/api/auth/*`: password/external auth protocol, token exchange and logout.
- `/api/hospitals/*`: public hospital registry, availability and login policy.
- `/api/notification-devices/*`: Gateway FCM device registration/removal.
- `/admin/*`: Gateway administration and control with separate authorization.

`GET /api/auth/backend` is an authenticated connection control response containing
only `backend`, `hospital_id` and `hospital_code`. It uses validated discovery and
rechecks the current session/destination before returning. Mobile can select an
ERP-specific capability protocol without downloading a business profile to infer
the backend. `GET /api/notification-devices/capabilities` independently reports
Gateway delivery availability as `data.push_enabled`; ERP notification history
does not advertise the Gateway's FCM configuration.

Mobile cannot relay `/api/status`, `/api/version`, `/api/management/*`,
`/api/notification-outbox/*` or other reserved internal/admin/Gateway/Updater
namespaces. New privileged ERP control routes must use the reserved management
namespace and retain ERP-side Gateway identity checks. Ambiguous or traversing
paths are rejected before the Go mux can normalize or redirect them.

Notification history/detail/read/summary and `/api/usage/activity` are business
requests and use the opaque ERP relay. FCM device routing/delivery, notification
outbox consumption and multi-hospital dashboard collection remain Gateway control
protocols. Management aggregates are not made available through a Mobile bearer.

`/depdrop/employee-by-id` remains a compatibility URL: ERP installations map only
its fixed path to `/api/personnel`, preserving the raw query. Go installations
retain the existing path and compatibility handler. No caller can select an
upstream through query parameters, headers or body fields.

Login token exchange parses only security fields needed for session binding.
It replaces `access_token`, `token_type`, `expires_at` and `scope`, preserving
unknown data members (including an optional opaque profile) and outer envelope
metadata. The ERP business `/api/profile` response is not enriched or rewritten:
Mobile uses its selected trusted hospital registry context; ERP emits public
Gateway-relative image/signature URLs and owns the profile's business fields.

Go installations without a backend marker temporarily retain the old per-service
DTO handlers and response transformations. Those files are compatibility-only:
do not add new ERP business fields or schemas there. This does not enable
dual-backend routing, retry an ERP failure against Go, or permit the retired ERP
API implementation. Hospital cutover and Go API retirement still require the
separate full migration, data rehearsal, physical-device and pilot evidence.

Automated coverage lives in `business_relay_test.go`, `token_exchange_test.go`,
`backend_discovery_test.go`, `backend_control_test.go` and the existing Go security/regression suites. It
uses isolated fixtures and temporary SQLite stores; it is not proof that the
remaining ERP capabilities or production Nginx/MySQL/storage deployment work.
Fresh verification on 2026-11-27 after both control endpoints: full
`rtk go test -race ./...` passed 779 tests in 10 packages, and `rtk go vet ./...`
and `rtk git diff --check` exited successfully.

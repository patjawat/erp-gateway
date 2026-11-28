# Generic ERP business relay implementation plan

**Goal:** Forward ERP API business requests and responses without Gateway business DTOs, while preserving existing Go installations and Gateway administration/session security.

**Architecture:** Authenticated backend discovery selects the ERP module or existing Go handlers for one approved upstream per hospital. ERP business traffic uses an opaque HTTP relay; Gateway auth, registry, management and delivery control stay local. Discovery failure never authorizes fallback.

**Spec:** Latest user requirements in the ERP/Mobile/Gateway migration review, superseding business DTO coupling in the earlier full migration plan.

## Constraints

- Gateway source only; no automatic commit, push, deployment or ERP changes. The user's separate 2026-11-28 instruction authorizes source commit/push; deployment remains a separate action.
- Keep token exchange, persistent sessions, hospital isolation, mTLS and egress controls.
- No business JSON decoding, field/error whitelists, URL enrichment or multipart reconstruction in ERP relay mode.
- Preserve existing Go upstream behavior until each hospital is explicitly cut over.
- Never call retired ERP implementations as fallback.

## Review focus

- Unknown fields, endpoints, errors and pagination metadata survive unchanged.
- Encoded traversal and reserved namespaces cannot expose management/auth endpoints.
- Client credentials, cookies and forwarding headers never become trusted upstream identity.
- Binary HEAD/Range, multipart fields and response end-to-end headers survive transport.
- Failed discovery, changed hospital configuration, revoked sessions and interrupted streams fail safely.

## Tasks

- [x] Add meaningful fidelity/security tests in `backend/internal/gateway/business_relay_test.go`; initial run failed before implementation (13 cases).
- [x] Implement opaque ERP dispatch and transport in `backend/internal/gateway/business_relay.go`, using `directoryUpstreamPath` backend discovery and existing session authorization.
- [x] Integrate dispatch ahead of business handlers in `server.go`; keep reserved local namespaces separate and retain Go handlers as compatibility-only.
- [x] Seed authenticated backend discovery after successful admin connection verification, avoiding redundant probes while preserving configuration-bound cache invalidation.
- [x] Add relay documentation and run focused tests, all Gateway tests with race detection, `go vet ./...` and whitespace checks.
- [ ] Report passing evidence and remaining ERP/runtime/pilot limitations; do not claim the full migration is complete.

## Verification

From `gateway/backend`: `rtk go test ./internal/gateway -run TestBusinessRelay -count=1`, then `rtk go test -race ./...` and `rtk go vet ./...`. From `gateway`: `rtk git diff --check`.

Fresh verification on 2026-11-27: full race suite passed 773 tests across 10 packages; `go vet ./...`, `gofmt -l` for changed Go files and `git diff --check` exited 0. The first full run exposed a deliberate traversal rejection changing from the old handler's 404 to the transport guard's 400; its regression expectation was updated, with the no-upstream-call assertion retained. An earlier disk-full run is not counted as passing evidence. No runtime deployment or live ERP/MySQL/device checks were performed by this Gateway task.

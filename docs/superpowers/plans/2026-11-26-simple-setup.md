# Single-switch Setup Implementation Plan

**Goal:** Make normal Gateway installation require only SETUP=true, with durable access policy and automatic Setup closure.

**Architecture:** Bootstrap derives and authenticates a request origin; a serializable webtransport policy becomes part of RuntimeConfig. SETUP controls only bootstrap availability. Runtime handlers resolve saved policy before legacy environment defaults.

**Tech Stack:** Go, embedded Setup HTML/JS, Angular, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-11-26-simple-setup-design.md`

## Global constraints

- Work in this Gateway checkout; no ERP writes, worktrees, dependency installation or live service/config/key changes.
- Preserve upstream HTTPS/mTLS and the existing one-time-code/CSRF protections.
- Existing strict APIs/tests retain their behavior; nil SetupEnabled means legacy bootstrap behavior.
- Persist optional `RuntimeConfig.WebAccess *webtransport.Settings` (`web_access`), leaving version 1 and all existing fields intact.

## Interfaces

- webtransport: `Settings` with allow_lan_http, lan_http_cidrs and allow_local_http JSON fields; `DefaultSetupPolicy() Policy`, `FromSettings(Settings) (Policy,error)`, `Policy.Settings() Settings`, `Policy.LocalHTTPEnabled() bool`, `Policy.BootstrapOrigin(*http.Request) (string,bool)`. Existing New/ValidateURL/AllowsLANRequest/Origin remain.
- bootstrap: `Options.SetupEnabled *bool`, `RuntimeConfig.ManagementPolicy(fallback webtransport.Policy) (webtransport.Policy,error)`. Saved policy takes precedence; setup session origin binding is internal.
- Metadata: existing lan_http_enabled, new local_http_enabled and setup_enabled booleans. An authenticated Setup session returns canonical public_url in both status and session responses, without replacing a form the operator has already edited.

## Tasks

- [x] Shared policy/bootstrap owner: managed local/private defaults, serializable policy, code/session/origin/disable gates, activation/migration/settings/recovery integration.
- [x] Runtime owner: parse SETUP with legacy compatibility, policy resolution per runtime and CLI, metadata/cookies/guards, focused main/runtime regressions.
- [x] UI owner: consume authenticated URL suggestion/local capability, show appropriate simple-mode help and advanced proxy fields, preserve CSR/validity helpers and dirty-input behavior.
- [x] Root: simplify .env examples and Compose defaults; document new/upgrade/proxy workflows and SETUP semantics.
- [x] Verify fresh private/local setup -> actual Admin -> SETUP=false restart and auto-closure, preserved key/config/identity, advanced CIDR migration, HTTPS and denial cases.
- [x] Run frontend tests/build/embed, Go tests/vet/Linux build, read-only Compose checks and independent review. Use a temporary isolated UI preview if needed; do not deploy customer services.

Delivery after verification: Gateway commit/push using the continuing delivery authorization, with exact server commands and no overwrite of the customer's live .env or data.

## Verification results

- Frontend: 128 tests passed in 11 files; production Angular build and Go embed staging passed.
- Go: 511 tests passed in 8 packages; go vet and static Linux/amd64 build passed.
- Private/local real-handler Setup -> Admin -> SETUP=false/true restart preserved policy, password, encryption key and identity.
- Found and fixed retired-runtime SQLite close timing; deterministic regression fails before the fix and passes after it, with 40 repeated restart/lifecycle runs passing.
- Targeted race-detector run for the retired-runtime close regression passed.
- Restore rejects broader HTTP/local capabilities or CIDRs than the destination policy; manual authenticated URL metadata regression passed.
- Read-only Compose checks passed for default, true, false, custom port/bind and invalid-empty SETUP propagation.
- Independent review closed all findings. Isolated browser preview verified code unlock, URL suggestion, conditional proxy fields, mobile/light/dark layouts and no overflow; preview server stopped.
- No live .env, customer container, CA, ERP or production deployment was changed.

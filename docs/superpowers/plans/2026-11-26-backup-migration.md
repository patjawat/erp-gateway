# Gateway Backup and Migration Implementation Plan

> **For agentic workers:** Execute task by task in the authorized current session, using available collaboration tools. Do not install missing implementation skills or create worktrees. Steps use checkboxes for tracking.

**Goal:** Deliver complete Gateway backup/download/restore, direct migration and configurable personal-Drive protection.

**Architecture:** A Restic repository and bounded SQLite online snapshot form the reusable engine. Bootstrap owns consistency, durable cutover, jobs and HTTP authorization integration; the existing Gateway authenticates Admin routes. Cloud OAuth/rclone and Angular/Setup surfaces consume that engine without host Docker/SSH privileges.

**Tech Stack:** Go 1.26, modernc SQLite, pinned Restic/rclone, Angular/Tailwind, owner Google OAuth.

**Spec:** ../specs/2026-11-26-backup-migration-design.md (approved).

## Global Constraints

- Gateway only; do not modify ERP or live user volumes/accounts/services.
- Every shell command starts with `rtk`; no new worktrees, simulators, host cron or Docker socket.
- Preserve v1 config recovery and arbitrary nonempty Admin passwords.
- Complete matching DB/config/key/identity; offline CA key excluded.
- Separate installation-local fences/journals from portable data identity.
- Default interval 15 minutes, timezone Asia/Bangkok; editable persistent settings and bounded server-side history pagination.
- Existing indigo/gold light/dark UI and modal forms; no new design approval round.
- No commit/push/deploy as a side effect of this plan; report verified workspace changes.

## Review Focus

- Crash between data/config replacement must recover one consistent generation (Task 3).
- A request or external worker active at source freeze must drain before final capture (Task 4).
- Wrong secret, corrupt/unbounded archive, malicious paths and stale deltas must fail before active writes (Tasks 1, 3, 4).
- Lost transfer/activation responses must never resume both installations (Task 4).
- Policy edits versus destructive retention, revoked OAuth and stale UI responses must preserve the latest verified good copy (Tasks 2, 5, 6).

### Task 1: Consistent encrypted backup engine

**Files:** create backend/internal/backup/{sqlite,repository,bundle}.go and corresponding tests.
**Interfaces:** `SnapshotSQLite(ctx,source,destination) error`; `VerifySQLite(ctx,path) error`; `Repository{Binary,Path,PasswordFile,Env}` with Init/Backup/Restore/Check/Snapshots/Forget; Pack/Unpack streamed versioned bundles.

- [x] Test live writes during snapshot, corruption, cancellation and path/type/size rejection using temporary fixtures.
- [x] Implement read-only dedicated SQLite backup connection and bounded Restic subprocess handling.
- [x] Run real encrypted repository backup/check/export/import/restore tests with a verified temporary binary.

### Task 2: Owner Drive OAuth and bundled tools

**Files:** create backend/internal/backupcloud/*.go and tests; deploy/gateway/install-backup-tools.sh; modify Dockerfile.
**Interfaces:** `Credentials`, `Attempt`; Begin/Exchange/Refresh/EnsureFolder; Prepare returning private `Prepared{Repository,Env}` with Credentials/Close.

- [x] Test PKCE/state, owner folder scope, refresh/revocation, and private rclone-config lifecycle against fake endpoints.
- [x] Implement Google HTTPS/localhost callbacks, encrypted credential persistence integration hook and fixed rclone remote.
- [x] Bundle checksum-pinned Linux arm64/amd64 tools; verify downloads without installing host packages.

### Task 3: Bootstrap capture, jobs and portable recovery

**Files:** create backend/internal/bootstrap/backup_{service,capture,http,restore}.go and tests; modify manager.go, main.go, gateway Config/admin route wiring and root Compose.
**Interfaces:** `Manager.StartBackups(ctx)`; `Manager.BackupHandler()` for authenticated delegation; fresh Setup routes reuse Setup code/CSRF. Job terminal state `completed`; inspect restore before explicit activation.

- [x] Capture matching config/key/cert/FCM/policy/cloud state under short generation locks; verify DB sentinel read-only.
- [x] Persist private local catalog, recovery kit and bounded jobs; stream downloads, never expose secrets in status.
- [x] Stage and validate portable restore, normalize paths, quarantine external work, journal data/config cutover with crash rollback before activation only.
- [x] Test complete cold restore, identity/token continuity, wrong key/secret, expired-cert repair, source-active confirmation and interrupted cutover.

### Task 4: Direct migration and durable source fence

**Files:** create backend/internal/bootstrap/backup_migration.go and tests; add request/worker freeze hooks to manager.go.
**Interfaces:** receiver pairing, scoped resumable transfer sessions; prepare/finalize/activate/cancel endpoints under backups/migration plus bounded transfer-only transport.

- [x] Persist source fence before drain, gate new mutating traffic and join workers; exclude local fence from portable capture.
- [x] Implement authenticated HTTPS pairing, explicit LAN opt-in, DNS/address/redirect validation and bounded resumable encrypted transfer into staging.
- [x] Pin base/final repository state, validate session/base/generation/object bindings, then require destination activation; retain file fallback.
- [x] Test interruption/resume, unrelated transfer, SSRF, restart fence, in-flight drain, lost activation acknowledgement and safe cancellation.

### Task 5: Configurable protection and retention

**Files:** create backend/internal/bootstrap/backup_policy.go and tests; integrate backupcloud with service.
**Interfaces:** versioned policy/revision, interval/daily/weekly next-run, local/Drive independent retention, paused imports, in-app health.

- [x] Implement no-overlap scheduler, coalesced ticks and local completion independent of cloud failure.
- [x] Serialize policy activation and forget/prune under dedicated service lock; preserve pinned/last-good copies.
- [x] Test timezone/cadence/next-run, hot persistence, local-only state, failed uploads and revision races.

### Task 6: Admin and Setup UX, operator guide and final verification

**Files:** frontend backup page/models/tests, route/nav/API helpers; backend/internal/admin/assets/{index.html,guided.js}; docs and README.
**Interfaces:** /admin/api/backups and /setup/api/backups contracts shared with bootstrap; native streamed download, async jobs, cursor history.

- [x] Implement manual backup/download/kit/import, status/settings modals, Google connection and direct migration controls.
- [x] Test first-page/lazy pagination, stale responses, keyboard/error states, polling cleanup and fresh Setup import.
- [x] Run backend tests/race checks for changed packages, frontend tests/build and isolated end-to-end backup/restore/transfer rehearsal.
- [x] Inspect rendered light/dark/responsive screens; document actual checks and unresolved external prerequisites, including real owner Google consent.

Self-review: every approved spec section maps to tasks above. Dependencies are engine -> capture/restore -> migration/scheduling -> integrated UI; independent cloud/engine/UI work can run concurrently with explicit contracts. No new approval gate is required.

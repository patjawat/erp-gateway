# Gateway local administrator password reset implementation plan

> Execute in this Codex session with parallel work on independent files, as project instructions require. Do not create a worktree or install additional skills.

**Goal:** Let the server owner recover a forgotten Gateway Admin password with `gateway setup-password`, preserving configuration, identity, encrypted data and existing Mobile sessions.

**Architecture:** An interactive local CLI sends the confirmed new password over an owner-only Unix socket to the running Gateway. The manager hashes the password, builds a replacement runtime with the same encryption key and configuration, atomically persists the verifier, and drains the previous runtime. This closes old Admin sessions while existing Mobile tokens remain in the same database. No public password-reset HTTP endpoint or automatic environment override is introduced.

**Tech stack:** Existing Go 1.26.11, `golang.org/x/sys/unix`, net/http over Unix sockets, existing PBKDF2 and runtime handoff. No new dependencies.

**Spec:** User request in this chat on 2026-11-26: provide a usable local password recovery command similar to initial setup and prove that deployed data and Mobile usage are preserved.

## Global constraints

- Gateway files only; ERP remains untouched. Shell commands start with `rtk`.
- Do not reset the user's actual account, read their password/private keys, restart their services, or change their volumes during testing.
- CLI command: `docker compose exec gateway /usr/local/bin/gateway setup-password`.
- Prompt twice with terminal echo disabled; use the existing 16–4096 byte Admin password range. Never accept passwords in arguments/environment or print/log them.
- Preserve username, identity/CA, encryption key, URL, network policy, hospital records and Mobile tokens. Do not initialize Setup or a new database.
- Socket: `sock` (mode `0600`) inside a private `.admin/` config subdirectory (mode `0700`), with a lifetime lock preventing another control listener from replacing an active socket. Reject unsafe preexisting paths.
- CLI and private listener work on Linux containers and native macOS. A failure before atomic config replacement leaves the old account/runtime usable. A post-rename directory-sync warning must install the confirmed new verifier and clearly report that the password changed.
- A failure to open the optional local control channel must not stop an otherwise healthy public Gateway/Mobile runtime; retain secure permissions and log the unavailable control channel.

## Review focus

1. Old Admin cookies must stop authenticating after reset while Mobile tokens and an in-flight Mobile request survive.
2. Shell history, terminal echo, errors, logs and responses must not disclose the new password.
3. Socket files/locks must resist unsafe path replacement and be cleaned up without deleting unrelated files.
4. Failed persistence/build, cancellation, invalid input, unconfigured/legacy and external config changes must not alter the account.
5. The command must work with `SETUP=false`, preserve data after restart and provide a useful error when the running image lacks the control listener.

### Task 1: Atomic password-only manager operation

**Files:** Create `backend/internal/bootstrap/password.go` and `password_test.go`.

**Interface:** `func (m *Manager) ResetAdminPassword(context.Context, string) (string, error)` returns the unchanged username on success.

- [x] Add failing tests for new/old password verification, Admin runtime replacement, identical non-password config/key/identity, validation/build/persist/cancel failures, closed/unconfigured/legacy rejection and `SETUP=false`.
- [x] Implement using the manager mutex, existing config lock, `currentLocked`, `passwordConfig`, candidate Build and atomic persistence. Close a failed candidate and retain the previous runtime on failure.
- [x] Run targeted and race tests; prove no live/test database or key is regenerated.

### Task 2: Private local transport and terminal CLI

**Files:** Create `backend/internal/bootstrap/admin_control.go` and tests; add focused terminal/CLI files under `backend/cmd/gateway/`; integrate command/listener in `main.go`.

**Interfaces:** `StartAdminControl(directory string, reset func(context.Context, string) (string, error)) (io.Closer, error)` and `RequestAdminPasswordReset(context.Context, directory string, password string) (string, error)`.

- [x] Write failing tests for private socket permissions, one owner, unsafe stale paths, malformed/oversized requests, cleanup, forwarding and bounded client errors.
- [x] Add the listener with no TCP exposure and no password logging; add `setup-password` to supported commands before normal startup.
- [x] Add hidden two-entry terminal input on Linux/macOS using existing unix APIs, restore terminal state on error/cancellation, reject non-terminal use and mismatched confirmation.
- [x] Run targeted tests and compile for Linux/arm64 and macOS host.

### Task 3: Real application and operator verification

**Files:** Add a Gateway integration test and update `.env.example`, `README.md`, `deploy/gateway/WEB_SETUP.md`.

- [x] Test a real persistent Gateway with hospital data, a Mobile token and Admin cookie before/after local reset: old Admin login/cookie rejected, new login accepted, existing Mobile token/proxy request succeeds and data/config/keys remain.
- [x] Update forgotten-password instructions with the exact command, unchanged username, hidden prompts, no restart requirement and the distinction from `setup-code`/CA passphrase/legacy env.
- [x] Run full Go tests, vet, focused races and Compose resolution. Build a separate Docker image/container/volumes and exercise `docker compose exec ... setup-password` through a pseudo-terminal with disposable credentials.
- [x] Restart only the disposable Gateway, verify new login/data, then remove only test resources. Review the complete diff before delivery.

## Verification evidence

- Full Go tests and vet passed; focused manager, private control/CLI and Mobile integration race tests passed.
- Linux/arm64 Docker image built and ran; static Linux/amd64 and native macOS build checks passed.
- Real Docker Compose interactive reset with SETUP=false accepted matching hidden entries, rejected mismatched confirmation, invalidated old Admin cookies/password and accepted the new password without restarting the container.
- Hospital record, non-password config, encryption key and certificate/private-key fingerprints stayed unchanged; the new login and hospital data survived a subsequent restart.
- Persistent Gateway/ERP fixture tests kept an existing Mobile token and an in-flight Mobile request working during runtime handoff and after restart.
- Native macOS PTY tests verified successful private-socket RPC, secret-free output and restored terminal state; failure/cancellation cases passed.
- Compose example resolution passed. Only disposable test resources were removed; the actual local Gateway account was not reset.

Commit and push under existing authorization after the final diff check.

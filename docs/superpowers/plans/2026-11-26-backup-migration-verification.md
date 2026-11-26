# Backup and migration verification — 26 November 2026

Final aggregate backend run: passed (`go test -p 1 -race -count=1 ./...`, with verified real Restic/rclone binaries supplied). All backend packages passed; frontend 182/182 passed. Product implementation and isolated packaged-image rehearsal are complete.

The approved implementation is in the current Gateway working tree. No ERP files, user Gateway volumes/accounts, production services, remote Git branches or Google accounts were changed for verification.

## Verified behavior

- Complete online SQLite/config/key/client-identity capture with live transactional writes; wrong secret/key, damaged archive, unsafe file types/paths and partial tool results fail before activation.
- Manual capture publishes the verified local file/backup-specific kit before cloud upload finishes. Native downloads stream and hold per-record leases rather than the repository policy lock.
- Fresh Setup import and authenticated replacement retain original Admin credentials and Mobile session/encryption data. A fresh valid Mobile token successfully called a real test ERP through restored Gateway mTLS.
- Journaled cutover, per-restore receipts, failed-build rollback, cancelled rollback and restart recovery preserve matching data/config/state. Expired client/Hospital CA data enters repair; CSR/renewal retains the original key.
- Direct HTTPS pairing, authenticated resumable objects, base/final binding and durable source fencing. A real TLS test deliberately loses a chunk acknowledgement, resumes, includes writes after the base, and sends no base object again in the final transfer.
- Historical base inspections cannot bypass final activation. Lost pairing responses can recover before the first manifest. A known-active destination cannot be bypassed by offline cancellation. Slow unauthenticated pairing does not hold migration control locks.
- Interval/daily/weekly policies, timezone, revision conflicts, retention grouping, pinned/last-good protection and local-only status. Policy save rejects busy maintenance promptly rather than mutating after a timed-out request.
- Owner Drive OAuth/PKCE, refresh/revocation, scoped folder/cursor pagination, connection replacement and fresh recovery-token consumption against fake provider endpoints. Unknown remote snapshots remain preserved; deleted references are not ranked as available copies.
- Restored notification review uses bounded server pagination and confirmed resume/cancel; completed/accepted/invalid-token work remains terminal. Retired updater work is cancel-only. Backup pin/delete supports local/Drive/both and protects live downloads/transfers and the last verified copy.
- Running-job cancellation preserves ready files and does not resume a fenced source. Restic gets SIGINT with a bounded grace period. A committed foreign-host crash lock was reproduced; controlled private-local recovery and exact immutable staged reads were tested without forcing shared Drive unlocks.
- Remote metadata checks run on each upload. Rotating data reads and fresh-cache real restore rehearsals run when due on a subsequent backup; corrupted data cannot advance successful rehearsal time.

## Evidence

- Angular: `npm test -- --watch=false` — 182 tests passed; production Angular build passed.
- Native core: real Restic 0.18.1/rclone 1.72.0 integrations, race detector, cancellation/crash recovery, exact restore, metadata/subset checks. Committed-lock cancellation/recovery repeated five times each: 10/10 passed.
- Go static analysis: `go vet ./...` passed. Go formatting and Git whitespace checks passed.
- Backend verification serializes packages on the resource-constrained Mac. The existing password-drain test passed twice independently and in the final complete serial race run; it was not weakened after a timeout during simultaneous image builds.
- Docker Compose: `docker compose --env-file /dev/null config --quiet` passed without reading the user's live environment file.
- Full images built for Linux arm64 and amd64. The amd64 image was built without running emulation; its runtime was not exercised on an amd64 server.
- Packaged Linux arm64 image ran as UID/GID 65532 with read-only root, dropped capabilities, 16 MiB `/tmp`, and two separate anonymous test volumes. Its actual private-socket CLI imported a synthetic Mac-generated full backup, passed healthcheck, exported it again, and the exported bundle was verified back on Mac with original Admin verifier, hospital and session counts. Rehearsal containers/volumes are removed after checks.
- Rendered Angular/Setup checked in isolated native Chrome at 1440/390 px, light/dark, including policy, recovery, work review, deletion scopes and job cancellation. No document overflow; original Gateway UI processes were not reused.

## Practical limits and owner checks

- Personal Google consent/client configuration and a real Drive upload require the owner's account; automated provider tests do not prove real-account connectivity. Follow `deploy/gateway/GOOGLE_DRIVE_BACKUP.md` and export a new offline kit after connecting.
- Direct transfer requires reachable HTTPS with a trusted hostname/IP certificate. DNS/reverse-proxy routing and production ERP reachability remain operator prerequisites; Mobile cannot discover a changed URL automatically.
- Ordinary backups contain only their recorded capture point. Planned live migration needs the final fenced capture. No fixed downtime or zero-data-loss disaster-recovery promise is made.
- Current portable/direct transfer limit is 16 GiB; staging/rollback need additional space. ERP data and offline CA signing keys are outside this Gateway backup.
- The owner deferred connecting Google Drive; keep that connection optional and inactive until requested. Commit/push was authorized separately after verification; production deployment remains pending. Rebuild the Gateway image to expose the new UI and bundled tools on an existing installation, preserving its Compose project and volumes.

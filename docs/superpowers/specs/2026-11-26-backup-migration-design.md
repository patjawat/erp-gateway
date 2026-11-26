# Gateway full backup, restore and server migration

Status: approved by owner on 26 November 2026; implementation authorized in the current Gateway workspace.

## Intent and agreed requirements

The owner operates the central Gateway alone. They need to move a configured Gateway from Docker on a Mac to a Linux production server, recover quickly after a failure, and keep automatic off-machine backups in their personal Gmail/Google Drive account. The selected initial backup interval is 15 minutes, and the owner explicitly requires backup settings to remain configurable. The interval is a default rather than a fixed policy. This is a snapshot interval and an approximate recovery-point target, not a guarantee that every upload completes within 15 minutes.

The owner explicitly requires a **สำรองข้อมูลตอนนี้ (Back up now)** button, a downloadable complete backup, and import from the fresh machine's Setup page. The normal path must work without connecting Google Drive. Preserve the configured Gateway identity and data so operation can continue after restore, with the smallest practical service interruption. An ordinary backup represents its captured point in time; changes made afterward require a final migration capture and are not automatically present in the downloaded file.

Retain the existing indigo/gold Angular design, light/dark themes and modal form pattern. Installation remains Docker Compose based. The user should not install backup tools on the server host or assemble SQLite/config/key files manually.

## Approaches and recommendation

1. **Full encrypted file on every run, uploaded with rclone.** One format is easy to download and import, but frequent uploads repeat the whole dataset and can consume personal Drive storage quickly.
2. **One consistent capture engine, portable migration export, and Restic through rclone for scheduled backups.** Restic supplies established encryption, compression, integrity checks and deduplication; the portable export still supports moving a single file between machines. This adds bundled tools but reduces repeated off-site data and avoids inventing streaming cryptography.

Approach 2 was accepted with the complete plan. The owner selected personal Drive, a configurable 15-minute default, portable import and direct server transfer. Bundle pinned, checksum-verified Restic/rclone Linux binaries in the Gateway image. Do not require a host cron job, Docker socket, remote shell runner or a Google Drive plugin in the operator's chat client.

## Scope

- Capture the complete Gateway SQLite database and the matching persistent configuration, Admin verifier, original encryption key, active client certificate/private key and Hospital server CA.
- Include explicitly configured Gateway-owned integration settings and credentials needed for recovery, such as Drive connection and FCM configuration. External credentials require explicit inclusion rules and fixed destination paths; never accept arbitrary host paths from the browser.
- Keep the offline CA signing private key on the owner's Mac. It is not part of Gateway backups.
- Preserve the current v1 encrypted configuration-recovery format and its restore command for compatibility. The new full-backup flow uses a distinct versioned format/catalog.
- This project protects Gateway state. It does not back up or control ERP databases, ERP images/migrations, Mobile source, Gateway source/images, Jenkins builds or retired release-TAR workflows.

## Complete, consistent capture

Create a bounded-memory SQLite snapshot using the existing modernc SQLite online-backup API and a dedicated read-only source connection. Step through pages with cancellation, bounded busy retry and progress; do not reserve Store's sole operational connection for the entire copy. Never copy a changing DB/WAL pair as independent files.

Capture config/key/identity references under a short manager lock, record a monotonic runtime generation, then release the lock for the database snapshot. Recheck the generation and protected file identities before sealing the dataset; discard/retry with a bound if configuration or credentials changed. Holding Manager.mu for the whole snapshot would block new Mobile requests and is prohibited.

The captured dataset has fixed logical filenames and a manifest: backup format version, Gateway schema compatibility version, instance/dataset ID, creation time, app version when known, file sizes and cryptographic checksums. Package only explicitly selected persistent files; exclude Unix sockets, lock files, temporary jobs, Setup codes and plaintext credential staging files. There must be no CA/key generation during capture.

Separate persistent dataset identity from an installation-local control identity. Source migration fences and activation/cutover journals are durable installation-local control records, not portable runtime configuration. Keep them outside the captured operational dataset; retain source identity/freeze provenance as inert encrypted backup metadata. A restored server gets a fresh destination installation ID and its own inactive/activation journal. Import must never cause source-side control records to resume, retire or pause the destination, and must not clear the still-running source's durable fence.

Verify the copied DB read-only with SQLite integrity checks and the encryption sentinel before recording capture success. Inspection must not call OpenStore, which can initialize/migrate data. Source Gateway traffic continues during normal capture; progress/status must distinguish waiting, capturing, verifying and uploading.

## Storage, encryption and portable export

Use Restic repositories for encrypted storage. Persisted local staging is private to the service account; stream files rather than placing an entire database/archive in memory. Upload only encrypted repository objects or an encrypted portable repository bundle.

For a portable export, materialize only the selected snapshot into a new local encrypted Restic repository and package that repository with a minimal, versioned technical wrapper. The package is one downloadable file, contains no repository password/recovery key, and can be imported independently of Google Drive. Do not put business names, secrets or plaintext config in an outer manifest.

For staged migration, keep a dedicated, pinned export repository through the base/final transfer. The base download is self-contained. Append the final complete capture to that same repository and transfer the newly required immutable repository objects in a delta package; this is repository-object deduplication, not SQL replay or a partial database applied to active data. Do not re-encrypt the final capture into an unrelated repository and call it a delta. Do not prune, repack, change repository keys or remove base objects while the migration is pinned. Freeze each export before packaging and exclude transient repository locks. Compute and verify a bounded, complete required-object inventory; never infer completeness from file timestamps alone. Some database changes may deduplicate poorly, so incremental transfer is an optimization rather than a bound on downtime or transferred bytes.

Create a separate random recovery secret for backup encryption. Automatic backup can access its protected local copy; the owner must also receive a recovery-key file/instructions for offline storage or a password manager. Do not derive the only backup-opening secret from an encryption key that is itself only inside the backup. The recovery kit identifies the repository/folder and OAuth client used, but it is never uploaded beside the backup in plaintext. Clearly report whether recovery material has been exported.

The private recovery-kit file contains the backup repository secret, repository/dataset/folder identifiers, the Google OAuth client ID and client secret, and format metadata. It excludes Google access/refresh tokens. Treat the entire kit as secret material. On a fresh host, the locally authenticated recovery wizard reads this kit, uses its original OAuth client to let the owner sign in to Google again, then accesses/decrypts the selected Drive snapshot. This path does not require the failed machine, its secret store or a separate visit to Google Cloud to rediscover the client secret. Local-file restore needs only the bundle and repository secret/kit. The kit remains usable independently of Admin credentials; loss of every copy of the kit/secret cannot be repaired by resetting the Admin password.

Keep normal Admin password policy separate: Admin passwords may be any nonempty value. This does not make an Admin login password the automatic backup encryption secret.

## Personal Google Drive connection

Use OAuth for the owner's personal Drive, with the Gateway owner's own Google OAuth client. Do not rely on a shared rclone OAuth client or on a service account owning files in personal Drive.

Create one application-owned backup folder; prefer drive.file access so the integration manages only files created/authorized for this application. Persist its folder ID and the OAuth client identity in recovery metadata. Reuse the same OAuth client when recovering an existing application-owned folder. Avoid broad personal-Drive browsing.

The first implementation must document and test two supported callback modes:

- Registered HTTPS callback on the production Gateway.
- Registered localhost callback for a Mac/local Gateway, or the same localhost callback carried through an SSH tunnel to a headless/private server. Do not claim arbitrary LAN HTTP IP callbacks are accepted by Google.

Use state, PKCE, expiration and single use for connection attempts, tied to the authenticated owner. Never display refresh tokens in browser responses, logs, process arguments or history. The owner performs Google sign-in/consent; no real account connection is required for automated tests. Google OAuth setup must include durable consent configuration rather than leaving an External app in a seven-day Testing-token state.

Store refresh credentials encrypted using the Gateway secret store. Materialize only a private temporary rclone config for a fixed subprocess invocation, persist refreshed credentials even when a later transfer fails, and remove temporary plaintext credentials. Use fixed bundled executable paths and allowlisted operations; no browser-supplied shell command, backend type or arbitrary destination URL.

Show connection state, destination folder, space information when available, and reconnect instructions. Quota, revoked permissions, expired refresh credentials or interrupted uploads must retain existing good backups and produce a visible failure state.

## Schedule, retention and observability

Provide a Backup settings modal in the Admin page. Configuration is stored persistently and included in full backups; common settings require no .env edits, host cron setup or Gateway restart. Fifteen minutes is the initial editable interval. Manual Back up now remains available independently of the automatic schedule.

Editable settings:

- **Automatic backup:** enabled/paused. Pausing stops new scheduled jobs and allows the current job to finish; cancelling a running job is a separate explicit action. Pausing never removes existing backups.
- **Schedule:** every configurable number of minutes/hours, daily at a chosen 24-hour time, or selected weekdays/time. Provide common interval presets and a custom interval. Use a stored timezone, default Asia/Bangkok, and display the next run. Use controlled fields rather than a shell command or user-authored cron string.
- **Destination:** managed local storage, or managed local storage plus the connected personal Google Drive folder. Drive connection/folder changes are validated before switching; keep existing valid backups accessible. Do not expose arbitrary host paths, additional providers or rclone commands as destination settings.
- **Retention:** independent local/Drive settings for latest-count and daily/weekly/monthly snapshots. Also offer an explicit keep-until-manually-deleted mode. Retain pinned migration/rollback snapshots and at least one verified good copy; changing policy cannot silently delete every usable backup.
- **In-app alerts:** enable/disable failure/stale-backup alerts and choose the stale threshold/grace period. Report Drive quota/connection problems and actual last-success time. New email/SMS/chat integrations are outside this scope.

Schedule changes take effect after save and recalculate the next run; they do not enqueue all missed ticks or unexpectedly trigger a restore/backup. Each job captures the policy/destination revision it uses. Serialize policy activation and destructive forget/prune operations with a dedicated repository-policy lock, independent of the public request/manager lock. A revision recheck alone is insufficient. If destructive maintenance is already running, show the change as pending until maintenance finishes or cancels safely; do not report Save complete or display the new retention policy as active while the old policy can still delete data. Recheck the current policy under that lock before each later destructive operation. Completed deletions cannot be retroactively cancelled. Persist/audit settings without logging secrets. Store a policy schema version separately from its edit revision; restore must validate compatibility or migrate in staging before commit, never silently enable an unsupported policy. Test settings persistence across restart and backup/restore.

Prevent overlapping capture/repository maintenance jobs; coalesce missed ticks rather than building an unbounded backlog. A local completed capture and an off-site completed backup have distinct statuses. Display the timestamp/age of the newest verified off-site snapshot and the protection target derived from current settings. A daily schedule must not continue displaying a fixed 15-minute protection promise. Changing the cadence updates the default target/alert timing; an explicitly customized stale threshold stays visible and must be validated against the selected schedule.

For local-only mode, show off-site protection as disabled/not configured and suppress off-site stale alerts and scheduled Drive maintenance. Continue showing the local latest verified snapshot and local health. Previously created Drive copies remain in history with their last-known/provider connection status; switching to local-only does not delete them or mislabel a local capture as a successful Drive upload.

Assistant-proposed editable retention defaults: local last 3 verified snapshots; Drive last 24 snapshots plus 7 daily, 4 weekly and 3 monthly snapshots. These counts are defaults, not fixed limits. Establish stable dataset/instance tags and explicit retention grouping across machines; changing temporary paths/hostnames must not silently disable retention. Show expected storage usage where measurable. Apply deletion only after a newer verified snapshot exists; preserve pinned migration/rollback snapshots until explicitly released. In keep-until-manually-deleted mode, storage pressure produces a visible failure/alert rather than silently deleting snapshots contrary to the selected policy.

All schedule/destination variants still capture complete Gateway state with its matching config/key/identity; do not expose a partial-backup option that appears fully restorable while omitting required keys. After restore, retain the imported settings but keep automatic scheduling paused until destination activation is confirmed, so the source and restored copy do not both resume the same operational schedule during cutover.

Separate retention metadata removal from repository pruning. Schedule integrity checks and rotating data-subset reads, plus a real restore rehearsal. Restic partial-backup exit status must not be treated as success. Failed captures/uploads do not advance last-success time.

Backup history, remote selectors and job lists use server-side filters, stable cursor pagination and first-page loading only. The UI must not load every snapshot on entry. Background catalog reconciliation has bounded work/retry and provider pagination; the page reads the local catalog and server aggregates.

## Restore and migration workflow

Expose a Backup and migration page with status, Back up now, Download migration file, Restore, Drive connection and schedule/retention forms in existing modals. The fresh Setup page offers Set up new and Restore existing Gateway. A local CLI emergency path remains available when the Admin UI is unavailable.

### Manual backup and fresh-machine import

The owner-visible path is:

1. On the running Gateway, press **สำรองข้อมูลตอนนี้**. Create a complete local capture and portable encrypted export without pausing Mobile service or waiting for Google Drive. If a job is already running, show that job and its progress rather than enqueueing duplicate captures. A separate, enabled Drive policy may upload afterward; a Drive failure cannot invalidate a verified local download.
2. Show captured data time, verification result, file size and **ดาวน์โหลดไฟล์สำรอง** when ready. Download uses authenticated, streamed delivery with bounded resource use; do not auto-delete a file while its download/import or a pinned migration still needs it. Show **ดาวน์โหลดชุดกู้คืน** separately and explain that the bundle requires this secret/kit. Normal local backup does not require Google OAuth credentials.
3. On the fresh server, start Gateway with Docker Compose and open Setup. Choose **นำเข้าข้อมูล Gateway เดิม**, select the backup and supply its recovery secret/kit. Inspect and stage the complete state before enabling public traffic. The wizard should not ask the owner to recreate hospitals, Admin credentials, ERP secrets or certificates already contained in the verified backup.
4. Show readiness checks for destination URL/HTTPS or the explicitly configured LAN mode, ERP connectivity, client-certificate validity and data compatibility. Provide local repair actions when a prerequisite fails. A successful import does not imply an expired certificate, missing reverse proxy or changed Mobile endpoint has been repaired automatically.
5. After confirmation, activate the destination using the journaled cutover below. Resume permitted work and keep imported automatic backup schedules paused until activation is confirmed. Preserve the existing Admin credentials and valid Mobile authorization present at the capture point. Show a clear service-ready result and the restored data time.

Ordinary disaster recovery uses that file's capture time. Records, sessions and configuration created on the source after capture cannot be recovered from this file. If the source still exists, the owner must stop or fence it before enabling the replacement; the same Gateway identity and external jobs must not run on both machines. For a planned move with continuing source activity, guide the owner to staged migration instead of implying that an old online backup is the final state.

### Validation and destination cutover

The restore flow accepts a portable file or a selected Drive snapshot plus the separate recovery secret. Show backup time, compatibility, data summary, certificate state and the destination URL before applying anything. A healthy target requires explicit confirmation tied to the inspected backup ID; an unconfigured target requires the locally issued Setup code. Do not silently replace an existing installation.

Restore in private staging, verify archive names/types/size limits/checksums, decrypt, validate DB integrity/encryption and settings, then prepare destination-owned files and container paths. Do not inherit Mac UID/path values, follow archive symlinks or extract traversal/device/hardlink entries. Do not start notification/usage/external-action workers during inspection or rehearsal.

Because data and config are separate volumes, two renames are not an atomic restore. Use an offline, journaled cutover with old generations retained and a durable commit/rollback record. Staging can occur while the target runs; committing restore drains requests and stops the database/workers before switching both sets. Support interrupted-cutover recovery on next startup and a tested rollback to the old generation. Never inspect or initialize an unverified backup as the active database.

A restore commit may briefly interrupt traffic and invalidates Admin sessions. Existing Mobile sessions can continue after reconnect when restored DB/key, upstream authorization and public endpoint still match, subject to the chosen backup's age and normal token expiry. Do not promise zero lost writes or exactly-once external notifications after disaster recovery. Quarantine/review restored pending external work where replay cannot be proven safe.

Assistant's default replay policy: quarantine every restored not-completed notification/push/webhook or updater-command job, including retry/in-progress rows. They are retained, not deleted or automatically resent. Unknown external-action job types stay disabled until classified. Internal recomputation and read-only usage polling may resume after activation. New jobs created after destination activation run normally. The owner sees a paginated restored-work list and explicitly resumes or cancels selected quarantined jobs; releasing such jobs is never part of a restore dry run. This does not expand the scope to ERP update control.

Automatic rollback is permitted only before the durable destination-active marker, public writes and external workers are enabled. Validation before that boundary is read-only or restricted to the prepared generation and creates no external effects. Once the new destination accepts writes or releases external work, disable automatic fallback to the old generation. Returning to an older dataset is then a separate explicit recovery operation with a fresh backup of the destination and a clear data-time comparison; it must not silently discard newly accepted data. Keep the source paused throughout cutover and operator validation.

### Planned migration with minimal interruption

Support direct transfer as well as downloaded files. An authenticated destination administrator (or locally authenticated fresh Setup) explicitly opens a short-lived receiver and obtains a one-use pairing code. The source administrator supplies the destination HTTPS origin and pairing code. Exchange it for a bounded transfer-only session, bind source/destination installation IDs, and stream resumable encrypted repository objects into private destination staging. Never send the recovery secret over unencrypted HTTP or accept arbitrary remote paths/commands. Validate destination origin, resolved addresses and redirects for every connection; allow LAN addresses only when explicitly selected, always exclude metadata/link-local/unspecified/multicast addresses, and retain TLS certificate verification. A configured destination snapshots its existing state and requires inspected-backup confirmation before replacement. Pairing is not approval to overwrite or activate. A dropped connection never resumes a fenced source automatically. Direct transfer shares the same capture, validation and activation engine as portable files.

Use two stages so full export, upload/import, compatibility checks and destination preparation happen while the source still serves Mobile:

1. **Prepare:** create and pin a migration session/base capture; download/import the self-contained base into the destination's private staging area. Validate space, format, keys, certificates, runtime architecture and endpoint/proxy configuration. Keep the destination inactive: no public writes or external workers. Display which Gateway remains active.
2. **Final capture:** only when the destination is ready, the owner selects **เก็บข้อมูลล่าสุดและเตรียมสลับเครื่อง**. Persist the source's migration fence before acknowledging it, reject new state-changing requests, stop external workers and drain in-flight requests/jobs that can change captured state. Pause any background maintenance or backup-policy mutations that could invalidate the capture. Read-only responses may continue only if they perform no writes or external effects; otherwise return a short maintenance/retry response. Do not generate the final snapshot until draining has succeeded. Surface stuck work and allow cancellation before handoff instead of falsely reporting a consistent final capture.
3. **Final transfer:** make the final complete consistent capture and append it to the pinned migration repository. Download only the new required encrypted objects as the final delta, or use a clearly labeled full final bundle if delta preparation is unavailable. Apply changes only to a private clone of the destination's staged repository. Inspect an authenticated manifest inside the encrypted snapshot that binds migration/session ID, source dataset, repository ID, base snapshot/export ID, a unique final capture ID and source freeze generation. After the final data snapshot is sealed and its ID is known, write a separate handoff descriptor as a small Restic-encrypted metadata snapshot that binds that data snapshot ID, the manifest and the complete required-object inventory for the final data snapshot. The descriptor does not include its own snapshot ID or self-referential object inventory; validate its own repository objects with Restic integrity checks. Outer wrapper metadata is only an untrusted hint. Verify the final snapshot, handoff descriptor, database/config/key consistency and that this final state succeeds the imported base through the session/freeze generation, not wall-clock time alone. Reject unrelated, stale, missing-object or mismatched deltas before changing active data. Never merge a delta into an operational database or guess its missing base.
4. **Switch:** perform the destination's journaled data/config commit and read-only readiness checks while it is still inactive. The owner then switches the existing public endpoint/reverse-proxy route and explicitly activates the prepared final generation. Durable activation records bind the migration ID, final snapshot and destination installation; retries are idempotent. Public writes and external workers start only on that destination after activation. Keep the source durably fenced, including across container/process restarts, until it is explicitly retired or a safe cancellation is confirmed.

The source must **never auto-resume after a timeout, lost browser connection or missing destination acknowledgement**. A lost activation response does not prove the destination stayed inactive. Cancellation/resumption requires the owner to establish that the destination has not activated, or to stop/fence it first and explicitly accept which dataset will be authoritative. Once the destination has accepted new writes, returning to the old source is a new recovery operation with a fresh destination backup, not an automatic migration rollback.

Reuse the same public Gateway hostname/entry point when possible so Mobile reconnects to the same address. Configure DNS TTL/routing, HTTPS and reachability in advance; a changed IP/URL is not discovered by Mobile automatically, and DNS caches may extend the transition. Where the owner controls one reverse proxy, prepare the new upstream before changing its route. Do not require access to the old server after a disaster for the ordinary portable-file restore path.

Show progress for base preparation, final drain/capture, transfer, validation and activation. Measure the interruption from the first source request rejected by the migration fence until the destination is publicly ready, and report its components in the rehearsal. File size, data churn, disk speed, network and endpoint propagation determine the duration; do not promise zero downtime or a fixed number of seconds before measuring. Bounded online snapshotting reduces blocking but does not guarantee zero resource impact.

Preserve Admin username/verifier, encryption key and Gateway client identity. Copying certificate with its original private key retains the same identity and normally avoids a new CSR/CA or Hospital pin change. If a backup certificate has expired, recover the data into a repair state and guide renewal with the preserved key; do not weaken outbound mTLS or generate a replacement identity silently.

Allow production URL/HTTPS/trusted-proxy configuration in the local recovery flow; changing .env alone must not be presented as overriding persisted settings. Fix the root Compose config-directory restore mismatch as part of this feature. Build the application image for the destination architecture; move data rather than an assumed Mac image.

## Failure behavior

- Missing/wrong recovery secret, corrupted/truncated bundle, wrong DB key, schema downgrade, insufficient space or invalid path: stop before changing the active generation; retain the current installation.
- Drive unavailable: retry with bounded backoff, retain a bounded local verified copy and clearly show off-site protection is late.
- Crash during upload: no completed-backup marker; resumable/retried work may proceed without removing good snapshots.
- Crash during restore: use the cutover journal to finish or roll back consistently; never initialize an empty DB/key as a fallback.
- Expired client certificate: preserve restored state and provide repair/renewal before outbound Hospital access is enabled.
- Lost original machine: recovery needs only the code/image, portable bundle or Drive access, and the offline recovery secret; original machine-local encryption material is contained inside the encrypted snapshot.
- Interrupted migration, missing delta or failed final verification: leave the prepared destination inactive and the source durably fenced. Retain verified base/final artifacts and a retry/cancel path; never activate a partial state or auto-resume both machines.

## Acceptance and delivery

1. Populate a real Gateway with hospitals, encrypted ERP tokens, usage/history and pending jobs. Snapshot while writes/Mobile traffic occur; restore into an isolated destination and compare the logical dataset and keys.
2. Move between independent Compose projects/volumes on Mac-compatible Linux containers and Linux server architectures. Verify the same Admin credentials and an unexpired Mobile token against a test ERP, and document endpoint changes.
3. Exercise the cold-start UI and emergency CLI, short Admin credentials, expired certificate repair, destination policy, path/UID normalization, corrupted archives, key mismatch, disk-full and rollback/restart interruption cases.
4. Use a fake OAuth/Drive service for automated token refresh/revocation, quota, interruption, resume, least-scope folder access and 15-minute scheduler tests. Provide a real owner-operated OAuth connection check after setup; never claim real Drive connectivity from mocks.
5. Run actual Restic/rclone integrations with isolated repositories; prove partial failures do not become success, retention grouping stays stable, and downloaded/restored data passes integrity/sentinel checks.
6. Verify UI pagination, modal behavior, light/dark mode, keyboard/error states and stale-response handling. Test manual/paused/interval/daily/weekly schedules, timezone/next-run display, hot settings changes, independent retention and keep-until-manually-deleted behavior, policy-revision rechecks, restart persistence and paused scheduling after restore. New feature behavior is tested before handoff.
7. With Drive disconnected, use **สำรองข้อมูลตอนนี้ → ดาวน์โหลดไฟล์สำรอง → Setup นำเข้าข้อมูล Gateway เดิม** between isolated projects. Prove the source stays available during ordinary capture/export and that the imported dataset matches its recorded capture point. Make a later source change and prove an earlier ordinary file does not claim to contain it.
8. Rehearse staged migration with writes after the base capture and before the final fence. Prove those writes/config changes are in the activated destination; prove the final transfer reuses base objects with a representative dataset without assuming every dataset deduplicates well. Reject cross-session/repository/base deltas and incomplete objects. Kill/restart both sides around drain, transfer, journal commit, activation and lost acknowledgements; prove the source never auto-resumes, the restored destination gets a distinct local installation/control identity, the inactive destination starts no external work, and cancellation cannot silently discard newer destination writes. Measure interruption and document public endpoint/reconnect behavior rather than asserting a fixed downtime target.

Implement in three linked deliverables: complete local backup/restore and portable migration; personal-Drive connection with scheduled off-site backup; migration/cutover recovery UI and operator rehearsal. The first deliverable must already recover a complete Gateway independently of cloud authorization. Keep existing commands/config recovery compatible throughout.

## Sources for integration constraints

- https://rclone.org/drive/
- https://rclone.org/remote_setup/
- https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html#other-services-via-rclone
- https://restic.readthedocs.io/en/stable/040_backup.html
- https://restic.readthedocs.io/en/stable/060_forget.html
- https://restic.readthedocs.io/en/stable/045_working_with_repos.html
- https://developers.google.com/identity/protocols/oauth2/native-app
- https://developers.google.com/identity/protocols/oauth2#expiration
- https://developers.google.com/workspace/drive/api/guides/handle-errors#storageQuotaExceeded

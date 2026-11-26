#!/usr/bin/env bash
# Load a trusted single-image archive and start only the existing Gateway service.
set -euo pipefail
die() { printf 'install-gateway: %s\n' "$*" >&2; exit 1; }
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
archive="$here/gateway-image.tar"
expected=''
if [[ "${1:-}" != --* && $# -gt 0 ]]; then
  archive=$1
  shift
fi
if [[ "${1:-}" != --* && $# -gt 0 ]]; then
  expected=$1
  shift
fi
[[ "$(uname -s)" == Linux ]] || die 'run the installer on the Linux Gateway server'
[[ -f "$archive" && ! -L "$archive" ]] || die 'archive must be a regular local file'
if [[ -z "$expected" ]]; then
  [[ "$(basename -- "$archive")" == gateway-image.tar && -f "$archive.sha256" && ! -L "$archive.sha256" ]] || die 'bundled checksum missing; provide a trusted SHA-256 explicitly'
  sidecar_pattern='^([0-9a-f]{64})  gateway-image[.]tar$'
  sidecar=$(head -c 1024 "$archive.sha256")
  [[ "$sidecar" =~ $sidecar_pattern ]] || die 'invalid bundled checksum'
  expected=${BASH_REMATCH[1]}
fi
sha_pattern='^[0-9a-f]{64}$'
[[ "$expected" =~ $sha_pattern ]] || die 'expected a lowercase 64-digit SHA-256'
case "$(uname -m)" in x86_64) platform=linux/amd64;; aarch64|arm64) platform=linux/arm64;; *) die 'unsupported Linux architecture';; esac
for need in docker python3; do command -v "$need" >/dev/null || die "$need is required"; done
[[ -z "${DOCKER_HOST:-}" ]] || die 'unset DOCKER_HOST and select a local Unix-socket Docker context'
context=$(docker context show) || die 'cannot identify current Docker context'
endpoint=$(docker context inspect "$context" --format '{{.Endpoints.docker.Host}}') || die 'cannot inspect Docker context'
[[ "$endpoint" == unix:///* ]] || die 'only a local Unix-socket Docker daemon is supported'
env_file="$here/.env"
explicit_env=0
overlays=()
with_ca=0
notification=''
legacy_tls=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file)
      [[ "$explicit_env" == 0 && $# -ge 2 ]] || die 'give one path after --env-file'
      explicit_env=1
      env_file=$2
      shift
      ;;
    --hospital-ca)
      [[ "$with_ca" == 0 ]] || die 'duplicate --hospital-ca'
      with_ca=1
      overlays+=("$here/docker-compose.hospital-ca.yml")
      ;;
    --legacy-tls)
      [[ "$legacy_tls" == 0 ]] || die 'duplicate --legacy-tls'
      legacy_tls=1
      overlays+=("$here/docker-compose.legacy-tls.yml")
      ;;
    --notifications|--notifications-volume)
      [[ -z "$notification" ]] || die 'choose at most one notifications overlay'
      notification=${1#--}
      overlays+=("$here/docker-compose.$notification.yml")
      ;;
    *) die "unknown option: $1";;
  esac
  shift
done
[[ -f "$here/docker-compose.yml" ]] || die 'Gateway docker-compose.yml is missing'
if [[ ! -f "$env_file" ]]; then
  [[ "$explicit_env" == 0 && -f "$here/.env.example" && ! -L "$here/.env.example" && ! -L "$env_file" ]] || die 'explicit environment file is missing'
  (umask 077; set -o noclobber; cat "$here/.env.example" > "$env_file") || die 'cannot create the blank installation template safely'
fi
project_dir=$(cd -- "$(dirname -- "$env_file")" && pwd -P)
env_file="$project_dir/$(basename -- "$env_file")"
compose=(docker --context "$context" compose --project-name erp-mobile-gateway --project-directory "$project_dir" --env-file "$env_file" -f "$here/docker-compose.yml")
if [[ "$with_ca" == 1 || "$legacy_tls" == 1 || -n "$notification" ]]; then
  for overlay in "${overlays[@]}"; do compose+=(-f "$overlay"); done
fi
# Validate without logging interpolated env, even on error.
"${compose[@]}" config --quiet >/dev/null 2>&1 || die 'invalid Gateway Compose configuration; check required local .env and overlay files'
image=$("${compose[@]}" config --images gateway 2>/dev/null) || die 'cannot resolve the configured Gateway image safely'
image_pattern='^[a-z0-9]+([._/-][a-z0-9]+)*:[0-9]{8}-[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$'
[[ "$image" =~ $image_pattern ]] || die 'GATEWAY_IMAGE must use the exact exported date/version tag'

# Refuse to silently omit overlays from an existing service in this project.
existing=$("${compose[@]}" ps -a -q gateway 2>/dev/null) || die 'cannot verify the existing Gateway project'
if [[ -n "$existing" ]]; then
  container_id_pattern='^[a-f0-9]{12,64}$'
  [[ "$existing" =~ $container_id_pattern ]] || die 'expected at most one existing Gateway container'
  used=$(docker --context "$context" container inspect "$existing" --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' 2>/dev/null) || die 'cannot verify existing Gateway overlays'
  [[ -n "$used" ]] || die 'existing Gateway has no Compose file inventory; review the migration manually'
  IFS=',' read -r -a used_files <<< "$used"
  for file in "${used_files[@]}"; do
    case "${file##*/}" in
      docker-compose.yml) ;;
      docker-compose.legacy-tls.yml) [[ "$legacy_tls" == 1 ]] || die 'existing project uses --legacy-tls; preserve this option';;
      docker-compose.hospital-ca.yml) [[ "$with_ca" == 1 ]] || die 'existing project uses --hospital-ca; include this option';;
      docker-compose.notifications.yml) [[ "$notification" == notifications ]] || die 'existing project uses --notifications; preserve this option';;
      docker-compose.notifications-volume.yml) [[ "$notification" == notifications-volume ]] || die 'existing project uses --notifications-volume; preserve this option';;
      *) die 'existing project uses an unsupported Compose overlay; review the migration manually';;
    esac
  done
  # Older bundles put TLS binds in the base file, so config_files alone cannot
  # prove that --legacy-tls is unnecessary. Compare the actual private runtime
  # inventory and resolved candidate without logging either environment.
  [[ -f "$here/verify-gateway-upgrade.py" && ! -L "$here/verify-gateway-upgrade.py" ]] || die 'upgrade identity preflight helper is missing'
  python3 "$here/verify-gateway-upgrade.py" "$context" "$existing" "$legacy_tls" "${compose[@]}" || die 'existing Gateway identity/data would change; no image loaded or container recreated'
fi

# Check integrity and inspect the tar without extracting or invoking Docker load.
identity=$(python3 - "$archive" "$expected" "$image" "$platform" <<'PY'
import hashlib, hmac, json, re, sys, tarfile

filename, expected, image, platform = sys.argv[1:]
digest = hashlib.sha256()
with open(filename, "rb") as archive:
    for block in iter(lambda: archive.read(1024 * 1024), b""):
        digest.update(block)
if not hmac.compare_digest(digest.hexdigest(), expected):
    sys.exit("archive checksum does not match the trusted SHA-256")

def read_member(tar, name, limit):
    entries = [m for m in tar.getmembers() if m.name == name]
    if len(entries) != 1 or not entries[0].isfile() or not 0 < entries[0].size <= limit:
        raise ValueError
    with tar.extractfile(entries[0]) as fp:
        return fp.read(limit + 1)

try:
    with tarfile.open(filename, "r:") as tar:
        manifest = json.loads(read_member(tar, "manifest.json", 1024 * 1024))
        if not isinstance(manifest, list) or len(manifest) != 1:
            raise ValueError
        entry = manifest[0]
        if entry.get("RepoTags") != [image]:
            sys.exit("archive must contain exactly the configured Gateway image tag")
        name = entry["Config"]
        if not re.fullmatch(r"(?:blobs/sha256/[a-f0-9]{64}|[a-f0-9]{64}\.json)", name):
            raise ValueError
        raw = read_member(tar, name, 1024 * 1024)
        config = json.loads(raw)
        if config.get("os", "") + "/" + config.get("architecture", "") != platform:
            sys.exit("archive platform does not match this Linux server")
        if config.get("config", {}).get("Entrypoint") != ["/usr/local/bin/gateway"]:
            sys.exit("archive does not contain the expected Gateway entrypoint")
        print("sha256:" + hashlib.sha256(raw).hexdigest() + " " + platform)
except (ValueError, TypeError, AttributeError, KeyError, OSError, tarfile.TarError):
    sys.exit("invalid or unsupported Docker image archive")
PY
) || die 'archive preflight failed; no image loaded'
# A daemon's inspect .Id may be an OCI manifest/index, not a config digest.
# Verify the exact local tag's canonical config by bounded streaming export.
[[ -f "$here/verify-image-identity.py" && ! -L "$here/verify-image-identity.py" ]] || die 'portable image identity helper is missing'
config_digest=${identity%% *}
if docker --context "$context" image inspect "$image" >/dev/null 2>&1; then
  previous=$(python3 "$here/verify-image-identity.py" "$context" "$image" "$config_digest" "$platform" "/usr/local/bin/gateway") || die 'configured tag does not match the approved image; refusing overwrite'
  [[ "$previous" == "$identity" ]] || die 'configured tag already points to a different image; refusing overwrite'
else
  docker --context "$context" image load --input "$archive"
fi
actual=$(python3 "$here/verify-image-identity.py" "$context" "$image" "$config_digest" "$platform" "/usr/local/bin/gateway") || die 'loaded image identity/platform/entrypoint differs; service not started'
[[ "$actual" == "$identity" ]] || die 'loaded image identity/platform differs; service not started'

# No compose down, network or volume migration, credential change, build or pull.
"${compose[@]}" up -d --no-build --pull never --wait gateway
printf 'Gateway process is healthy: %s (%s). Verify an authorized Hospital connection separately.\n' "$image" "$platform"
printf 'Fresh/recovery setup: open the existing HTTPS setup URL or http://localhost:8080/setup/ through SSH.\n'
printf 'Read its one-time code locally: docker compose exec gateway /usr/local/bin/gateway setup-code\n'

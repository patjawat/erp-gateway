#!/usr/bin/env bash
# Standalone Mac loopback sandbox. Does not read the production .env.
set -euo pipefail
umask 077
die() { printf 'Gateway dev: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'USAGE'
Usage: ./deploy/gateway/dev-gateway.sh [--port PORT]
       ./deploy/gateway/dev-gateway.sh --native [--port PORT]
       ./deploy/gateway/dev-gateway.sh --image LOCAL_IMAGE [--port PORT]
       ./deploy/gateway/dev-gateway.sh --stop

The default builds Angular and the current Go source, then serves in the
foreground on 127.0.0.1:8080. npm installs locked frontend dependencies on the
first run; Go needs its cached toolchain/dependencies. --native is an
alias for this default. Ctrl+C stops it. --image explicitly uses an existing
compatible local Docker image; it never pulls/builds. --stop stops only that
Docker development project and keeps its data.
USAGE
}
port=8080 image= mode=auto
while (($#)); do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --native) [[ "$mode" == auto ]] || die 'Choose only one mode.'; mode=native; shift ;;
    --stop) [[ "$mode" == auto ]] || die 'Choose only one mode.'; mode=stop; shift ;;
    --port) (($# >= 2)) || die '--port needs a number.'; port=$2; shift 2 ;;
    --image) (($# >= 2)) && [[ -n "$2" ]] || die '--image needs an existing local image tag.'; image=$2; shift 2 ;;
    *) die "Unknown argument: $1 (see --help)." ;;
  esac
done
if [[ "$mode" == auto ]]; then
  if [[ -n "$image" ]]; then mode=docker; else mode=native; fi
fi
[[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] && ((port <= 65535)) || die 'Port must be 1–65535.'
[[ "$mode" == docker || -z "$image" ]] || die '--image is only valid in Docker mode.'
[[ "$(uname -s)" == Darwin ]] || die 'This entry point is for Mac local development only.'
command -v openssl >/dev/null || die 'OpenSSL is required.'
here=$(cd -- "$(dirname -- "$0")" && pwd -P)
root=$(cd -- "$here/../.." && pwd -P)
state="$root/secrets/local-gateway"
[[ ! -L "$root/secrets" && ! -L "$state" ]] || die 'Development state paths must not be symlinks.'

docker_failure() {
  local output=$1
  if printf '%s' "$output" | grep -Eqi 'read.only file system|input/output error|snapshotter|containerd.*(database|metadata)|no space left on device'; then
    printf '%s\n' "$output" >&2
    die 'Docker Desktop storage is unavailable. Check Docker Desktop → Troubleshoot/diagnostics and available disk space. Do not reset or prune. For an independent local preview, run this command with --native.'
  fi
  printf '%s\n' "$output" >&2
  if printf '%s' "$output" | grep -Eqi 'address already in use|port is already allocated|ports are not available'; then
    die "Loopback port $port is already in use. Leave the existing service alone and try another port (for example --port 18080)."
  fi
  die 'Docker Desktop could not complete this step. Check that it is running and accessible. --native is an explicit alternative.'
}
if [[ "$mode" != native ]]; then
  command -v docker >/dev/null || die 'Docker Desktop is not available. Use --native if Go is cached.'
  docker_local() {
    # Ignore inherited DOCKER/COMPOSE variables and pin the local macOS engine.
    env -i HOME="$HOME" PATH="$PATH" DOCKER_CONFIG="$HOME/.docker" docker --context desktop-linux "$@"
  }
  endpoint=$(docker_local context inspect desktop-linux --format '{{.Endpoints.docker.Host}}' 2>&1) || docker_failure "$endpoint"
  [[ "$endpoint" == "unix://$HOME/.docker/run/docker.sock" ]] || die 'The desktop-linux context does not point to the local Docker Desktop socket.'
  engine=$(docker_local info --format '{{.OSType}}/{{.Architecture}}' 2>&1) || docker_failure "$engine"
  case "$engine" in
    linux/aarch64|linux/arm64) arch=arm64 ;;
    linux/x86_64|linux/amd64) arch=amd64 ;;
    *) die 'Unsupported local Docker engine architecture.' ;;
  esac
  compose() {
    docker_local compose --project-name hospital-erp-gateway-dev --project-directory "$state" \
      --env-file "$state/compose.env" --file "$here/docker-compose.local-dev.yml" "$@"
  }
  if [[ "$mode" == stop ]]; then
    [[ ! -L "$state/compose.env" ]] || die 'Development configuration must not be a symlink.'
    [[ -f "$state/compose.env" ]] || { printf 'No Docker development project configured.\n'; exit 0; }
    output=$(compose stop gateway 2>&1) || docker_failure "$output"
    printf '%s\n' "$output"
    printf 'Stopped the Docker development project; data was kept.\n'
    exit 0
  fi
  [[ "$image" =~ ^[a-z0-9][a-z0-9./:_@-]*$ ]] || die 'Invalid local image reference. Use --image with a trusted existing Gateway tag.'
  output=$(docker_local image inspect --format '{{.Os}}/{{.Architecture}}' "$image" 2>&1) || docker_failure "$output"
  [[ "$output" == "linux/$arch" ]] || die "This image is not Linux/$arch. Use a matching existing image, or --native."
else
  command -v go >/dev/null || die 'Go is not installed; native mode needs the cached Go toolchain and dependencies.'
  # Locate the toolchain without auto-downloading it, including Go's cache of
  # versioned toolchains, so this mode works without an available Docker engine.
  version=$(awk '$1 == "go" { print $2; exit }' "$root/backend/go.mod")
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'Cannot determine the required Go version.'
  go_cmd=$(command -v go)
  case "$(uname -m)" in arm64) goarch=arm64;; x86_64) goarch=amd64;; *) die 'Unsupported Mac architecture.';; esac
  if ! GOTOOLCHAIN=local "$go_cmd" version | grep -Fq "go$version darwin/$goarch"; then
    modcache=$(GOTOOLCHAIN=local "$go_cmd" env GOMODCACHE)
    go_cmd="$modcache/golang.org/toolchain@v0.0.1-go$version.darwin-$goarch/bin/go"
    [[ -x "$go_cmd" ]] || die "Go $version for darwin/$goarch is not cached; nothing was downloaded."
  fi
fi

mkdir -p -- "$state"
[[ -d "$state" && ! -L "$state" && -O "$state" ]] || die 'Development state must be an owned directory.'
chmod 700 "$state"
lock="$state/.setup-lock"
mkdir "$lock" 2>/dev/null || die "A dev setup is already running or left a lock at $lock. Check that no setup is running before removing the empty lock directory."
work=
cleanup() {
  [[ -z "$work" ]] || rm -rf -- "$work"
  rmdir "$lock" 2>/dev/null || :
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
work=$(mktemp -d "$state/.tmp.XXXXXX")

credentials="$state/credentials.env"
openssl_path=$(command -v openssl)
if [[ ! -e "$credentials" && ! -L "$credentials" ]]; then
  [[ ! -e "$state/native/gateway.db" && ! -L "$state/native/gateway.db" ]] || die 'Existing development data has no credentials file. Recover this file from backup; a new encryption key would not preserve it.'
  if [[ "$mode" == docker ]]; then
    volumes=$(docker_local volume ls --filter 'name=hospital-erp-gateway-dev_gateway-dev-data' --format '{{.Name}}' 2>&1) || docker_failure "$volumes"
    if printf '%s\n' "$volumes" | grep -Fxq 'hospital-erp-gateway-dev_gateway-dev-data'; then
      die 'The existing Docker development volume has no credentials file. Recover the file from backup rather than changing its encryption key.'
    fi
  fi
  key=$("$openssl_path" rand -base64 32)
  password=$("$openssl_path" rand -hex 20)
  printf 'DEV_GATEWAY_KEY=%s\nDEV_GATEWAY_PASSWORD=%s\n' "$key" "$password" > "$work/credentials.env"
  mv -n -- "$work/credentials.env" "$credentials"
fi
[[ -f "$credentials" && ! -L "$credentials" && -O "$credentials" ]] || die 'Invalid local credentials file.'
chmod 600 "$credentials"
key=$(sed -n 's/^DEV_GATEWAY_KEY=//p' "$credentials")
password=$(sed -n 's/^DEV_GATEWAY_PASSWORD=//p' "$credentials")
[[ "$key" =~ ^[A-Za-z0-9+/]{43}=$ && "$password" =~ ^[0-9a-f]{40}$ ]] || die 'Local credentials are invalid; not replacing them or the database.'

tls_dir="$state/tls"
[[ ! -L "$tls_dir" ]] || die 'Development TLS directory must not be a symlink.'
mkdir -p -- "$tls_dir"
chmod 700 "$tls_dir"
cert="$tls_dir/client-cert.pem"
cert_key="$tls_dir/client-key.pem"
[[ ! -L "$cert" && ! -L "$cert_key" ]] || die 'Development TLS files must not be symlinks.'
if [[ ! -f "$cert" || ! -f "$cert_key" ]] ||
   ! "$openssl_path" x509 -in "$cert" -noout -checkend 86400 >/dev/null 2>&1 ||
   ! "$openssl_path" x509 -in "$cert" -pubkey -noout > "$work/cert.pub" 2>/dev/null ||
   ! "$openssl_path" pkey -in "$cert_key" -passin pass: -pubout > "$work/key.pub" 2>/dev/null ||
   ! cmp -s "$work/cert.pub" "$work/key.pub"; then
  # A short-lived, locally self-signed client identity only; never a production
  # key or CA, and nothing is imported into the macOS trust store.
  cat > "$work/openssl.cnf" <<'CERT_CONFIG'
[req]
distinguished_name = dn
x509_extensions = local_client
prompt = no
[dn]
CN = Hospital ERP Gateway Local Dev Only
[local_client]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = clientAuth
CERT_CONFIG
  "$openssl_path" req -new -x509 -newkey rsa:2048 -nodes -days 7 -sha256 -config "$work/openssl.cnf" \
    -keyout "$work/client-key.pem" -out "$work/client-cert.pem" > /dev/null 2>"$work/openssl.log" ||
    die 'Could not generate the local development TLS pair.'
  mv -f -- "$work/client-cert.pem" "$cert"
  mv -f -- "$work/client-key.pem" "$cert_key"
fi
# The private host directory is 0700; mounted files must be readable by the
# container's non-root UID even when Docker shares the host owner differently.
chmod 444 "$cert" "$cert_key"
cert_sha=$(shasum -a 256 "$cert" | awk '{print $1}')

show_access() {
  printf 'Admin:  http://127.0.0.1:%s/admin/\nHealth: http://127.0.0.1:%s/healthz\n' "$port" "$port"
  printf 'User:   dev-admin\nPassword: %s\nLocal preview only; no Hospital upstreams are configured.\n' "$password"
}

if [[ "$mode" == native ]]; then
  native="$state/native"
  [[ ! -L "$native" ]] || die 'Native data directory must not be a symlink.'
  mkdir -p -- "$native"
  [[ ! -L "$native/config" ]] || die 'Native configuration directory must not be a symlink.'
  [[ ! -L "$native/gateway.db" && ! -L "$native/gateway.db-wal" && ! -L "$native/gateway.db-shm" ]] || die 'Native database must not be a symlink.'
  bash "$here/build-admin-ui.sh" || die 'Angular build failed; the existing Gateway was not changed.'
  (
    cd -- "$root/backend"
    GOTOOLCHAIN=local GOPROXY=off GONOPROXY=none GOSUMDB=off GOTELEMETRY=off GOFLAGS='' GOWORK=off CGO_ENABLED=0 \
      "$go_cmd" build -trimpath -o "$work/gateway" ./cmd/gateway
  ) || die 'Offline native build failed. No Go dependencies were downloaded.'
  # Start in foreground, preserving signal handling; keep the executable in
  # the setup temp directory for as long as this process is alive.
  printf 'Starting foreground native preview. Press Ctrl+C to stop it.\n'
  show_access
  env -i HOME="$HOME" PATH="$PATH" \
    LISTEN_ADDR="127.0.0.1:$port" GATEWAY_DATABASE_PATH="$native/gateway.db" GATEWAY_CONFIG_DIR="$native/config" \
    GATEWAY_CLIENT_TLS_CERT_FILE="$cert" GATEWAY_CLIENT_TLS_KEY_FILE="$cert_key" \
    GATEWAY_ENCRYPTION_KEY="$key" GATEWAY_ADMIN_USERNAME=dev-admin GATEWAY_ADMIN_PASSWORD="$password" \
    GATEWAY_COOKIE_SECURE=false GATEWAY_ALLOW_HTTP_UPSTREAM=false \
    "$work/gateway"
  exit
fi

[[ ! -L "$state/compose.env" ]] || die 'Development configuration must not be a symlink.'
{
  printf 'DEV_GATEWAY_IMAGE=%s\nDEV_GATEWAY_PORT=%s\nDEV_GATEWAY_CERT_SHA256=%s\n' "$image" "$port" "$cert_sha"
  cat "$credentials"
} > "$work/compose.env"
# Compose resolves relative bind paths against --project-directory, so this
# fixed path doesn't interpolate arbitrary parent path characters from .env.
printf 'DEV_GATEWAY_STATE=.\n' >> "$work/compose.env"
mv -f -- "$work/compose.env" "$state/compose.env"
if ! output=$(compose up --detach --wait --wait-timeout 45 --no-build --pull never 2>&1); then
  docker_failure "$output"
fi
show_access
printf 'Stop Docker preview: ./deploy/gateway/dev-gateway.sh --stop\n'

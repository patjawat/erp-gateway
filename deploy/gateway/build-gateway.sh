#!/usr/bin/env bash
# Build/export locally, without sending project files to a registry.
set -euo pipefail
umask 077
printf '%s\n' 'Gateway TAR packaging is paused by the project decision. Build the Gateway image with docker compose build; no TAR or release bundle was created.' >&2
exit 1

# Historical implementation retained below while TAR delivery is paused.
die() { printf 'build-gateway: %s\n' "$*" >&2; exit 1; }
usage() {
  printf '%s\n' 'ใช้งาน: build-gateway.sh (เลือกสถาปัตยกรรมจากเมนู)' \
    'หรือ: build-gateway.sh linux/amd64|linux/arm64 IMAGE:YYYYMMDD-VERSION [OUTPUT.tar]' \
    'ไม่ระบุ OUTPUT: บันทึกที่ gateway/deploy/release/gateway-release.tar' \
    'เก็บสำรองไฟล์เดิมก่อนเปลี่ยน gateway-release.tar; พาธ OUTPUT เองจะไม่ถูกเขียนทับ'
}
cancel() { printf '%s\n' 'ยกเลิกแล้ว ยังไม่ได้เริ่ม build'; exit 0; }
image_pattern='^[a-z0-9]+([._/-][a-z0-9]+)*:[0-9]{8}-[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$'
case "${1:-}" in -h|--help) usage; exit 0;; esac
if [[ $# -eq 0 ]]; then
  [[ -t 0 ]] || { usage >&2; exit 1; }
  printf '%s\n' 'เลือก CPU ของเซิร์ฟเวอร์ปลายทาง (ดูจาก uname -m):' \
    '  1) amd64 / x86_64' '  2) arm64 / aarch64' '  q) ยกเลิก'
  while :; do
    printf '%s' 'เลือก 1 หรือ 2 [q เพื่อยกเลิก]: '
    IFS= read -r selection || cancel
    case "$selection" in
      1) platform=linux/amd64; break;;
      2) platform=linux/arm64; break;;
      q|Q) cancel;;
      *) printf '%s\n' 'ตัวเลือกไม่ถูกต้อง ลองอีกครั้ง';;
    esac
  done
  timestamp=$(date -u '+%Y%m%d-mtls-%H%M%S')
  default_image="hospital-erp-gateway-local:$timestamp-${platform#linux/}"
  while :; do
    printf 'Image tag [%s] (Enter ใช้ค่าเดิม, q ยกเลิก): ' "$default_image"
    IFS= read -r entered || cancel
    case "$entered" in
      q|Q) cancel;;
      '') image=$default_image;;
      *:*) image=$entered;;
      *) image="hospital-erp-gateway-local:$entered";;
    esac
    if [[ "$image" =~ $image_pattern ]]; then break; fi
    printf '%s\n' 'tag ต้องเริ่มด้วยวันที่ YYYYMMDD- ตามด้วยเวอร์ชัน ลองอีกครั้ง'
  done
  set -- "$platform" "$image"
fi
[[ $# -eq 2 || $# -eq 3 ]] || { usage >&2; exit 1; }
platform=$1 image=$2
case "$platform" in linux/amd64|linux/arm64) ;; *) die 'platform must be exactly linux/amd64 or linux/arm64';; esac
[[ "$image" =~ $image_pattern ]] || die 'use an explicit date/version tag; do not use latest'
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
root=$(cd -- "$here/../.." && pwd -P)
default_output=0
if [[ $# -eq 3 ]]; then
  archive=$3
else
  release="$root/deploy/release"
  [[ ! -L "$release" ]] || die 'default deploy/release must not be a symbolic link'
  mkdir -p -- "$release" || die 'cannot create gateway/deploy/release'
  default_output=1
  archive="$release/gateway-release.tar"
fi
basename_pattern='^[a-zA-Z0-9][a-zA-Z0-9._-]*\.tar$'
name=$(basename -- "$archive")
[[ "$name" =~ $basename_pattern ]] || die 'output filename must be a simple .tar name'
output=$(cd -- "$(dirname -- "$archive")" && pwd -P) || die 'output directory must exist'
archive="$output/$name"
[[ ! -L "$archive" ]] || die 'refusing a symbolic link as the archive destination'
if [[ "$default_output" == 1 ]]; then
  [[ ! -e "$archive" || -f "$archive" ]] || die 'existing default archive is not a regular file'
else
  [[ ! -e "$archive" ]] || die 'refusing to overwrite an explicit output archive'
fi
for need in docker python3; do command -v "$need" >/dev/null || die "$need is required"; done
[[ -z "${DOCKER_HOST:-}" ]] || die 'unset DOCKER_HOST and select a local Unix-socket Docker context'
context=$(docker context show) || die 'cannot identify current Docker context'
endpoint=$(docker context inspect "$context" --format '{{.Endpoints.docker.Host}}') || die 'cannot inspect Docker context'
[[ "$endpoint" == unix:///* ]] || die 'only a local Unix-socket Docker daemon is supported'
if docker --context "$context" image inspect "$image" >/dev/null 2>&1; then
  die 'this image tag already exists; choose a new immutable version'
fi

work=$(mktemp -d "$output/.gateway-export.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
python3 - "$root" "$work/source" <<'PY'
import pathlib, re, sys

root = pathlib.Path(sys.argv[1]).resolve()
target = pathlib.Path(sys.argv[2])
names = [
    "Dockerfile", "go.mod", "go.sum",
    "internal/admin/assets/app.js",
    "internal/admin/assets/index.html",
    "internal/admin/assets/style.css",
    "internal/admin/assets/guided.js",
    "internal/admin/assets/fonts/Sarabun-Regular.ttf",
    "internal/admin/assets/fonts/Sarabun-SemiBold.ttf",
]
for prefix in ("cmd/gateway", "internal/admin", "internal/bootstrap", "internal/contracts",
               "internal/gateway", "internal/transportauth"):
    base = root / prefix
    if not base.is_dir() or base.resolve() != base:
        sys.exit(f"missing or linked required source directory: {prefix}")
    names += [
        file.relative_to(root).as_posix()
        for file in base.glob("*.go") if not file.name.endswith("_test.go")
    ]
if len(names) <= 6:
    sys.exit("no Go source staged")
potential_key = re.compile(
    rb"-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----|AKIA[A-Z0-9]{16}|ghp_[A-Za-z0-9]{36}|dckr_pat_[A-Za-z0-9_-]{20,}"
)
for name in sorted(set(names)):
    src = root / name
    if src.resolve() != src or not src.is_file():
        sys.exit(f"missing or linked release source file: {name}")
    raw = src.read_bytes()
    if potential_key.search(raw):
        sys.exit(f"potential private key or token in allowed source: {name}")
    dst = target / name
    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_bytes(raw)
print(f"Gateway build: {len(set(names))} explicitly allowlisted source files staged.")
PY

# Explicitly use the builder for the selected local Docker context. No push.
docker --context "$context" buildx build --builder "$context" --platform "$platform" --target gateway --load --tag "$image" "$work/source"
built=$(docker --context "$context" image inspect "$image" --format '{{.Id}} {{.Os}}/{{.Architecture}}')
[[ "${built##* }" == "$platform" ]] || die 'the loaded image has the wrong platform'

# Prepare the fixed-name outer folder before saving the inner Docker archive.
release="$work/gateway-release"
mkdir "$release"
python3 - "$here" "$release" "$image" <<'PYCODE'
import pathlib, re, sys
source, destination = map(pathlib.Path, sys.argv[1:3])
image = sys.argv[3].encode()
files = (
    "install-gateway.sh", "docker-compose.yml", ".env.example",
    "docker-compose.hospital-ca.yml", "docker-compose.notifications.yml",
    "docker-compose.notifications-volume.yml", "OFFLINE_GATEWAY.md",
    "docker-compose.legacy-tls.yml", "WEB_SETUP.md",
    "verify-gateway-upgrade.py",
    "verify-image-identity.py",
)
for name in files:
    path = source / name
    if not path.is_file() or path.is_symlink():
        sys.exit(f"required runtime file is missing or linked: {name}")
    raw = path.read_bytes()
    if name == ".env.example":
        for key in ("GATEWAY_ENCRYPTION_KEY", "GATEWAY_ADMIN_PASSWORD"):
            if not re.search(rb"(?m)^" + key.encode() + rb"=$", raw):
                sys.exit(f"secret must be blank in template: {key}")
    if re.search(rb"-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----|dckr_pat_[A-Za-z0-9_-]{20,}", raw):
        sys.exit(f"potential key material in runtime input: {name}")
    if name == ".env.example":
        raw, count = re.subn(rb"(?m)^GATEWAY_IMAGE=[^\r\n]*$", b"GATEWAY_IMAGE=" + image, raw)
        if count != 1:
            sys.exit("expected exactly one image entry in the Gateway env template")
    if name == "docker-compose.yml":
        old = b"${GATEWAY_IMAGE:-erp-platform/gateway:0.1.0}"
        if raw.count(old) != 1:
            sys.exit("unexpected Gateway Compose image default; review the release kit")
        raw = raw.replace(old, b"${GATEWAY_IMAGE:-" + image + b"}")
    item = destination / ("README.md" if name == "OFFLINE_GATEWAY.md" else name)
    item.write_bytes(raw)
    if name.endswith(".sh"):
        item.chmod(0o700)
PYCODE
docker --context "$context" image save --output "$release/gateway-image.tar" "$image"
python3 - "$work" "$archive" "$image" "$built" "$default_output" <<'PYCODE'
import datetime, errno, fcntl, hashlib, os, pathlib, shutil, sys, tarfile

work, target = map(pathlib.Path, sys.argv[1:3])
image, identity, default_output = sys.argv[3:]
root = work / "gateway-release"

def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()

inner = sha256(root / "gateway-image.tar")
(root / "gateway-image.tar.sha256").write_text(f"{inner}  gateway-image.tar\n")
(root / "image-metadata.txt").write_text(f"{image}\n{identity}\n")
pending = work / "gateway-bundle.tar"
with tarfile.open(pending, "w") as bundle:
    bundle.add(root, arcname="gateway-release")
outer = sha256(pending)
with pending.open("rb") as fp:
    os.fsync(fp.fileno())

def publish_exclusive(source, destination):
    try:
        os.link(source, destination)
    except OSError as e:
        if e.errno not in (errno.EPERM, errno.EXDEV, errno.EOPNOTSUPP, errno.ENOSYS):
            raise
        # External filesystems such as exFAT may not implement hardlinks.
        # Exclusive creation still protects data already at the destination.
        out = destination.open("xb")
        try:
            with out, source.open("rb") as src:
                shutil.copyfileobj(src, out)
                out.flush()
                os.fsync(out.fileno())
        except BaseException:
            destination.unlink(missing_ok=True)
            raise

if default_output == "1":
    lockfile = target.parent / ".gateway-release.lock"
    lock_fd = os.open(lockfile, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        fcntl.flock(lock_fd, fcntl.LOCK_EX)
        if target.is_symlink() or (target.exists() and not target.is_file()):
            sys.exit("existing gateway-release.tar must be a regular file; nothing replaced")
        backup = None
        if target.exists():
            stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
            backup = target.with_name(f"gateway-release.previous.{stamp}.tar")
            suffix = 0
            while True:
                try:
                    publish_exclusive(target, backup)
                    break
                except FileExistsError:
                    suffix += 1
                    backup = target.with_name(f"gateway-release.previous.{stamp}.{suffix}.tar")
                except OSError:
                    sys.exit("unable to back up previous bundle; active gateway-release.tar left unchanged")
        os.replace(pending, target)
        if backup is not None:
            print(f"Previous bundle: {backup}")
    finally:
        os.close(lock_fd)
else:
    publish_exclusive(pending, target)
print(f"Bundle: {target}\nBundle SHA-256: {outer}\nInner image SHA-256: {inner}\nImage: {image} ({identity})")
PYCODE

#!/bin/sh
# Build-stage only. These Linux tools are copied into the image; the server host
# needs no backup packages. Checksums are pinned from the upstream release
# SHA256SUMS files, not downloaded dynamically alongside an unchecked binary.
set -eu

arch=${1:?usage: install-backup-tools.sh <amd64|arm64> <output-directory>}
destination=${2:?usage: install-backup-tools.sh <amd64|arm64> <output-directory>}
restic_version=0.18.1
rclone_version=1.72.0

case "$arch" in
  amd64)
    restic_sha=680838f19d67151adba227e1570cdd8af12c19cf1735783ed1ba928bc41f363d
    rclone_sha=f3757aa829828c0f3359301bea25eef4d4fd62de735c47546ee6866c5b5545e2
    ;;
  arm64)
    restic_sha=87f53fddde38764095e9c058a3b31834052c37e5826d2acf34e18923c006bd45
    rclone_sha=c1669ef42d4ad65e3bb3f2cf0b2acf76cf0cbffefe463349a4f2244d8dbed701
    ;;
  *) echo "unsupported backup-tool architecture: $arch" >&2; exit 1 ;;
esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM

curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
  --retry 3 --max-time 120 \
  "https://github.com/restic/restic/releases/download/v${restic_version}/restic_${restic_version}_linux_${arch}.bz2" \
  --output "$work/restic.bz2"
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
  --retry 3 --max-time 120 \
  "https://github.com/rclone/rclone/releases/download/v${rclone_version}/rclone-v${rclone_version}-linux-${arch}.zip" \
  --output "$work/rclone.zip"

printf '%s  %s\n%s  %s\n' "$restic_sha" "$work/restic.bz2" "$rclone_sha" "$work/rclone.zip" | sha256sum -c - > /dev/null
bzip2 -dc "$work/restic.bz2" > "$work/restic"
unzip -p "$work/rclone.zip" "rclone-v${rclone_version}-linux-${arch}/rclone" > "$work/rclone"
test -s "$work/restic"
test -s "$work/rclone"
mkdir -p "$destination"
install -m 0755 "$work/restic" "$destination/restic"
install -m 0755 "$work/rclone" "$destination/rclone"

#!/usr/bin/env bash
# Offline operations for the private, dedicated Hospital ERP Gateway client CA.
set +x +v +a
set -euo pipefail
unset passphrase confirm_passphrase
umask 077
die() { printf 'gateway-client-ca: %s\n' "$*" >&2; exit 1; }
usage() {
  printf '%s\n' 'ใช้งาน (บน Mac ที่ทำหน้าที่ออกใบรับรอง):' \
    '  bash issue-gateway-client-cert.sh init CA_DIR' \
    '  bash issue-gateway-client-cert.sh sign CA_DIR GATEWAY_CSR OUTPUT_CERT.pem [DAYS]' \
    'CA_DIR ต้องอยู่นอกโครงการ; ไฟล์ปลายทางต้องยังไม่มี; DAYS ค่าเริ่มต้น 90 (1–365)'
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  init) [[ $# -eq 2 ]] || { usage >&2; exit 1; } ;;
  sign) [[ $# -eq 4 || $# -eq 5 ]] || { usage >&2; exit 1; } ;;
  *) usage >&2; exit 1 ;;
esac
op=$1
ssl=${OPENSSL:-openssl}
command -v "$ssl" >/dev/null || die 'OpenSSL 3 or later is required (use Homebrew OpenSSL on macOS)'
command -v python3 >/dev/null || die 'Python 3 is required'
version=$("$ssl" version)
[[ "$version" =~ ^OpenSSL\ ([3-9]|[1-9][0-9]+)\. ]] ||
  die 'OpenSSL 3 or later is required; the macOS LibreSSL executable is not supported'
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo=$(cd -- "$here/../.." && pwd -P)
# Exclude all sibling Hospital ERP checkouts too, not just this Gateway repo.
platform_dir=$(cd -- "$repo/.." && pwd -P)
[[ "${platform_dir##*/}" == erp-platform ]] || platform_dir=$repo

# Validate immutable ownership/permissions without reading the CA key contents.
ca_dir=$(python3 - "$op" "$2" "$platform_dir" <<'PY'
import os, stat, sys
from pathlib import Path

def die(message):
    sys.exit(f"gateway-client-ca: {message}")

op, chosen, platform = sys.argv[1:]
raw = Path(chosen).expanduser()
try:
    parent = raw.parent.resolve(strict=True)
    directory = parent / raw.name
    platform = Path(platform)
    if directory == platform or platform in directory.parents:
        die("the CA directory must be outside the Hospital ERP source tree")
    info = parent.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
        die("the CA parent must belong to you and not allow group/other writes")
    if op == "init":
        if os.path.lexists(directory):
            die("CA directory already exists; refusing to overwrite")
    else:
        info = directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
            die("CA directory must be a real directory owned by you and private (0700)")
        for name in ("gateway-client-ca.key.pem", "gateway-client-ca.crt.pem"):
            info = (directory / name).lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_nlink != 1:
                die("expected original, user-owned CA key and certificate (no links)")
            if name.endswith(".key.pem") and info.st_mode & 0o077:
                die("CA private key is accessible to others; use chmod 600")
except OSError:
    die("CA path or required files are missing or inaccessible")
print(directory)
PY
) || exit 1

ca_key="$ca_dir/gateway-client-ca.key.pem"
ca_cert="$ca_dir/gateway-client-ca.crt.pem"
ca_subject='/CN=Hospital ERP Gateway Client Issuing CA'
client_subject='/CN=Hospital ERP Central Gateway'
verify_ca() {
  local attributes subject issuer
  attributes=$("$ssl" x509 -in "$ca_cert" -noout -ext basicConstraints,keyUsage)
  [[ "$attributes" == *'CA:TRUE, pathlen:0'* && "$attributes" == *'Certificate Sign'* ]] ||
    die 'the supplied certificate is not the dedicated pathlen:0 issuing CA'
  subject=$("$ssl" x509 -in "$ca_cert" -noout -subject -nameopt RFC2253)
  issuer=$("$ssl" x509 -in "$ca_cert" -noout -issuer -nameopt RFC2253)
  [[ "${subject#subject=}" == 'CN=Hospital ERP Gateway Client Issuing CA' &&
     "${issuer#issuer=}" == "${subject#subject=}" ]] ||
    die 'unexpected CA identity or issuer'
  "$ssl" verify -auth_level 2 -check_ss_sig -CAfile "$ca_cert" "$ca_cert" >/dev/null
}

read_passphrase() {
  [[ -t 0 ]] || die 'run this command in an interactive Terminal for the CA passphrase'
  IFS= read -r -s -p 'CA passphrase: ' passphrase || die 'passphrase entry cancelled'
  printf '\n' >&2
  [[ -n "$passphrase" ]] || die 'passphrase cannot be empty'
}

if [[ "$op" == init ]]; then
  read_passphrase
  IFS= read -r -s -p 'Confirm CA passphrase: ' confirm_passphrase || die 'passphrase entry cancelled'
  printf '\n' >&2
  [[ "$passphrase" == "$confirm_passphrase" ]] || die 'CA passphrases do not match'
  unset confirm_passphrase
  mkdir -m 700 -- "$ca_dir" || die 'could not create a new private CA directory'
  trap 'rm -f -- "$ca_key" "$ca_cert"; rmdir -- "$ca_dir" 2>/dev/null || true' EXIT
  # Only in shell memory and an anonymous pipe: never a file, env or CLI argument.
  printf '%s\n' "$passphrase" | "$ssl" genpkey -algorithm EC -pkeyopt ec_paramgen_curve:prime256v1 \
    -aes-256-cbc -pass stdin -out "$ca_key"
  printf '%s\n' "$passphrase" | "$ssl" req -x509 -new -sha256 -days 3650 -config /dev/null \
    -passin stdin -key "$ca_key" -subj "$ca_subject" \
    -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -addext 'subjectKeyIdentifier=hash' -out "$ca_cert"
  unset passphrase
  verify_ca
  trap - EXIT
  printf 'Public CA certificate: %s\n' "$ca_cert"
  "$ssl" x509 -in "$ca_cert" -noout -fingerprint -sha256
  exit 0
fi

days=${5:-90}
[[ "$days" =~ ^[1-9][0-9]{0,2}$ ]] && [[ "$days" -le 365 ]] || die 'DAYS must be between 1 and 365'
csr=$3
out=$4
[[ -f "$csr" && ! -L "$csr" ]] || die 'CSR must be a local regular PEM file without links'
[[ ! -e "$out" && ! -L "$out" ]] || die 'output path already exists; refusing overwrite'
[[ "$(head -n 1 -- "$ca_key")" == '-----BEGIN ENCRYPTED PRIVATE KEY-----' ]] ||
  die 'CA private key must be encrypted PKCS#8 PEM'
verify_ca
"$ssl" x509 -in "$ca_cert" -noout -checkend "$((days * 86400 + 60))" >/dev/null ||
  die 'CA validity is too short for the requested client lifetime'

# Stage in the output directory for atomic, no-overwrite publish. Keep the exact
# CSR bytes that were verified, even if the public original is changed later.
outdir=$(cd -- "$(dirname -- "$out")" && pwd -P)
output="$outdir/$(basename -- "$out")"
work=$(mktemp -d "$outdir/.gateway-cert-issue.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
python3 - "$csr" "$work/request.pem" <<'PY'
import pathlib, re, sys
with open(sys.argv[1], "rb") as source:
    data = source.read(64 * 1024 + 1)
if len(data) > 64 * 1024 or not re.fullmatch(
    rb"-----BEGIN CERTIFICATE REQUEST-----\r?\n(?:[A-Za-z0-9+/=]+\r?\n)+-----END CERTIFICATE REQUEST-----(?:\r?\n)?",
    data,
):
    sys.exit("gateway-client-ca: expected one standard PEM certificate request")
pathlib.Path(sys.argv[2]).write_bytes(data)
PY
proof=$(LC_ALL=C "$ssl" req -in "$work/request.pem" -verify -noout 2>&1) ||
  die 'CSR proof-of-possession signature did not verify'
[[ "$proof" == *'self-signature verify OK'* ]] || die 'CSR proof-of-possession signature did not verify'
cat > "$work/client-extensions.cnf" <<'EXT'
[gateway_client]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,clientAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always
EXT
read_passphrase
serial=$("$ssl" rand -hex 16)
# The CSR supplies only the public key. Subject and all extensions come from the
# issuer; supplied SAN/serverAuth/CA constraints are deliberately not copied.
printf '%s\n' "$passphrase" | "$ssl" x509 -req -in "$work/request.pem" -CA "$ca_cert" -CAkey "$ca_key" -passin stdin \
  -subj "$client_subject" -copy_extensions none -set_serial "0x$serial" -sha256 -days "$days" \
  -extfile "$work/client-extensions.cnf" -extensions gateway_client -out "$work/client.pem"
unset passphrase
"$ssl" verify -auth_level 2 -purpose sslclient -CAfile "$ca_cert" "$work/client.pem" >/dev/null ||
  die 'issued certificate failed chain, minimum key strength or TLS client purpose verification'
if "$ssl" verify -auth_level 2 -purpose sslserver -CAfile "$ca_cert" "$work/client.pem" >/dev/null 2>&1; then
  die 'issued certificate unexpectedly valid for TLS server purpose'
fi
spki_hash() {
  "$ssl" pkey -pubin -outform DER | "$ssl" dgst -sha256 -r | awk '{print $1}'
}
csr_pin=$("$ssl" req -in "$work/request.pem" -noout -pubkey | spki_hash)
pin=$("$ssl" x509 -in "$work/client.pem" -noout -pubkey | spki_hash)
[[ "$pin" =~ ^[a-f0-9]{64}$ && "$pin" == "$csr_pin" ]] || die 'issued public key does not match the verified CSR'
python3 - "$work/client.pem" "$output" <<'PY'
import os, sys
try:
    os.link(sys.argv[1], sys.argv[2])  # atomic and refuses every pre-existing entry
except FileExistsError:
    sys.exit("gateway-client-ca: output appeared meanwhile; refusing overwrite")
except OSError:
    sys.exit("gateway-client-ca: cannot safely publish certificate (private local APFS-compatible directory needed)")
PY
printf 'Public Gateway client certificate: %s\nHOSPITAL_GATEWAY_CLIENT_SPKI_SHA256=%s\n' "$output" "$pin"

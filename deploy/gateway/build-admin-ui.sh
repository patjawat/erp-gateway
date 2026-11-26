#!/usr/bin/env bash
# Build Angular and stage its static output for Go's embed.FS.
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
root=$(cd -- "$here/../.." && pwd -P)
frontend="$root/frontend"
target="$root/backend/internal/admin/assets/ui"
die() { printf 'Gateway UI: %s\n' "$*" >&2; exit 1; }
command -v npm >/dev/null || die 'npm is required. Use Node 22.22.3 or newer, or build with Docker Compose.'
command -v python3 >/dev/null || die 'python3 is required to stage the embedded Angular assets.'
[[ -f "$frontend/package-lock.json" && ! -L "$frontend" ]] || die 'The frontend package and lockfile are required.'
[[ ! -L "$target" && ! -L "$(dirname -- "$target")" ]] || die 'Generated UI paths must not be symlinks.'
if [[ ! -d "$frontend/node_modules" ]]; then
  npm --prefix "$frontend" ci --no-audit --no-fund
fi
npm --prefix "$frontend" run build
[[ -f "$frontend/dist/browser/index.html" ]] || die 'Angular did not produce dist/browser/index.html.'
python3 - "$frontend/dist/browser" "$target" <<'PY'
from pathlib import Path
import re
import shutil
import sys
import tempfile

source, target = map(Path, sys.argv[1:])
index = (source / 'index.html').read_text()
if '__GATEWAY_CSP_NONCE__' not in index or not re.search(r"<base\b[^>]*\bhref\s*=\s*[\"']/admin/[\"'][^>]*>", index):
    sys.exit('Angular output must use base href /admin/ and the Gateway CSP nonce placeholder.')
target.parent.mkdir(parents=True, exist_ok=True)
stage = Path(tempfile.mkdtemp(prefix='.ui-build-', dir=target.parent))
backup = None
try:
    shutil.copytree(source, stage, dirs_exist_ok=True)
    readme = target / 'README.md'
    if readme.is_file() and not readme.is_symlink():
        shutil.copy2(readme, stage / 'README.md')
    if target.exists():
        backup = Path(tempfile.mkdtemp(prefix='.ui-previous-', dir=target.parent))
        backup.rmdir()
        target.rename(backup)
    try:
        stage.rename(target)
    except BaseException:
        if backup is not None:
            backup.rename(target)
            backup = None
        raise
finally:
    if stage.exists():
        shutil.rmtree(stage)
    if backup is not None and backup.exists():
        shutil.rmtree(backup)
print(f'Embedded Angular assets: {target}')
PY

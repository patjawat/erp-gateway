#!/usr/bin/env python3
"""Portable identity fixtures and mock-Docker installer integration; no daemon writes."""
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
spec = importlib.util.spec_from_file_location("image_guard", HERE / "verify-image-identity.py")
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
IMAGE = "hospital-erp-gateway-local:20261125-fixture-amd64"


def image_tar(entrypoint="/usr/local/bin/gateway", platform="linux/amd64", tag=IMAGE, classic=False, marker="", config_first=True):
    operating, architecture = platform.split("/")
    raw = json.dumps({"os": operating, "architecture": architecture, "config": {"Entrypoint": [entrypoint]}, "fixture": marker}).encode()
    digest = hashlib.sha256(raw).hexdigest()
    name = digest + ".json" if classic else "blobs/sha256/" + digest
    manifest = json.dumps([{"Config": name, "RepoTags": [tag], "Layers": []}]).encode()
    output = io.BytesIO()
    records = [(name, raw), ("manifest.json", manifest)]
    if not config_first:
        records.reverse()
    with tarfile.open(fileobj=output, mode="w") as archive:
        for filename, body in records:
            info = tarfile.TarInfo(filename)
            info.size = len(body)
            archive.addfile(info, io.BytesIO(body))
    return output.getvalue(), "sha256:" + digest


class ImageIdentityTests(unittest.TestCase):
    def test_classic_and_containerd_archive_formats(self):
        for classic in (False, True):
            for first in (False, True):
                with self.subTest(classic=classic, config_first=first):
                    archive, digest = image_tar(classic=classic, config_first=first)
                    actual = guard.verify_stream(guard.BoundedReader(io.BytesIO(archive)), IMAGE, digest, "linux/amd64", "/usr/local/bin/gateway")
                    self.assertEqual(actual, digest + " linux/amd64")

    def test_wrong_config_tag_platform_and_entrypoint_rejected(self):
        for kind in ("config", "tag", "platform", "entrypoint"):
            with self.subTest(kind=kind):
                archive, digest = image_tar(entrypoint="/unexpected" if kind == "entrypoint" else "/usr/local/bin/gateway", platform="linux/arm64" if kind == "platform" else "linux/amd64", tag="other:tag" if kind == "tag" else IMAGE)
                if kind == "config":
                    digest = "sha256:" + "a" * 64
                with self.assertRaises(guard.ImageRejected):
                    guard.verify_stream(guard.BoundedReader(io.BytesIO(archive)), IMAGE, digest, "linux/amd64", "/usr/local/bin/gateway")

    def test_stream_limit_and_both_service_entrypoints(self):
        archive, digest = image_tar(entrypoint="/usr/local/bin/hospital-api")
        self.assertEqual(guard.verify_stream(guard.BoundedReader(io.BytesIO(archive)), IMAGE, digest, "linux/amd64", "/usr/local/bin/hospital-api"), digest + " linux/amd64")
        with self.assertRaises(guard.ImageRejected):
            guard.verify_stream(guard.BoundedReader(io.BytesIO(archive), 100), IMAGE, digest, "linux/amd64", "/usr/local/bin/hospital-api")

    def test_standalone_helpers_remain_identical(self):
        self.assertEqual((HERE / "verify-image-identity.py").read_bytes(), (ROOT / "api/deploy/hospital/verify-image-identity.py").read_bytes())

    def test_installer_blocks_with_mock_classic_and_containerd_daemons(self):
        installers = ((HERE / "install-gateway.sh", "gateway"), (ROOT / "api/deploy/hospital/install-hospital-api.sh", "hospital-api"))
        for installer, service in installers:
            for scenario in ("classic", "containerd", "collision", "load_containerd", "bad_postload"):
                with self.subTest(service=service, scenario=scenario), tempfile.TemporaryDirectory(prefix="image-id-fixture-") as folder:
                    work = Path(folder)
                    binary = work / "bin"
                    binary.mkdir()
                    shutil.copyfile(HERE / "verify-image-identity.py", work / "verify-image-identity.py")
                    archive, digest = image_tar(entrypoint="/usr/local/bin/" + service)
                    wrong, _ = image_tar(entrypoint="/usr/local/bin/" + service, marker="different config")
                    (work / "image.tar").write_bytes(wrong if scenario in ("collision", "bad_postload") else archive)
                    if scenario not in ("load_containerd", "bad_postload"):
                        (work / "present").touch()
                    mock = binary / "docker"
                    mock.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
root=pathlib.Path(os.environ['MOCK_ROOT'])
args=sys.argv[1:]
if args[:2]==['--context','test']: args=args[2:]
with (root/'calls').open('a') as log: log.write(' '.join(args[:2])+'\\n')
if args[:2]==['image','inspect']:
    if not (root/'present').exists(): sys.exit(1)
    print(json.dumps({'Id':os.environ['MOCK_STORE_ID'],'Os':'linux','Architecture':'amd64'}))
elif args[:2]==['image','save']:
    sys.stdout.buffer.write((root/'image.tar').read_bytes())
elif args[:2]==['image','load']:
    (root/'present').touch()
else: sys.exit(8)
''')
                    mock.chmod(0o700)
                    text = installer.read_text()
                    block = text[text.index("# A daemon's inspect"):text.index("\n# No compose down")]
                    shell = 'set -euo pipefail\ndie(){ printf "%s\\n" "$*" >&2; exit 1; }\nhere=$1; context=test; image=$2; identity=$3; platform=linux/amd64; archive=$4\n' + block + '\nprintf "READY\\n"\n'
                    env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ["PATH"], MOCK_ROOT=str(work), MOCK_STORE_ID=digest if scenario == "classic" else "sha256:" + "9" * 64)
                    result = subprocess.run(["bash", "-c", shell, "fixture", str(work), IMAGE, digest + " linux/amd64", str(work / "image.tar")], env=env, capture_output=True, text=True, timeout=10)
                    success = scenario in ("classic", "containerd", "load_containerd")
                    self.assertEqual(result.returncode == 0, success, result.stderr)
                    self.assertEqual("READY" in result.stdout, success)
                    calls = (work / "calls").read_text()
                    self.assertEqual("image load" in calls, scenario in ("load_containerd", "bad_postload"))


if __name__ == "__main__":
    unittest.main()

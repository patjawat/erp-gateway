#!/usr/bin/env python3
"""Verify canonical image CONFIG identity, independent of Docker's image store.

Docker's classic image store exposes the config digest as inspect .Id, while a
containerd image store may expose an OCI manifest/index digest. Stream an export
of the exact local tag instead. No layers/config are extracted or printed.
"""
import hashlib
import hmac
import json
import re
import signal
import subprocess
import sys
import tarfile

MAX_METADATA = 1024 * 1024
MAX_MEMBERS = 100000
MAX_STREAM = 8 * 1024 * 1024 * 1024
MAX_SECONDS = 180


class ImageRejected(ValueError):
    pass


class BoundedReader:
    def __init__(self, stream, limit=MAX_STREAM):
        self.stream, self.limit, self.count = stream, limit, 0

    def read(self, size=-1):
        if size < 0:
            size = MAX_METADATA
        data = self.stream.read(min(size, self.limit - self.count + 1))
        self.count += len(data)
        if self.count > self.limit:
            raise ImageRejected("local image export exceeds the streaming byte limit")
        return data


class BoundedTarInfo(tarfile.TarInfo):
    @classmethod
    def frombuf(cls, buf, encoding, errors):
        info = super().frombuf(buf, encoding, errors)
        if info.type in (tarfile.XHDTYPE, tarfile.XGLTYPE, tarfile.GNUTYPE_LONGNAME, tarfile.GNUTYPE_LONGLINK) and info.size > MAX_METADATA:
            raise ImageRejected("extended tar metadata exceeds its size limit")
        return info


def verify_stream(stream, image, config_digest, platform, entrypoint):
    if not re.fullmatch(r"sha256:[a-f0-9]{64}", config_digest):
        raise ImageRejected("invalid expected config digest")
    if platform not in ("linux/amd64", "linux/arm64"):
        raise ImageRejected("unsupported expected platform")
    if entrypoint not in ("/usr/local/bin/gateway", "/usr/local/bin/hospital-api"):
        raise ImageRejected("unsupported expected entrypoint")
    if not image or image.startswith("-") or len(image) > 255 or any(c in image for c in "\r\n\x00"):
        raise ImageRejected("invalid exact local image tag")
    hex_digest = config_digest[7:]
    config_names = {"blobs/sha256/" + hex_digest, hex_digest + ".json"}
    manifest = None
    config = None
    config_name = None
    count = 0
    with tarfile.open(fileobj=stream, mode="r|", tarinfo=BoundedTarInfo) as archive:
        for member in archive:
            count += 1
            if count > MAX_MEMBERS:
                raise ImageRejected("local image export has too many members")
            if member.name != "manifest.json" and member.name not in config_names:
                continue
            if not member.isfile() or not 0 < member.size <= MAX_METADATA:
                raise ImageRejected("invalid local image identity metadata")
            with archive.extractfile(member) as source:
                raw = source.read(MAX_METADATA + 1)
            if len(raw) != member.size:
                raise ImageRejected("truncated local image identity metadata")
            if member.name == "manifest.json":
                if manifest is not None:
                    raise ImageRejected("duplicate local image manifest")
                manifest = json.loads(raw)
            else:
                if config is not None:
                    raise ImageRejected("duplicate local image config")
                if not hmac.compare_digest(hashlib.sha256(raw).hexdigest(), hex_digest):
                    raise ImageRejected("local image config content differs from its approved digest")
                config = json.loads(raw)
                config_name = member.name
    if not isinstance(manifest, list) or len(manifest) != 1 or not isinstance(manifest[0], dict):
        raise ImageRejected("local tag must export exactly one image")
    if manifest[0].get("RepoTags") != [image]:
        raise ImageRejected("local export does not identify the exact configured image tag")
    if manifest[0].get("Config") != config_name or not isinstance(config, dict):
        raise ImageRejected("local tag points to a different image config; refusing overwrite/start")
    if config.get("os", "") + "/" + config.get("architecture", "") != platform:
        raise ImageRejected("local image platform differs from the approved archive")
    if config.get("config", {}).get("Entrypoint") != [entrypoint]:
        raise ImageRejected("local image entrypoint differs from the approved service")
    return config_digest + " " + platform


def verify_local(context, image, config_digest, platform, entrypoint):
    process = None
    previous = signal.getsignal(signal.SIGALRM)

    def deadline(_signum, _frame):
        raise ImageRejected("local image export exceeded its verification deadline")

    signal.signal(signal.SIGALRM, deadline)
    signal.alarm(MAX_SECONDS)
    try:
        process = subprocess.Popen(["docker", "--context", context, "image", "save", "--", image], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        stream = BoundedReader(process.stdout)
        identity = verify_stream(stream, image, config_digest, platform, entrypoint)
        # Consume padding/output so the producer cannot block on a full pipe.
        while stream.read(MAX_METADATA):
            pass
        if process.wait(timeout=10) != 0:
            raise ImageRejected("Docker could not export the local image completely")
        return identity
    finally:
        signal.alarm(0)
        signal.signal(signal.SIGALRM, previous)
        if process is not None:
            if process.poll() is None:
                process.kill()
            if process.stdout is not None:
                process.stdout.close()
            process.wait()


if __name__ == "__main__":
    try:
        if len(sys.argv) != 6:
            raise ImageRejected("expected context, exact tag, config digest, platform and entrypoint")
        print(verify_local(*sys.argv[1:]))
    except ImageRejected as error:
        sys.exit(f"Image identity verification refused: {error}")
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, KeyError, AttributeError, tarfile.TarError):
        sys.exit("Image identity verification refused: invalid or incomplete local image export")

#!/usr/bin/env python3
"""Build the packaged bubblewrap binary for one Linux platform.

Mirrors the Rust repository's release staging: bubblewrap is built from pinned
sources and copied into the package's `codex-resources` directory, where the
sandbox looks for it when no system `bwrap` is on PATH. The receipt records the
staged binary's digest, which is what a release pins for the runtime digest
check.

The build needs meson, ninja, a C compiler, and libcap headers. It only runs on
Linux targets: bubblewrap is a Linux sandbox launcher.

Checksums establish input identity, not security or license approval.

Usage:
    python3 third_party/bwrap/prepare_bwrap.py --platform linux-amd64 [--force]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import platform
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent
CACHE = ROOT / "cache"
WORK = ROOT / "work"
BUILD = ROOT / "build"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def load_manifest() -> dict:
    with (ROOT / "sources.json").open("r", encoding="utf-8") as handle:
        return json.load(handle)


def host_platform() -> str:
    goos = {"Windows": "windows", "Darwin": "darwin", "Linux": "linux"}.get(platform.system())
    if goos is None:
        raise SystemExit(f"unsupported host platform {platform.system()!r}")
    goarch = {"AMD64": "amd64", "ARM64": "arm64", "x86_64": "amd64", "aarch64": "arm64"}.get(
        platform.machine()
    )
    if goarch is None:
        raise SystemExit(f"unsupported host architecture {platform.machine()!r}")
    return f"{goos}-{goarch}"


def verify_download(url: str, destination: Path, digest: str, size: int | None) -> Path:
    if not destination.is_file():
        print(f"==> Downloading {url}")
        destination.parent.mkdir(parents=True, exist_ok=True)
        import urllib.request

        with urllib.request.urlopen(url, timeout=300) as response, destination.open("wb") as out:
            shutil.copyfileobj(response, out)
    actual = sha256_file(destination)
    if actual != digest:
        raise SystemExit(f"digest mismatch for {destination.name}: {actual} != {digest}")
    if size is not None and destination.stat().st_size != size:
        raise SystemExit(f"size mismatch for {destination.name}")
    return destination


def run(command: list[str]) -> None:
    print("==> " + " ".join(command))
    try:
        result = subprocess.run(command)
    except FileNotFoundError:
        raise SystemExit(
            f"{command[0]} is required to build the packaged sandbox launcher; install meson and ninja"
        )
    if result.returncode != 0:
        raise SystemExit(f"command failed: {' '.join(command)}")


def resolve_build_tools() -> None:
    for program in ("meson", "ninja"):
        if shutil.which(program) is None:
            raise SystemExit(
                f"{program} is required to build the packaged sandbox launcher; install meson and ninja"
            )


def prepare(platform_id: str, force: bool) -> int:
    goos, _, goarch = platform_id.partition("-")
    if goos != "linux":
        raise SystemExit(
            f"bubblewrap is a Linux sandbox launcher; {platform_id} is not a supported target"
        )
    manifest = load_manifest()
    source = manifest["sources"][0]
    output = BUILD / platform_id / "bin" / "bwrap"
    if output.is_file() and not force:
        print(f"==> bubblewrap already prepared at {output}")
        return 0

    archive = verify_download(
        source["url"], CACHE / Path(source["url"]).name, source["sha256"], source.get("bytes")
    )
    source_dir = WORK / f"{source['name']}-{source['version']}"
    if force and source_dir.is_dir():
        shutil.rmtree(source_dir)
    if not source_dir.is_dir():
        WORK.mkdir(parents=True, exist_ok=True)
        with tarfile.open(archive) as bundle:
            bundle.extractall(WORK)
    if platform.machine().lower() in {"aarch64", "arm64"} and goarch != "arm64":
        raise SystemExit(f"cannot build {platform_id} on {platform.machine()}")

    resolve_build_tools()
    build_dir = WORK / f"build-{platform_id}"
    if force and build_dir.is_dir():
        shutil.rmtree(build_dir)
    run(["meson", "setup", str(build_dir), str(source_dir), "--buildtype=release"])
    run(["meson", "compile", "-C", str(build_dir)])

    candidate = build_dir / "bwrap"
    if not candidate.is_file():
        raise SystemExit(f"the bubblewrap build produced no {candidate}")
    output.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(candidate, output)
    output.chmod(0o755)
    receipt = {
        "schemaVersion": 1,
        "platform": platform_id,
        "source": {
            "name": source["name"],
            "version": source["version"],
            "url": source["url"],
            "sha256": source["sha256"],
            "license": manifest["license"],
        },
        "executable": {"file": "bwrap", "sha256": sha256_file(output)},
    }
    (output.parent.parent / "prepared.json").write_text(
        json.dumps(receipt, indent=2) + "\n", encoding="utf-8"
    )
    # A one-line digest file the build scripts can read without a JSON parser.
    (output.parent.parent / "bundled-bwrap.sha256").write_text(
        receipt["executable"]["sha256"] + "\n", encoding="utf-8"
    )
    print(f"==> Prepared {output}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", default=None, help="target platform, e.g. linux-amd64")
    parser.add_argument("--force", action="store_true", help="rebuild even when bwrap exists")
    arguments = parser.parse_args()
    return prepare(arguments.platform or host_platform(), arguments.force)


if __name__ == "__main__":
    sys.exit(main())

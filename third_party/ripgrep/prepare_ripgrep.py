#!/usr/bin/env python3
"""Stage the packaged ripgrep binary for one platform.

Mirrors the Rust repository's packaged CLI layout: every release copies the
pinned ripgrep executable into the package's `codex-path` directory, which the
CLI puts on PATH for its own tools (`package_layout.path_dir`). Pinned archive
digests are verified before the binary is staged, and a receipt records what was
staged.

Checksums establish input identity, not security or license approval.

Usage:
    python3 third_party/ripgrep/prepare_ripgrep.py [--platform <goos>-<goarch>] [--force]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import platform
import shutil
import sys
import tarfile
import zipfile
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


def resolve_target(manifest: dict, platform_id: str) -> dict:
    goos, _, goarch = platform_id.partition("-")
    for entry in manifest["targets"]:
        if entry["goos"] == goos and entry["goarch"] == goarch:
            return entry
    raise SystemExit(f"no pinned ripgrep release for {platform_id}")


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


def extract_executable(archive: Path, destination: Path, executable_name: str) -> Path:
    extracted = WORK / archive.name.removesuffix(".zip").removesuffix(".tar.gz")
    if extracted.is_dir():
        shutil.rmtree(extracted)
    extracted.mkdir(parents=True, exist_ok=True)
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as bundle:
            bundle.extractall(extracted)
    else:
        with tarfile.open(archive) as bundle:
            bundle.extractall(extracted)
    candidate = next((path for path in extracted.rglob(executable_name) if path.is_file()), None)
    if candidate is None:
        raise SystemExit(f"{archive.name} contains no {executable_name}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(candidate, destination)
    if not executable_name.endswith(".exe"):
        destination.chmod(0o755)
    return destination


def prepare(platform_id: str, force: bool) -> int:
    manifest = load_manifest()
    entry = resolve_target(manifest, platform_id)
    goos = entry["goos"]
    executable_name = "rg.exe" if goos == "windows" else "rg"
    output = BUILD / platform_id / "bin" / executable_name
    if output.is_file() and not force:
        print(f"==> ripgrep already prepared at {output}")
        return 0

    archive = verify_download(
        entry["url"], CACHE / Path(entry["url"]).name, entry["sha256"], entry.get("bytes")
    )
    extract_executable(archive, output, executable_name)
    receipt = {
        "schemaVersion": 1,
        "platform": platform_id,
        "source": {
            "name": "ripgrep",
            "target": entry["target"],
            "url": entry["url"],
            "sha256": entry["sha256"],
            "license": manifest["license"],
        },
        "executable": {"file": executable_name, "sha256": sha256_file(output)},
    }
    (output.parent.parent / "prepared.json").write_text(
        json.dumps(receipt, indent=2) + "\n", encoding="utf-8"
    )
    print(f"==> Prepared {output}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", default=None, help="target platform, e.g. windows-amd64")
    parser.add_argument("--force", action="store_true", help="stage even when ripgrep exists")
    arguments = parser.parse_args()
    return prepare(arguments.platform or host_platform(), arguments.force)


if __name__ == "__main__":
    sys.exit(main())

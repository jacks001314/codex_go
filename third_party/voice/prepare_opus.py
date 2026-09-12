#!/usr/bin/env python3
"""Prepare the packaged Opus codec for one platform.

Mirrors the Rust repository's private voice runtime staging: pinned sources are
verified by digest, built into a platform directory, and recorded in a receipt.
The result is a shared library the Go voice helper loads at runtime, so release
builds stay CGO-free.

Checksums establish input identity, not security or license approval.

Usage:
    python3 third_party/voice/prepare_opus.py [--platform <goos>-<goarch>] [--force]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import shutil
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent
CACHE = ROOT / "cache"
WORK = ROOT / "work"
BUILD = ROOT / "build"

OPUS_LIBRARY_NAMES = {
    "windows": "libopus.dll",
    "darwin": "libopus.0.dylib",
    "linux": "libopus.so.0",
}


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


def resolve_build_program(manifest: dict, goos: str) -> tuple[str, Path | None]:
    if goos == "windows":
        tool = next(
            (item for item in manifest.get("buildTools", []) if item.get("platform") == goos),
            None,
        )
        if tool is None:
            raise SystemExit("no pinned build program for windows")
        archive = verify_download(tool["url"], CACHE / Path(tool["url"]).name, tool["sha256"], None)
        extracted = CACHE / "ninja"
        if not (extracted / "ninja.exe").is_file():
            extracted.mkdir(parents=True, exist_ok=True)
            with zipfile.ZipFile(archive) as bundle:
                bundle.extractall(extracted)
        return str(extracted / "ninja.exe"), archive
    program = shutil.which("ninja") or shutil.which("ninja-build")
    if program is None:
        raise SystemExit("ninja is required to build the packaged codec")
    return program, None


def run(command: list[str], *, cwd: Path | None = None) -> None:
    print("==> " + " ".join(command))
    result = subprocess.run(command, cwd=cwd)
    if result.returncode != 0:
        raise SystemExit(f"command failed with {result.returncode}: {' '.join(command)}")


def prepare(platform_id: str, force: bool) -> int:
    manifest = load_manifest()
    source = manifest["sources"][0]
    goos = platform_id.split("-", 1)[0]
    library_name = OPUS_LIBRARY_NAMES.get(goos)
    if library_name is None:
        raise SystemExit(f"unsupported target platform {goos!r}")

    output = BUILD / platform_id
    library = output / "lib" / library_name
    if library.is_file() and not force:
        print(f"==> Codec already prepared at {library}")
        return 0

    archive = verify_download(source["url"], CACHE / Path(source["url"]).name, source["sha256"], source["bytes"])
    source_dir = WORK / f"opus-{source['version']}"
    if not source_dir.is_dir():
        WORK.mkdir(parents=True, exist_ok=True)
        with tarfile.open(archive) as bundle:
            bundle.extractall(WORK)

    build_program, tool_archive = resolve_build_program(manifest, goos)
    build_dir = WORK / f"build-{platform_id}"
    prefix = WORK / f"prefix-{platform_id}"
    if force and build_dir.is_dir():
        shutil.rmtree(build_dir)
    run(
        [
            "cmake",
            "-S",
            str(source_dir),
            "-B",
            str(build_dir),
            "-G",
            "Ninja",
            f"-DCMAKE_MAKE_PROGRAM={build_program}",
            "-DCMAKE_BUILD_TYPE=Release",
            "-DOPUS_BUILD_SHARED_LIBRARY=ON",
            "-DOPUS_BUILD_PROGRAMS=OFF",
            "-DOPUS_BUILD_TESTING=OFF",
            f"-DCMAKE_INSTALL_PREFIX={prefix}",
        ]
    )
    run(["cmake", "--build", str(build_dir), "--target", "install"])

    candidate = next(prefix.rglob(library_name), None)
    if candidate is None:
        raise SystemExit(f"the build produced no {library_name}")
    (output / "lib").mkdir(parents=True, exist_ok=True)
    shutil.copy2(candidate, library)
    include = prefix / "include"
    if include.is_dir():
        shutil.copytree(include, output / "include", dirs_exist_ok=True)

    receipt = {
        "schemaVersion": 1,
        "platform": platform_id,
        "source": {
            "name": source["name"],
            "version": source["version"],
            "url": source["url"],
            "sha256": source["sha256"],
        },
        "library": {"file": library_name, "sha256": sha256_file(library)},
        "buildProgram": build_program,
        "buildToolSha256": sha256_file(tool_archive) if tool_archive else None,
        "cmake": subprocess.run(["cmake", "--version"], capture_output=True, text=True)
        .stdout.splitlines()[0]
        .strip(),
    }
    (output / "built.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
    print(f"==> Prepared {library}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", default=None, help="target platform, e.g. windows-amd64")
    parser.add_argument("--force", action="store_true", help="rebuild even when the codec exists")
    arguments = parser.parse_args()
    return prepare(arguments.platform or host_platform(), arguments.force)


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Stage the packaged bubblewrap binary for one Linux platform.

Mirrors the Rust repository's release staging: bubblewrap is copied into the
package's `codex-resources` directory, where the sandbox looks for it when no
system `bwrap` is on PATH. The receipt records the staged binary's digest,
which is what a release pins for the runtime digest check.

By default the script builds from the pinned source when the host can compile
it (a matching Linux host with meson and ninja) and otherwise stages the pinned
prebuilt binary from the upstream Codex release. `--source` and `--prebuilt`
force either path. Building needs meson, ninja, a C compiler, and libcap
headers; the prebuilt archive is a small static Linux binary, so a Windows or
macOS release host can stage every Linux target without a Linux toolchain.

Checksums establish input identity, not security or license approval.

Usage:
    python3 third_party/bwrap/prepare_bwrap.py --platform linux-amd64 [--force]
    python3 third_party/bwrap/prepare_bwrap.py --platform linux-amd64 --prebuilt
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
                f"{program} is required to build the packaged sandbox launcher; "
                "install meson and ninja or rerun with --prebuilt"
            )


def can_build_from_source(platform_id: str) -> bool:
    return platform.system() == "Linux" and host_platform() == platform_id


def resolve_prebuilt(manifest: dict, platform_id: str) -> dict | None:
    goos, _, goarch = platform_id.partition("-")
    for entry in manifest.get("prebuilt", []):
        if entry.get("goos") == goos and entry.get("goarch") == goarch:
            return entry
    return None


def write_receipt(manifest: dict, platform_id: str, output: Path, source: dict) -> None:
    receipt = {
        "schemaVersion": 1,
        "platform": platform_id,
        "source": source,
        "executable": {"file": "bwrap", "sha256": sha256_file(output)},
    }
    (output.parent.parent / "prepared.json").write_text(
        json.dumps(receipt, indent=2) + "\n", encoding="utf-8"
    )
    # A one-line digest file the build scripts can read without a JSON parser.
    (output.parent.parent / "bundled-bwrap.sha256").write_text(
        receipt["executable"]["sha256"] + "\n", encoding="utf-8"
    )


def prepare_from_source(manifest: dict, platform_id: str, output: Path, force: bool) -> None:
    goarch = platform_id.partition("-")[2]
    source = manifest["sources"][0]
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
    write_receipt(
        manifest,
        platform_id,
        output,
        {
            "name": source["name"],
            "version": source["version"],
            "url": source["url"],
            "sha256": source["sha256"],
            "license": manifest["license"],
        },
    )


def prepare_from_prebuilt(manifest: dict, platform_id: str, output: Path) -> None:
    entry = resolve_prebuilt(manifest, platform_id)
    if entry is None:
        raise SystemExit(
            f"no pinned prebuilt bubblewrap for {platform_id}; "
            "build it on a matching Linux host with --source"
        )
    archive = verify_download(
        entry["url"], CACHE / Path(entry["url"]).name, entry["sha256"], entry.get("bytes")
    )
    output.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as bundle:
        member = next(
            (
                item
                for item in bundle.getmembers()
                if item.isfile() and Path(item.name).name == entry["member"]
            ),
            None,
        )
        if member is None:
            raise SystemExit(f"{archive.name} contains no {entry['member']}")
        source = bundle.extractfile(member)
        if source is None:
            raise SystemExit(f"{archive.name} carries no readable {entry['member']}")
        with source, output.open("wb") as handle:
            shutil.copyfileobj(source, handle)
    output.chmod(0o755)
    write_receipt(
        manifest,
        platform_id,
        output,
        {
            "name": "bubblewrap",
            "kind": "prebuilt",
            "upstream": entry["upstream"],
            "upstreamRelease": entry["upstreamRelease"],
            "target": entry["target"],
            "url": entry["url"],
            "sha256": entry["sha256"],
            "license": manifest["license"],
        },
    )


def prepare(platform_id: str, force: bool, mode: str) -> int:
    goos, _, _ = platform_id.partition("-")
    if goos != "linux":
        raise SystemExit(
            f"bubblewrap is a Linux sandbox launcher; {platform_id} is not a supported target"
        )
    manifest = load_manifest()
    output = BUILD / platform_id / "bin" / "bwrap"
    if output.is_file() and not force:
        print(f"==> bubblewrap already prepared at {output}")
        return 0
    if mode == "prebuilt":
        prepare_from_prebuilt(manifest, platform_id, output)
    elif mode == "source":
        prepare_from_source(manifest, platform_id, output, force)
    elif can_build_from_source(platform_id) and shutil.which("meson") and shutil.which("ninja"):
        prepare_from_source(manifest, platform_id, output, force)
    else:
        print("==> Build tools are unavailable; staging the pinned prebuilt bubblewrap")
        prepare_from_prebuilt(manifest, platform_id, output)
    print(f"==> Prepared {output}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", default=None, help="target platform, e.g. linux-amd64")
    parser.add_argument("--force", action="store_true", help="rebuild even when bwrap exists")
    mode_group = parser.add_mutually_exclusive_group()
    mode_group.add_argument(
        "--source",
        action="store_const",
        const="source",
        dest="mode",
        help="build from the pinned bubblewrap source",
    )
    mode_group.add_argument(
        "--prebuilt",
        action="store_const",
        const="prebuilt",
        dest="mode",
        help="stage the pinned upstream Codex prebuilt binary",
    )
    parser.set_defaults(mode="auto")
    arguments = parser.parse_args()
    return prepare(arguments.platform or host_platform(), arguments.force, arguments.mode)


if __name__ == "__main__":
    sys.exit(main())

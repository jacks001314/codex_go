#!/usr/bin/env python3
"""Regenerate shell/snapshot_scripts.go from the Rust shell-command crate.

The shell-snapshot capture scripts must match Rust byte for byte, so they are
extracted instead of transcribed. Usage:

    python scripts/extract_snapshot_scripts.py [--rust <shell-command/src>]

--rust defaults to $CODEX_RUST_ROOT/shell-command/src when that variable is set,
otherwise to ../git/codex/codex-rs/shell-command/src next to this repository.
"""

import argparse
import pathlib
import re
import sys


def raw_literals(text):
    """Return the r#"..."# literals of text in order."""
    out = []
    for match in re.finditer(r'r(#*)"', text):
        hashes = match.group(1)
        start = match.end()
        end = text.find('"' + hashes, start)
        if end == -1:
            raise SystemExit("unterminated raw string")
        out.append(text[start:end])
    return out


def normal_literal(text, needle):
    """Decode the string literal of the `const <needle>` declaration."""
    index = text.find(needle)
    if index == -1:
        raise SystemExit("missing %s" % needle)
    start = text.find('"', index)
    end = start + 1
    while True:
        if text[end] == "\\":
            end += 2
            continue
        if text[end] == '"':
            break
        end += 1
    body = text[start + 1:end]
    return body.replace("\\\\", "\\").replace('\\"', '"').replace("\\n", "\n")


HEADER = '''package shell

// Code generated from the Rust shell-command crate. DO NOT EDIT by hand: these
// are verbatim script bodies, and snapshot behaviour depends on their exact
// bytes. Regenerate with scripts/extract_snapshot_scripts.py.
//
// Sources: shell_snapshot_capture.rs (capture scripts), startup.rs (login
// startup seeding), shell_snapshot.rs (POSIX ENV expansion helper),
// shell_snapshot_exports.rs (native export declarations).
'''


def main():
    parser = argparse.ArgumentParser()
    repository = pathlib.Path(__file__).resolve().parent.parent
    default_rust = pathlib.Path("../git/codex/codex-rs/shell-command/src")
    parser.add_argument(
        "--rust",
        type=pathlib.Path,
        default=pathlib.Path(
            pathlib.Path.cwd() / default_rust
            if pathlib.Path.cwd() != repository
            else repository / default_rust
        ),
        help="path to codex-rs/shell-command/src",
    )
    args = parser.parse_args()
    root = args.rust
    if not root.is_dir():
        raise SystemExit("not a directory: %s" % root)

    capture = (root / "shell_snapshot_capture.rs").read_text(encoding="utf-8")
    startup = (root / "startup.rs").read_text(encoding="utf-8")
    snapshot = (root / "shell_snapshot.rs").read_text(encoding="utf-8")
    exports = (root / "shell_snapshot_exports.rs").read_text(encoding="utf-8")

    capture_literals = raw_literals(capture)
    sh_literals = raw_literals(capture[capture.index("fn sh_snapshot_script"):])
    startup_literals = raw_literals(startup)
    snapshot_literals = raw_literals(snapshot)
    export_literals = raw_literals(exports)

    constants = [
        ("snapshotCommandHelper", capture_literals[0]),
        ("snapshotEnvironment", capture_literals[1]),
        ("snapshotZshScript", capture_literals[2]),
        ("snapshotBashScript", capture_literals[3]),
        ("snapshotShStartupScript", sh_literals[0]),
        ("snapshotShScript", sh_literals[1]),
        ("bashShSnapshotHeader", normal_literal(capture, "BASH_SH_SNAPSHOT_HEADER")),
        ("shellStartupZsh", startup_literals[0]),
        ("shellStartupBash", startup_literals[1]),
        ("posixEnvPathExpansionFunction", snapshot_literals[0]),
        ("snapshotExportsBash", export_literals[0]),
        ("snapshotExportsZsh", export_literals[1]),
        ("snapshotExportsSh", export_literals[2]),
    ]

    parts = [HEADER]
    for name, body in constants:
        if "`" in body:
            raise SystemExit("%s contains a backtick; pick different quoting" % name)
        parts.append("\nconst %s = `%s`\n" % (name, body))
    output = repository / "shell" / "snapshot_scripts.go"
    with output.open("w", encoding="utf-8", newline="\n") as handle:
        handle.write("".join(parts))
    print("wrote %s" % output, file=sys.stderr)


if __name__ == "__main__":
    main()

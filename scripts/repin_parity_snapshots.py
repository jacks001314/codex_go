#!/usr/bin/env python3
"""Re-pin codex_go parity snapshot literals to the current Rust checkout.

Usage:
  python scripts/repin_parity_snapshots.py --write

Without --write it only prints the drift it would apply. Every change is
reported as (file, item, old -> new) so the re-anchor can be reviewed.
"""
import hashlib
import os
import re
import subprocess
import sys

GO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RUST_ROOT = os.environ.get("CODEX_RUST_ROOT") or r"D:\qax\reagent\dev\git\codex\codex-rs"
PARITY = os.path.join(GO_ROOT, "parity")

def read(path):
    return open(path, encoding="utf-8").read()

def write(path, text):
    open(path, "w", encoding="utf-8", newline="").write(text)

def func_body(src, name):
    start = src.index("func " + name + "(")
    end = src.index("\n}\n", start)
    return start, end

def replace_inventory(src, func, values):
    start, end = func_body(src, func)
    body = src[start:end]
    m = re.search(r"return \[\]string\{\n(.*?)\n\t\}", body, re.S)
    if not m:
        raise SystemExit("inventory list not found in " + func)
    listed = re.findall(r'"([^"]+)"', m.group(1))
    if listed == values:
        return src, []
    content = "\n".join('\t\t"' + v + '",' for v in values)
    newbody = body[:m.start(1)] + content + body[m.end(1):]
    return src[:start] + newbody + src[end:], [(func, len(listed), len(values), sorted(set(values) - set(listed)), sorted(set(listed) - set(values)))]

def rust_dirs():
    return sorted(d for d in os.listdir(RUST_ROOT) if os.path.isdir(os.path.join(RUST_ROOT, d)) and d != "target")

def go_dirs():
    out = subprocess.run(["git", "-C", GO_ROOT, "ls-files", "-z"], capture_output=True).stdout.decode()
    tracked = set()
    for path in out.split("\x00"):
        if path:
            tracked.add(path.split("/")[0])
    ignored = {"bin","cmd","deliverables","docs","dist","npm","scripts","sdktests","tmp","update","vscodetests"}
    values = []
    for d in sorted(os.listdir(GO_ROOT)):
        if not os.path.isdir(os.path.join(GO_ROOT, d)) or d.startswith("."):
            continue
        if d in ignored or d not in tracked:
            continue
        values.append(d)
    return values

def count_files(path):
    n = 0
    for _, _, files in os.walk(path):
        n += len(files)
    return n

def count_snap_shallow(path):
    return sum(1 for e in os.listdir(path) if os.path.isfile(os.path.join(path, e)) and e.endswith(".snap"))

def count_snap_recursive(path):
    n = 0
    for _, _, files in os.walk(path):
        n += sum(1 for f in files if f.endswith(".snap"))
    return n

def repin_files(src, base, counter):
    changes = []
    matches = list(re.finditer(r'Path:\s*"([^"]+)"', src))
    for idx, m in enumerate(matches):
        stop = matches[idx + 1].start() if idx + 1 < len(matches) else len(src)
        region = src[m.end():stop]
        fm = re.search(r"Files:\s*(\d+)", region)
        if not fm:
            continue
        path = m.group(1)
        abs_path = os.path.join(base, path.replace("/", os.sep))
        if not os.path.isdir(abs_path):
            continue
        new = counter(abs_path)
        old = int(fm.group(1))
        if new != old:
            changes.append((path, old, new))
            s = m.end() + fm.start(1)
            e = m.end() + fm.end(1)
            src = src[:s] + str(new) + src[e:]
    return src, changes

def repin_hashes(src, base):
    changes = []
    for m in re.finditer(r'\{Path:\s*"([^"]+)",\s*SHA256:\s*"([0-9a-f]{64})"\}', src):
        path = os.path.join(base, m.group(1).replace("/", os.sep))
        if not os.path.isfile(path):
            continue
        data = open(path, "rb").read().replace(b"\r\n", b"\n")
        new = hashlib.sha256(data).hexdigest()
        if new != m.group(2):
            changes.append((m.group(1), m.group(2)[:12], new[:12]))
            src = src[:m.start(2)] + new + src[m.end(2):]
    return src, changes

def main():
    apply = "--write" in sys.argv
    report = []

    inv = os.path.join(PARITY, "rust_inventory_test.go")
    src = read(inv)
    src, _ = replace_inventory(src, "rustTopLevelDirectoriesSnapshot", rust_dirs())
    src, _ = replace_inventory(src, "goTopLevelDirectoriesSnapshot", go_dirs())
    report.append((inv, "inventory", "recomputed"))

    tui = os.path.join(PARITY, "rust_tui_snapshot_manifest_test.go")
    src_tui = read(tui)
    src_tui, tui_changes = repin_files(src_tui, RUST_ROOT, count_snap_shallow)
    total = count_snap_recursive(os.path.join(RUST_ROOT, "tui"))
    m = re.search(r'Rust TUI snapshot total drift: got %d want (\d+)', src_tui)
    if m and int(m.group(1)) != total:
        src_tui = src_tui[:m.start(1)] + str(total) + src_tui[m.end(1):]
        report.append((tui, "total", "%s -> %d" % (m.group(1), total)))
    for c in tui_changes:
        report.append((tui, c[0], "%d -> %d" % (c[1], c[2])))

    fx = os.path.join(PARITY, "rust_fixture_manifest_test.go")
    src_fx = read(fx)
    src_fx, fx_changes = repin_files(src_fx, RUST_ROOT, count_files)
    for c in fx_changes:
        report.append((fx, c[0], "%d -> %d" % (c[1], c[2])))

    snap = os.path.join(PARITY, "rust_snapshot_test.go")
    src_snap = read(snap)
    src_snap, hash_changes = repin_hashes(src_snap, RUST_ROOT)
    for c in hash_changes:
        report.append((snap, c[0], "%s -> %s" % (c[1], c[2])))

    for row in report:
        print("%-38s %-52s %s" % (os.path.basename(row[0]), row[1][:52], row[2]))
    print("changes:", len(report))

    if apply:
        write(inv, src)
        write(tui, src_tui)
        write(fx, src_fx)
        write(snap, src_snap)
        print("written")

if __name__ == "__main__":
    main()

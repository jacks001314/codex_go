#!/usr/bin/env python3
"""Round-109 symbol-coverage audit (reproducible).

For every Rust commit in bd19459358..<target>, extract the *new public API
identifiers* (pub fn/struct/enum/trait/const/static/type) from the added lines
of non-test files, then look for the same identifier (underscore/case folded)
anywhere in the Go sources or Go string literals.

Outputs: update/r109_symbol_coverage_2026_10_17.tsv
This is a comparison aid, not a correctness proof: a present symbol means the
Go port exposes the same surface, not that its behaviour was re-verified.
"""
import subprocess,re,sys,io,os,json,collections
sys.stdout.reconfigure(encoding="utf-8")
RUST=r"D:\qax\reagent\dev\git\codex"
GO=r"D:\qax\reagent\dev\codex_go"
FROM="bd19459358f534ed1cae464ec13d56600aeb45f2"
TO="ce0323f2f2d3b25e82806cd555237da3db4e1897"
log=subprocess.run(["git","-C",RUST,"log","--reverse","--format=@@@%H","--unified=0","-p",FROM+".."+TO],capture_output=True,text=True,errors="replace").stdout

gosyms=set(); gokeys=set()
norm=lambda s: re.sub(r"[^a-z0-9]","",s.lower())
for root,dirs,files in os.walk(GO):
    dirs[:]=[d for d in dirs if d not in (".git","update","attachments",".syntropy","node_modules")]
    for f in files:
        if not f.endswith(".go"): continue
        try: t=io.open(os.path.join(root,f),encoding="utf-8",errors="ignore").read()
        except: continue
        for m in re.finditer(r"[A-Za-z_][A-Za-z0-9_]*",t): gosyms.add(norm(m.group(0)))
        for m in re.finditer(r'"([^"\n]{3,80})"',t): gokeys.add(norm(m.group(1)))
print("go syms",len(gosyms),"keys",len(gokeys))

def testfile(f):
    return (f.endswith("_tests.rs") or f.endswith("_test.rs") or f.endswith("tests.rs") or "/tests/" in f or "/testdata/" in f or f.endswith(".snap"))

PAT=[
 r"\bpub(?:\([^)]*\))?\s+(?:async\s+)?(?:unsafe\s+)?fn\s+([a-z_][a-z0-9_]{3,})",
 r"\bpub(?:\([^)]*\))?\s+struct\s+([A-Z][A-Za-z0-9_]{2,})",
 r"\bpub(?:\([^)]*\))?\s+enum\s+([A-Z][A-Za-z0-9_]{2,})",
 r"\bpub(?:\([^)]*\))?\s+trait\s+([A-Z][A-Za-z0-9_]{2,})",
 r"\bpub(?:\([^)]*\))?\s+const\s+([A-Z][A-Za-z0-9_]{3,})",
 r"\bpub(?:\([^)]*\))?\s+static\s+([A-Z][A-Za-z0-9_]{3,})",
 r"\bpub(?:\([^)]*\))?\s+type\s+([A-Za-z][A-Za-z0-9_]{3,})",
]
commits=[]; cur=None; curfile=""
for line in log.split("\n"):
    if line.startswith("@@@"):
        cur={"sha":line[3:].strip(),"files":[],"syms":set(),"keys":set()}; commits.append(cur); curfile=""
    elif cur is not None:
        if line.startswith("+++ b/"):
            curfile=line[6:]; cur["files"].append(curfile)
        elif line.startswith("+") and not line.startswith("+++") and not testfile(curfile):
            body=line[1:]
            for pat in PAT:
                for m in re.finditer(pat,body):
                    n=m.group(1)
                    if len(n)>=4 and n.lower() not in ("default","new","value","values","string","result","error","option","self"): cur["syms"].add(n)
            for m in re.finditer(r'"(?:rename\s*=\s*)?"([a-z][a-z0-9_.]{5,50})"',body): cur["keys"].add(m.group(1))
print("commits",len(commits))
rows=[]
for c in commits:
    s=c["syms"]; k=c["keys"]
    m=[x for x in s if norm(x) in gosyms]
    r=(len(m)/len(s)) if s else None
    rows.append({"sha":c["sha"],"n":len(s),"m":len(m),"r":r,"unmatched":sorted(s-set(m))[:10],
                 "nkeys":len(k),"mkeys":len([x for x in k if norm(x) in gokeys])})
json.dump(rows,io.open(r"C:\Users\axhuo\AppData\Local\Temp\sync5\coverage2.json","w",encoding="utf-8"))
todo=[x for x in rows if x["r"] is not None]
print("with pub symbols",len(todo))
bands=collections.Counter(round(x["r"]*4)/4 for x in todo); print(sorted(bands.items()))
for th in (0.9,0.8,0.5,0.2): print(">=%.1f"%th,sum(1 for x in todo if x["r"]>=th))
print("avg",sum(x["r"] for x in todo)/max(1,len(todo)))
#!/usr/bin/env python3
"""Mutation runner (set MUT_REPO=<worktree> to mutate a worktree's HEAD) for phase-0 fixes.
usage: run.py SPEC.json [NAME ...]   (MUT_GOCACHE=<dir> keeps the Go build cache of the mutants out of the shared .cache/gocache)
SPEC.json: list of {"name","rf","file","old","new","count"?(default 1),"pkgs"?(default "./...")}
Each mutant is applied alone to a fresh copy of the committed HEAD (git archive), with shared module/build caches,
then `go test -count=1 <pkgs>` runs. Prints KILLED / SURVIVED / INVALID / NOMATCH per mutant and a summary."""
import json, os, re, subprocess, sys, tempfile, shutil
REPO = os.environ.get("MUT_REPO") or os.path.abspath(os.path.join(os.path.dirname(__file__), "../../.."))
TOOLS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../.."))
GO = os.path.join(TOOLS, ".tools/go/bin/go")
specs = json.load(open(sys.argv[1]))
only = set(sys.argv[2:])
res = {}
for s in specs:
    if only and s["name"] not in only:
        continue
    d = tempfile.mkdtemp(prefix="mut-" + s["name"] + "-")
    subprocess.run(f"git -C {REPO} archive HEAD | tar -x -C {d}", shell=True, check=True)
    p = os.path.join(d, s["file"])
    src = open(p).read()
    n = src.count(s["old"])
    if n != s.get("count", 1):
        print(f"[{s['name']}] NOMATCH ({n} matches, wanted {s.get('count',1)}) in {s['file']}"); res[s["name"]] = "NOMATCH"; shutil.rmtree(d); continue
    open(p, "w").write(src.replace(s["old"], s["new"]))
    env = dict(os.environ, GOPATH=os.path.join(TOOLS, ".cache/gopath"), GOMODCACHE=os.path.join(TOOLS, ".cache/gomod"),
               GOCACHE=os.environ.get("MUT_GOCACHE") or os.path.join(TOOLS, ".cache/gocache"), GOTOOLCHAIN="local", GOFLAGS="-mod=readonly")
    r = subprocess.run(f"{GO} test -count=1 {s.get('pkgs','./...')} 2>&1", shell=True, cwd=d, text=True, capture_output=True, env=env, timeout=1200)
    out = r.stdout
    if re.search(r"\[build failed\]|\[setup failed\]", out) or (r.returncode != 0 and not re.search(r"^--- FAIL", out, re.M) and re.search(r"^# ", out, re.M)):
        print(f"[{s['name']}] INVALID (build failed)\n  " + "\n  ".join(out.splitlines()[:6])); res[s["name"]] = "INVALID"
    else:
        fails = re.findall(r"^\s*--- FAIL: (\S+)", out, re.M)
        if fails or r.returncode != 0:
            print(f"[{s['name']}] KILLED by: {', '.join(sorted(set(fails))) or '(package failure)'}"); res[s["name"]] = "KILLED"
        else:
            print(f"[{s['name']}] SURVIVED"); res[s["name"]] = "SURVIVED"
    shutil.rmtree(d, ignore_errors=True)
c = {k: list(res.values()).count(k) for k in ("KILLED", "SURVIVED", "INVALID", "NOMATCH")}
print("summary:", c)

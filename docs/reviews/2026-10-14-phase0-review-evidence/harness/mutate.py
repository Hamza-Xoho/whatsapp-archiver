#!/usr/bin/env python3
"""usage: mutate.py NAME FILE  (reads OLD/NEW from env MUT_OLD / MUT_NEW; MUT_COUNT=expected matches)"""
import os, subprocess, sys, re, shutil
SP = "/private/tmp/claude-501/-Users-hamza-Code-whatsapp-archiver/284f1bb0-ebf3-4003-8336-9709f09e28c1/scratchpad"
name, rel = sys.argv[1], sys.argv[2]
old, new = os.environ["MUT_OLD"], os.environ["MUT_NEW"]
want = int(os.environ.get("MUT_COUNT", "1"))
extra = os.environ.get("MUT_PKGS", "./...")
d = subprocess.check_output([f"{SP}/mut.sh", name], text=True).strip()
p = os.path.join(d, rel)
src = open(p).read()
n = src.count(old)
if n != want:
    print(f"[{name}] PATTERN MATCHED {n} TIMES (wanted {want}) in {rel}"); sys.exit(3)
open(p, "w").write(src.replace(old, new))
env = dict(os.environ)
env.update({"PATH": "/Users/hamza/Code/whatsapp-archiver/archiver/.tools/bin:/Users/hamza/Code/whatsapp-archiver/archiver/.tools/go/bin:" + os.environ["PATH"],
            "GOPATH": d + "/.cache/gopath", "GOMODCACHE": d + "/.cache/gomod", "GOCACHE": d + "/.cache/gocache", "GOTOOLCHAIN": "auto"})
r = subprocess.run(f"cd {d} && go test -count=1 {extra} 2>&1", shell=True, text=True, capture_output=True, env=env, timeout=900)
out = r.stdout
if "[build failed]" in out or re.search(r"^# ", out, re.M) and "cannot" in out:
    print(f"[{name}] INVALID MUTANT (build failed)\n" + "\n".join(out.splitlines()[:8])); sys.exit(4)
fails = re.findall(r"^--- FAIL: (\S+)", out, re.M)
pk = re.findall(r"^FAIL\s+(\S+)", out, re.M)
if fails or pk:
    print(f"[{name}] KILLED by: {', '.join(fails) or '(package-level failure)'}  pkgs={pk}")
else:
    print(f"[{name}] SURVIVED  (no test failed)")
shutil.rmtree(d, ignore_errors=True)

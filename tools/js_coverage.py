#!/usr/bin/env python3
"""Report the functions in runtime.js that no test ever runs.

The web runtime had no coverage at all: `go tool cover` sees Go, and runtime.js
is a 700KB file the Go tests drive through `node`. So a guard could be written,
wired in, and never executed, and nothing would say so — which is how the same
gap was found on the Go side (`get_readonly.go`, a whole file at 0%).

Node writes V8 coverage when NODE_V8_COVERAGE points at a directory, and the
Go tests spawn `node`, so the children inherit it. The catch: the drivers load
the runtime with `(0, eval)(fs.readFileSync(...))`, and eval'd code has no URL.
It is found here by size instead — and the offsets land exactly on the file's
bytes, which is what makes the mapping back to line numbers honest.

Usage:
    NODE_V8_COVERAGE=<dir> go test ./internal/jsserve ./internal/parity
    python3 tools/js_coverage.py <dir> internal/jsserve/runtime.js [filter]
"""

import json
import os
import pathlib
import re
import sys


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    covdir, runtime_path = sys.argv[1], sys.argv[2]
    name_filter = sys.argv[3] if len(sys.argv) > 3 else ""

    src = pathlib.Path(runtime_path).read_text(encoding="utf-8")
    # Offsets are byte offsets into the evaluated source; the drivers eval the
    # file verbatim, so they are offsets into this text.
    size = len(src)

    # functionName -> whether any run entered it, and where it starts.
    entered: dict[tuple[str, int], bool] = {}
    for path in pathlib.Path(covdir).glob("*.json"):
        try:
            doc = json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            continue
        for script in doc.get("result", []):
            if script.get("url"):
                continue  # a real file: not the eval'd runtime
            ranges = [r for fn in script.get("functions", []) for r in fn.get("ranges", [])]
            if not ranges or max(r["endOffset"] for r in ranges) > size:
                continue
            for fn in script.get("functions", []):
                name = fn.get("functionName") or ""
                if not name:
                    continue
                rs = fn.get("ranges", [])
                if not rs:
                    continue
                key = (name, rs[0]["startOffset"])
                # The first range is the function body; count > 0 means entered.
                entered[key] = entered.get(key, False) or rs[0].get("count", 0) > 0

    if not entered:
        print("no coverage found for the runtime — was NODE_V8_COVERAGE set for the test run?")
        return 1

    # Offset -> line, computed once.
    line_of = []
    line = 1
    for ch in src:
        line_of.append(line)
        if ch == "\n":
            line += 1
    line_of.append(line)

    cold = sorted(
        (line_of[off], name)
        for (name, off), hit in entered.items()
        if not hit and (not name_filter or re.search(name_filter, name, re.I))
    )
    total = len(entered)
    hit = sum(1 for v in entered.values() if v)
    print(f"runtime.js: {hit} of {total} functions entered "
          f"({100.0 * hit / total:.1f}%)")
    if name_filter:
        print(f"never entered, matching /{name_filter}/i:")
    else:
        print("never entered:")
    for ln, name in cold:
        print(f"  {os.path.basename(runtime_path)}:{ln}  {name}")
    if not cold:
        print("  (none)")
    return 0


if __name__ == "__main__":
    sys.exit(main())

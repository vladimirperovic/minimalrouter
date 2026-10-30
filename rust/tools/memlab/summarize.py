"""Summarise memdrive results: python -I summarize.py results.jsonl"""
import collections
import json
import re
import statistics
import sys

rows = [json.loads(l) for l in open(sys.argv[1])]
samples = [r for r in rows if "daemon" in r]
loads = [r for r in rows if r.get("event") == "load"]
starts = [r for r in rows if r.get("event") == "start"]

LABELS = ["idle_1m", "idle_4m", "load_mid", "load_end", "post_1m"]
METRICS = ["rss", "anon", "file", "pss", "private_dirty", "hwm", "threads"]


def mib(kb):
    return kb / 1024


by = collections.defaultdict(list)
for s in samples:
    by[(s["daemon"], s["impl"], s["label"])].append(s)

for daemon in ["routerd", "router-applyd"]:
    print(f"\n## {daemon} (MiB, mean of arms; range in brackets)")
    print("| sample | impl | " + " | ".join(METRICS) + " |")
    print("|---|---|" + "---:|" * len(METRICS))
    for label in LABELS:
        for impl in ["go", "rust"]:
            g = by.get((daemon, impl, label), [])
            if not g:
                continue
            cells = []
            for m in METRICS:
                vals = [x[m] for x in g]
                if m == "threads":
                    cells.append("/".join(str(v) for v in vals))
                    continue
                vals = [mib(v) for v in vals]
                cells.append(f"{statistics.mean(vals):.1f} [{min(vals):.1f}–{max(vals):.1f}]")
            print(f"| {label} | {impl} | " + " | ".join(cells) + " |")

print("\n## load")
for l in loads:
    print(f"arm {l['arm']} {l['impl']}: {l['requests']} req in {l['seconds']} s = {l['requests']/l['seconds']:.1f} req/s, "
          f"{l['bytes']/1e6:.1f} MB, status {l['status']}, errors {l['errors']}")

print("\n## CPU ticks (1 tick = 10 ms) between idle_4m and load_end")
for daemon in ["routerd", "router-applyd"]:
    for arm in sorted({s["arm"] for s in samples}):
        a = {s["label"]: s for s in samples if s["arm"] == arm and s["daemon"] == daemon}
        if "idle_4m" in a and "load_end" in a:
            ld = next(l for l in loads if l["arm"] == arm)
            ticks = a["load_end"]["cpu_ticks"] - a["idle_4m"]["cpu_ticks"]
            print(f"{daemon} arm {arm} {a['idle_4m']['impl']}: {ticks} ticks = {ticks * 10 / ld['requests']:.2f} ms/request; "
                  f"startup+idle ticks {a['idle_4m']['cpu_ticks']}")

print("\n## start")
for s in starts:
    m = re.search(r"applyd_ms=(\d+) routerd_ms=(\d+)", s["output"])
    print(f"arm {s['arm']} {s['impl']}: applyd {m.group(1)} ms, routerd {m.group(2)} ms")

print("\n## system MemAvailable (MiB)")
for arm in sorted({s["arm"] for s in samples}):
    a = {s["label"]: s for s in samples if s["arm"] == arm and s["daemon"] == "routerd"}
    print(f"arm {arm} {next(iter(a.values()))['impl']}: " + ", ".join(f"{k} {mib(a[k]['mem_available']):.0f}" for k in LABELS if k in a))

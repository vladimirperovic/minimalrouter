"""Drive the Go vs Rust memory comparison on the lab router.

Usage: MR_LAB_CREDS=creds.txt python -I memdrive.py OUT.jsonl IMPL [IMPL ...]
Each IMPL arm: fresh state, start daemons, idle samples, API load, post-load
sample. IMPL is a directory under /opt/memtest on the router holding routerd
and router-applyd; memtest.sh must be at /opt/memtest/memtest.sh and
"memtest.sh backup" must have run once. MR_LAB_CREDS names a local, untracked
key=value file with root_recovery (root SSH) and dashboard_admin; the values
are never printed.
"""
import http.client
import json
import os
import pathlib
import random
import re
import ssl
import sys
import threading
import time

import paramiko

CREDS_FILE = pathlib.Path(os.environ["MR_LAB_CREDS"])
CREDS = dict(l.split("=", 1) for l in CREDS_FILE.read_text().split())
HOST, PORT = "192.168.1.1", 8443
ORIGIN = f"https://{HOST}:{PORT}"
CTX = ssl._create_unverified_context()

READS = """accounting accounting/insights audit/events auth/session config devices/pauses
dns-activity dns-activity/alerts dns-activity/alerts/exceptions dns-activity/alerts/summary
dns-activity/recent dns-activity/settings dns-filter dns-filter/operations
dns-filter/profiles firewall/activity firewall/status firmware/local-status
firmware/status gateway/events gateway/history gateway/insights gateway/recovery gateway/settings
gateway/summary health network/devices network/lan network/leases
recovery/status setup/interfaces setup/status snapshots startup/boots system system/diagnostics
system/interfaces transactions/pending wireguard/provisioning-preview""".split()

IDLE_FIRST, IDLE_SECOND = 60, 180
LOAD_SECONDS, LOAD_THREADS = 180, 4
POST_LOAD = 60


def ssh(cmd):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username="root", password=CREDS["root_recovery"], timeout=15,
              allow_agent=False, look_for_keys=False)
    _, out, err = c.exec_command(cmd, timeout=900)
    text = out.read().decode(errors="replace") + err.read().decode(errors="replace")
    rc = out.channel.recv_exit_status()
    c.close()
    if rc != 0:
        raise RuntimeError(f"{cmd!r} failed ({rc}): {text}")
    return text


def parse_samples(text):
    rows = []
    for line in text.splitlines():
        parts = line.split()
        if len(parts) < 3 or parts[0] != "sample":
            continue
        row = {"label": parts[1], "daemon": parts[2]}
        for kv in parts[3:]:
            k, _, v = kv.partition("=")
            row[k] = int(v) if v.isdigit() else v
        rows.append(row)
    return rows


def request(conn, method, path, body=None, cookie=None, csrf=None):
    headers = {"Origin": ORIGIN}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if cookie:
        headers["Cookie"] = cookie
    if csrf:
        headers["X-CSRF-Token"] = csrf
    conn.request(method, path, body=None if body is None else json.dumps(body), headers=headers)
    r = conn.getresponse()
    data = r.read()
    return r.status, r.getheaders(), data


def connect():
    return http.client.HTTPSConnection(HOST, PORT, context=CTX, timeout=60)


def login():
    for attempt in range(20):
        try:
            conn = connect()
            st, hdr, body = request(conn, "POST", "/api/v1/auth/login", {"password": CREDS["dashboard_admin"]})
            conn.close()
            if st == 200:
                cookie = dict((k.lower(), v) for k, v in hdr)["set-cookie"].split(";", 1)[0]
                return cookie, json.loads(body)["csrf_token"]
            raise RuntimeError(f"login HTTP {st}: {body[:200]!r}")
        except (OSError, http.client.HTTPException) as e:
            last = e
            time.sleep(3)
    raise RuntimeError(f"login failed: {last}")


def static_paths(cookie):
    conn = connect()
    st, _, body = request(conn, "GET", "/", cookie=cookie)
    conn.close()
    assets = sorted(set(re.findall(r'(?:src|href)="(/assets/[^"]+)"', body.decode(errors="replace"))))
    return ["/"] + assets


def load(cookie, csrf, seconds, threads):
    paths = ["/api/v1/" + p for p in READS] + static_paths(cookie)
    stats = {"requests": 0, "bytes": 0, "status": {}, "errors": 0}
    lock = threading.Lock()
    deadline = time.monotonic() + seconds

    def worker(seed):
        rnd = random.Random(seed)
        conn = connect()
        while time.monotonic() < deadline:
            order = paths[:]
            rnd.shuffle(order)
            for p in order:
                if time.monotonic() >= deadline:
                    break
                try:
                    st, _, data = request(conn, "GET", p, cookie=cookie, csrf=csrf)
                    with lock:
                        stats["requests"] += 1
                        stats["bytes"] += len(data)
                        stats["status"][str(st)] = stats["status"].get(str(st), 0) + 1
                except (OSError, http.client.HTTPException):
                    with lock:
                        stats["errors"] += 1
                    conn.close()
                    conn = connect()
        conn.close()

    ts = [threading.Thread(target=worker, args=(i,)) for i in range(threads)]
    for t in ts:
        t.start()
    return ts, stats, paths


def main():
    out = pathlib.Path(sys.argv[1])
    for arm, impl in enumerate(sys.argv[2:], 1):
        def record(obj):
            obj.update(arm=arm, impl=impl, at=time.strftime("%H:%M:%S"))
            with out.open("a") as f:
                f.write(json.dumps(obj) + "\n")
            print(json.dumps(obj), flush=True)

        text = ssh(f"sh /opt/memtest/memtest.sh start {impl}")
        record({"event": "start", "output": text.strip()})

        def sample(label):
            for row in parse_samples(ssh(f"sh /opt/memtest/memtest.sh sample {label}")):
                record(row)

        time.sleep(IDLE_FIRST)
        sample("idle_1m")
        time.sleep(IDLE_SECOND)
        sample("idle_4m")

        cookie, csrf = login()
        ts, stats, paths = load(cookie, csrf, LOAD_SECONDS, LOAD_THREADS)
        time.sleep(LOAD_SECONDS / 2)
        sample("load_mid")
        for t in ts:
            t.join()
        sample("load_end")
        record({"event": "load", "seconds": LOAD_SECONDS, "threads": LOAD_THREADS,
                "paths": len(paths), **stats})
        time.sleep(POST_LOAD)
        sample("post_1m")


if __name__ == "__main__":
    main()

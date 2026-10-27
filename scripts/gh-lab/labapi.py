"""API client and load generator for the GitHub lab (standard library only).

  labapi.py login                       sign in, keep the session in $LAB_RUN/session.json
  labapi.py call METHOD PATH [JSON]     print "HTTP <code>" then the body
  labapi.py load SECONDS THREADS        read-only load; prints a JSON summary
  labapi.py wrong N                     N wrong logins; prints the status codes

Settings come from the environment: ROUTER_URL (default https://192.168.1.1:8443),
DASH_PASS and LAB_RUN. The appliance uses a self-signed certificate.
"""
import http.client
import json
import os
import pathlib
import random
import ssl
import sys
import threading
import time

URL = os.environ.get("ROUTER_URL", "https://192.168.1.1:8443")
HOST, PORT = URL.split("//", 1)[1].split(":")
PORT = int(PORT)
RUN = pathlib.Path(os.environ.get("LAB_RUN", "/tmp/minimalrouter-lab/run"))
SESSION = RUN / "session.json"
CTX = ssl._create_unverified_context()

READS = """accounting accounting/insights audit/events auth/session config devices/pauses
dns-activity dns-activity/alerts dns-activity/alerts/summary dns-activity/recent dns-activity/settings
dns-filter dns-filter/operations dns-filter/profiles firewall/activity firewall/status
firmware/local-status firmware/status gateway/events gateway/history gateway/insights gateway/recovery
gateway/settings gateway/summary health network/devices network/lan network/leases recovery/status
setup/status snapshots startup/boots system system/diagnostics system/interfaces
transactions/pending wireguard/provisioning-preview""".split()


def request(conn, method, path, body=None, cookie=None, csrf=None):
    headers = {"Origin": URL}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if cookie:
        headers["Cookie"] = cookie
    if csrf:
        headers["X-CSRF-Token"] = csrf
    conn.request(method, path, body=None if body is None else json.dumps(body), headers=headers)
    response = conn.getresponse()
    return response.status, response.getheaders(), response.read()


def connect():
    return http.client.HTTPSConnection(HOST, PORT, context=CTX, timeout=60)


def login(password=None):
    password = password or os.environ["DASH_PASS"]
    # The router allows 5 logins per minute per source, successful ones included;
    # a test that signs in often waits out the window instead of failing.
    for _ in range(8):
        conn = connect()
        status, headers, body = request(conn, "POST", "/api/v1/auth/login", {"password": password})
        conn.close()
        if status != 429:
            break
        time.sleep(15)
    if status != 200:
        raise SystemExit(f"login failed: HTTP {status} {body[:200]!r}")
    cookie = dict((k.lower(), v) for k, v in headers)["set-cookie"].split(";", 1)[0]
    session = {"cookie": cookie, "csrf": json.loads(body)["csrf_token"]}
    RUN.mkdir(parents=True, exist_ok=True)
    SESSION.write_text(json.dumps(session))
    return session


def session():
    if SESSION.exists():
        saved = json.loads(SESSION.read_text())
        conn = connect()
        status, _, _ = request(conn, "GET", "/api/v1/auth/session", cookie=saved["cookie"])
        conn.close()
        if status == 200:
            return saved
    return login()


def static_paths(saved):
    conn = connect()
    status, _, body = request(conn, "GET", "/", cookie=saved["cookie"])
    conn.close()
    import re

    assets = sorted(set(re.findall(r'(?:src|href)="(/assets/[^"]+)"', body.decode(errors="replace"))))
    return ["/"] + assets


def load(seconds, threads):
    saved = session()
    paths = ["/api/v1/" + p for p in READS] + static_paths(saved)
    stats = {"requests": 0, "bytes": 0, "status": {}, "errors": 0}
    lock = threading.Lock()
    deadline = time.monotonic() + seconds

    def worker(seed):
        rnd = random.Random(seed)
        conn = connect()
        while time.monotonic() < deadline:
            order = paths[:]
            rnd.shuffle(order)
            for path in order:
                if time.monotonic() >= deadline:
                    break
                try:
                    status, _, data = request(conn, "GET", path, cookie=saved["cookie"], csrf=saved["csrf"])
                    with lock:
                        stats["requests"] += 1
                        stats["bytes"] += len(data)
                        stats["status"][str(status)] = stats["status"].get(str(status), 0) + 1
                except (OSError, http.client.HTTPException):
                    with lock:
                        stats["errors"] += 1
                    conn.close()
                    conn = connect()
        conn.close()

    workers = [threading.Thread(target=worker, args=(i,)) for i in range(threads)]
    for w in workers:
        w.start()
    for w in workers:
        w.join()
    stats["paths"] = len(paths)
    print(json.dumps(stats))


def main():
    cmd = sys.argv[1]
    if cmd == "login":
        login()
        print("logged in")
    elif cmd == "call":
        saved = session()
        body = json.loads(sys.argv[4]) if len(sys.argv) > 4 and sys.argv[4] else None
        conn = connect()
        status, _, data = request(conn, sys.argv[2].upper(), "/api/v1/" + sys.argv[3].lstrip("/"), body, saved["cookie"], saved["csrf"])
        conn.close()
        print(f"HTTP {status}")
        print(data.decode(errors="replace"))
    elif cmd == "reads":
        print("\n".join(READS))
    elif cmd == "load":
        load(float(sys.argv[2]), int(sys.argv[3]))
    elif cmd == "wrong":
        codes = []
        for i in range(int(sys.argv[2])):
            conn = connect()
            status, _, _ = request(conn, "POST", "/api/v1/auth/login", {"password": f"definitely-wrong-{i}"})
            conn.close()
            codes.append(status)
        print(" ".join(str(c) for c in codes))
    else:
        raise SystemExit(__doc__)


if __name__ == "__main__":
    main()

"""Random-input fuzzing of the management API for the GitHub lab.

  fuzz.py ITERATIONS [SEED]

Phase 1 mutates the live configuration (PUT /config) with hostile values. Phase
2 sends hostile JSON bodies to every mutating endpoint in api/openapi.yaml,
except the ones that change credentials, restart the machine or install
firmware. A mutation that the router accepts is undone straight away.

Passing means: no 5xx response, no dropped connection, the health endpoint
still answers, and the original configuration is back at the end. The seed is
printed so any failure can be replayed.
"""
import copy
import http.client
import json
import os
import random
import re
import ssl
import sys
import time

URL = os.environ.get("ROUTER_URL", "https://192.168.1.1:8443")
HOST, PORT = URL.split("//", 1)[1].split(":")
PORT = int(PORT)
PASSWORD = os.environ["DASH_PASS"]
CTX = ssl._create_unverified_context()

# Paths that would cut off the lab, change credentials or leave the machine.
SKIP_FIELDS = ("lan", "trusted_networks", "wifi", "wg_client")
SKIP_ENDPOINTS = re.compile(r"firmware|update|reboot|factory|restore|password|totp|logout|login|import|backup|recovery|shutdown")

HOSTILE = [
    None, "", " ", "A", "A" * 255, "A" * 4096, "A" * 70000, -1, 0, 1, 65535, 65536, 2**31, 2**63, -(2**63), 1e308, 1e-308,
    True, False, [], {}, ["x"] * 300, [None], [[[[]]]], {"a": {"b": {"c": {}}}},
    "../../etc/passwd", "/etc/shadow", "\u0000", "a\u0000b", "\ud800", "‮‭", "\U0001f600" * 200, "\n", "a\r\nX-Injected: 1",
    "; rm -rf /", "$(id)", "`id`", "' OR 1=1 --", "<script>alert(1)</script>", "%s%s%s%n", "{{7*7}}", "${jndi:ldap://x/a}",
    "192.168.1.1/33", "999.999.999.999", "::ffff:1.2.3.4", "fe80::1%eth0", "0.0.0.0/0", "255.255.255.255", "-1.-1.-1.-1",
    "example.com\n.evil", "exa mple.com", "a" * 63 + "." + "b" * 64, "1e400", "NaN", "Infinity", "0x41", "0b11", "١٢٣",
]


def connect():
    return http.client.HTTPSConnection(HOST, PORT, context=CTX, timeout=60)


def request(method, path, body=None, cookie=None, csrf=None, raw=None):
    headers = {"Origin": URL}
    data = raw
    if body is not None:
        data = json.dumps(body)
    if data is not None:
        headers["Content-Type"] = "application/json"
    if cookie:
        headers["Cookie"] = cookie
    if csrf:
        headers["X-CSRF-Token"] = csrf
    conn = connect()
    try:
        conn.request(method, path, body=data, headers=headers)
        response = conn.getresponse()
        return response.status, response.getheaders(), response.read()
    finally:
        conn.close()


def login():
    for _ in range(8):
        status, headers, body = request("POST", "/api/v1/auth/login", {"password": PASSWORD})
        if status != 429:
            break
        time.sleep(15)
    if status != 200:
        raise SystemExit(f"login failed: HTTP {status}")
    cookie = dict((k.lower(), v) for k, v in headers)["set-cookie"].split(";", 1)[0]
    return cookie, json.loads(body)["csrf_token"]


def walk(node, path=()):
    yield path, node
    if isinstance(node, dict):
        for key, value in node.items():
            yield from walk(value, path + (key,))
    elif isinstance(node, list):
        for index, value in enumerate(node):
            yield from walk(value, path + (index,))


def set_path(root, path, value):
    cursor = root
    for step in path[:-1]:
        cursor = cursor[step]
    cursor[path[-1]] = value


def drop_path(root, path):
    cursor = root
    for step in path[:-1]:
        cursor = cursor[step]
    if isinstance(cursor, dict):
        cursor.pop(path[-1], None)
    elif isinstance(cursor, list) and path[-1] < len(cursor):
        cursor.pop(path[-1])


def mutate(cfg, rnd):
    out = copy.deepcopy(cfg)
    candidates = [p for p, _ in walk(out) if p and p[0] not in SKIP_FIELDS and p[0] not in ("revision", "updated_at")]
    for _ in range(rnd.choice([1, 1, 1, 2, 3])):
        path = rnd.choice(candidates)
        kind = rnd.random()
        try:
            if kind < 0.6:
                set_path(out, path, rnd.choice(HOSTILE))
            elif kind < 0.75:
                drop_path(out, path)
            elif kind < 0.9:
                parent = out
                for step in path[:-1]:
                    parent = parent[step]
                if isinstance(parent, dict):
                    parent["unexpected_" + str(rnd.randint(0, 9))] = rnd.choice(HOSTILE)
            else:
                set_path(out, path, [rnd.choice(HOSTILE) for _ in range(rnd.randint(1, 5))])
        except (KeyError, IndexError, TypeError):
            pass
    return out


def endpoints():
    text = open(os.path.join(os.path.dirname(__file__), "..", "..", "api", "openapi.yaml"), encoding="utf-8").read()
    found, current = [], None
    for line in text.splitlines():
        m = re.match(r"^  (/[^:\s]*):\s*$", line)
        if m:
            current = m.group(1)
            continue
        m = re.match(r"^    (post|put|patch|delete):\s*$", line)
        if m and current:
            found.append((m.group(1).upper(), re.sub(r"\{[^}]+\}", "x", current)))
    return [(m, p) for m, p in found if not SKIP_ENDPOINTS.search(p)]


def main():
    iterations = int(sys.argv[1])
    seed = int(sys.argv[2]) if len(sys.argv) > 2 else int(time.time())
    rnd = random.Random(seed)
    print(f"fuzz seed={seed} iterations={iterations}")
    cookie, csrf = login()
    status, _, body = request("GET", "/api/v1/config", cookie=cookie)
    original = json.loads(body)
    counts, bad = {}, []

    def note(label, status):
        counts[status] = counts.get(status, 0) + 1
        if status >= 500 or status == 0:
            bad.append(f"{label} -> HTTP {status}")

    def put(cfg):
        return request("PUT", "/api/v1/config", cfg, cookie, csrf)

    def confirm_pending():
        status, _, body = request("GET", "/api/v1/transactions/pending", cookie=cookie)
        if status == 200:
            try:
                tx = json.loads(body)
            except ValueError:
                return
            if isinstance(tx, dict) and tx.get("id"):
                request("POST", f"/api/v1/transactions/{tx['id']}/confirm", {}, cookie, csrf)

    def restore():
        # A change accepted by a hostile request on another endpoint can leave a
        # provisional transaction open, which blocks the next configuration write.
        confirm_pending()
        status, _, body = put(original)
        if status == 202:
            pending = json.loads(request("GET", "/api/v1/transactions/pending", cookie=cookie)[2])
            request("POST", f"/api/v1/transactions/{pending['id']}/confirm", {}, cookie, csrf)
        return status, body

    # Phase 1: hostile configurations.
    for i in range(iterations):
        cfg = mutate(original, rnd)
        try:
            status, _, body = put(cfg)
        except (OSError, http.client.HTTPException) as err:
            note(f"config #{i} (connection {err.__class__.__name__})", 0)
            time.sleep(2)
            cookie, csrf = login()
            continue
        note(f"config #{i} seed={seed}", status)
        if status in (200, 202):
            if status == 202:
                pending = json.loads(request("GET", "/api/v1/transactions/pending", cookie=cookie)[2])
                request("POST", f"/api/v1/transactions/{pending['id']}/confirm", {}, cookie, csrf)
            restore()
        if i % 50 == 49:
            if request("GET", "/api/v1/health", cookie=cookie)[0] != 200:
                bad.append(f"health not 200 after {i + 1} iterations")

    # Phase 2: hostile bodies on every other mutating endpoint.
    for method, path in endpoints():
        for _ in range(max(3, iterations // 40)):
            body = rnd.choice(HOSTILE) if rnd.random() < 0.5 else mutate(original, rnd)
            raw = None if rnd.random() < 0.9 else rnd.choice(["{", "[1,2", '{"a":', "\x00\x01", "{" * 5000])
            try:
                status, _, _ = request(method, "/api/v1" + path, None if raw else body, cookie, csrf, raw)
            except (OSError, http.client.HTTPException) as err:
                note(f"{method} {path} ({err.__class__.__name__})", 0)
                time.sleep(2)
                cookie, csrf = login()
                continue
            note(f"{method} {path} seed={seed}", status)

    # Whatever was accepted along the way, the configuration must be the original.
    status, restore_body = restore()
    _, _, body = request("GET", "/api/v1/config", cookie=cookie)
    now = json.loads(body)
    for key in ("revision", "updated_at"):
        original.pop(key, None)
        now.pop(key, None)
    if now != original:
        bad.append(f"the configuration differs from the original after the fuzz run (final restore: HTTP {status})")
        if status not in (200, 202):
            bad.append(f"final restore answered {restore_body[:160]!r}")
        before = dict(walk(original))
        current = dict(walk(now))
        shown = 0
        for path in sorted(set(before) | set(current)):
            if before.get(path) != current.get(path) and not isinstance(before.get(path, current.get(path)), (dict, list)):
                bad.append(f"  at {'/'.join(map(str, path))}: {str(before.get(path, '<absent>'))[:60]} -> {str(current.get(path, '<absent>'))[:60]}")
                shown += 1
                if shown == 6:
                    break
    if request("GET", "/api/v1/health", cookie=cookie)[0] != 200:
        bad.append("health is not 200 at the end")

    print("status counts:", json.dumps(counts, sort_keys=True))
    if bad:
        print(f"{len(bad)} problems, first {min(len(bad), 8)}:")
        for line in bad[:8]:
            print("  " + line)
        sys.exit(1)
    print(f"no 5xx, no dropped connection, configuration restored ({sum(counts.values())} requests)")


if __name__ == "__main__":
    main()

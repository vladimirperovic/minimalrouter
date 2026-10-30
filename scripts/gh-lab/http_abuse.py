"""Hostile HTTP against the management port, for the GitHub lab.

Each attack is followed by a check that the server still answers a normal
request. Prints "OK name" or "FAIL name: reason" per attack and exits non-zero
if any attack failed. The router must refuse or close, never crash, hang or
answer 5xx.
"""
import os
import socket
import ssl
import sys
import time

HOST = os.environ.get("ROUTER_HOST", "192.168.1.1")
PORT = int(os.environ.get("ROUTER_PORT", "8443"))
CTX = ssl.create_default_context()
CTX.check_hostname = False
CTX.verify_mode = ssl.CERT_NONE


def tls(timeout=10):
    raw = socket.create_connection((HOST, PORT), timeout=timeout)
    return CTX.wrap_socket(raw, server_hostname=HOST)


def read_all(sock, limit=1 << 20, wait=5):
    sock.settimeout(wait)
    data = b""
    try:
        while len(data) < limit:
            chunk = sock.recv(65536)
            if not chunk:
                break
            data += chunk
    except (socket.timeout, ssl.SSLError, ConnectionError, OSError):
        pass
    return data


def statuses(data):
    return [int(line.split()[1]) for line in data.split(b"\r\n") if line.startswith(b"HTTP/1.") and len(line.split()) > 1]


def healthy():
    for _ in range(15):
        try:
            sock = tls()
            sock.sendall(f"GET /api/v1/setup/status HTTP/1.1\r\nHost: {HOST}\r\nOrigin: https://{HOST}:{PORT}\r\nConnection: close\r\n\r\n".encode())
            if 200 in statuses(read_all(sock)):
                return True
        except (OSError, ssl.SSLError):
            pass
        time.sleep(1)
    return False


def raw_request(payload, wait=5):
    try:
        sock = tls()
        sock.sendall(payload)
        data = read_all(sock, wait=wait)
        sock.close()
        return data
    except (OSError, ssl.SSLError) as err:
        return b"ERR " + str(err).encode()


RESULTS = []


def attack(name, fn):
    try:
        reason = fn()
    except Exception as err:  # an attack script bug must not look like a pass
        reason = f"attack raised {err!r}"
    if reason is None and not healthy():
        reason = "the server did not answer a normal request afterwards"
    RESULTS.append((name, reason))
    print(("OK   " if reason is None else "FAIL ") + name + ("" if reason is None else f": {reason}"), flush=True)


def refused_or_closed(data, allowed=(400, 401, 403, 404, 405, 408, 411, 413, 414, 421, 422, 426, 429, 431, 501, 505)):
    codes = statuses(data)
    if any(c >= 500 and c not in (501, 505) for c in codes):
        return f"server answered {codes}"
    if codes and not set(codes) <= set(allowed) | {200}:
        return f"unexpected statuses {codes}"
    return None


def slowloris():
    socks = []
    for _ in range(160):
        try:
            sock = tls(timeout=5)
            sock.sendall(f"GET /api/v1/setup/status HTTP/1.1\r\nHost: {HOST}\r\nX-Slow: ".encode())
            socks.append(sock)
        except (OSError, ssl.SSLError):
            break
    time.sleep(20)
    for sock in socks:
        try:
            sock.close()
        except OSError:
            pass
    return None if len(socks) > 20 else f"only {len(socks)} connections could be opened"


def smuggling():
    data = raw_request(
        f"POST /api/v1/auth/login HTTP/1.1\r\nHost: {HOST}\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"
        f"GET /api/v1/config HTTP/1.1\r\nHost: {HOST}\r\n\r\n".encode()
    )
    codes = statuses(data)
    if 200 in codes and b"revision" in data:
        return "an unauthenticated configuration read was served through request smuggling"
    if codes and codes[0] != 400 and b"onnection: close" not in data and len(codes) > 1:
        return f"conflicting Content-Length and Transfer-Encoding were not rejected ({codes})"
    return refused_or_closed(data)


def header_bomb():
    return refused_or_closed(raw_request(f"GET / HTTP/1.1\r\nHost: {HOST}\r\nX-Big: ".encode() + b"A" * 300_000 + b"\r\n\r\n"))


def many_headers():
    headers = "".join(f"X-H{i}: v\r\n" for i in range(6000))
    return refused_or_closed(raw_request(f"GET / HTTP/1.1\r\nHost: {HOST}\r\n{headers}\r\n".encode()))


def bad_request_lines():
    for line in [b"GET /\x00 HTTP/1.1", b"GET http://evil.example/ HTTP/1.1", b"GET /%zz HTTP/1.1", b"GET / HTTP/9.9", b"GET /\r\n",
                 b"G\x01T / HTTP/1.1", b"GET  /  HTTP/1.1", b"\r\n\r\nGET / HTTP/1.1", b"GET /" + b"a" * 70000 + b" HTTP/1.1", b"CONNECT x:443 HTTP/1.1",
                 b"TRACE / HTTP/1.1", b"GET / HTTP/1.1\r\nHost: a\r\nHost: b"]:
        data = raw_request(line + f"\r\nHost: {HOST}\r\n\r\n".encode() if b"Host" not in line else line + b"\r\n\r\n")
        reason = refused_or_closed(data)
        if reason:
            return f"{line[:40]!r}: {reason}"
    return None


def bad_content_length():
    for value in ["999999999999999999999", "-1", "1, 2", "abc", "+5", " 5"]:
        data = raw_request(f"POST /api/v1/auth/login HTTP/1.1\r\nHost: {HOST}\r\nContent-Length: {value}\r\nContent-Type: application/json\r\n\r\n{{}}".encode(), wait=3)
        reason = refused_or_closed(data)
        if reason:
            return f"Content-Length {value!r}: {reason}"
    return None


def expect_continue():
    data = raw_request(f"POST /api/v1/auth/login HTTP/1.1\r\nHost: {HOST}\r\nContent-Length: 104857600\r\nExpect: 100-continue\r\nContent-Type: application/json\r\n\r\n".encode(), wait=3)
    if 100 in statuses(data):
        return "the server invited a 100 MB body on an unauthenticated login"
    return refused_or_closed(data)


def pipelining():
    request = f"GET /api/v1/setup/status HTTP/1.1\r\nHost: {HOST}\r\nOrigin: https://{HOST}:{PORT}\r\n\r\n"
    data = raw_request((request * 200).encode(), wait=8)
    codes = statuses(data)
    if not codes or any(c >= 500 for c in codes):
        return f"pipelined requests gave {codes[:5]}"
    return None


def slow_header():
    sock = tls()
    sock.sendall(b"GET / HTTP/1.1\r\n")
    started = time.time()
    sock.settimeout(40)
    try:
        for _ in range(35):
            sock.sendall(b"X: y\r\n")
            time.sleep(1)
    except (OSError, ssl.SSLError):
        pass
    elapsed = time.time() - started
    sock.close()
    return None if elapsed < 30 else "a header trickle kept the connection open for more than 30 s"


def plain_http_on_tls_port():
    raw = socket.create_connection((HOST, PORT), timeout=5)
    raw.sendall(b"GET / HTTP/1.1\r\nHost: x\r\n\r\n")
    data = read_all(raw, wait=3)
    raw.close()
    if b"revision" in data or b"200 OK" in data:
        return "plain HTTP was served on the TLS port"
    return None


def tls_garbage():
    for payload in [b"\x16\x03\x01\x00\x05\x01\x00\x00\x01\x00", os.urandom(4096), b"\x16\x03\x03\xff\xff" + os.urandom(100), b"\x80\x80\x01\x03\x01"]:
        raw = socket.create_connection((HOST, PORT), timeout=5)
        raw.sendall(payload)
        read_all(raw, wait=2)
        raw.close()
    return None


def connection_flood():
    socks = []
    for _ in range(400):
        try:
            socks.append(socket.create_connection((HOST, PORT), timeout=3))
        except OSError:
            break
    time.sleep(3)
    for sock in socks:
        sock.close()
    return None


for name, fn in [
    ("slowloris: 160 half-open requests held for 20 s", slowloris),
    ("request smuggling (Content-Length and Transfer-Encoding)", smuggling),
    ("300 KB header", header_bomb),
    ("6000 headers", many_headers),
    ("malformed request lines", bad_request_lines),
    ("malformed Content-Length values", bad_content_length),
    ("Expect: 100-continue with a 100 MB body", expect_continue),
    ("200 pipelined requests", pipelining),
    ("header trickle (slow client)", slow_header),
    ("plain HTTP on the TLS port", plain_http_on_tls_port),
    ("TLS handshake garbage", tls_garbage),
    ("400 raw connections at once", connection_flood),
]:
    attack(name, fn)

failed = [r for r in RESULTS if r[1]]
print(f"{len(RESULTS) - len(failed)} of {len(RESULTS)} attacks refused cleanly")
sys.exit(1 if failed else 0)

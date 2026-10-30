"""Uncached DNS load and exact wire-response probes for the GitHub LAN client."""
import ipaddress
import json
import secrets
import socket
import struct
import sys
import time

ZONE = "flood.lab.invalid"


def question(name, ident):
    labels = name.rstrip(".").encode("ascii").split(b".")
    if any(not label or len(label) > 63 for label in labels):
        raise ValueError("invalid DNS name")
    return struct.pack("!6H", ident, 0x100, 1, 0, 0, 0) + b"".join(bytes([len(x)]) + x for x in labels) + b"\0\0\1\0\1"


def read_name(data, offset):
    labels, seen, end = [], set(), None
    while True:
        if offset in seen or offset >= len(data):
            raise ValueError("truncated or looping DNS name")
        seen.add(offset)
        length = data[offset]
        offset += 1
        if length & 0xc0 == 0xc0:
            if offset >= len(data):
                raise ValueError("truncated DNS pointer")
            end = end or offset + 1
            offset = ((length & 0x3f) << 8) | data[offset]
        elif length == 0:
            return b".".join(labels).decode("ascii").lower(), end or offset
        elif length > 63 or offset + length > len(data):
            raise ValueError("invalid DNS label")
        else:
            labels.append(data[offset:offset + length])
            offset += length


def answer(data, query, expected):
    if len(data) < 12:
        raise ValueError("empty/truncated DNS response")
    ident, flags, qd, an, _, _ = struct.unpack_from("!6H", data)
    if ident != struct.unpack_from("!H", query)[0] or flags & 0x8000 == 0 or flags & 0x020f or qd != 1:
        raise ValueError(f"DNS response mismatch, truncation or non-NOERROR rcode: flags={flags:#x}")
    name, offset = read_name(data, 12)
    expected_name, _ = read_name(query, 12)
    if name != expected_name or data[offset:offset + 4] != b"\0\1\0\1":
        raise ValueError("DNS question mismatch")
    offset += 4
    addresses = []
    for _ in range(an):
        owner, offset = read_name(data, offset)
        if offset + 10 > len(data):
            raise ValueError("truncated DNS answer")
        kind, cls, _, length = struct.unpack_from("!HHIH", data, offset)
        offset += 10
        if offset + length > len(data):
            raise ValueError("truncated DNS rdata")
        if owner == name and kind == 1 and cls == 1 and length == 4:
            addresses.append(str(ipaddress.IPv4Address(data[offset:offset + length])))
        offset += length
    if expected == "public":
        if not addresses or not all(ipaddress.IPv4Address(ip).is_global for ip in addresses):
            raise ValueError(f"DNS did not return public A addresses: {addresses!r}")
    elif addresses != [expected]:
        raise ValueError(f"DNS A answer {addresses!r}, expected exactly {[expected]!r}")
    return addresses


def receive(sock, length):
    data = b""
    while len(data) < length:
        chunk = sock.recv(length - len(data))
        if not chunk:
            raise ValueError("truncated DNS TCP response")
        data += chunk
    return data


def probe(target, name, expected, transport):
    query = question(name, secrets.randbelow(65536))
    started = time.monotonic()
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM if transport == "tcp" else socket.SOCK_DGRAM) as sock:
        sock.settimeout(5)
        sock.connect((target, 53))
        if transport == "tcp":
            sock.sendall(struct.pack("!H", len(query)) + query)
            raw = receive(sock, struct.unpack("!H", receive(sock, 2))[0])
        else:
            sock.send(query)
            raw = sock.recv(65535)
    addresses = answer(raw, query, expected)
    return {"target": target, "name": name, "transport": transport, "addresses": addresses,
            "seconds": round(time.monotonic() - started, 4)}


def flood(target, duration=12):
    # Unique names, no pacing, same twelve-second saturation as the old test.
    # The provider answers this reserved zone locally: no flood reaches public DNS.
    started, count = time.monotonic(), 0
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        while time.monotonic() - started < duration:
            name = f"q{count:012x}.{ZONE}"
            sock.sendto(question(name, count % 65536), (target, 53))
            count += 1
    elapsed = time.monotonic() - started
    if count == 0:
        raise RuntimeError("DNS flood sent no traffic")
    return {"queries_sent": count, "seconds": round(elapsed, 3), "queries_per_second": round(count / elapsed)}


if __name__ == "__main__":
    if sys.argv[1] == "probe":
        print(json.dumps(probe(*sys.argv[2:6])))
    elif sys.argv[1] == "flood":
        print(json.dumps(flood(sys.argv[2])))
    else:
        raise SystemExit("usage: dns_stress.py probe IP NAME EXPECTED_IP udp|tcp | flood IP")

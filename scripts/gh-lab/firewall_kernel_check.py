"""Linux kernel regression in a fresh, root-owned unshare --net namespace only.

Proves the old text hash changes when an unchanged table is recreated, while
the strict observer preserves policy equality and rejects actual drift.
"""
import json
import os
import pathlib
import socket
import subprocess

import firewall_observer as observer

MAIN = """table inet minimalrouter {
 counter mr_revision_7 { }
 counter observed { }
 chain input { type filter hook input priority 0; policy drop;
  iifname "lo" counter name observed accept
  drop
 }
 chain forward { type filter hook forward priority 0; policy drop; }
 chain output { type filter hook output priority 0; policy drop; oifname "lo" accept; }
}
"""
PAUSE = """table inet minimalrouter_pause {
 set blocked_ipv4 { type ipv4_addr; flags timeout; }
 chain forward { type filter hook forward priority -10; policy accept;
  ip saddr @blocked_ipv4 drop
 }
}
"""


def nft(*args, data=None):
    return subprocess.check_output(["nft", *args], input=data, text=True, stderr=subprocess.STDOUT)


def snapshot():
    return json.loads(nft("-s", "-j", "list", "ruleset"))


def main():
    if os.geteuid() != 0 or pathlib.Path("/proc/self/ns/net").readlink() == pathlib.Path("/proc/1/ns/net").readlink():
        raise RuntimeError("requires root inside a separate disposable network namespace")
    if any("table" in item for item in snapshot()["nftables"]):
        raise RuntimeError("requires a new empty network namespace")
    subprocess.check_call(["ip", "link", "set", "lo", "up"])
    nft("-f", "-", data=MAIN + PAUSE)
    before, old_text = snapshot(), nft("-s", "list", "ruleset")
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sender:
        sender.sendto(b"observer counter regression", ("127.0.0.1", 9))
    observer.compare(before, snapshot())  # ignore only runtime counter readings
    nft("-f", "-", data="delete table inet minimalrouter\n" + MAIN)
    after = snapshot()
    if old_text == nft("-s", "list", "ruleset"):
        raise AssertionError("did not reproduce the old table-order hash failure")
    observer.compare(before, after)
    print("PASS: kernel table recreation changes old text hash; full policy and hook priorities remain identical")
    for bad in [MAIN.replace("policy drop", "policy accept", 1),
                MAIN.replace('iifname "lo" counter name observed accept\n  drop', 'drop\n  iifname "lo" counter name observed accept'),
                MAIN.replace("  drop\n", "")]:
        nft("-f", "-", data="delete table inet minimalrouter\n" + bad)
        try:
            observer.compare(before, snapshot())
        except ValueError:
            pass
        else:
            raise AssertionError("real policy or rule-order drift was accepted")
    print("PASS: changed default policy, reordered rules and deleted rules all fail")


if __name__ == "__main__":
    main()

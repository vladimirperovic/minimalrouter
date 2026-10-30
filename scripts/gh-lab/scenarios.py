"""Strict observers for the disposable GitHub QEMU chaos scenarios.

No transport exception, missing HTTP response or DNS error is a passing result.
Only the CLI touches the lab; importing this module is safe for offline tests.
"""
import copy
import json
import pathlib
import subprocess
import sys
import time

import contracts
import labapi

HERE = pathlib.Path(__file__).resolve().parent
PROVIDER = "11.255.0.2"
ZONE = "flood.lab.invalid"
LOCAL = "local." + ZONE


def command(*args, timeout=40):
    try:
        return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=timeout).strip()
    except subprocess.CalledProcessError as error:
        print(error.output[-5000:])
        raise


class Lab:
    def __init__(self):
        self.saved = labapi.session()

    def api(self, method, path, body=None, timeout=60):
        conn = labapi.connect(timeout)
        try:
            status, _, raw = labapi.request(conn, method, "/api/v1/" + path, body,
                                           self.saved["cookie"], self.saved["csrf"])
            if type(status) is not int or not 100 <= status <= 599:
                raise RuntimeError("missing or malformed HTTP status")
            return status, json.loads(raw)
        finally:
            conn.close()

    def get(self, path):
        status, body = self.api("GET", path)
        if status != 200:
            raise RuntimeError(f"GET {path}: HTTP {status}, expected 200")
        return body

    def shell(self, script):
        return command("bash", "-c", '. "$1/lib.sh"; rsh "$2"', "lab", str(HERE), script)

    def identity(self, name):
        if name not in ("routerd", "router-applyd", "dnsmasq", "pppd"):
            raise ValueError("invalid daemon name")
        identity = command("bash", "-c", '. "$1/lib.sh"; router_identity "$2"',
                           "lab", str(HERE), name)
        fields = identity.split()
        if len(fields) != 2 or any(not field.isascii() or not field.isdecimal() for field in fields) or int(fields[0]) <= 1:
            raise RuntimeError("missing or malformed process identity")
        return tuple(fields)

    def process_state(self, action, identity):
        if action not in ("rss", "STOP", "CONT") or len(identity) != 2 or any(
                not field.isascii() or not field.isdecimal() for field in identity) or int(identity[0]) <= 1:
            raise ValueError("invalid process observation")
        return command("bash", "-c", '. "$1/lib.sh"; rsh sh -s "$2" "$3" "$4" < "$1/process-state.sh"',
                       "lab", str(HERE), action, *identity)

    def memory(self, name, identity):
        if self.identity(name) != identity:
            raise RuntimeError(f"{name} changed before memory observation")
        rss = self.process_state("rss", identity)
        if not rss.isascii() or not rss.isdecimal():
            raise RuntimeError("missing or malformed process RSS")
        if self.identity(name) != identity:
            raise RuntimeError(f"{name} changed during memory observation")
        return int(rss)

    def client(self, *args):
        return command("sudo", "ip", "netns", "exec", "client", *args)

    def reboot(self):
        # ssh may disconnect after the reboot request has been acknowledged.
        self.shell("nohup sh -c 'sleep 2; reboot' >/dev/null 2>&1 </dev/null &")
        time.sleep(25)
        end = time.monotonic() + 600
        while True:
            try:
                self.shell("test -s /run/minimalrouter/routerd.ready")
                self.saved = labapi.session()
                require_ready(self)
                return
            except (OSError, ValueError, RuntimeError, subprocess.SubprocessError):
                if time.monotonic() >= end:
                    raise RuntimeError("helper timeout: reboot reconciliation did not recover")
                time.sleep(2)


def require_ready(lab):
    engine = lab.get("recovery/status")["engine"]
    if engine["apply_in_progress"] or engine["recovery_required"]:
        raise RuntimeError(f"engine is not ready: {engine}")
    if lab.get("transactions/pending")["pending"]:
        raise RuntimeError("a configuration transaction is still pending")
    lab.get("health")


def changed_paths(before, after, path="config"):
    """Report locations only: never log configuration secrets or field values."""
    if type(before) is not type(after):
        return [path]
    if isinstance(before, dict):
        result = []
        for key in sorted(before.keys() | after.keys()):
            child = f"{path}.{key}"
            if key not in before or key not in after:
                result.append(child)
            else:
                result.extend(changed_paths(before[key], after[key], child))
        return result
    if isinstance(before, list):
        if len(before) != len(after):
            return [path]
        return [p for i, (left, right) in enumerate(zip(before, after))
                for p in changed_paths(left, right, f"{path}[{i}]")]
    return [] if before == after else [path]


def require_unchanged(before, after):
    paths = changed_paths(before, after)
    if paths:
        raise RuntimeError("canonical configuration changed: " + ", ".join(paths))


def require_timeout(status, body, elapsed):
    if status != 422 or body.get("tx", {}).get("state") != "RecoveryRequired":
        raise RuntimeError(f"frozen helper: HTTP {status}, expected 422/RecoveryRequired")
    if "privileged apply outcome remained unknown" not in body.get("error", ""):
        raise RuntimeError("frozen helper: response does not identify an ambiguous helper outcome")
    if not 115 <= elapsed <= 155:
        raise RuntimeError(f"frozen helper: response took {elapsed:.1f}s, expected the 120s IPC deadline")


def frozen_helper(lab):
    require_ready(lab)
    before = lab.get("config")
    candidate = copy.deepcopy(before)
    candidate["system"]["hostname"] = "frozen-helper"
    identity = lab.identity("router-applyd")
    stopped = False
    try:
        # Mark first so even an interrupted SSH acknowledgement triggers SIGCONT.
        stopped = True
        lab.process_state("STOP", identity)
        started = time.monotonic()
        status, body = lab.api("PUT", "config", candidate, timeout=150)
        elapsed = time.monotonic() - started
        require_timeout(status, body, elapsed)
        require_unchanged(before, lab.get("config"))
        engine = lab.get("recovery/status")["engine"]
        if engine["apply_in_progress"] or not engine["recovery_required"]:
            raise RuntimeError("ambiguous helper outcome did not fail closed")
        # A second mutation must be refused while recovery is required.
        blocked, detail = lab.api("PUT", "config", before)
        if blocked != 422 or detail.get("tx", {}).get("state") != "RecoveryRequired":
            raise RuntimeError("engine accepted another change before reconciliation")
        print(f"frozen helper: HTTP {status}/RecoveryRequired after {elapsed:.1f}s; further mutation refused")
    finally:
        if stopped:
            try:
                lab.process_state("CONT", identity)
            finally:
                # The old request may still be queued in the helper. Reconcile
                # even if its original process vanished before SIGCONT.
                lab.reboot()
                require_unchanged(before, lab.get("config"))
                require_ready(lab)
    print("frozen helper: SIGCONT and reboot reconciliation complete; canonical state unchanged, no pending change")


def committed(lab, candidate):
    status, body = lab.api("PUT", "config", candidate)
    if status != 200 or body.get("state") != "Committed":
        raise RuntimeError(f"DNS fixture configuration: HTTP {status}, expected 200/Committed")
    require_ready(lab)


def dns_probe(lab, target, name, expected, transport):
    output = lab.client("python3", str(HERE / "dns_stress.py"), "probe", target, name, expected, transport)
    print(output)


def dns_checks(lab, tag):
    # Fresh names and TTL=0 ensure the forwarding probes cannot pass from cache.
    for transport in ("udp", "tcp"):
        dns_probe(lab, "192.168.1.1", LOCAL, "192.168.1.1", transport)
        dns_probe(lab, "192.168.1.1", f"{tag}-{transport}.{ZONE}", PROVIDER, transport)
        dns_probe(lab, "192.168.1.1", "example.com", "public", transport)


def dns_flood(lab):
    require_ready(lab)
    before = lab.get("config")
    candidate = copy.deepcopy(before)
    if candidate["dhcp"]["dns_enabled"]:
        raise RuntimeError("DNS flood fixture requires the plain-DNS lab profile")
    candidate["dhcp"]["dns_servers"] = [PROVIDER]
    candidate["dns"]["records"] = (candidate["dns"].get("records") or []) + [{"name": LOCAL, "ip": "192.168.1.1"}]
    configured = False
    try:
        configured = True  # restore even if the observer loses the PUT response
        committed(lab, candidate)
        dns_probe(lab, PROVIDER, "provider-baseline." + ZONE, PROVIDER, "udp")
        dns_checks(lab, "before")
        resolver = lab.identity("dnsmasq")
        routerd = lab.identity("routerd")
        memory = lab.memory("routerd", routerd)
        print(lab.client("python3", str(HERE / "dns_stress.py"), "flood", "192.168.1.1"))
        time.sleep(5)  # unchanged recovery budget from the original scenario
        dns_checks(lab, "after")
        if lab.identity("dnsmasq") != resolver:
            raise RuntimeError("dnsmasq restarted during the flood")
        if lab.identity("routerd") != routerd:
            raise RuntimeError("routerd restarted during the flood")
        require_ready(lab)
        after = lab.memory("routerd", routerd)
        if after - memory >= 30 * 1024:
            raise RuntimeError("routerd grew by at least 30 MiB during the DNS flood")
        print(f"DNS flood: local and uncached forwarded UDP/TCP answers correct; no resolver restart; routerd RSS {memory} -> {after} KiB")
    except Exception:
        # Preserve the exact DNS response/transport error and independent provider
        # control before restoring the fixture. Never turn a failed probe green.
        try:
            dns_probe(lab, PROVIDER, "provider-failure-control." + ZONE, PROVIDER, "udp")
        except Exception as error:
            print(f"provider control also failed: {error}")
        print(lab.shell("date; pidof dnsmasq; ss -lnut; cat /proc/net/snmp; tail -n 30 /var/log/messages"))
        raise
    finally:
        if configured:
            current = lab.get("config")
            current["dhcp"]["dns_servers"] = before["dhcp"]["dns_servers"]
            if "records" in before["dns"]:
                current["dns"]["records"] = before["dns"]["records"]
            else:
                current["dns"].pop("records", None)
            committed(lab, current)
            restored = lab.get("config")
            for key in ("revision", "updated_at"):
                restored.pop(key, None)
            original = copy.deepcopy(before)
            for key in ("revision", "updated_at"):
                original.pop(key, None)
            require_unchanged(original, restored)


if __name__ == "__main__":
    contracts.validate_target(labapi.URL)
    {"frozen-helper": frozen_helper, "dns-flood": dns_flood}[sys.argv[1]](Lab())

"""3,000 deterministic negative API contracts against a disposable router.

This is a case matrix, not 3,000 copies of a unit test. Six independent
categories have 500 unique requests each. Positive controls, state checks,
and the existing network/chaos suite are additional checks, not counted cases.
Use --catalog to inspect without connecting. Normal execution requires the
GitHub lab's live QEMU PID; --fixture permits a loopback integration server.
Only case metadata/statuses are saved, never sessions or response bodies.
"""

import argparse
import copy
import dataclasses
import gzip
import hashlib
import http.client
import ipaddress
import json
import os
import pathlib
import re
import ssl
import sys
import time
import urllib.parse
import xml.etree.ElementTree as ET


ROOT = pathlib.Path(__file__).resolve().parents[2]
CATEGORIES = ("authentication", "read_only", "csrf", "origin", "cross_site", "config_types")
PER_CATEGORY = 500
TOTAL = PER_CATEGORY * len(CATEGORIES)
BODIES = (b"{}", b"null", b"[]", b'""', b"0", b"true", b"false", b'{"enabled":true}',
          b'{"id":"missing"}', b'{"config":{}}', b'{"password":"invalid"}', b'{"confirm":"REBOOT"}')
FORGED_COOKIES = (None, "mr_session=", "unrelated=invalid", "mr_session=short", "mr_session=not-a-session",
                  "mr_session=" + "0" * 64)
BAD_CSRF = (None, "", "wrong", "x" * 31, "x" * 33, "x" * 63, "x" * 65, "token with spaces",
            "Bearer invalid", "[object Object]", "null", "undefined")
BAD_ORIGINS = ("null", "undefined", "not-a-url", "https://evil.example", "http://{authority}",
               "https://{authority}.evil.example", "https://evil.example@{authority}", "file://{authority}",
               "ws://{authority}", "ftp://{authority}", "https://{host}:1", "https://{host}:65535")


@dataclasses.dataclass(frozen=True)
class Case:
    category: str
    name: str
    method: str
    path: str
    body: bytes = b"{}"
    variant: int = 0
    expected: int = 403

    def metadata(self):
        return {"category": self.category, "name": self.name, "method": self.method, "path": self.path,
                "body_sha256": hashlib.sha256(self.body).hexdigest(), "variant": self.variant, "expected": self.expected}


def route_inventory():
    source = (ROOT / "rust/crates/mr-api/src/api/routes.rs").read_text(encoding="utf-8")
    protected = source.split("let protected:", 1)[1]
    routes = re.findall(r'\("(GET|POST|PUT|PATCH|DELETE) (/api/v1/[^\"]+)"', protected)
    if not routes or len(routes) != len(set(routes)):
        raise ValueError("missing or duplicate protected route inventory")
    return routes


def full_fixture():
    with gzip.open(ROOT / "rust/tests/fixtures/config_cases.jsonl.gz", "rt", encoding="utf-8") as source:
        for line in source:
            row = json.loads(line)
            if row["name"] == "full":
                return json.loads(row["input"])
    raise ValueError("full synthetic configuration fixture is missing")


def nodes(value, path=()):
    if path:
        yield path, value
    if isinstance(value, dict):
        for key in sorted(value):
            yield from nodes(value[key], path + (key,))
    elif isinstance(value, list):
        for index, item in enumerate(value):
            yield from nodes(item, path + (index,))


def wrong_types(value):
    # null is deliberately absent: Go-compatible decoding accepts it as a zero value.
    if isinstance(value, bool):
        return ("true", "false", 0, 1, [], {})
    if isinstance(value, (int, float)):
        return ("not-a-number", True, False, [], {}, "1")
    if isinstance(value, str):
        return (True, False, 0, 1, [], {})
    if isinstance(value, dict):
        return (True, False, 0, 1, [], "not-an-object")
    if isinstance(value, list):
        return (True, False, 0, 1, {}, "not-an-array")
    return ()


def make_catalog():
    routes = route_inventory()
    writes = [(method, path) for method, path in routes if method not in ("GET", "HEAD", "OPTIONS")]
    groups = {category: [] for category in CATEGORIES}

    def add(category, label, method, path, body=b"{}", variant=0, expected=403):
        path = re.sub(r"\{[^}]+\}", "contract-missing", path)
        name = f"{category}/{method} {path}/{label}"
        groups[category].append(Case(category, name, method, path, body, variant, expected))

    # Variant-first ordering visits every route before adding another variant.
    for variant in range(len(FORGED_COOKIES)):
        for method, path in routes:
            add("authentication", f"cookie-{variant}", method, path, variant=variant, expected=401)
    for variant, body in enumerate(BODIES):
        for method, path in writes:
            if path != "/api/v1/auth/logout":  # read-only sessions may log themselves out
                add("read_only", f"body-{variant}", method, path, body, variant)
            add("cross_site", f"body-{variant}", method, path, body, variant)
    for variant in range(len(BAD_CSRF)):
        for method, path in writes:
            add("csrf", f"token-{variant}", method, path, variant=variant)
    for variant in range(len(BAD_ORIGINS)):
        for method, path in writes:
            add("origin", f"origin-{variant}", method, path, variant=variant)

    fixture = full_fixture()
    fields = [(path, wrong_types(value)) for path, value in nodes(fixture) if wrong_types(value)]
    for variant in range(6):
        for path, values in fields:
            candidate = copy.deepcopy(fixture)
            cursor = candidate
            for part in path[:-1]:
                cursor = cursor[part]
            cursor[path[-1]] = values[variant]
            body = json.dumps(candidate, separators=(",", ":"), ensure_ascii=True).encode()
            label = ".".join(map(str, path)) + f"/type-{variant}"
            add("config_types", label, "POST", "/api/v1/config/preview", body, variant, 400)

    cases = []
    for category, pool in groups.items():
        if len(pool) < PER_CATEGORY:
            raise ValueError(f"{category}: only {len(pool)} distinct cases; need {PER_CATEGORY}")
        cases.extend(pool[:PER_CATEGORY])
    signatures = [json.dumps({k: v for k, v in case.metadata().items() if k != "name"}, sort_keys=True) for case in cases]
    if len(cases) != TOTAL or len({case.name for case in cases}) != TOTAL or len(set(signatures)) != TOTAL:
        raise ValueError("catalogue must contain exactly 3,000 unique named cases")
    return cases


def catalog_digest(cases):
    return hashlib.sha256(json.dumps([c.metadata() for c in cases], sort_keys=True).encode()).hexdigest()


def validate_target(url, fixture=False):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        raise ValueError("target must be an HTTPS origin without credentials, path or query")
    if fixture:
        if not ipaddress.ip_address(parsed.hostname).is_loopback:
            raise ValueError("integration fixtures must bind loopback")
    else:
        run = pathlib.Path(os.environ.get("LAB_RUN", "/nonexistent"))
        if os.environ.get("GITHUB_ACTIONS") != "true" or not (run / "router.pid").is_file():
            raise ValueError("requires the disposable GitHub QEMU lab; use --catalog for offline inspection")
        pid = int((run / "router.pid").read_text().strip())
        command = pathlib.Path(f"/proc/{pid}/cmdline").read_bytes()
        if b"qemu-system-x86_64" not in command or str(run.parent / "router.raw").encode() not in command:
            raise ValueError("lab PID does not name this disposable router disk")
        if parsed.hostname != "192.168.1.1" or parsed.port != 8443:
            raise ValueError("target does not match the isolated GitHub lab")
    return parsed


class Client:
    def __init__(self, parsed):
        self.parsed = parsed
        self.conn = None

    def close(self):
        if self.conn:
            self.conn.close()
            self.conn = None

    def request(self, method, path, body=None, headers=None):
        if self.conn is None:
            self.conn = http.client.HTTPSConnection(self.parsed.hostname, self.parsed.port or 443,
                                                    context=ssl._create_unverified_context(), timeout=30)
        try:
            self.conn.request(method, path, body=body, headers=headers or {})
            response = self.conn.getresponse()
            return response.status, {k.lower(): v for k, v in response.getheaders()}, response.read()
        except (OSError, http.client.HTTPException):
            self.close()
            raise


def sign_in(client, url, read_only):
    data = json.dumps({"password": os.environ["DASH_PASS"], "read_only": read_only}).encode()
    for attempt in range(9):
        status, headers, body = client.request("POST", "/api/v1/auth/login", data,
                                               {"Origin": url, "Content-Type": "application/json"})
        if status != 429 or attempt == 8:
            break
        time.sleep(15)
    if status != 200:
        raise RuntimeError(f"login control failed: HTTP {status}")
    session = json.loads(body)
    if session.get("read_only") is not read_only:
        raise RuntimeError("login returned the wrong session mode")
    if "set-cookie" not in headers or not session.get("csrf_token"):
        raise RuntimeError("login did not issue a session and CSRF token")
    return {"Cookie": headers["set-cookie"].split(";", 1)[0], "X-CSRF-Token": session["csrf_token"],
            "Origin": url, "Content-Type": "application/json"}


def config_state(client, headers):
    status, _, body = client.request("GET", "/api/v1/config", headers=headers)
    if status != 200:
        raise RuntimeError(f"state checkpoint failed: HTTP {status}")
    value = json.loads(body)
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest(), value


def case_headers(case, admin, observer, parsed):
    headers = dict(observer if case.category == "read_only" else admin)
    if case.category == "authentication":
        headers.pop("Cookie")
        cookie = FORGED_COOKIES[case.variant]
        if cookie is not None:
            headers["Cookie"] = cookie
    elif case.category == "csrf":
        headers.pop("X-CSRF-Token")
        value = BAD_CSRF[case.variant]
        if value is not None:
            if value == admin["X-CSRF-Token"]:
                raise RuntimeError("negative CSRF input unexpectedly equals the issued token")
            headers["X-CSRF-Token"] = value
    elif case.category == "origin":
        headers["Origin"] = BAD_ORIGINS[case.variant].format(authority=parsed.netloc, host=parsed.hostname)
    elif case.category == "cross_site":
        headers["Sec-Fetch-Site"] = "cross-site"
    return headers


def verify_response(case, status, headers, body):
    if status != case.expected:
        raise AssertionError(f"expected HTTP {case.expected}, received {status}")
    markers = {"authentication": b"Unauthorized or expired session", "read_only": b"Read-only session",
               "csrf": b"CSRF token mismatch", "origin": b"Origin rejected", "cross_site": b"Cross-site request rejected",
               "config_types": b"Invalid configuration payload."}
    if markers[case.category] not in body:
        raise AssertionError("response did not identify the expected guard (a different 4xx is not a pass)")
    for name, value in {"x-content-type-options": "nosniff", "x-frame-options": "DENY", "cache-control": "no-store"}.items():
        if headers.get(name) != value:
            raise AssertionError(f"missing or incorrect security header: {name}")
    if not headers.get("strict-transport-security", "").startswith("max-age="):
        raise AssertionError("missing HSTS")


def write_reports(out, cases, results, error=None):
    out.mkdir(parents=True, exist_ok=True)
    passed = sum(r["result"] == "PASS" for r in results)
    failed = sum(r["result"] == "FAIL" for r in results)
    skipped = len(cases) - len(results)
    summary = {"planned": len(cases), "executed": len(results), "passed": passed, "failed": failed,
               "not_run": skipped, "catalog_sha256": catalog_digest(cases),
               "source_commit": os.environ.get("GITHUB_SHA"),
               "categories": {g: sum(c.category == g for c in cases) for g in CATEGORIES}, "error": error}
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    suite = ET.Element("testsuite", name="Rust installed API contracts", tests=str(len(cases) + bool(error)),
                       failures=str(failed), errors=str(bool(error)), skipped=str(skipped))
    indexed = {r["name"]: r for r in results}
    for case in cases:
        row = indexed.get(case.name)
        child = ET.SubElement(suite, "testcase", classname=case.category, name=case.name,
                              time=str(row["seconds"] if row else 0))
        if row is None:
            ET.SubElement(child, "skipped", message="not run; suite aborted")
        elif row["result"] == "FAIL":
            ET.SubElement(child, "failure", message=row["detail"])
    if error:
        ET.SubElement(suite, "testcase", name="suite preflight/checkpoint").append(ET.Element("error", message=error))
    ET.ElementTree(suite).write(out / "junit.xml", encoding="utf-8", xml_declaration=True)
    return summary


def execute(url, out, fixture=False):
    cases = make_catalog()
    results, error = [], None
    client = None
    out.mkdir(parents=True, exist_ok=True)
    try:
        parsed = validate_target(url, fixture)
        client = Client(parsed)
        admin = sign_in(client, url, False)
        observer = sign_in(client, url, True)
        initial, config = config_state(client, admin)
        if client.request("GET", "/api/v1/config", headers=observer)[0] != 200:
            raise RuntimeError("positive read-only read control failed")
        if client.request("POST", "/api/v1/config/preview", json.dumps(config).encode(), admin)[0] != 200:
            raise RuntimeError("positive live configuration preview control failed")
        # If this fixture fails decoding, all type errors could be false positives.
        if client.request("POST", "/api/v1/config/preview", json.dumps(full_fixture()).encode(), admin)[0] not in (200, 422):
            raise RuntimeError("unmodified synthetic fixture is not accepted by the configuration decoder")
        with (out / "results.jsonl").open("w", encoding="utf-8") as stream:
            for index, case in enumerate(cases):
                row = {**case.metadata(), "result": "PASS", "detail": "expected guard and security headers"}
                started = time.monotonic()
                try:
                    headers = case_headers(case, admin, observer, parsed)
                    status, response_headers, body = client.request(case.method, case.path, case.body, headers)
                    row["status"] = status
                    verify_response(case, status, response_headers, body)
                except (AssertionError, OSError, http.client.HTTPException, RuntimeError, ValueError) as exc:
                    row.update(result="FAIL", detail=f"{type(exc).__name__}: {exc}")
                row["seconds"] = round(time.monotonic() - started, 6)
                results.append(row)
                stream.write(json.dumps(row) + "\n")
                stream.flush()
                if row["result"] == "FAIL":
                    raise RuntimeError(f"stopped at {case.name}: {row['detail']}")
                if (index + 1) % 100 == 0 or index + 1 == len(cases):
                    if config_state(client, admin)[0] != initial:
                        raise RuntimeError(f"canonical configuration changed after case {index + 1}")
                    if client.request("GET", "/api/v1/health", headers=admin)[0] != 200:
                        raise RuntimeError(f"health failed after case {index + 1}")
                    print(f"contracts: {index + 1}/{TOTAL} passed, state unchanged", flush=True)
        if config_state(client, admin)[0] != initial:
            raise RuntimeError("final configuration differs from the original")
    except (AssertionError, OSError, http.client.HTTPException, RuntimeError, ValueError, KeyError) as exc:
        error = f"{type(exc).__name__}: {exc}"
    finally:
        if client:
            client.close()
        summary = write_reports(out, cases, results, error)
    print(json.dumps(summary, sort_keys=True), flush=True)
    return 0 if not error and summary["passed"] == TOTAL and not summary["failed"] and not summary["not_run"] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog", action="store_true", help="inspect catalogue without connecting")
    parser.add_argument("--fixture", action="store_true", help="allow a loopback HTTPS integration fixture")
    parser.add_argument("--url", default=os.environ.get("ROUTER_URL", "https://192.168.1.1:8443"))
    parser.add_argument("--out", type=pathlib.Path, default=pathlib.Path("build/contracts"))
    args = parser.parse_args()
    if args.catalog:
        cases = make_catalog()
        print(json.dumps({"planned": len(cases), "protected_routes": len(route_inventory()),
                          "categories": {g: sum(c.category == g for c in cases) for g in CATEGORIES},
                          "catalog_sha256": catalog_digest(cases)}, indent=2))
        return 0
    status = execute(args.url, args.out, args.fixture)
    step_summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if step_summary:
        summary = json.loads((args.out / "summary.json").read_text(encoding="utf-8"))
        with open(step_summary, "a", encoding="utf-8") as stream:
            stream.write(f"\n### API contracts ({'integration fixture' if args.fixture else 'installed QEMU router'})\n\n")
            stream.write(f"Planned: {TOTAL}; executed: {summary['executed']}; passed: {summary['passed']}; "
                         f"failed: {summary['failed']}; not run: {summary['not_run']}.\n\n")
            stream.write("| Category | Cases |\n|---|---:|\n")
            for category, count in summary["categories"].items():
                stream.write(f"| {category} | {count} |\n")
            stream.write(f"\nCatalogue SHA-256: `{summary['catalog_sha256']}`. Per-case JSONL and JUnit are in the run artifacts.\n")
    return status


if __name__ == "__main__":
    sys.exit(main())

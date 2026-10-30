"""Observer regressions: a broken/aborted lab must never appear green."""
import contextlib
import io
import json
import os
import pathlib
import tempfile
import unittest
from unittest import mock
import xml.etree.ElementTree as ET

import contracts


SECURITY = {"x-content-type-options": "nosniff", "x-frame-options": "DENY", "cache-control": "no-store",
            "strict-transport-security": "max-age=63072000"}


class ContractsTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cases = contracts.make_catalog()

    def test_catalog_is_exactly_3000_distinct_requests_with_balanced_categories(self):
        self.assertEqual(len(self.cases), 3000)
        self.assertEqual(len({c.name for c in self.cases}), 3000)
        wire_cases = {(c.category, c.method, c.path, c.body, c.variant) for c in self.cases}
        self.assertEqual(len(wire_cases), 3000)
        for category in contracts.CATEGORIES:
            self.assertEqual(sum(c.category == category for c in self.cases), 500)

    def test_catalog_covers_every_protected_route_and_every_write_guard(self):
        expected = {(m, contracts.re.sub(r"\{[^}]+\}", "contract-missing", p)) for m, p in contracts.route_inventory()}
        self.assertEqual({(c.method, c.path) for c in self.cases if c.category == "authentication"}, expected)
        writes = {(m, p) for m, p in expected if m != "GET"}
        for category in ("csrf", "origin", "cross_site"):
            self.assertEqual({(c.method, c.path) for c in self.cases if c.category == category}, writes)

    def test_catalog_is_replayable_and_does_not_rewrite_fixtures(self):
        before = contracts.full_fixture()
        self.assertEqual(contracts.catalog_digest(self.cases), contracts.catalog_digest(contracts.make_catalog()))
        self.assertEqual(before, contracts.full_fixture())

    def test_read_only_logout_is_not_misclassified_as_forbidden(self):
        self.assertFalse(any(c.category == "read_only" and c.path.endswith("/auth/logout") for c in self.cases))

    def test_type_cases_cover_all_top_level_configuration_groups(self):
        names = [c.name for c in self.cases if c.category == "config_types"]
        for field in contracts.full_fixture():
            self.assertTrue(any("/" + field + "/type-" in n or "/" + field + "." in n for n in names), field)

    def test_non_lab_targets_are_refused_before_connecting(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            for url in ("https://192.168.1.1:8443", "https://example.com"):
                with self.assertRaises(ValueError):
                    contracts.validate_target(url)

    def test_fixture_mode_only_accepts_https_loopback_origins(self):
        contracts.validate_target("https://127.0.0.1:1234", fixture=True)
        for url in ("https://192.168.1.1:8443", "http://127.0.0.1:1234", "https://u:p@127.0.0.1:1234",
                    "https://127.0.0.1/path", "https://127.0.0.1?query=1"):
            with self.assertRaises(ValueError, msg=url):
                contracts.validate_target(url, fixture=True)

    def test_a_different_4xx_does_not_pass_the_expected_guard(self):
        case = next(c for c in self.cases if c.category == "csrf")
        for status, body in ((401, b"Unauthorized"), (403, b"Origin rejected"), (200, b"CSRF token mismatch"),
                             (500, b"CSRF token mismatch")):
            with self.assertRaises(AssertionError):
                contracts.verify_response(case, status, SECURITY, body)

    def test_missing_security_headers_fail_a_correct_status(self):
        case = self.cases[0]
        for key in SECURITY:
            headers = dict(SECURITY)
            del headers[key]
            with self.assertRaises(AssertionError, msg=key):
                contracts.verify_response(case, 401, headers, b"Unauthorized or expired session")

    def test_aborted_cases_are_not_reported_as_passed_or_executed(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = pathlib.Path(tmp)
            one = {**self.cases[0].metadata(), "result": "FAIL", "detail": "HTTP 500", "seconds": 0.1}
            summary = contracts.write_reports(out, self.cases, [one], "stopped")
            self.assertEqual((summary["passed"], summary["failed"], summary["executed"], summary["not_run"]), (0, 1, 1, 2999))
            xml = ET.parse(out / "junit.xml").getroot()
            self.assertEqual(len(xml.findall("testcase/skipped")), 2999)
            self.assertEqual(int(xml.attrib["tests"]), len(xml.findall("testcase")))

    def test_preflight_failure_is_nonzero_with_no_fictitious_results(self):
        with tempfile.TemporaryDirectory() as tmp, mock.patch.dict(os.environ, {}, clear=True), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(contracts.execute("https://192.168.1.1:8443", pathlib.Path(tmp)), 1)
            result = json.loads((pathlib.Path(tmp) / "summary.json").read_text())
            self.assertEqual(result["executed"], 0)
            self.assertEqual(result["not_run"], 3000)
            self.assertTrue(result["error"])


if __name__ == "__main__":
    unittest.main()

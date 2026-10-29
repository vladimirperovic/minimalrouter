"""Fault observers must fail on transport errors and always restore the lab."""
import contextlib
import copy
import io
import socket
import struct
import unittest
from unittest import mock

import dns_stress
import scenarios


CONFIG = {"revision": 7, "updated_at": "synthetic", "system": {"hostname": "lab-router"},
          "dhcp": {"dns_enabled": False, "dns_servers": ["1.1.1.1"]}, "dns": {"records": None}}
TIMEOUT = {"tx": {"state": "RecoveryRequired"}, "error": "privileged apply outcome remained unknown after retry"}


class FrozenHelperTests(unittest.TestCase):
    def lab(self):
        lab = mock.Mock()
        lab.shell.return_value = "123"
        lab.get.side_effect = lambda path: copy.deepcopy({"config": CONFIG, "recovery/status": {
            "engine": {"apply_in_progress": False, "recovery_required": False}},
            "transactions/pending": {"pending": False}, "health": {}}[path])
        return lab

    def run_scenario(self, lab):
        with mock.patch.object(scenarios.time, "monotonic", side_effect=[10, 130]), \
                contextlib.redirect_stdout(io.StringIO()):
            scenarios.frozen_helper(lab)

    def test_transport_timeout_is_failure_and_always_resumes_and_reconciles(self):
        lab = self.lab()
        lab.api.side_effect = TimeoutError("no HTTP response")
        with self.assertRaisesRegex(TimeoutError, "no HTTP response"):
            self.run_scenario(lab)
        self.assertIn(mock.call("kill -CONT 123"), lab.shell.call_args_list)
        lab.reboot.assert_called_once()
        self.assertEqual(lab.api.call_args.kwargs["timeout"], 150)

    def test_empty_status_wrong_status_and_wrong_transaction_cannot_pass(self):
        for status, body in [(None, {}), ("", {}), (200, {}), (500, {}), (422, {"tx": {"state": "Rejected"}})]:
            with self.subTest(status=status, body=body):
                lab = self.lab()
                lab.api.return_value = (status, body)
                with self.assertRaises(RuntimeError):
                    self.run_scenario(lab)
                lab.reboot.assert_called_once()
                self.assertIn(mock.call("kill -CONT 123"), lab.shell.call_args_list)

    def test_canonical_change_after_reboot_is_failure(self):
        lab = self.lab()
        lab.api.side_effect = TimeoutError("interrupted")
        lab.reboot.side_effect = lambda: CONFIG.update(revision=8)
        before = copy.deepcopy(CONFIG)
        try:
            with self.assertRaisesRegex(RuntimeError, "canonical configuration changed"):
                self.run_scenario(lab)
        finally:
            CONFIG.update(before)

    def test_exact_deadline_outcome_refuses_writes_and_finishes_reconciliation(self):
        lab = self.lab()
        state = {"recovery": False}
        get = lab.get.side_effect
        def api(*args, **kwargs):
            state["recovery"] = True
            return 422, copy.deepcopy(TIMEOUT)
        def read(path):
            body = get(path)
            if path == "recovery/status":
                body["engine"]["recovery_required"] = state["recovery"]
            return body
        lab.api.side_effect = api
        lab.get.side_effect = read
        lab.reboot.side_effect = lambda: state.update(recovery=False)
        self.run_scenario(lab)
        self.assertEqual(lab.api.call_count, 2)
        lab.reboot.assert_called_once()

    def test_early_rejection_is_not_proof_of_the_helper_deadline(self):
        with self.assertRaisesRegex(RuntimeError, "120s IPC deadline"):
            scenarios.require_timeout(422, TIMEOUT, 0.1)

    def test_http_client_propagates_timeout_and_closes_connection(self):
        lab = scenarios.Lab.__new__(scenarios.Lab)
        lab.saved = {"cookie": "synthetic", "csrf": "synthetic"}
        conn = mock.Mock()
        with mock.patch.object(scenarios.labapi, "connect", return_value=conn), \
                mock.patch.object(scenarios.labapi, "request", side_effect=TimeoutError("missing response")):
            with self.assertRaises(TimeoutError):
                lab.api("PUT", "config", {}, timeout=150)
        conn.close.assert_called_once()

    def test_http_client_rejects_missing_or_malformed_status(self):
        lab = scenarios.Lab.__new__(scenarios.Lab)
        lab.saved = {"cookie": "synthetic", "csrf": "synthetic"}
        for code in (None, "", "422", 0, 600):
            with mock.patch.object(scenarios.labapi, "connect"), \
                    mock.patch.object(scenarios.labapi, "request", return_value=(code, [], b"{}")):
                with self.assertRaisesRegex(RuntimeError, "HTTP status"):
                    lab.api("GET", "health")


class DNSWireTests(unittest.TestCase):
    def setUp(self):
        self.query = dns_stress.question("probe.flood.lab.invalid", 1234)
        self.response = struct.pack("!6H", 1234, 0x8180, 1, 1, 0, 0) + self.query[12:] + \
            b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 0, 4) + socket.inet_aton("11.255.0.2")

    def test_exact_uncached_answer_with_compressed_name(self):
        self.assertEqual(dns_stress.answer(self.response, self.query, "11.255.0.2"), ["11.255.0.2"])

    def test_empty_servfail_nxdomain_truncation_and_wrong_id_are_failures(self):
        packets = [b"", self.response[:10], self.response[:-1]]
        for flags in (0x8182, 0x8183, 0x8380):
            packets.append(self.response[:2] + struct.pack("!H", flags) + self.response[4:])
        packets.append(b"\x00\x01" + self.response[2:])
        for packet in packets:
            with self.subTest(packet=packet):
                with self.assertRaises(ValueError):
                    dns_stress.answer(packet, self.query, "11.255.0.2")

    def test_wrong_ip_and_empty_answer_are_not_success(self):
        with self.assertRaisesRegex(ValueError, "DNS A answer"):
            dns_stress.answer(self.response, self.query, "192.168.1.1")
        empty = struct.pack("!6H", 1234, 0x8180, 1, 0, 0, 0) + self.query[12:]
        with self.assertRaisesRegex(ValueError, "DNS A answer"):
            dns_stress.answer(empty, self.query, "11.255.0.2")

    def test_public_control_rejects_private_answers(self):
        self.assertEqual(dns_stress.answer(self.response, self.query, "public"), ["11.255.0.2"])
        private = self.response[:-4] + socket.inet_aton("192.168.1.1")
        with self.assertRaisesRegex(ValueError, "public A addresses"):
            dns_stress.answer(private, self.query, "public")

    def test_compression_loop_and_question_mismatch_are_failures(self):
        with self.assertRaisesRegex(ValueError, "looping"):
            dns_stress.read_name(b"\xc0\x00", 0)
        with self.assertRaisesRegex(ValueError, "question mismatch"):
            dns_stress.answer(self.response, dns_stress.question("other.flood.lab.invalid", 1234), "11.255.0.2")

    def test_tcp_premature_close_is_not_an_empty_success(self):
        sock = mock.Mock()
        sock.recv.side_effect = [b"\x00", b""]
        with self.assertRaisesRegex(ValueError, "truncated DNS TCP"):
            dns_stress.receive(sock, 2)

    def test_flood_names_are_unique_and_twelve_seconds_are_unpaced(self):
        with mock.patch.object(dns_stress.socket, "socket") as factory, \
                mock.patch.object(dns_stress.time, "monotonic", side_effect=[0, 0, 1, 12, 12]):
            result = dns_stress.flood("192.168.1.1")
        sent = factory.return_value.__enter__.return_value.sendto.call_args_list
        self.assertEqual(result["queries_sent"], 2)
        self.assertEqual(result["seconds"], 12)
        self.assertNotEqual(sent[0].args[0], sent[1].args[0])
        self.assertIn(b"flood\x03lab\x07invalid", sent[0].args[0])


class DNSCleanupTests(unittest.TestCase):
    def test_failed_dns_probe_fails_scenario_and_restores_original_configuration(self):
        lab = mock.Mock()
        current = copy.deepcopy(CONFIG)
        def get(path):
            return copy.deepcopy({"config": current, "recovery/status": {"engine": {
                "apply_in_progress": False, "recovery_required": False}},
                "transactions/pending": {"pending": False}, "health": {}}[path])
        def api(method, path, candidate):
            current.update(copy.deepcopy(candidate))
            current["revision"] += 1
            return 200, {"state": "Committed"}
        lab.get.side_effect = get
        lab.api.side_effect = api
        with mock.patch.object(scenarios, "dns_probe", side_effect=TimeoutError("DNS timed out")), \
                contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(TimeoutError, "DNS timed out"):
                scenarios.dns_flood(lab)
        self.assertEqual(lab.api.call_count, 2)
        self.assertEqual(current["dhcp"], CONFIG["dhcp"])
        self.assertEqual(current["dns"], CONFIG["dns"])


if __name__ == "__main__":
    unittest.main()

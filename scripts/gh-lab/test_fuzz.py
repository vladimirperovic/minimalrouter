"""A redacted config must never be used to undo changes to stored secrets."""
import json
import os
import unittest
from unittest import mock

with mock.patch.dict(os.environ, {"DASH_PASS": "test-only"}):
    import fuzz


def reply(value, status=200):
    return status, [], json.dumps(value).encode()


class SnapshotTests(unittest.TestCase):
    def boundary(self, *responses):
        call = mock.Mock(side_effect=[reply({"snapshot": {"id": "baseline"}}), *responses])
        return fuzz.SnapshotBoundary(call), call

    def test_restore_uses_server_snapshot_not_redacted_get(self):
        boundary, call = self.boundary(reply({"pending": False}), reply({"base_revision": 77}), reply({"state": "Committed"}))
        boundary.restore()
        self.assertEqual(call.call_args, mock.call("POST", "/api/v1/snapshots/baseline/restore", {"expected_revision": 77}))
        self.assertFalse(any(args.args[1] == "/api/v1/config" for args in call.call_args_list))

    def test_rejected_restore_is_failure_not_ignored(self):
        boundary, _ = self.boundary(reply({"pending": False}), reply({"base_revision": 77}), reply({}, 422))
        with self.assertRaisesRegex(RuntimeError, "restore failed: HTTP 422"):
            boundary.restore()

    def test_never_confirms_an_unknown_pending_transaction(self):
        boundary, call = self.boundary(reply({"id": "someone-else"}))
        with self.assertRaisesRegex(RuntimeError, "unknown pending"):
            boundary.restore()
        self.assertEqual(call.call_count, 2)

    def test_confirms_only_id_returned_by_this_request(self):
        boundary, call = self.boundary(reply({"id": "ours"}), reply({}))
        boundary.finish(202, json.dumps({"id": "ours", "state": "AwaitingConfirmation"}))
        self.assertEqual(call.call_args, mock.call("POST", "/api/v1/transactions/ours/confirm", {}))
        boundary, call = self.boundary(reply({"id": "other"}))
        with self.assertRaisesRegex(RuntimeError, "differs"):
            boundary.finish(202, json.dumps({"id": "ours", "state": "AwaitingConfirmation"}))
        self.assertEqual(call.call_count, 2)

    def test_async_non_configuration_result_is_not_confirmed(self):
        boundary, call = self.boundary()
        boundary.finish(202, json.dumps({"id": "dns-operation", "state": "running"}))
        self.assertEqual(call.call_count, 1)

    def test_recovery_machinery_is_not_fuzzed_or_pruned(self):
        endpoints = fuzz.endpoints()
        self.assertNotIn(("PUT", "/config"), endpoints)
        self.assertFalse(any("snapshots" in p or "transactions" in p for _, p in endpoints))
        self.assertIn(("POST", "/qos/speedtest"), endpoints)


if __name__ == "__main__":
    unittest.main()

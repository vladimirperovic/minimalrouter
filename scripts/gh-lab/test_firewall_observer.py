"""Only allocation handles and independent table listing order may differ."""
import copy
import unittest

import firewall_observer as observer


def fixture():
    main = [
        {"table": {"family": "inet", "name": "minimalrouter", "handle": 1}},
        {"counter": {"family": "inet", "table": "minimalrouter", "name": "mr_revision_7", "handle": 2}},
    ]
    for name in ("input", "forward", "output"):
        main += [{"chain": {"family": "inet", "table": "minimalrouter", "name": name,
                            "type": "filter", "hook": name, "prio": 0, "policy": "drop", "handle": 3}}]
    main += [
        {"rule": {"family": "inet", "table": "minimalrouter", "chain": "input", "handle": 4,
                  "expr": [{"match": {"left": {"meta": {"key": "iifname"}}, "op": "==", "right": "lo"}}, {"accept": None}]}},
        {"rule": {"family": "inet", "table": "minimalrouter", "chain": "input", "handle": 5, "expr": [{"drop": None}]}},
    ]
    pause = [
        {"table": {"family": "inet", "name": "minimalrouter_pause", "handle": 8}},
        {"set": {"family": "inet", "table": "minimalrouter_pause", "name": "blocked_ipv4", "type": "ipv4_addr", "flags": ["timeout"]}},
        {"chain": {"family": "inet", "table": "minimalrouter_pause", "name": "forward",
                   "type": "filter", "hook": "forward", "prio": -10, "policy": "accept"}},
        {"rule": {"family": "inet", "table": "minimalrouter_pause", "chain": "forward",
                  "expr": [{"match": {"left": {"payload": {"protocol": "ip", "field": "saddr"}},
                                      "op": "==", "right": "@blocked_ipv4"}}, {"drop": None}]}},
    ]
    return {"nftables": main + pause}


class FirewallObserverTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()

    def test_recreated_table_can_move_in_dump_and_get_new_handles(self):
        after = copy.deepcopy(self.before)
        items = after["nftables"]
        after["nftables"] = items[7:] + items[:7]
        for item in after["nftables"]:
            for obj in item.values():
                if "handle" in obj:
                    obj["handle"] += 100
        self.assertEqual(observer.render(self.before), observer.render(after))

    def assert_change_fails(self, mutate):
        after = copy.deepcopy(self.before)
        mutate(after["nftables"])
        with self.assertRaises(ValueError):
            observer.compare(self.before, after)

    def test_changed_default_policy_fails(self):
        self.assert_change_fails(lambda items: items[2]["chain"].update(policy="accept"))

    def test_changed_priority_and_hook_fail(self):
        self.assert_change_fails(lambda items: items[3]["chain"].update(prio=20))
        self.assert_change_fails(lambda items: items[3]["chain"].update(hook="input"))

    def test_equal_priority_registration_order_is_not_ignored(self):
        self.assert_change_fails(lambda items: items[9]["chain"].update(prio=0))

    def test_rule_deletion_and_addition_fail(self):
        self.assert_change_fails(lambda items: items.pop(6))
        self.assert_change_fails(lambda items: items.append(copy.deepcopy(items[5])))

    def test_rule_reordering_and_expression_changes_fail(self):
        self.assert_change_fails(lambda items: items.__setitem__(slice(5, 7), items[5:7][::-1]))
        self.assert_change_fails(lambda items: items[5]["rule"]["expr"][0]["match"].update(right="eth0"))

    def test_static_and_pause_set_contents_are_retained(self):
        self.assert_change_fails(lambda items: items[8]["set"].update(elem=["192.168.1.123"]))
        self.assert_change_fails(lambda items: items[8]["set"].update(flags=[]))

    def test_revision_marker_is_not_ignored(self):
        self.assert_change_fails(lambda items: items[1]["counter"].update(name="mr_revision_8"))

    def test_missing_extra_duplicate_tables_and_unsupported_objects_fail(self):
        self.assert_change_fails(lambda items: items.pop(7))
        self.assert_change_fails(lambda items: items.append({"table": {"family": "inet", "name": "unowned"}}))
        self.assert_change_fails(lambda items: items.append(copy.deepcopy(items[0])))
        self.assert_change_fails(lambda items: items.append({"unknown": {"family": "inet", "table": "minimalrouter"}}))

    def test_empty_malformed_and_failed_capture_cannot_pass(self):
        for raw in ({}, {"nftables": []}, {"nftables": [{"error": "nft failed"}]}, {"nftables": None}):
            with self.subTest(raw=raw):
                with self.assertRaises(ValueError):
                    observer.canonical(raw)


if __name__ == "__main__":
    unittest.main()

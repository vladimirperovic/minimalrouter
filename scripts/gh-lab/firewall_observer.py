"""Compare nft -s -j snapshots of the isolated lab without hiding policy changes.

Only the listing order of independent tables and kernel object handles are
normalized. Object/rule/expression order, set contents, revision counters,
chain hooks/priorities/policies and every other field remain significant.
"""
import argparse
import copy
import difflib
import hashlib
import json
import pathlib
import re
import sys

TABLES = {("inet", "minimalrouter"), ("inet", "minimalrouter_pause")}
KINDS = {"table", "chain", "rule", "set", "map", "counter", "quota", "limit", "flowtable"}


def canonical(document):
    if not isinstance(document, dict) or set(document) != {"nftables"} or not isinstance(document["nftables"], list):
        raise ValueError("invalid nft ruleset envelope")
    tables, objects, hooks, chains = {}, [], set(), {}
    for item in document["nftables"]:
        if not isinstance(item, dict) or len(item) != 1:
            raise ValueError("invalid nft object")
        kind, body = next(iter(item.items()))
        if kind == "metainfo":
            continue  # nft version/schema metadata, never packet policy
        if kind not in KINDS or not isinstance(body, dict):
            raise ValueError(f"unsupported nft object: {kind}")
        body = copy.deepcopy(body)
        body.pop("handle", None)  # allocation identity changes on recreation
        key = (body.get("family"), body.get("name") if kind == "table" else body.get("table"))
        if key not in TABLES:
            raise ValueError(f"unexpected firewall table: {key}")
        if kind == "table":
            if key in tables:
                raise ValueError("duplicate nft table")
            tables[key] = [{kind: body}]
        else:
            objects.append((key, kind, body))
        if kind == "chain":
            chain_key = (*key, body.get("name"))
            if chain_key in chains:
                raise ValueError("duplicate nft chain")
            chains[chain_key] = body
            if "hook" in body:
                if type(body.get("prio")) is not int or body.get("policy") not in ("accept", "drop"):
                    raise ValueError("invalid base-chain priority or policy")
                hook = (body["family"], body["hook"], body.get("dev"), body["prio"])
                if hook in hooks:
                    # Same-priority ordering is undefined; do not silently
                    # equate potentially order-sensitive chain registrations.
                    raise ValueError("ambiguous equal-priority base chains")
                hooks.add(hook)
    if set(tables) != TABLES:
        raise ValueError("required main or pause firewall table missing")
    for name in ("input", "forward", "output"):
        chain = chains.get(("inet", "minimalrouter", name), {})
        if chain.get("hook") != name or chain.get("policy") != "drop":
            raise ValueError(f"main {name} chain is not fail-closed")
    pause = chains.get(("inet", "minimalrouter_pause", "forward"), {})
    if pause.get("hook") != "forward" or pause.get("prio") != -10 or pause.get("policy") != "accept":
        raise ValueError("pause enforcement hook changed")
    for key, kind, body in objects:
        if kind == "rule" and (*key, body.get("chain")) not in chains:
            raise ValueError("rule references a missing chain")
        tables[key].append({kind: body})
    markers = [obj["counter"]["name"] for obj in tables[("inet", "minimalrouter")]
               if "counter" in obj and re.fullmatch(r"mr_revision_[0-9]+", obj["counter"].get("name", ""))]
    if len(markers) != 1:
        raise ValueError("canonical firewall revision marker missing or ambiguous")
    return {"tables": [tables[key] for key in sorted(tables)]}


def render(document):
    return json.dumps(canonical(document), sort_keys=True, indent=2) + "\n"


def compare(before, after):
    left, right = render(before), render(after)
    if left != right:
        diff = "".join(difflib.unified_diff(left.splitlines(True), right.splitlines(True),
                                             fromfile="firewall before", tofile="firewall after"))
        raise ValueError("firewall policy changed:\n" + diff)
    return hashlib.sha256(left.encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=pathlib.Path)
    parser.add_argument("after", nargs="?", type=pathlib.Path)
    args = parser.parse_args()
    try:
        before = json.loads(args.before.read_text())
        text = render(before)
        args.before.with_suffix(args.before.suffix + ".canonical.json").write_text(text)
        if args.after:
            after = json.loads(args.after.read_text())
            args.after.with_suffix(args.after.suffix + ".canonical.json").write_text(render(after))
            print("firewall policy identical: " + compare(before, after))
            order = lambda doc: [(item["table"]["family"], item["table"]["name"])
                                 for item in doc["nftables"] if "table" in item]
            if order(before) != order(after):
                print("independent table listing order changed; policies, hook priorities and ordered rules match")
        else:
            print(hashlib.sha256(text.encode()).hexdigest())
    except (ValueError, OSError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

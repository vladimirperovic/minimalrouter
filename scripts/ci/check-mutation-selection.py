#!/usr/bin/env python3
"""Refuse an empty, incomplete or accidentally broad safety mutation gate."""
import json
import sys
from pathlib import Path

selected = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
expected = {"verify_wg_client_handshake", "ApplyResponse::validate"}
actual = {item.get("function", {}).get("function_name") for item in selected}
if actual != expected or len(selected) < len(expected):
    raise SystemExit(f"Unexpected safety mutation selection: {len(selected)} mutations, {actual!r}")
print(f"Selected {len(selected)} mutations in {', '.join(sorted(actual))}")

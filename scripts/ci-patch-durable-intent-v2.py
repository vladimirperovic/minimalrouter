from pathlib import Path
import subprocess

patch_path = Path("scripts/ci-patch-confirm-commit-retry.py")
exec(compile(patch_path.read_text(), str(patch_path), "exec"))

changed_go_files = [
    "internal/apply/statemachine.go",
    "internal/apply/two_phase_confirmation_test.go",
]
subprocess.run(["gofmt", "-w", *changed_go_files], check=True)
subprocess.run(["git", "add", "internal/apply"], check=True)

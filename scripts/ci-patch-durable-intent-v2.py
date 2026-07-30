from pathlib import Path
import subprocess

patch_path = Path("scripts/ci-patch-two-phase-confirmation.py")
exec(compile(patch_path.read_text(), str(patch_path), "exec"))

changed_go_files = [
    "internal/apply/ipc.go",
    "internal/apply/statemachine.go",
    "internal/apply/two_phase_confirmation_test.go",
    "cmd/router-applyd/main.go",
    "cmd/router-applyd/outcome.go",
    "cmd/router-applyd/reconcile_override_test.go",
]
subprocess.run(["gofmt", "-w", *changed_go_files], check=True)
subprocess.run(["git", "add", "cmd/router-applyd", "internal/apply"], check=True)

"""Mass-kill success requires successful delivery and four observed restarts."""
import os
import pathlib
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest


HERE = pathlib.Path(__file__).resolve().parent
BASH = ("C:/Program Files/Git/bin/bash.exe" if sys.platform == "win32"
        else shutil.which("bash"))
NAMES = ("routerd", "router-applyd", "dnsmasq", "pppd")
SLEEP_ARGS = ("-I", "-c", "import time; time.sleep(60)")


class MassKillHarness(unittest.TestCase):
    def setUp(self):
        self.assertTrue(BASH and pathlib.Path(BASH).exists(), "Bash is required")
        self.temp = tempfile.TemporaryDirectory(prefix="minimalrouter-mass-kill-")
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        # Load the real function bodies without executing the chaos suite.
        source = (HERE / "chaos.sh").read_text().split("# -------------------------------------------------------------------- main", 1)[0]
        source = source.replace('. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"', '. "$MR_TEST_LIB"', 1)
        self.functions = self.root / "functions.sh"
        self.functions.write_text(source)
        self.env = dict(os.environ, LAB_DIR=self.root.as_posix(),
                        MR_TEST_LIB=(HERE / "lib.sh").as_posix(),
                        MR_TEST_FUNCTIONS=self.functions.as_posix(),
                        MR_TEST_OBSERVER=(HERE / "process-identity.sh").as_posix())

    def shell(self, script, **extra):
        return subprocess.run([BASH, "-c", '. "$MR_TEST_FUNCTIONS"\n' + script],
                              env=dict(self.env, **extra), capture_output=True,
                              text=True, input="", timeout=20)

class MassKillTests(MassKillHarness):
    def fake_case(self, mode):
        return self.shell(r'''
router_identity() {
    local pid
    case "$1" in routerd) pid=101 ;; router-applyd) pid=102 ;; dnsmasq) pid=103 ;; pppd) pid=104 ;; esac
    if [ -e "$LAB_DIR/dispatched" ]; then
        if [ "$MODE" = unchanged ] && [ "$1" = dnsmasq ]; then
            printf '%s 10\n' "$pid"
        else
            printf '%s 20\n' "$((pid + 100))"
        fi
    else
        printf '%s 10\n' "$pid"
    fi
}
rsh() {
    # Consume the actual stdin program; no remote host or process is touched.
    printf '%s\n' "$@" > "$LAB_DIR/ssh-argv"
    cat > "$LAB_DIR/ssh-stdin"
    [ "$MODE" != dispatch_failure ] || { echo transport-failed >&2; return 42; }
    touch "$LAB_DIR/dispatched"
    [ "$MODE" = missing_ack ] || echo 'mass-kill dispatched: 4'
}
router_restarted_ready() {
    echo "$1" >> "$LAB_DIR/readiness"
    router_identity "$1"
}
wait_for() { shift; "$@"; }
router_healthy() { echo healthy >> "$LAB_DIR/recovery"; }
firewall_snapshot() { printf '{}\n' > "$1"; }
ppp_up() { echo ppp >> "$LAB_DIR/recovery"; }
client() { echo dns >> "$LAB_DIR/recovery"; }
python3() { echo firewall >> "$LAB_DIR/recovery"; }
no_crashes() { echo kernel >> "$LAB_DIR/recovery"; }
c_kill_everything
''', MODE=mode)

    def test_failed_ssh_dispatch_cannot_pass_on_healthy_services(self):
        result = self.fake_case("dispatch_failure")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("dispatch failed", result.stdout)
        self.assertFalse((self.root / "readiness").exists())
        self.assertFalse((self.root / "recovery").exists())

    def test_missing_dispatch_acknowledgement_is_failure(self):
        result = self.fake_case("missing_ack")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("acknowledgement missing", result.stdout)
        self.assertFalse((self.root / "readiness").exists())

    def test_one_unchanged_daemon_blocks_all_recovery_probes(self):
        result = self.fake_case("unchanged")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertFalse((self.root / "readiness").exists())
        self.assertFalse((self.root / "recovery").exists())
        after = (self.root / "logs/process-mass-kill-after.txt").read_text()
        self.assertIn("dnsmasq 103 10", after)

    def test_success_records_four_changed_identities_and_uses_numeric_ssh_argv(self):
        result = self.fake_case("success")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        before = (self.root / "logs/process-mass-kill-before.txt").read_text().splitlines()
        after = (self.root / "logs/process-mass-kill-after.txt").read_text().splitlines()
        self.assertEqual(len(before), 4)
        self.assertEqual(len(after), 4)
        for name, old, new in zip(NAMES, before, after):
            self.assertEqual(old.split()[0], name)
            self.assertEqual(new.split()[0], name)
            self.assertNotEqual(old.split()[1:], new.split()[1:])
        argv = (self.root / "ssh-argv").read_text().splitlines()
        self.assertEqual(argv[:3], ["sh", "-s", "--"])
        self.assertEqual(argv[3:], ["101", "10", "102", "10", "103", "10", "104", "10"])
        self.assertNotIn("pkill", (self.root / "ssh-stdin").read_text())
        self.assertEqual((self.root / "recovery").read_text().splitlines(), ["ppp", "dns", "firewall", "kernel"])

    def test_invalid_or_multiple_identity_output_is_rejected_before_dispatch(self):
        for identity in ("1 10", "101 10\n102 20", "101;echo bad 10", "101"):
            with self.subTest(identity=identity):
                result = self.shell('router_identity() { printf "%s\\n" "$BAD_ID"; }\nmass_kill_identity routerd', BAD_ID=identity)
                self.assertNotEqual(result.returncode, 0)


@unittest.skipUnless(sys.platform.startswith("linux"), "requires real Linux /proc and processes")
class MassKillLinuxTests(MassKillHarness):
    def start(self, binary):
        args = binary if isinstance(binary, list) else [str(binary), *SLEEP_ARGS]
        process = subprocess.Popen(args, start_new_session=True)
        def cleanup():
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
        self.addCleanup(cleanup)
        return process

    @staticmethod
    def identity(process):
        fields = pathlib.Path(f"/proc/{process.pid}/stat").read_text().rsplit(") ", 1)[1].split()
        return f"{process.pid} {fields[19]}"

    def fixture_processes(self):
        processes = [self.start(sys.executable) for _ in NAMES]
        return processes, [self.identity(p) for p in processes]

    def dispatch(self, identities):
        return self.shell('rsh() { "$@"; }\nmass_kill_dispatch ' +
                          " ".join(shlex.quote(x) for x in identities))

    def test_numeric_batch_really_kills_all_four_and_leaves_a_distractor_alive(self):
        processes, identities = self.fixture_processes()
        distractor = self.start(["sh", "-c", "sleep 60 & wait", "ssh-observer",
                                 'pkill -9 -f "slots/.*/bin/routerd-amd64"; pkill -9 dnsmasq'])
        former_matches = subprocess.run(["pgrep", "-f", "slots/.*/bin/routerd-amd64"],
                                        text=True, capture_output=True, check=True)
        self.assertIn(str(distractor.pid), former_matches.stdout.splitlines())
        result = self.dispatch(identities)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(), "mass-kill dispatched: 4")
        for process in processes:
            self.assertEqual(process.wait(timeout=5), -signal.SIGKILL)
        self.assertIsNone(distractor.poll())

    def test_changed_starttime_refuses_the_entire_batch(self):
        processes, identities = self.fixture_processes()
        pid, started = identities[2].split()
        identities[2] = f"{pid} {int(started) + 1}"
        result = self.dispatch(identities)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(all(p.poll() is None for p in processes))

    def test_duplicate_target_refuses_the_entire_batch(self):
        processes, identities = self.fixture_processes()
        identities[3] = identities[2]
        result = self.dispatch(identities)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(all(p.poll() is None for p in processes))

    def test_wait_observes_the_last_delayed_replacement(self):
        # BusyBox pgrep -x can miss a real daemon with an absolute argv[0].
        for comm in pathlib.Path("/proc").glob("[0-9]*/comm"):
            try:
                name = comm.read_text().strip()
            except (FileNotFoundError, ProcessLookupError, PermissionError):
                continue
            if name in ("dnsmasq", "pppd"):
                self.skipTest(f"requires a fixture environment without an existing {name}")
        slot_root = self.root / "slots"
        bin_dir = slot_root / "fixture/bin"
        bin_dir.mkdir(parents=True)
        binaries = []
        for name in NAMES:
            binary = bin_dir / (name + "-amd64" if name.startswith("router") else name)
            # Use a native executable whose behavior does not depend on the
            # symlink name; Alpine's sleep commands are multicall applets.
            binary.symlink_to(sys.executable)
            binaries.append(binary)
        old_processes = [self.start(binary) for binary in binaries]
        identities = [self.identity(p) for p in old_processes]
        for process in old_processes:
            process.kill()
            process.wait(timeout=5)
        replacements = []
        def replace():
            time.sleep(0.2)
            replacements.extend(self.start(binary) for binary in binaries[:3])
            time.sleep(0.5)
            replacements.append(self.start(binaries[3]))
        worker = threading.Thread(target=replace)
        worker.start()
        script = r'''
router_identity() { sh "$MR_TEST_OBSERVER" "$1" "$MR_SLOT_ROOT"; }
router_restarted_ready() { router_identity "$1"; }
wait_for 8 mass_kill_restarted "$LOGS/process-mass-kill-after.txt" ''' + " ".join(shlex.quote(x) for x in identities)
        started = time.monotonic()
        result = self.shell(script, MR_SLOT_ROOT=slot_root.as_posix())
        worker.join(timeout=5)
        self.assertFalse(worker.is_alive())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertGreaterEqual(time.monotonic() - started, 0.7)
        after = (self.root / "logs/process-mass-kill-after.txt").read_text().splitlines()
        self.assertEqual([int(line.split()[1]) for line in after], [p.pid for p in replacements])


if __name__ == "__main__":
    unittest.main()

"""The daemon observer must not mistake an SSH shell for the running binary."""
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest


OBSERVER = pathlib.Path(__file__).with_name("process-identity.sh")
SLEEP_ARGS = ("-I", "-c", "import time; time.sleep(60)")


@unittest.skipUnless(sys.platform.startswith("linux"), "requires real Linux /proc and pgrep")
class ProcessIdentityTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(shutil.which("pgrep"), "Linux CI must provide pgrep")
        self.temp = tempfile.TemporaryDirectory(prefix="minimalrouter-process-")
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / "slots"
        self.binary = self.root / "fixture" / "bin" / "routerd-amd64"
        self.binary.parent.mkdir(parents=True)
        # Python's native executable accepts a renamed symlink. Both BusyBox
        # and Alpine GNU coreutils sleep select an applet from argv[0].
        self.binary.symlink_to(sys.executable)

    def start(self, args, **kwargs):
        process = subprocess.Popen(args, start_new_session=True, **kwargs)
        def stop():
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=5)
        self.addCleanup(stop)
        return process

    def observe(self, name="routerd", env=None):
        return subprocess.run(["sh", str(OBSERVER), name, str(self.root)],
                              text=True, capture_output=True, timeout=10, env=env)

    def require_no_system_daemon(self, name):
        # The fixture precondition must also work with BusyBox pgrep. Read the
        # kernel name, including for a daemon launched with an absolute argv[0].
        for comm in pathlib.Path("/proc").glob("[0-9]*/comm"):
            try:
                existing = comm.read_text().strip()
            except (FileNotFoundError, ProcessLookupError, PermissionError):
                continue
            if existing == name:
                self.skipTest(f"requires a fixture environment without an existing {name}")

    def test_remote_shell_with_lower_pid_is_not_the_daemon(self):
        # This is the command shape that the former remote pgrep observer
        # matched. Create it first so it has the lower PID, like PID wrap in
        # the long-running load test. It stays a shell while sleep runs.
        shell = self.start(["sh", "-c", "sleep 60 & wait", "probe",
                            "pgrep -f 'slots/.*/bin/routerd-amd64' | head -n 1"])
        daemon = self.start([str(self.binary), *SLEEP_ARGS])
        old = subprocess.run(["pgrep", "-f", "slots/.*/bin/routerd-amd64"],
                             text=True, capture_output=True, check=True)
        self.assertIn(str(shell.pid), old.stdout.splitlines())
        result = self.observe()
        self.assertEqual(result.returncode, 0, result.stderr)
        pid, started = result.stdout.split()
        self.assertEqual(int(pid), daemon.pid)
        self.assertRegex(started, r"^[0-9]+$")
        self.assertNotEqual(int(pid), shell.pid)

    def test_a_missing_or_ambiguous_daemon_is_failure(self):
        self.assertNotEqual(self.observe().returncode, 0)
        self.start([str(self.binary), *SLEEP_ARGS])
        self.start([str(self.binary), *SLEEP_ARGS])
        result = self.observe()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("multiple matching processes", result.stderr)

    def test_real_restart_changes_identity(self):
        first = self.start([str(self.binary), *SLEEP_ARGS])
        before = self.observe()
        self.assertEqual(before.returncode, 0, before.stderr)
        first.terminate()
        first.wait(timeout=5)
        self.start([str(self.binary), *SLEEP_ARGS])
        after = self.observe()
        self.assertEqual(after.returncode, 0, after.stderr)
        self.assertNotEqual(before.stdout, after.stdout)

    def test_invalid_daemon_name_cannot_become_a_pattern(self):
        self.assertNotEqual(self.observe(".*").returncode, 0)

    def test_system_daemons_require_exact_comm_and_one_live_process(self):
        for name in ("dnsmasq", "pppd"):
            self.require_no_system_daemon(name)
            with self.subTest(name=name):
                self.assertNotEqual(self.observe(name).returncode, 0)
                # Both command text and a spoofed argv[0] must be ignored.
                self.start(["sh", "-c", "sleep 60 & wait", "probe", name])
                self.start([name, *SLEEP_ARGS], executable=sys.executable)
                self.assertNotEqual(self.observe(name).returncode, 0)
                binary = self.binary.with_name(name)
                binary.symlink_to(sys.executable)
                daemon = self.start([str(binary), *SLEEP_ARGS])
                self.assertEqual(pathlib.Path(f"/proc/{daemon.pid}/comm").read_text().strip(), name)
                result = self.observe(name)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(int(result.stdout.split()[0]), daemon.pid)
                self.start([str(binary), *SLEEP_ARGS])
                self.assertNotEqual(self.observe(name).returncode, 0)

    def test_system_daemon_discovery_does_not_depend_on_pgrep(self):
        mock_bin = pathlib.Path(self.temp.name) / "mock-bin"
        mock_bin.mkdir()
        calls = mock_bin / "pgrep-calls"
        pgrep = mock_bin / "pgrep"
        pgrep.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$MR_PGREP_CALLS"\nexit 1\n')
        pgrep.chmod(0o755)
        env = dict(os.environ, PATH=str(mock_bin) + os.pathsep + os.environ["PATH"],
                   MR_PGREP_CALLS=str(calls))
        for name in ("dnsmasq", "pppd"):
            self.require_no_system_daemon(name)
            with self.subTest(name=name):
                binary = self.binary.with_name(name)
                binary.symlink_to(sys.executable)
                daemon = self.start([str(binary), *SLEEP_ARGS])
                self.assertEqual(pathlib.Path(f"/proc/{daemon.pid}/comm").read_text().strip(), name)
                fields = pathlib.Path(f"/proc/{daemon.pid}/stat").read_text().rsplit(") ", 1)[1].split()
                result = self.observe(name, env=env)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), f"{daemon.pid} {fields[19]}")
        self.assertFalse(calls.exists(), "system-daemon discovery must not invoke pgrep")


if __name__ == "__main__":
    unittest.main()

"""The daemon observer must not mistake an SSH shell for the running binary."""
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest


OBSERVER = pathlib.Path(__file__).with_name("process-identity.sh")


@unittest.skipUnless(sys.platform.startswith("linux"), "requires real Linux /proc and pgrep")
class ProcessIdentityTests(unittest.TestCase):
    def setUp(self):
        self.assertIsNotNone(shutil.which("pgrep"), "Linux CI must provide pgrep")
        self.temp = tempfile.TemporaryDirectory(prefix="minimalrouter-process-")
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / "slots"
        self.binary = self.root / "fixture" / "bin" / "routerd-amd64"
        self.binary.parent.mkdir(parents=True)
        self.binary.symlink_to(shutil.which("sleep"))

    def start(self, args):
        process = subprocess.Popen(args)
        def stop():
            if process.poll() is None:
                process.terminate()
            process.wait(timeout=5)
        self.addCleanup(stop)
        return process

    def observe(self, name="routerd"):
        return subprocess.run(["sh", str(OBSERVER), name, str(self.root)],
                              text=True, capture_output=True, timeout=10)

    def test_remote_shell_with_lower_pid_is_not_the_daemon(self):
        # This is the command shape that the former remote pgrep observer
        # matched. Create it first so it has the lower PID, like PID wrap in
        # the long-running load test. It stays a shell while sleep runs.
        shell = self.start(["sh", "-c", "sleep 60 & wait", "probe",
                            "pgrep -f 'slots/.*/bin/routerd-amd64' | head -n 1"])
        daemon = self.start([str(self.binary), "60"])
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
        self.start([str(self.binary), "60"])
        self.start([str(self.binary), "60"])
        result = self.observe()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("multiple slot processes", result.stderr)

    def test_real_restart_changes_identity(self):
        first = self.start([str(self.binary), "60"])
        before = self.observe()
        self.assertEqual(before.returncode, 0, before.stderr)
        first.terminate()
        first.wait(timeout=5)
        self.start([str(self.binary), "60"])
        after = self.observe()
        self.assertEqual(after.returncode, 0, after.stderr)
        self.assertNotEqual(before.stdout, after.stdout)

    def test_invalid_daemon_name_cannot_become_a_pattern(self):
        self.assertNotEqual(self.observe(".*").returncode, 0)


if __name__ == "__main__":
    unittest.main()

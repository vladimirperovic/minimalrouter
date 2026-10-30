"""Active chaos observers must use real identities, never their SSH command text."""
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import scenarios


HERE = pathlib.Path(__file__).resolve().parent
OBSERVER = HERE / "process-state.sh"
BASH = ("C:/Program Files/Git/bin/bash.exe" if sys.platform == "win32" else shutil.which("bash"))


class ProcessTransportTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(BASH and pathlib.Path(BASH).exists(), "Bash is required")
        self.temp = tempfile.TemporaryDirectory(prefix="minimalrouter-state-")
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.env = dict(os.environ, MR_TEST_DIR=self.root.as_posix(), MR_SOURCE_DIR=HERE.as_posix())

    def test_python_observer_streams_source_and_only_numeric_identity_reaches_guest(self):
        (self.root / "lib.sh").write_text('''router_identity() { printf '123 456\\n'; }
rsh() {
    printf '%s\\n' "$@" > "$MR_TEST_DIR/argv"
    cat > "$MR_TEST_DIR/stdin"
    printf '24576\\n'
}
''')
        (self.root / OBSERVER.name).write_text(OBSERVER.read_text())
        original = scenarios.command
        def command(*args, **kwargs):
            return original(BASH, *args[1:], **kwargs)
        lab = scenarios.Lab.__new__(scenarios.Lab)
        with mock.patch.object(scenarios, "HERE", self.root), mock.patch.object(scenarios, "command", command), \
                mock.patch.dict(os.environ, self.env):
            identity = lab.identity("routerd")
            self.assertEqual(lab.memory("routerd", identity), 24576)
        self.assertEqual((self.root / "argv").read_text().splitlines(), ["sh", "-s", "rss", "123", "456"])
        self.assertEqual((self.root / "stdin").read_text(), OBSERVER.read_text())

    def test_chaos_memory_rejects_changed_identity_bad_data_and_failed_transport(self):
        source = (HERE / "chaos.sh").read_text()
        function = "daemon_memory_mib() {" + source.split("daemon_memory_mib() {", 1)[1].split("\n}", 1)[0] + "\n}\n"
        script = '''HERE=$MR_SOURCE_DIR
router_identity() {
    if [ "$MODE" = changed ] && [ -e "$MR_TEST_DIR/read" ]; then echo '123 457'; else echo '123 456'; fi
}
rsh() {
    printf '%s\\n' "$@" > "$MR_TEST_DIR/argv"
    cat > "$MR_TEST_DIR/stdin"
    touch "$MR_TEST_DIR/read"
    case "$MODE" in failure) return 42 ;; missing) return 0 ;; malformed) echo invalid ;; *) echo 24576 ;; esac
}
''' + function + '\ndaemon_memory_mib routerd\n'
        for mode in ("success", "changed", "failure", "missing", "malformed"):
            with self.subTest(mode=mode):
                (self.root / "read").unlink(missing_ok=True)
                result = subprocess.run([BASH, "-c", script], env=dict(self.env, MODE=mode),
                                        text=True, capture_output=True, timeout=10)
                if mode == "success":
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(result.stdout.strip(), "24")
                else:
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual((self.root / "argv").read_text().splitlines(), ["sh", "-s", "rss", "123", "456"])
                self.assertEqual((self.root / "stdin").read_text(), OBSERVER.read_text())

    def test_memory_requires_same_full_identity_before_and_after_read(self):
        identity = ("123", "456")
        for observed, rss in (([("123", "457")], "1024"),
                              ([identity, ("123", "457")], "1024"),
                              ([identity], ""), ([identity], "not-a-number")):
            with self.subTest(observed=observed, rss=rss):
                lab = scenarios.Lab.__new__(scenarios.Lab)
                with mock.patch.object(lab, "identity", side_effect=observed), \
                        mock.patch.object(lab, "process_state", return_value=rss):
                    with self.assertRaises(RuntimeError):
                        lab.memory("routerd", identity)

    def test_invalid_identity_and_action_cannot_reach_transport(self):
        lab = scenarios.Lab.__new__(scenarios.Lab)
        with mock.patch.object(scenarios, "command") as command:
            for action, identity in (("KILL", ("123", "456")), ("rss", ("1", "456")),
                                     ("rss", ("123;echo", "456")), ("rss", ("123", ""))):
                with self.subTest(action=action, identity=identity), self.assertRaises(ValueError):
                    lab.process_state(action, identity)
            command.assert_not_called()


@unittest.skipUnless(sys.platform.startswith("linux"), "requires real Linux /proc and signals")
class ProcessStateLinuxTests(unittest.TestCase):
    def setUp(self):
        self.process = subprocess.Popen([sys.executable, "-I", "-u", "-c",
                                         "import time; data=b'x'*(24*1024*1024); print('ready'); time.sleep(60)"],
                                        stdout=subprocess.PIPE, text=True, start_new_session=True)
        self.addCleanup(self.cleanup)
        self.assertEqual(self.process.stdout.readline().strip(), "ready")
        self.identity = self.read_identity()

    def cleanup(self):
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGKILL)
        self.process.wait(timeout=5)
        self.process.stdout.close()

    def read_identity(self):
        fields = pathlib.Path(f"/proc/{self.process.pid}/stat").read_text().rsplit(") ", 1)[1].split()
        return str(self.process.pid), fields[19]

    def observe(self, action, identity=None, env=None):
        return subprocess.run(["sh", str(OBSERVER), action, *(identity or self.identity)],
                              text=True, capture_output=True, timeout=10, env=env)

    def test_real_rss_is_measured_and_stale_starttime_is_rejected(self):
        result = self.observe("rss")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertGreater(int(result.stdout), 24 * 1024)
        stale = (self.identity[0], str(int(self.identity[1]) + 1))
        self.assertNotEqual(self.observe("rss", stale).returncode, 0)
        self.assertNotEqual(self.observe("STOP", stale).returncode, 0)
        self.assertIsNone(self.process.poll())
        state = pathlib.Path(f"/proc/{self.process.pid}/stat").read_text().rsplit(") ", 1)[1].split()[0]
        self.assertNotEqual(state, "T", "stale identity must not stop the process")

    def test_stop_and_continue_target_the_identified_process(self):
        self.assertEqual(self.observe("STOP").returncode, 0)
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            fields = pathlib.Path(f"/proc/{self.process.pid}/stat").read_text().rsplit(") ", 1)[1].split()
            if fields[0] == "T":
                break
            time.sleep(0.01)
        self.assertEqual(fields[0], "T")
        self.assertEqual(self.observe("CONT").returncode, 0)
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            fields = pathlib.Path(f"/proc/{self.process.pid}/stat").read_text().rsplit(") ", 1)[1].split()
            if fields[0] != "T":
                break
            time.sleep(0.01)
        self.assertNotIn(fields[0], ("T", "Z", "X"), "CONT must resume the same live process")
        self.assertEqual(self.read_identity(), self.identity)

    def test_missing_rss_and_exit_during_read_cannot_pass(self):
        with tempfile.TemporaryDirectory(prefix="minimalrouter-rss-") as directory:
            root = pathlib.Path(directory)
            awk = root / "awk"
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       MR_TARGET=self.identity[0])
            # Execute the actual observer with a controlled /proc-read boundary.
            awk.write_text("#!/bin/sh\nexit 0\n")
            awk.chmod(0o755)
            self.assertNotEqual(self.observe("rss", env=env).returncode, 0)
            awk.write_text('''#!/bin/sh
kill -KILL "$MR_TARGET"
while IFS= read -r stat < "/proc/$MR_TARGET/stat"; do
    fields=${stat##*) }
    case "$fields" in 'Z '*|'X '*) break ;; esac
    sleep 0.01
done
printf '24576\\n'
''')
            self.assertNotEqual(self.observe("rss", env=env).returncode, 0)


if __name__ == "__main__":
    unittest.main()

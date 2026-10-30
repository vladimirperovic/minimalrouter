"""Run the real Bash restart cases against a deterministic supervisor timeline.

All SSH, process, listener and HTTP observations are local fixtures. The stale
socket and readiness files deliberately survive SIGKILL, just as in the lab.
"""
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest


HERE = pathlib.Path(__file__).resolve().parent
BASH = os.environ.get("LAB_TEST_BASH") or shutil.which("bash")

HARNESS = r'''
set -u
. "$1"
# A deterministic clock in this test shell, not a wall-clock sleep or a change
# to the appliance clock. SECONDS loses its special property after unset.
unset SECONDS
SECONDS=0
sleep() { SECONDS=$((SECONDS + $1)); printf '%s\n' "$SECONDS" >> "$LAB_RUN/sleeps"; }
router_identity() {
    local calls=0 started=$NEW_START
    [ ! -f "$LAB_RUN/identity-calls" ] || read -r calls < "$LAB_RUN/identity-calls"
    calls=$((calls + 1))
    printf '%s\n' "$calls" > "$LAB_RUN/identity-calls"
    [ "$MODE" != missing_initial ] || return 1
    if [ ! -f "$LAB_RUN/killed" ]; then
        printf '%s %s\n' "$OLD_PID" "$OLD_START"
        return 0
    fi
    [ "$MODE" != missing ] && [ "$SECONDS" -ge "$RESPAWN_AT" ] || return 1
    case "$MODE" in
        old) printf '%s %s\n' "$OLD_PID" "$OLD_START"; return 0 ;;
        malformed) printf '%s invalid-starttime\n' "$NEW_PID"; return 0 ;;
        unstable) started=$((NEW_START + calls)) ;;
    esac
    printf '%s %s\n' "$NEW_PID" "$started"
}
rsh() {
    if [[ "$*" == kill\ -9\ * ]]; then
        printf '%s\n' "$*" >> "$LAB_RUN/kills"
        [ "$MODE" != kill_failed ] || return 1
        printf yes > "$LAB_RUN/killed"
        return 0
    fi
    if [ "$1" = sh ] && [ "${2:-}" = -s ]; then
        # Execute the actual stdin readiness script with the fixture commands.
        ( . /dev/stdin "${3:-}" "${4:-}" )
    else
        eval "$*"
    fi
}
test() {
    case "$*" in
        '-s /run/minimalrouter/routerd.ready') [ "$MODE" != missing_marker ] ;;
        '-S /run/minimalrouter/apply.sock') return 0 ;;
        *) builtin test "$@" ;;
    esac
}
rc-service() { return 0; }
ss() {
    local owner=$NEW_PID endpoint peer state=LISTEN
    [ "$MODE" != no_listener ] || return 0
    [ "$SECONDS" -ge "$LISTEN_AT" ] || owner=$OLD_PID
    [ "$MODE" != wrong_owner ] || owner=$OLD_PID
    [ "$MODE" != pid_prefix ] || owner=${NEW_PID}0
    [ "$MODE" != not_listening ] || state=CLOSE
    if [ "$DAEMON" = router-applyd ]; then
        endpoint=/run/minimalrouter/apply.sock
        peer='*'
        [ "$MODE" != wrong_endpoint ] || endpoint=${endpoint}.other
        if [ "$MODE" = peer_endpoint ]; then peer=$endpoint; endpoint=${endpoint}.other; fi
        printf 'u_str %s 0 128 %s 999 %s 0 users:(("helper",pid=%s,fd=3))\n' "$state" "$endpoint" "$peer" "$owner"
    else
        endpoint=192.168.1.1:8443
        peer='0.0.0.0:*'
        [ "$MODE" != wrong_endpoint ] || endpoint=192.168.1.1:18443
        if [ "$MODE" = peer_endpoint ]; then peer=$endpoint; endpoint=192.168.1.1:18443; fi
        printf '%s 0 128 %s %s users:(("routerd",pid=%s,fd=3))\n' "$state" "$endpoint" "$peer" "$owner"
    fi
    [ "$MODE" != ss_error ]
}
curl() {
    printf '%s\n' "$SECONDS" >> "$LAB_RUN/http-probes"
    [ "$MODE" != http_error ] || return 1
    if [ "$MODE" = http_unready ] || [ "$SECONDS" -lt "$HTTP_AT" ]; then
        printf 503
    else
        printf 200
    fi
}
firewall_fingerprint() {
    if [ "$MODE" = firewall_changed ] && [ -f "$LAB_RUN/killed" ]; then
        echo changed
    else
        echo unchanged
    fi
}
api() {
    printf '%s\n' "$*" >> "$LAB_RUN/api-calls"
    if [ "$MODE" = api_unhealthy ]; then echo 'HTTP 500'; else echo 'HTTP 200'; fi
}
ip() {
    [ "$MODE" != ppp_down ] || return 1
    echo '7: ppp0 inet 10.250.0.50/32'
}
http_code() { api call "$@" | head -n 1 | awk '{print $2}'; }
'''


class RestartWaitTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if BASH is None:
            raise RuntimeError("Bash is required for the lab restart regressions")
        source = (HERE / "suite.sh").read_text(encoding="utf-8")
        # Source just the real functions, without executing the entire VM suite.
        functions = re.findall(r"(?ms)^t_kill_(?:routerd|applyd)\(\) \{.*?^\}", source)
        if len(functions) != 2:
            raise RuntimeError("expected both actual daemon restart test functions")
        cls.functions = "\n".join(functions)

    def run_case(self, daemon, mode="delayed", *, respawn=5, listen=8,
                 http=10, new_pid=202, command=None):
        with tempfile.TemporaryDirectory(prefix="mr-restart-wait-") as directory:
            env = os.environ.copy()
            env.update(LAB_DIR=directory, DAEMON=daemon, MODE=mode,
                       OLD_PID="101", OLD_START="1000", NEW_PID=str(new_pid),
                       NEW_START="2000", RESPAWN_AT=str(respawn),
                       LISTEN_AT=str(listen), HTTP_AT=str(http))
            function = "t_kill_applyd" if daemon == "router-applyd" else "t_kill_routerd"
            script = HARNESS + "\n" + self.functions + "\n" + (command or function)
            result = subprocess.run([BASH, "-c", script, "restart-test", str(HERE / "lib.sh")],
                                    env=env, text=True, capture_output=True, timeout=30)
            run = pathlib.Path(directory) / "run"
            evidence = {name: (run / name).read_text().splitlines() if (run / name).exists() else []
                        for name in ("sleeps", "kills", "api-calls", "http-probes")}
            return result, evidence

    def test_applyd_waits_through_stale_socket_and_delayed_respawn(self):
        result, evidence = self.run_case("router-applyd")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(evidence["sleeps"][-1], "10")
        self.assertEqual(evidence["kills"], ["kill -9 101"])
        self.assertIn("call GET health", evidence["api-calls"])
        self.assertIn("101 1000", result.stdout)
        self.assertIn("202 2000", result.stdout)

    def test_routerd_waits_through_stale_marker_and_delayed_respawn(self):
        result, evidence = self.run_case("routerd")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(evidence["sleeps"][-1], "10")
        self.assertIn("login", evidence["api-calls"])

    def test_same_pid_with_new_starttime_is_a_real_restart(self):
        for daemon in ("routerd", "router-applyd"):
            with self.subTest(daemon=daemon):
                result, _ = self.run_case(daemon, new_pid=101)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("101 1000", result.stdout)
                self.assertIn("101 2000", result.stdout)

    def test_missing_or_old_process_waits_until_the_original_deadline(self):
        for daemon, deadline in (("routerd", 120), ("router-applyd", 180)):
            for mode in ("missing", "old"):
                with self.subTest(daemon=daemon, mode=mode):
                    result, evidence = self.run_case(daemon, mode, respawn=0, listen=0, http=0)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(evidence["sleeps"][-1], str(deadline))
                    self.assertEqual(evidence["http-probes"], [])

    def test_live_process_with_stale_files_or_unready_http_cannot_pass(self):
        for daemon in ("routerd", "router-applyd"):
            for mode in ("no_listener", "wrong_owner", "pid_prefix", "wrong_endpoint", "peer_endpoint", "not_listening",
                         "missing_marker", "http_unready", "http_error", "malformed", "unstable"):
                with self.subTest(daemon=daemon, mode=mode):
                    # Exercise the shared predicate under a short deadline;
                    # the separate test above proves the suite's 120/180s limits.
                    command = "printf yes > \"$LAB_RUN/killed\"; wait_for_router_restart 4 \"$DAEMON\" '101 1000'"
                    result, evidence = self.run_case(daemon, mode, respawn=0, listen=0, http=0, command=command)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertEqual(evidence["sleeps"][-1], "4")

    def test_a_missing_initial_process_or_failed_kill_is_not_success(self):
        for daemon in ("routerd", "router-applyd"):
            for mode in ("missing_initial", "kill_failed"):
                with self.subTest(daemon=daemon, mode=mode):
                    result, evidence = self.run_case(daemon, mode)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(evidence["sleeps"], [])
                    if mode == "missing_initial":
                        self.assertEqual(evidence["kills"], [])

    def test_failed_ss_with_valid_looking_output_is_rejected(self):
        for daemon in ("routerd", "router-applyd"):
            with self.subTest(daemon=daemon):
                command = "printf yes > \"$LAB_RUN/killed\"; wait_for_router_restart 4 \"$DAEMON\" '101 1000'"
                result, evidence = self.run_case(daemon, "ss_error", respawn=0, listen=0, http=0, command=command)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual(evidence["sleeps"][-1], "4")
                self.assertEqual(evidence["http-probes"], [])

    def test_post_restart_firewall_api_and_pppoe_checks_are_still_required(self):
        for daemon, mode in (("routerd", "firewall_changed"), ("router-applyd", "firewall_changed"),
                             ("router-applyd", "api_unhealthy"), ("routerd", "ppp_down")):
            with self.subTest(daemon=daemon, mode=mode):
                result, evidence = self.run_case(daemon, mode, respawn=0, listen=0, http=0)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertNotEqual(evidence["http-probes"], [])


if __name__ == "__main__":
    unittest.main()

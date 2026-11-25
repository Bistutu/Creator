import contextlib
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from tools import local_check


class LocalCheckTest(unittest.TestCase):
    def test_second_run_is_refused_until_lock_is_released(self):
        with tempfile.TemporaryDirectory() as tmp:
            lock = Path(tmp) / "check.lock"
            with local_check.single_check(lock):
                with self.assertRaisesRegex(local_check.CheckStopped, "Another local Creator check"):
                    with local_check.single_check(lock):
                        self.fail("a second check took the lock")
            with local_check.single_check(lock):
                self.assertTrue(lock.is_file())

    def test_overloaded_host_refuses_to_start_a_process(self):
        with patch.object(local_check.os, "getloadavg", return_value=(2.0, 2.0, 2.0)), \
             patch.object(local_check.os, "cpu_count", return_value=2), \
             patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
             patch.object(local_check.subprocess, "Popen") as spawn:
            with self.assertRaisesRegex(local_check.CheckStopped, "Host load"):
                local_check.local_check(["unused"], 5)
            spawn.assert_not_called()

    def test_native_check_waits_before_preparation_and_before_the_suite(self):
        for options in ({"deskpad": True}, {"instance": "creator-check"}):
            with self.subTest(**options), tempfile.TemporaryDirectory() as home:
                elapsed = 0.0
                prepared = False

                def advance(seconds):
                    nonlocal elapsed
                    elapsed += seconds

                def host_load():
                    busy = elapsed < 2 or (prepared and elapsed < 4)
                    return (4.2 if busy else 3.9, 0, 0)

                def prepare(*_args, **_kwargs):
                    nonlocal prepared
                    self.assertGreaterEqual(elapsed, 2, "native work started before host load fell")
                    prepared = True
                    return 0

                def start_check(command, env, deadline, _check, **_kwargs):
                    if command[0] == "limactl":
                        return prepare()
                    self.assertEqual(elapsed, 4, "the suite did not wait for load created during preparation")
                    self.assertEqual(deadline, 10, "the post-preparation wait must use the existing time budget")
                    if options.get("deskpad"):
                        self.assertEqual(env["WEBCRATE_MAC_TEST_START_LOAD"], "4.0")
                    return 0

                with patch.object(local_check.os, "cpu_count", return_value=10), \
                     patch.object(local_check.os, "getloadavg", side_effect=host_load), \
                     patch.object(local_check.time, "monotonic", side_effect=lambda: elapsed), \
                     patch.object(local_check.time, "sleep", side_effect=advance), \
                     patch.object(local_check.Path, "home", return_value=Path(home)), \
                     patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
                     patch.object(local_check, "check_host"), \
                     patch.object(local_check.os, "nice"), \
                     patch.object(local_check, "prepare_deskpad", side_effect=prepare), \
                     patch.object(local_check, "stop_deskpad_fixtures"), \
                     patch.object(local_check, "require_idle_guest"), \
                     patch.object(local_check, "stop_guest"), \
                     patch.object(local_check, "run", side_effect=start_check):
                    self.assertEqual(local_check.local_check(["fixture"], 8, **options), 0)

    def test_native_wait_after_boot_respects_deadline_and_stops_the_guest(self):
        elapsed = 0.0
        booted = False

        def start_guest(*_args, **_kwargs):
            nonlocal booted, elapsed
            booted = True
            elapsed = 4
            return 0

        def advance(seconds):
            nonlocal elapsed
            elapsed += seconds

        with patch.object(local_check.os, "cpu_count", return_value=10), \
             patch.object(local_check.os, "getloadavg", side_effect=lambda: (5.0 if booted else 3.0, 0, 0)), \
             patch.object(local_check.time, "monotonic", side_effect=lambda: elapsed), \
             patch.object(local_check.time, "sleep", side_effect=advance), \
             patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
             patch.object(local_check, "check_host"), \
             patch.object(local_check.os, "nice"), \
             patch.object(local_check, "require_idle_guest"), \
             patch.object(local_check, "stop_guest") as stop, \
             patch.object(local_check, "run", side_effect=start_guest) as run:
            with self.assertRaises(local_check.CheckStopped) as stopped:
                local_check.local_check(["fixture"], 5, instance="creator-check")
            self.assertEqual(stopped.exception.status, 124)
            self.assertEqual(elapsed, 5)
            self.assertEqual([call.args[0] for call in run.call_args_list], [
                ["limactl", "start", "--tty=false", "creator-check"],
            ])
            stop.assert_called_once_with("creator-check")

    def test_deskpad_preparation_pressure_prevents_the_suite_from_starting(self):
        prepared = None

        def prepare(workspace, _env, _deadline, _check):
            nonlocal prepared
            prepared = Path(workspace)

        disk = SimpleNamespace(free=8 * 1024**3)
        with tempfile.TemporaryDirectory() as home, \
             patch.object(local_check.os, "cpu_count", return_value=10), \
             patch.object(local_check.os, "getloadavg", side_effect=lambda: (8.0 if prepared else 3.0, 0, 0)), \
             patch.object(local_check.shutil, "disk_usage", return_value=disk), \
             patch.object(local_check.sys, "platform", "linux"), \
             patch.object(local_check.Path, "home", return_value=Path(home)), \
             patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
             patch.object(local_check.os, "nice"), \
             patch.object(local_check, "prepare_deskpad", side_effect=prepare), \
             patch.object(local_check, "run") as run:
            with self.assertRaisesRegex(local_check.CheckStopped, "Host load is 8.0") as stopped:
                local_check.local_check(["fixture"], 5, deskpad=True)
            self.assertEqual(stopped.exception.status, 75)
            run.assert_not_called()
            self.assertFalse(prepared.exists())
            self.assertFalse((Path(home) / "Applications").exists())

    def test_native_check_exits_without_preparing_a_guest_when_load_stays_high(self):
        with patch.object(local_check.os, "cpu_count", return_value=10), \
             patch.object(local_check.os, "getloadavg", return_value=(4.2, 0, 0)), \
             patch.object(local_check.time, "monotonic", side_effect=[0, local_check.NATIVE_START_WAIT]), \
             patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
             patch.object(local_check.tempfile, "TemporaryDirectory") as scratch, \
             patch.object(local_check, "require_idle_guest") as prepare, \
             patch.object(local_check, "run") as run:
            with self.assertRaisesRegex(local_check.CheckStopped, "Host load stayed at or above 4.0") as stopped:
                local_check.local_check(["fixture"], 8, instance="creator-check")
            self.assertEqual(stopped.exception.status, 75)
            scratch.assert_not_called()
            prepare.assert_not_called()
            run.assert_not_called()

    def test_native_check_can_be_interrupted_while_waiting_for_headroom(self):
        original_handler = signal.getsignal(signal.SIGINT)

        def interrupt(_):
            signal.getsignal(signal.SIGINT)(signal.SIGINT, None)

        with patch.object(local_check.os, "cpu_count", return_value=10), \
             patch.object(local_check.os, "getloadavg", return_value=(4.2, 0, 0)), \
             patch.object(local_check.time, "sleep", side_effect=interrupt), \
             patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
             patch.object(local_check, "prepare_deskpad") as prepare, \
             patch.object(local_check, "run") as run:
            with self.assertRaisesRegex(local_check.CheckStopped, "interrupted") as stopped:
                local_check.local_check(["fixture"], 8, deskpad=True)
            self.assertEqual(stopped.exception.status, 130)
            self.assertIs(signal.getsignal(signal.SIGINT), original_handler)
            prepare.assert_not_called()
            run.assert_not_called()

    def test_limited_disk_and_memory_refuse_to_start(self):
        disk = SimpleNamespace(free=1024**3)
        with patch.object(local_check.os, "getloadavg", return_value=(0, 0, 0)), \
             patch.object(local_check.shutil, "disk_usage", return_value=disk):
            with self.assertRaisesRegex(local_check.CheckStopped, "free scratch space"):
                local_check.check_host(Path("/tmp"))
        disk = SimpleNamespace(free=8 * 1024**3)
        pressure = subprocess.CompletedProcess([], 0, "System-wide memory free percentage: 19%\n")
        with patch.object(local_check.os, "getloadavg", return_value=(0, 0, 0)), \
             patch.object(local_check.shutil, "disk_usage", return_value=disk), \
             patch.object(local_check.sys, "platform", "darwin"), \
             patch.object(local_check.subprocess, "run", return_value=pressure):
            with self.assertRaisesRegex(local_check.CheckStopped, "19% available memory"):
                local_check.check_host(Path("/tmp"))

    def test_non_native_check_can_run_below_full_cpu_load(self):
        disk = SimpleNamespace(free=8 * 1024**3)
        with patch.object(local_check.os, "cpu_count", return_value=10), \
             patch.object(local_check.os, "getloadavg", return_value=(8.0, 0, 0)), \
             patch.object(local_check.shutil, "disk_usage", return_value=disk), \
             patch.object(local_check.sys, "platform", "linux"):
            local_check.check_host(Path("/tmp"))

    def test_expired_deadline_does_not_spawn(self):
        with patch.object(local_check.subprocess, "Popen") as spawn:
            with self.assertRaises(local_check.CheckStopped) as stopped:
                local_check.run(["unused"], {}, time.monotonic() - 1, lambda: None)
            self.assertEqual(stopped.exception.status, 124)
            spawn.assert_not_called()

    def test_macos_group_permission_error_still_checks_for_live_members(self):
        process = SimpleNamespace(pid=1234)
        with patch.object(local_check.sys, "platform", "darwin"), \
             patch.object(local_check.os, "killpg", side_effect=PermissionError), \
             patch.object(local_check.subprocess, "run") as list_processes:
            list_processes.return_value = subprocess.CompletedProcess([], 0, "1234 Z\n2345 S\n")
            self.assertFalse(local_check.send_group(process, 0))
            list_processes.return_value = subprocess.CompletedProcess([], 0, "1234 S\n2345 S\n")
            with self.assertRaises(PermissionError):
                local_check.send_group(process, 0)

    def test_cleanup_on_success_and_timeout_only_targets_the_check_group(self):
        worker = """
import pathlib, signal, sys
marker = pathlib.Path(sys.argv[1])
def stopped(*_):
    marker.write_text('terminated')
    raise SystemExit(0)
signal.signal(signal.SIGTERM, stopped)
marker.write_text('ready')
signal.pause()
"""
        launcher = """
import pathlib, signal, subprocess, sys, time
subprocess.Popen([sys.executable, '-c', sys.argv[1], sys.argv[2]])
marker = pathlib.Path(sys.argv[2])
while not marker.exists():
    time.sleep(0.01)
if sys.argv[3] != 'success':
    signal.pause()
"""
        with subprocess.Popen([sys.executable, "-c", "import signal; signal.pause()"],
                              start_new_session=True) as unrelated:
            try:
                for case in ("success", "timeout", "pressure"):
                    with self.subTest(case=case), tempfile.TemporaryDirectory() as tmp:
                        marker = Path(tmp) / "worker-state"
                        command = [sys.executable, "-c", launcher, worker, str(marker), case]
                        deadline = time.monotonic() + (5 if case == "pressure" else 2)

                        def check():
                            if case == "pressure" and marker.is_file():
                                with patch.object(local_check.os, "cpu_count", return_value=10), \
                                     patch.object(local_check.os, "getloadavg", return_value=(8.0, 0, 0)):
                                    local_check.check_host(Path(tmp), native=True)

                        if case in ("timeout", "pressure"):
                            with self.assertRaises(local_check.CheckStopped) as stopped:
                                local_check.run(command, os.environ, deadline, check)
                            self.assertEqual(stopped.exception.status, 75 if case == "pressure" else 124)
                        else:
                            self.assertEqual(local_check.run(command, os.environ, deadline, lambda: None), 0)
                        self.assertEqual(marker.read_text(), "terminated")
                        self.assertIsNone(unrelated.poll())
            finally:
                unrelated.terminate()

    def test_deskpad_reuses_running_display_with_its_current_mode(self):
        with tempfile.TemporaryDirectory() as tmp, \
             patch.object(local_check.sys, "platform", "darwin"), \
             patch.object(local_check.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)), \
             patch.object(local_check.subprocess, "Popen") as spawn, \
             patch.object(local_check, "run", return_value=0) as run:
            env = {}
            local_check.prepare_deskpad(tmp, env, time.monotonic() + 5, lambda: None)
            probe = Path(tmp) / "mac-test-display"
            self.assertEqual(run.call_count, 2)
            self.assertEqual(run.call_args.args[0], [probe, "reuse-deskpad", "DeskPad Display"])
            self.assertEqual(env["WEBCRATE_MAC_TEST_DISPLAY"], "DeskPad Display")
            self.assertEqual(env["WEBCRATE_MAC_TEST_DISPLAY_TOOL"], str(probe))
            self.assertEqual(env["WEBCRATE_MAC_TEST_SCRATCH"], tmp)
            spawn.assert_not_called()

    def test_deskpad_fallback_survives_a_failed_test(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp) / "DeskPad.app"
            executable = app / "Contents/MacOS/DeskPad"
            executable.parent.mkdir(parents=True)
            executable.touch()
            with patch.object(local_check, "DESKPAD_APP", app), \
                 patch.object(local_check.os, "getloadavg", return_value=(0, 0, 0)), \
                 patch.object(Path, "home", return_value=Path(tmp)), \
                 patch.object(local_check.sys, "platform", "darwin"), \
                 patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
                 patch.object(local_check, "check_host"), \
                 patch.object(local_check.os, "nice"), \
                 patch.object(local_check.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)), \
                 patch.object(local_check.subprocess, "Popen") as spawn, \
                 patch.object(local_check, "run", side_effect=[0, 1, 0, 7]) as run, \
                 patch.object(local_check, "stop_group") as stop, \
                 patch.object(local_check, "stop_deskpad_fixtures") as cleanup:
                self.assertEqual(local_check.local_check(["failing-test"], 10, deskpad=True), 7)
                self.assertEqual(run.call_args_list[1].args[0], ["/usr/bin/open", "-g", "-a", app])
                self.assertEqual(run.call_args_list[2].args[0][1:], ["deskpad", "DeskPad Display"])
                spawn.assert_called_once_with([executable], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                              stderr=subprocess.DEVNULL, start_new_session=True)
                stop.assert_not_called()
                cleanup.assert_called_once()

    @unittest.skipUnless(sys.platform == "darwin", "requires the macOS process listing")
    def test_deskpad_timeout_stops_apps_launched_in_another_session(self):
        launcher = """
import os, pathlib, shutil, subprocess, sys
workspace = pathlib.Path(os.environ['TMPDIR'])
installed = pathlib.Path(os.environ['WEBCRATE_MAC_TEST_APPLICATIONS'])
domains = workspace / 'mac-test-domains'
domains.mkdir()
(domains / sys.argv[3]).touch()
runner = installed / 'Creator Fixture.app/Contents/MacOS/WebCrateRunner'
runner.parent.mkdir(parents=True)
shutil.copyfile(sys.argv[2], runner)
runner.chmod(0o700)
app = subprocess.Popen([runner], start_new_session=True)
pathlib.Path(sys.argv[1]).write_text(f'{app.pid}\\n{workspace}\\n{installed}')
app.wait()
"""
        with tempfile.TemporaryDirectory() as tmp, \
             subprocess.Popen(["/bin/sleep", "60"], start_new_session=True) as unrelated, \
             patch.object(local_check, "prepare_deskpad"), \
             patch.object(local_check, "single_check", return_value=contextlib.nullcontext()), \
             patch.object(local_check, "check_host"), \
             patch.object(local_check.os, "getloadavg", return_value=(0, 0, 0)), \
             patch.object(local_check.os, "nice"):
            marker = Path(tmp) / "fixture"
            executable = Path(tmp) / "runner"
            home = Path(tmp) / "user"
            domain = "com.webcrate.apps.app-0123456789abcdef"
            fixture_data = home / "Library/WebKit" / domain
            unrelated_data = home / "Library/WebKit/com.webcrate.apps.app-fedcba9876543210"
            fixture_data.mkdir(parents=True)
            unrelated_data.mkdir()
            subprocess.run(["/usr/bin/xcrun", "clang", "-x", "c", "-", "-o", executable],
                           input="#include <unistd.h>\nint main(void) { sleep(60); return 0; }\n",
                           text=True, check=True, timeout=10)
            try:
                with patch.object(Path, "home", return_value=home), self.assertRaises(local_check.CheckStopped) as stopped:
                    local_check.local_check([sys.executable, "-c", launcher, str(marker), str(executable), domain],
                                            2, deskpad=True)
                self.assertEqual(stopped.exception.status, 124)
                pid, workspace, installed = marker.read_text().splitlines()
                state = subprocess.run(["/bin/ps", "-p", pid, "-o", "stat="],
                                       capture_output=True, text=True, timeout=2).stdout.strip()
                self.assertTrue(not state or state.startswith("Z"), f"isolated application is still running ({state})")
                self.assertFalse(Path(workspace).exists())
                self.assertFalse(Path(installed).exists())
                self.assertFalse(fixture_data.exists())
                self.assertTrue(unrelated_data.is_dir())
                self.assertIsNone(unrelated.poll())
            finally:
                unrelated.terminate()
                if marker.is_file():
                    pid, _, installed = marker.read_text().splitlines()
                    command = subprocess.run(["/bin/ps", "-ww", "-p", pid, "-o", "comm="],
                                             capture_output=True, text=True, timeout=2).stdout.strip()
                    if command and Path(command).resolve().is_relative_to(Path(installed).resolve()):
                        with contextlib.suppress(ProcessLookupError):
                            os.kill(int(pid), signal.SIGKILL)

    def test_deskpad_cleanup_removes_only_claimed_preference_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            workspace = root / "check"
            domains = workspace / "mac-test-domains"
            domains.mkdir(parents=True)
            domain = "com.webcrate.apps.app-0123456789abcdef"
            untouched = "com.webcrate.apps.app-fedcba9876543210"
            (domains / domain).touch()
            (domains / untouched).symlink_to(domains / domain)
            preferences = home / "Library/Preferences"
            preferences.mkdir(parents=True)
            (preferences / (domain + ".plist")).touch()
            keep = preferences / (untouched + ".plist")
            keep.touch()
            with patch.object(Path, "home", return_value=home), \
                 patch.object(local_check.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)) as defaults:
                local_check.remove_deskpad_fixture_domains(workspace)
            defaults.assert_called_once_with(["/usr/bin/defaults", "delete", domain], capture_output=True, timeout=3)
            self.assertFalse((preferences / (domain + ".plist")).exists())
            self.assertTrue(keep.is_file())

    def test_guest_with_running_or_oversized_configuration_is_left_alone(self):
        guest = {
            "status": "Running", "vmType": "vz", "cpus": 1, "memory": 1024**3,
            "config": {"os": "Linux", "video": {"display": "none"}},
        }
        with patch.object(local_check, "guest_status", return_value=guest):
            with self.assertRaisesRegex(local_check.CheckStopped, "be stopped"):
                local_check.require_idle_guest("unused")
            guest.update(status="Stopped", cpus=2)
            with self.assertRaisesRegex(local_check.CheckStopped, "one CPU"):
                local_check.require_idle_guest("unused")
            guest.update(cpus=1)
            local_check.require_idle_guest("unused")

    def test_guest_stop_is_verified_and_falls_back_if_still_running(self):
        states = [{"status": "Running"}, {"status": "Running"}, {"status": "Stopped"}]
        with patch.object(local_check, "guest_status", side_effect=states), \
             patch.object(local_check.subprocess, "run") as command:
            local_check.stop_guest("fixture")
            self.assertEqual([call.args[0] for call in command.call_args_list], [
                ["limactl", "stop", "fixture"],
                ["limactl", "stop", "--force", "fixture"],
            ])


if __name__ == "__main__":
    unittest.main()

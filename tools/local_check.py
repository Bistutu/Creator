"""Limit local Creator checks and clean up the processes they start."""

import argparse
import contextlib
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time


DESKPAD_APP = Path("/Applications/DeskPad.app")
DESKPAD_DISPLAY = "DeskPad Display"
NATIVE_START_WAIT = 60
NATIVE_START_LOAD_RATIO = 0.4
NATIVE_RUN_LOAD_RATIO = 0.8


class CheckStopped(RuntimeError):
    def __init__(self, reason, status=75):
        super().__init__(reason)
        self.status = status


@contextlib.contextmanager
def single_check(path):
    flags = os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW
    with os.fdopen(os.open(path, flags, 0o600), "wb") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise CheckStopped("Another local Creator check is running.") from error
        yield


def check_host(directory, *, native=False):
    load = os.getloadavg()[0]
    cores = os.cpu_count() or 1
    limit = max(1.0, NATIVE_RUN_LOAD_RATIO * cores) if native else cores
    if load >= limit:
        raise CheckStopped(f"Host load is {load:.1f} across {cores} logical CPUs (limit {limit:.1f}).")
    if min(shutil.disk_usage(path).free for path in (directory, Path.cwd())) < 2 * 1024**3:
        raise CheckStopped("Local checks need 2 GiB of free scratch space.")
    if sys.platform == "darwin":
        result = subprocess.run(
            ["/usr/bin/memory_pressure", "-Q"], capture_output=True,
            text=True, timeout=4, check=True, env={**os.environ, "LC_ALL": "C"},
        )
        available = re.search(r"memory free percentage: (\d+)%", result.stdout)
        if available is None:
            raise CheckStopped("macOS did not report memory pressure.")
        if int(available[1]) < 20:
            raise CheckStopped(f"macOS reports {available[1]}% available memory.")


def wait_for_native_headroom(limit, check, deadline=None):
    wait_until = time.monotonic() + NATIVE_START_WAIT
    if deadline is not None:
        wait_until = min(wait_until, deadline)
    waiting = False
    while True:
        check()
        now = time.monotonic()
        if deadline is not None and now >= deadline:
            raise CheckStopped("The local check reached its time limit while waiting for host load.", 124)
        load = os.getloadavg()[0]
        if load < limit:
            if waiting:
                print(f"Continuing native check at host load {load:.1f}.", flush=True)
            return
        remaining = wait_until - now
        if remaining <= 0:
            raise CheckStopped(f"Host load stayed at or above {limit:.1f}.")
        if not waiting:
            print(f"Waiting up to {remaining:.1f} seconds for host load below {limit:.1f} "
                  f"(currently {load:.1f}).", flush=True)
            waiting = True
        time.sleep(min(1, remaining))


def send_group(process, action):
    try:
        os.killpg(process.pid, action)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        if sys.platform != "darwin":
            raise
        # An exited group can still return EPERM while macOS reaps it.
        # Preserve the error if any live member still needs cleanup.
        listing = subprocess.run(["/bin/ps", "-axo", "pgid=,state="], capture_output=True,
                                 text=True, timeout=3, check=True)
        for line in listing.stdout.splitlines():
            group, state = line.split(maxsplit=1)
            if group == str(process.pid) and not state.startswith("Z"):
                raise
        return False


def stop_group(process):
    end = time.monotonic() + 3
    if send_group(process, signal.SIGTERM):
        while time.monotonic() < end:
            process.poll()
            if not send_group(process, 0):
                break
            time.sleep(0.05)
        else:
            send_group(process, signal.SIGKILL)
    process.wait(timeout=3)


def run(command, env, deadline, check, keep_children=False):
    check()
    if time.monotonic() >= deadline:
        raise CheckStopped("The local check reached its time limit.", 124)
    process = subprocess.Popen(command, env=env, start_new_session=True)
    done = False
    try:
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise CheckStopped("The local check reached its time limit.", 124)
            try:
                result = process.wait(timeout=min(3, remaining))
                done = result == 0
                return result
            except subprocess.TimeoutExpired:
                if time.monotonic() < deadline:
                    check()
    finally:
        if not (done and keep_children):
            stop_group(process)


def prepare_deskpad(workspace, env, deadline, check):
    if sys.platform != "darwin":
        raise CheckStopped("DeskPad native checks require macOS.", 1)
    probe = Path(workspace) / "mac-test-display"
    source = Path(__file__).with_name("mac_test_display.m")
    compiler = ["/usr/bin/xcrun", "clang", "-fobjc-arc", "-framework", "AppKit", "-framework", "CoreGraphics",
                source, "-o", probe]
    if run(compiler, env, deadline, check):
        raise CheckStopped("Could not build the DeskPad display probe.", 1)
    running = subprocess.run(["/usr/bin/pgrep", "-U", str(os.getuid()), "-x", "DeskPad"],
                             stdout=subprocess.DEVNULL, timeout=3)
    started = running.returncode == 1
    if started:
        binary = DESKPAD_APP / "Contents/MacOS/DeskPad"
        if not binary.is_file():
            raise CheckStopped("Install DeskPad in Applications before running a DeskPad check.", 1)
        if run(["/usr/bin/open", "-g", "-a", DESKPAD_APP], os.environ, deadline, check):
            # A direct launch also works in sessions without Launch Services.
            check()
            subprocess.Popen([binary], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                             stderr=subprocess.DEVNULL, start_new_session=True)
    else:
        running.check_returncode()
    verb = "deskpad" if started else "reuse-deskpad"
    if run([probe, verb, DESKPAD_DISPLAY], env, deadline, check):
        raise CheckStopped("DeskPad did not make its virtual display ready.", 1)
    env["WEBCRATE_MAC_TEST_DISPLAY"] = DESKPAD_DISPLAY
    env["WEBCRATE_MAC_TEST_DISPLAY_TOOL"] = str(probe)
    env["WEBCRATE_MAC_TEST_SCRATCH"] = str(workspace)


def deskpad_fixture_pids(*directories):
    # Launch Services can open a test app in a different process group.
    prefixes = tuple(str(path) + os.sep for directory in directories
                     for path in (Path(directory).absolute(), Path(directory).resolve()))
    listing = subprocess.run(["/bin/ps", "-ww", "-U", str(os.getuid()), "-o", "pid=,comm="],
                             capture_output=True, text=True, check=True, timeout=3)
    pids = []
    for line in listing.stdout.splitlines():
        parts = line.split(maxsplit=1)
        if len(parts) == 2 and parts[1].startswith(prefixes):
            pids.append(int(parts[0]))
    return pids


def stop_deskpad_fixtures(workspace, applications):
    pids = deskpad_fixture_pids(workspace, applications)
    for action in (signal.SIGTERM, signal.SIGKILL):
        if not pids:
            break
        for pid in pids:
            with contextlib.suppress(ProcessLookupError):
                os.kill(pid, action)
        deadline = time.monotonic() + 1
        while pids and time.monotonic() < deadline:
            time.sleep(0.1)
            pids = deskpad_fixture_pids(workspace, applications)
    if pids:
        raise CheckStopped("Could not stop the isolated macOS test applications.", 1)
    remove_deskpad_fixture_domains(workspace)


def remove_deskpad_fixture_domains(workspace):
    domains = Path(workspace) / "mac-test-domains"
    if domains.is_symlink() or not domains.is_dir():
        return
    user_library = Path.home() / "Library"
    for marker in domains.iterdir():
        domain = marker.name
        if marker.is_symlink() or not marker.is_file() or not re.fullmatch(r"com\.webcrate\.apps\.app-[0-9a-f]{16}", domain):
            continue
        preferences = user_library / "Preferences" / (domain + ".plist")
        if preferences.exists():
            # defaults may report an absent domain and leave an empty plist.
            subprocess.run(["/usr/bin/defaults", "delete", domain], capture_output=True, timeout=3)
            preferences.unlink(missing_ok=True)
        data = user_library / "WebKit" / domain
        if data.is_dir():
            shutil.rmtree(data)


def guest_status(instance):
    result = subprocess.run(
        ["limactl", "list", "--json", instance], capture_output=True, text=True, timeout=5, check=True,
    )
    return json.loads(result.stdout) if result.stdout.strip() else {}


def require_idle_guest(instance):
    guest = guest_status(instance)
    config = guest.get("config", {})
    if guest.get("status") != "Stopped":
        raise CheckStopped(f"The configured Lima instance {instance} must already exist and be stopped.")
    if (guest.get("vmType") != "vz" or config.get("os") != "Linux"
            or guest.get("cpus") != 1 or not 0 < guest.get("memory", 0) <= 1024**3
            or config.get("video", {}).get("display") != "none"):
        raise CheckStopped("The test guest must use VZ, one CPU, at most 1 GiB of memory and no display.")


def stop_guest(instance):
    if guest_status(instance).get("status") == "Stopped":
        return
    try:
        subprocess.run(["limactl", "stop", instance], timeout=15, check=True)
        if guest_status(instance).get("status") == "Stopped":
            return
    except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
        pass
    subprocess.run(["limactl", "stop", "--force", instance], timeout=10, check=True)
    if guest_status(instance).get("status") != "Stopped":
        raise CheckStopped(f"Could not verify that {instance} stopped.", 1)


def local_check(command, timeout, instance=None, deskpad=False):
    signal_received = None

    def cancel(signum, _):
        nonlocal signal_received
        signal_received = signum

    def check_interrupted():
        if signal_received is not None:
            raise CheckStopped("The local check was interrupted.", 128 + signal_received)

    signals = (signal.SIGINT, signal.SIGTERM)
    handlers = [signal.signal(sig, cancel) for sig in signals]
    try:
        with single_check(Path("/tmp") / f"creator-local-check-{os.getuid()}.lock"):
            native = deskpad or instance is not None
            start_load = max(1.0, NATIVE_START_LOAD_RATIO * (os.cpu_count() or 1))
            if native:
                wait_for_native_headroom(start_load, check_interrupted)
            with tempfile.TemporaryDirectory(prefix="creator-check-", dir="/tmp") as workspace:
                env = {**os.environ, "TMPDIR": workspace, "GOMAXPROCS": "2"}
                env["GOFLAGS"] = f"{env.get('GOFLAGS', '')} -p=1".strip()

                def check():
                    check_interrupted()
                    check_host(workspace, native=native)

                check()
                try:
                    os.nice(10)
                except PermissionError:
                    print("Lower process priority is unavailable. The serial and time limits still apply.", file=sys.stderr)
                deadline = time.monotonic() + timeout
                if deskpad:
                    prepare_deskpad(workspace, env, deadline, check)
                    wait_for_native_headroom(start_load, check, deadline)
                    env["WEBCRATE_MAC_TEST_START_LOAD"] = str(start_load)
                    applications = Path.home() / "Applications"
                    applications.mkdir(exist_ok=True)
                    # Launch Services ignores bundles installed in /tmp.
                    with tempfile.TemporaryDirectory(prefix="Creator Acceptance ", dir=applications.resolve()) as installed:
                        env["WEBCRATE_MAC_TEST_APPLICATIONS"] = installed
                        try:
                            return run(command, env, deadline, check)
                        finally:
                            stop_deskpad_fixtures(workspace, installed)
                if instance is None:
                    return run(command, env, deadline, check)
                require_idle_guest(instance)
                try:
                    result = run(["limactl", "start", "--tty=false", instance], env, deadline, check, keep_children=True)
                    if result:
                        return result
                    wait_for_native_headroom(start_load, check, deadline)
                    return run(command, env, deadline, check)
                finally:
                    stop_guest(instance)
    finally:
        for sig, handler in zip(signals, handlers):
            signal.signal(sig, handler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--timeout", type=int, default=90, help="total seconds for startup and testing, at most 180")
    display = parser.add_mutually_exclusive_group()
    display.add_argument("--deskpad", action="store_true", help="use the DeskPad virtual display for a macOS native check")
    display.add_argument("--lima", metavar="INSTANCE", help="start and stop an existing headless Linux test instance")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command
    if command and command[0] == "--":
        command = command[1:]
    if not command or not 1 <= args.timeout <= 180:
        parser.error("provide a command and a timeout from 1 to 180 seconds")
    try:
        result = local_check(command, args.timeout, args.lima, args.deskpad)
        return 128 - result if result < 0 else result
    except CheckStopped as error:
        print(error, file=sys.stderr)
        return error.status
    except (OSError, subprocess.SubprocessError, ValueError) as error:
        print(f"Local check failed ({error}).", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())

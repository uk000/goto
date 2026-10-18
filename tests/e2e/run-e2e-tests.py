"""
E2E test driver: finds every e2e test under tests/ and runs them one after the other, then prints a summary and
exits non-zero if any of them failed. This is what `make e2e-test` runs.

An e2e test is a script at tests/<area>/<name>-e2e-test.py that runs the goto binary given in the GOTO_BIN env var
(or else the first executable in bin/), accepts -v for verbose output, and exits non-zero on failure (e.g. a script
ending in unittest.main()). To add an e2e test, just add such a script in a folder under tests/, and this driver
picks it up. No changes are needed here.

Usage: python3 tests/e2e/run-e2e-tests.py [-v] [--list] [--timeout SECONDS] [name ...]
  -v         passes -v to the e2e tests, for verbose output
  --list     lists the e2e tests found, without running them
  --timeout  max seconds for one e2e test, after which it is killed along with the goto instances it started
  name       runs only the e2e tests whose path contains any of the given names, e.g. `registry`
"""
from pathlib import Path
import argparse
import os
import signal
import subprocess
import sys
import time

TESTS_DIR = Path(__file__).resolve().parents[1]
PROJECT_ROOT = TESTS_DIR.parent
DRIVER_DIR = Path(__file__).resolve().parent
E2E_TEST_PATTERN = "*/*-e2e-test.py"
DEFAULT_TIMEOUT = 600


def discover(filters):
    """Returns the e2e test scripts under tests/ (not the driver's own folder), narrowed down by the filters."""
    scripts = [p for p in sorted(TESTS_DIR.glob(E2E_TEST_PATTERN)) if p.parent != DRIVER_DIR]
    if filters:
        scripts = [p for p in scripts if any(f in str(p.relative_to(TESTS_DIR)) for f in filters)]
    return scripts


def _kill_group(process):
    """Terminates the e2e test and the goto instances it started, which all share the process group."""
    try:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
    except ProcessLookupError:
        pass


def run_e2e_test(script, verbose, timeout):
    """Runs one e2e test script with its output going straight to ours, and returns (passed, seconds, note)."""
    command = [sys.executable, str(script)] + (["-v"] if verbose else [])
    env = dict(os.environ, PYTHONUNBUFFERED="1")
    started = time.time()
    # A process group of its own, so that a hung e2e test can be killed together with the goto instances it started.
    process = subprocess.Popen(command, cwd=str(PROJECT_ROOT), env=env, start_new_session=True)
    try:
        code = process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        _kill_group(process)
        return False, time.time() - started, "timed out after %ds" % timeout
    except BaseException:
        _kill_group(process)
        raise
    return code == 0, time.time() - started, "" if code == 0 else "exit code %d" % code


def main():
    parser = argparse.ArgumentParser(description="Runs all the e2e tests under tests/")
    parser.add_argument("-v", "--verbose", action="store_true", help="verbose output from the e2e tests")
    parser.add_argument("--list", action="store_true", help="list the e2e tests, without running them")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT, help="max seconds for one e2e test")
    parser.add_argument("names", nargs="*", help="run only the e2e tests whose path contains one of these")
    args = parser.parse_args()

    scripts = discover(args.names)
    if not scripts:
        print("No e2e tests found under %s matching %s%s" % (
            TESTS_DIR, E2E_TEST_PATTERN, " and " + ", ".join(args.names) if args.names else ""))
        return 2
    if args.list:
        for script in scripts:
            print(script.relative_to(TESTS_DIR))
        return 0

    results = []
    for script in scripts:
        name = str(script.relative_to(TESTS_DIR))
        print("\n===== E2E TEST: %s =====" % name, flush=True)
        results.append((name,) + run_e2e_test(script, args.verbose, args.timeout))

    width = max(len(name) for name, _, _, _ in results)
    print("\n===== E2E TEST SUMMARY =====")
    for name, passed, seconds, note in results:
        print("%s  %s  %6.1fs  %s" % ("PASS" if passed else "FAIL", name.ljust(width), seconds, note))
    failed = [name for name, passed, _, _ in results if not passed]
    print("%d passed, %d failed" % (len(results) - len(failed), len(failed)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())

from pathlib import Path
import http.client
import json
import os
import shutil
import socket
import stat
import subprocess
import tempfile
import threading
import time
import unittest


def _find_free_ports(count):
    # All sockets are held open until every port is picked, so the returned ports are guaranteed to be distinct.
    sockets = []
    try:
        for _ in range(count):
            s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sockets.append(s)
            s.bind(("127.0.0.1", 0))
        return [s.getsockname()[1] for s in sockets]
    finally:
        for s in sockets:
            s.close()


def _find_binary():
    """The binary given in the GOTO_BIN env var (as `make e2e-test` does), else the first one in bin/."""
    given = os.environ.get("GOTO_BIN")
    if given:
        path = Path(given).resolve()
        if not (path.is_file() and os.access(path, os.X_OK)):
            raise FileNotFoundError(f"GOTO_BIN {given} is not an executable file")
        return path
    bin_dir = Path(__file__).resolve().parents[2] / "bin"
    if not bin_dir.exists():
        raise FileNotFoundError(f"bin directory not found at {bin_dir}")
    candidates = [p for p in sorted(bin_dir.iterdir()) if p.is_file() and os.access(p, os.X_OK)]
    if not candidates:
        raise FileNotFoundError(f"No executable binary found in {bin_dir}")
    return candidates[0]


class _UnixHTTPConnection(http.client.HTTPConnection):
    """An HTTP connection over a unix domain socket."""

    def __init__(self, path, timeout=3):
        super().__init__("localhost", timeout=timeout)
        self.unix_path = path

    def connect(self):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(self.timeout)
        sock.connect(self.unix_path)
        self.sock = sock


def _request(target, method, uri, body=None):
    """
    Makes a request to a TCP port (int) or a unix socket (str path), and returns (status, headers, text).
    Raises OSError if the target doesn't accept connections.
    """
    if isinstance(target, int):
        conn = http.client.HTTPConnection("127.0.0.1", target, timeout=3)
    else:
        conn = _UnixHTTPConnection(target)
    try:
        conn.request(method, uri, body=body)
        response = conn.getresponse()
        return response.status, response.headers, response.read().decode("utf-8", "replace")
    finally:
        conn.close()


def _http(target, method, uri, body=None):
    """Makes a request as _request does, and returns (status, text)."""
    status, _, text = _request(target, method, uri, body)
    return status, text


def _wait_until(condition, timeout, interval=0.2):
    deadline = time.time() + timeout
    while True:
        try:
            if condition():
                return True
        except OSError:
            pass
        if time.time() >= deadline:
            return False
        time.sleep(interval)


def _accepts_unix(path):
    """Tells if something accepts connections on the unix socket."""
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
            s.settimeout(2)
            s.connect(path)
            return True
    except OSError:
        return False


def _accepts_tcp(port):
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=1):
            return True
    except OSError:
        return False


class _Goto:
    """One run of the goto binary, with its output collected."""

    def __init__(self, extra_args=()):
        self.port, self.grpc_port, self.rpc_port = _find_free_ports(3)
        self.args = ["--port", str(self.port), "--grpcPort", str(self.grpc_port), "--rpcPort", str(self.rpc_port)]
        self.args += list(extra_args)
        self.lines = []
        self.process = subprocess.Popen(
            [str(_find_binary())] + self.args,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
        )
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()

    def _read(self):
        for line in self.process.stdout:
            self.lines.append(line.rstrip("\n"))

    def alive(self):
        return self.process.poll() is None

    def logged(self, text, timeout=5):
        return _wait_until(lambda: any(text in line for line in list(self.lines)), timeout)

    def wait_ready(self, timeout=30):
        """
        Waits until the servers can serve the listeners that are added, and until the process handles stop signals:
        listeners added before that are open, but not served, and a process stopped before that is simply killed.
        The servers log that they are ready, and take them into use a second later, hence the wait that follows.
        """
        ready = self.logged("is ready. Starting additional HTTP listeners", timeout)
        ready = ready and self.logged("is ready. Starting additional JSONRPC listeners", timeout)
        if ready:
            time.sleep(1.5)
        return ready

    def output(self):
        return "\n".join(self.lines)

    def stop(self):
        if self.alive():
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        self.reader.join(timeout=5)
        if self.process.stdout:
            self.process.stdout.close()


class _UDSTestCase(unittest.TestCase):
    """Helpers for tests that need a short dir for sockets, which is cleaned up after the test."""

    def make_dir(self):
        # Socket paths are limited to about 100 characters, which long temp dir paths can exceed.
        path = tempfile.mkdtemp(prefix="u")
        self.addCleanup(shutil.rmtree, path, True)
        return path

    def start_goto(self, extra_args=()):
        goto = _Goto(extra_args)
        self.addCleanup(goto.stop)
        return goto


class GotoUDSTest(_UDSTestCase):
    """One run of goto with --uds, and listeners added to it via the API."""

    @classmethod
    def setUpClass(cls):
        cls.dir = tempfile.mkdtemp(prefix="u")
        cls.sock = os.path.join(cls.dir, "goto.sock")
        cls.goto = _Goto(["--uds", cls.sock])
        ready = cls.goto.wait_ready() and _wait_until(
            lambda: cls.goto.alive() and _http(cls.sock, "GET", "/server/listeners")[0] == 200, 20
        )
        if not ready:
            output = cls.goto.output()
            cls.tearDownClass()
            raise RuntimeError(f"goto did not serve on the socket {cls.sock}:\n{output}")

    @classmethod
    def tearDownClass(cls):
        cls.goto.stop()
        shutil.rmtree(cls.dir, True)

    def new_sock(self, name):
        return os.path.join(self.dir, name)

    def add(self, port, protocol, sock):
        payload = {"port": port, "protocol": protocol, "uds": sock, "open": True}
        return _http(self.goto.port, "POST", "/server/listeners/add", json.dumps(payload))

    def listeners(self):
        status, body = _http(self.goto.port, "GET", "/server/listeners")
        self.assertEqual(status, 200, body)
        return json.loads(body)

    def test_startup_socket_is_a_socket_with_restricted_permissions(self):
        mode = os.stat(self.sock).st_mode
        self.assertTrue(stat.S_ISSOCK(mode), "the --uds path must be a socket")
        self.assertEqual(stat.S_IMODE(mode), 0o660)

    def test_startup_socket_serves_the_same_apis_as_the_primary_port(self):
        over_socket = _http(self.sock, "GET", "/server/listeners")
        over_tcp = _http(self.goto.port, "GET", "/server/listeners")
        self.assertEqual(over_socket[0], 200)
        self.assertEqual(over_tcp[0], 200)
        self.assertEqual(sorted(json.loads(over_socket[1])), sorted(json.loads(over_tcp[1])), "same listeners on both")
        status, _ = _http(self.sock, "GET", "/version")
        self.assertEqual(status, 200)

    def test_listeners_can_be_managed_over_the_socket(self):
        port = _find_free_ports(1)[0]
        sock = self.new_sock("m.sock")
        payload = json.dumps({"port": port, "protocol": "http", "uds": sock, "open": True})
        status, body = _http(self.sock, "POST", "/server/listeners/add", payload)
        self.assertEqual(status, 200, body)
        self.assertTrue(_wait_until(lambda: _accepts_unix(sock), 5))
        _http(self.sock, "POST", f"/server/listeners/{port}/remove")

    def test_dynamic_http_listener_on_uds(self):
        port = _find_free_ports(1)[0]
        sock = self.new_sock("h.sock")
        status, body = self.add(port, "http", sock)
        self.assertEqual(status, 200, body)
        self.assertIn(f"Listener {port} added and opened on UDS {sock}", body)

        self.assertEqual(_http(sock, "GET", "/server/listeners")[0], 200, "the listener serves over the socket")
        self.assertFalse(_accepts_tcp(port), "the TCP port of a listener on a socket must not be bound")
        info = self.listeners()[str(port)]
        self.assertEqual(info["uds"], sock)
        self.assertTrue(info["open"])
        self.assertTrue(stat.S_ISSOCK(os.stat(sock).st_mode))
        self.assertEqual(stat.S_IMODE(os.stat(sock).st_mode), 0o660)

    def test_requests_on_a_socket_are_those_of_its_listener_port(self):
        # A socket has no port in its address, so the port of a request is what goto maps the socket to.
        port = _find_free_ports(1)[0]
        sock = self.new_sock("p.sock")
        self.assertEqual(self.add(port, "http", sock)[0], 200)
        _, headers, _ = _request(sock, "GET", "/foo")
        self.assertEqual(headers.get("Goto-Port"), str(port), "requests on the socket of a listener are its port's")
        _, headers, _ = _request(self.sock, "GET", "/foo")
        self.assertEqual(headers.get("Goto-Port"), str(self.goto.port), "requests on --uds are the primary port's")

    def test_dynamic_tcp_and_grpc_listeners_on_uds(self):
        for protocol in ("tcp", "grpc"):
            with self.subTest(protocol=protocol):
                port = _find_free_ports(1)[0]
                sock = self.new_sock(f"{protocol}.sock")
                status, body = self.add(port, protocol, sock)
                self.assertEqual(status, 200, body)
                self.assertTrue(_wait_until(lambda: _accepts_unix(sock), 5), f"{protocol} listener not on {sock}")
                self.assertFalse(_accepts_tcp(port))
                self.assertEqual(self.listeners()[str(port)]["uds"], sock)

    def test_udp_listener_on_uds_is_rejected(self):
        port = _find_free_ports(1)[0]
        sock = self.new_sock("udp.sock")
        status, body = self.add(port, "udp", sock)
        self.assertEqual(status, 400, body)
        self.assertIn("UDS", body)
        self.assertFalse(os.path.exists(sock))
        self.assertNotIn(str(port), self.listeners())

    def test_socket_path_can_only_be_used_by_one_listener(self):
        sock = self.new_sock("d.sock")
        first, second = _find_free_ports(2)
        self.assertEqual(self.add(first, "http", sock)[0], 200)
        status, body = self.add(second, "http", sock)
        self.assertEqual(status, 400, body)
        self.assertIn("already used", body)
        self.assertNotIn(str(second), self.listeners())
        self.assertEqual(_http(sock, "GET", "/server/listeners")[0], 200, "the first listener must be unaffected")

    def test_socket_path_of_the_server_can_not_be_used_by_a_listener(self):
        port = _find_free_ports(1)[0]
        status, body = self.add(port, "http", self.sock)
        self.assertEqual(status, 400, body)
        self.assertIn("already used by the server", body)
        self.assertEqual(_http(self.sock, "GET", "/server/listeners")[0], 200, "the server socket must be unaffected")

    def test_closing_a_listener_removes_its_socket_and_opening_brings_it_back(self):
        port = _find_free_ports(1)[0]
        sock = self.new_sock("c.sock")
        self.assertEqual(self.add(port, "http", sock)[0], 200)
        self.assertTrue(_accepts_unix(sock))

        status, body = _http(self.goto.port, "PUT", f"/server/listeners/{port}/close")
        self.assertEqual(status, 200, body)
        self.assertTrue(_wait_until(lambda: not os.path.exists(sock), 5), "the socket file must be removed")
        self.assertFalse(_accepts_unix(sock))
        self.assertTrue(
            self.goto.logged(f"Closing http listener: {port} (UDS: {sock})"), "the close must be logged with the socket"
        )

        status, body = _http(self.goto.port, "PUT", f"/server/listeners/{port}/open")
        self.assertEqual(status, 200, body)
        self.assertTrue(_wait_until(lambda: _accepts_unix(sock), 5), "the socket must be back when opened")
        self.assertEqual(_http(sock, "GET", "/server/listeners")[0], 200)

    def test_removing_a_listener_removes_its_socket(self):
        port = _find_free_ports(1)[0]
        sock = self.new_sock("r.sock")
        self.assertEqual(self.add(port, "http", sock)[0], 200)
        status, body = _http(self.goto.port, "POST", f"/server/listeners/{port}/remove")
        self.assertEqual(status, 200, body)
        self.assertTrue(_wait_until(lambda: not os.path.exists(sock), 5))
        self.assertNotIn(str(port), self.listeners())

    def test_updating_a_listener_moves_it_to_another_socket(self):
        port = _find_free_ports(1)[0]
        old, new = self.new_sock("old.sock"), self.new_sock("new.sock")
        self.assertEqual(self.add(port, "http", old)[0], 200)
        payload = json.dumps({"port": port, "protocol": "http", "uds": new, "open": True})
        status, body = _http(self.goto.port, "PUT", "/server/listeners/update", payload)
        self.assertEqual(status, 200, body)
        self.assertIn("restarted", body)
        self.assertTrue(_wait_until(lambda: _accepts_unix(new), 5), "the listener must serve on the new socket")
        self.assertTrue(_wait_until(lambda: not os.path.exists(old), 5), "the old socket must be removed")
        self.assertEqual(self.listeners()[str(port)]["uds"], new)

    def test_listener_without_uds_is_unchanged(self):
        port = _find_free_ports(1)[0]
        payload = json.dumps({"port": port, "protocol": "http", "open": True})
        status, body = _http(self.goto.port, "POST", "/server/listeners/add", payload)
        self.assertEqual(status, 200, body)
        self.assertEqual(body.strip(), f"Listener {port} added and opened.")
        self.assertTrue(_wait_until(lambda: _accepts_tcp(port), 5))
        self.assertNotIn("uds", self.listeners()[str(port)])


class GotoUDSStartupTest(_UDSTestCase):
    """Runs of goto that are started over existing files at the socket path."""

    def test_stale_socket_file_is_replaced(self):
        sock = os.path.join(self.make_dir(), "stale.sock")
        # Closing a bound socket leaves its file, as a process that died would.
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
            s.bind(sock)
        self.assertTrue(stat.S_ISSOCK(os.stat(sock).st_mode))
        self.assertFalse(_accepts_unix(sock), "nothing listens on the stale socket")

        goto = self.start_goto(["--uds", sock])
        ready = goto.wait_ready() and _wait_until(
            lambda: goto.alive() and _http(sock, "GET", "/server/listeners")[0] == 200, 20
        )
        self.assertTrue(ready, f"goto did not serve on the stale socket path:\n{goto.output()}")

    def test_server_removes_its_socket_when_it_stops(self):
        sock = os.path.join(self.make_dir(), "stop.sock")
        goto = self.start_goto(["--uds", sock])
        self.assertTrue(goto.wait_ready(), goto.output())
        self.assertTrue(_wait_until(lambda: goto.alive() and _accepts_unix(sock), 20), goto.output())
        goto.stop()
        self.assertFalse(goto.alive())
        self.assertTrue(
            _wait_until(lambda: not os.path.exists(sock), 5),
            f"the socket file must be removed on stop:\n{goto.output()}",
        )

    def test_server_removes_the_sockets_of_its_listeners_when_it_stops(self):
        directory = self.make_dir()
        goto = self.start_goto()
        self.assertTrue(goto.wait_ready(), goto.output())
        socks = {}
        for protocol in ("http", "tcp", "grpc"):
            port = _find_free_ports(1)[0]
            socks[protocol] = os.path.join(directory, f"{protocol}.sock")
            payload = json.dumps({"port": port, "protocol": protocol, "uds": socks[protocol], "open": True})
            status, body = _http(goto.port, "POST", "/server/listeners/add", payload)
            self.assertEqual(status, 200, body)
            self.assertTrue(_wait_until(lambda: _accepts_unix(socks[protocol]), 5), protocol)

        goto.stop()
        self.assertFalse(goto.alive())
        for protocol, sock in socks.items():
            with self.subTest(protocol=protocol):
                self.assertFalse(os.path.exists(sock), f"the socket of the {protocol} listener must be removed on stop")

    def test_path_that_is_not_a_socket_stops_the_server_and_is_left_alone(self):
        path = os.path.join(self.make_dir(), "regular")
        Path(path).write_text("keep me")

        goto = self.start_goto(["--uds", path])
        exited = _wait_until(lambda: not goto.alive(), 20)
        self.assertTrue(exited, f"goto must not start with a --uds path that is not a socket:\n{goto.output()}")
        self.assertNotEqual(goto.process.returncode, 0)
        self.assertIn("not a socket", goto.output())
        self.assertEqual(Path(path).read_text(), "keep me")


if __name__ == "__main__":
    unittest.main()

from pathlib import Path
import http.client
import os
import socket
import ssl
import subprocess
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


def _wait_for_tcp_port(port, timeout):
    deadline = time.time() + timeout
    while True:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return True
        except OSError:
            if time.time() >= deadline:
                return False
            time.sleep(0.2)


def _get_root(port, https, timeout, alive=lambda: True):
    """GET / on the port until it answers, and return (status, reason). Raises the last error on timeout."""
    deadline = time.time() + timeout
    context = None
    if https:
        # goto serves a self-signed certificate
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
    while True:
        if https:
            conn = http.client.HTTPSConnection("127.0.0.1", port, timeout=1, context=context)
        else:
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=1)
        try:
            conn.request("GET", "/")
            response = conn.getresponse()
            response.read()
            return response.status, response.reason
        except OSError:
            if not alive() or time.time() >= deadline:
                raise
            time.sleep(0.2)
        finally:
            conn.close()


def _build_overlay(title, lines):
    width = max(len(title), *(len(line) for line in lines), 0)
    border = "+" + "-" * (width + 2) + "+"
    overlay = [border, f"| {title.center(width)} |", border]
    for line in lines:
        overlay.append(f"| {line.ljust(width)} |")
    overlay.append(border)
    return overlay


def _print_nested_overlay(process_lines, client_lines):
    client_overlay = _build_overlay("HTTP CLIENT CALL OUTPUT", client_lines)
    outer_lines = ["STDOUT:"] + process_lines[0] + [""] + ["STDERR:"] + process_lines[1] + [""] + client_overlay
    outer_overlay = _build_overlay("PROCESS LOGS", outer_lines)
    print("\n".join(outer_overlay))


class _GotoBinaryTests:
    """
    Starts the goto binary on ports picked here, and checks that it listens on them. Each subclass is one run of
    the binary, and passes its HTTP/HTTPS ports through a different argument (see port_args).

    The ports are picked here and only those are probed. This avoids conflicts with other local services, and
    probing well-known ports could mistake another service for goto (e.g. macOS AirPlay Receiver answers 403 on 5000).
    """

    http_count = 1  # number of HTTP ports, the first of which is goto's main port
    https_count = 0  # number of additional HTTPS ports

    @classmethod
    def port_args(cls):
        """Command line args that give goto its cls.http_ports and cls.https_ports."""
        raise NotImplementedError

    @classmethod
    def find_binary(cls):
        """The binary given in the GOTO_BIN env var (as `make e2e-test` does), else the first one in bin/."""
        given = os.environ.get("GOTO_BIN")
        if given:
            path = Path(given).resolve()
            if not (path.is_file() and os.access(path, os.X_OK)):
                raise FileNotFoundError(f"GOTO_BIN {given} is not an executable file")
            return path

        project_root = Path(__file__).resolve().parents[2]
        bin_dir = project_root / "bin"
        if not bin_dir.exists():
            raise FileNotFoundError(f"bin directory not found at {bin_dir}")

        candidates = [p for p in sorted(bin_dir.iterdir()) if p.is_file() and os.access(p, os.X_OK)]
        if not candidates:
            raise FileNotFoundError(f"No executable binary found in {bin_dir}")
        return candidates[0]

    @classmethod
    def setUpClass(cls):
        cls.binary_path = cls.find_binary()
        cls.process_stdout_lines = []
        cls.process_stderr_lines = []

        def _read_stream(stream, collector):
            for raw_line in stream:
                collector.append(raw_line.rstrip("\n"))

        ports = _find_free_ports(cls.http_count + cls.https_count + 2)
        cls.http_ports = ports[: cls.http_count]
        cls.https_ports = ports[cls.http_count : cls.http_count + cls.https_count]
        cls.grpc_port, cls.jsonrpc_port = ports[-2:]
        cls.port = None
        cls.args = cls.port_args() + ["--grpcPort", str(cls.grpc_port), "--rpcPort", str(cls.jsonrpc_port)]

        cls.process = subprocess.Popen(
            [str(cls.binary_path)] + cls.args,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )

        cls.stdout_thread = threading.Thread(
            target=_read_stream,
            args=(cls.process.stdout, cls.process_stdout_lines),
            daemon=True,
        )
        cls.stderr_thread = threading.Thread(
            target=_read_stream,
            args=(cls.process.stderr, cls.process_stderr_lines),
            daemon=True,
        )
        cls.stdout_thread.start()
        cls.stderr_thread.start()

        try:
            cls.response_status, cls.response_reason = _get_root(
                cls.http_ports[0], False, 10, alive=lambda: cls.process.poll() is None
            )
        except OSError as e:
            cls.tearDownClass()
            raise RuntimeError(f"Server did not become available on port {cls.http_ports[0]}: {e}")
        cls.port = cls.http_ports[0]

    @classmethod
    def tearDownClass(cls):
        if getattr(cls, "process", None):
            cls.process.terminate()
            try:
                cls.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                cls.process.kill()

    def test_grpc_port(self):
        self.assertTrue(
            _wait_for_tcp_port(self.grpc_port, 10),
            f"goto is not listening on the given GRPC port {self.grpc_port}",
        )

    def test_jsonrpc_port(self):
        self.assertTrue(
            _wait_for_tcp_port(self.jsonrpc_port, 10),
            f"goto is not listening on the given JSONRPC port {self.jsonrpc_port}",
        )

    def test_root_path(self):
        self.assertIsNotNone(self.port, "Server port must be discovered")
        status, reason = _get_root(self.port, False, 5)
        self.assertGreater(status, 0)
        self.assertLess(status, 600)

        client_lines = [
            f"Args: {' '.join(self.args)}",
            f"GET / -> {status} {reason}",
            f"Connected on port {self.port}",
            f"GRPC port {self.grpc_port}, JSONRPC port {self.jsonrpc_port}",
        ]
        process_lines = [self.__class__.process_stdout_lines, self.__class__.process_stderr_lines]
        _print_nested_overlay(process_lines, client_lines)


class GotoPortFlagTest(_GotoBinaryTests, unittest.TestCase):
    """One run of goto with its HTTP port given via --port."""

    @classmethod
    def port_args(cls):
        return ["--port", str(cls.http_ports[0])]


class GotoPortsFlagTest(_GotoBinaryTests, unittest.TestCase):
    """
    A separate run of goto with its HTTP and HTTPS ports given via --ports, a comma separated list of
    port/protocol values. The first port is goto's main port, which is always HTTP. The other listeners are
    started after the main one, so they are waited for.
    """

    http_count = 2
    https_count = 1

    @classmethod
    def port_args(cls):
        ports = [f"{p}/http" for p in cls.http_ports] + [f"{p}/https" for p in cls.https_ports]
        return ["--ports", ",".join(ports)]

    def test_http_ports(self):
        for port in self.http_ports:
            with self.subTest(port=port):
                status, _ = _get_root(port, False, 10)
                self.assertGreater(status, 0)
                self.assertLess(status, 600)

    def test_https_ports(self):
        for port in self.https_ports:
            with self.subTest(port=port):
                status, _ = _get_root(port, True, 10)
                self.assertGreater(status, 0)
                self.assertLess(status, 600)


if __name__ == "__main__":
    unittest.main()

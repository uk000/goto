"""
Registry e2e test: launches one goto instance as a registry, and three goto instances as peers that register
themselves with it, and then calls the registry's APIs to verify that all the peers have connected.

Then it has the registry push traffic targets (autoInvoke off) to every peer, to send requests to the other two
peers, start the traffic on all peers (10s of it), and verify that the registry received the results of all the peers.

All the instances run locally, so every instance gets its own set of free ports (HTTP, GRPC, JSON-RPC). The
registry's URL given to the peers is built from the registry's HTTP port picked here.

Run with: GOTO_BIN=bin/goto python3 tests/registry/registry-e2e-test.py -v   (or `make e2e-test E2E_ARGS=registry`)
"""
from pathlib import Path
import http.client
import importlib.util
import json
import os
import subprocess
import threading
import time
import unittest

# The startup e2e test has the helpers to start goto instances. Its module name has hyphens, so it can't be
# imported with a regular import statement.
_E2E_TEST_PATH = Path(__file__).resolve().parents[1] / "startup" / "startup-e2e-test.py"
_spec = importlib.util.spec_from_file_location("goto_e2e_test", _E2E_TEST_PATH)
_e2e = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_e2e)

_find_free_ports = _e2e._find_free_ports
_get_root = _e2e._get_root
_build_overlay = _e2e._build_overlay
_find_binary = _e2e._GotoBinaryTests.find_binary

REGISTRY_LABEL = "Registry"
PEER_LABELS = ["peer1", "peer2", "peer3"]
PEER_REGISTRATION_TIMEOUT = 15  # seconds

# Peer traffic: each peer sends TRAFFIC_REQUEST_COUNT requests to each of the other peers, with TRAFFIC_DELAY
# between requests, so the traffic takes 50 x 200ms = 10s.
TRAFFIC_REQUEST_COUNT = 50
TRAFFIC_DELAY = "200ms"
TRAFFIC_MIN_SECONDS = 9.0
TRAFFIC_TIMEOUT = 45  # seconds to wait for all the results to reach the registry after the traffic starts


class _GotoInstance:
    """One goto process on its own HTTP, GRPC and JSON-RPC ports, with its stdout and stderr captured."""

    def __init__(self, name, binary, http_port, grpc_port, jsonrpc_port, extra_args):
        self.name = name
        self.http_port = http_port
        self.grpc_port = grpc_port
        self.jsonrpc_port = jsonrpc_port
        self.args = [
            "--port", str(http_port),
            "--grpcPort", str(grpc_port),
            "--rpcPort", str(jsonrpc_port),
        ] + extra_args
        self.stdout_lines = []
        self.stderr_lines = []
        self._binary = binary
        self._process = None

    def start(self):
        self._process = subprocess.Popen(
            [str(self._binary)] + self.args,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            env=self._env(),
        )
        for stream, collector in ((self._process.stdout, self.stdout_lines), (self._process.stderr, self.stderr_lines)):
            threading.Thread(target=self._read_stream, args=(stream, collector), daemon=True).start()

    @staticmethod
    def _env():
        # A peer registers itself with the registry using POD_IP as its address, which defaults to the IP of the
        # machine's outbound interface. That IP is not always reachable from the machine itself (e.g. with a VPN),
        # so the instances are told to use the loopback address, as all of them run locally.
        return dict(os.environ, POD_IP="127.0.0.1")

    @staticmethod
    def _read_stream(stream, collector):
        for raw_line in stream:
            collector.append(raw_line.rstrip("\n"))

    def is_alive(self):
        return self._process is not None and self._process.poll() is None

    def wait_until_serving(self, timeout):
        """Blocks until GET / on the instance's HTTP port answers. Raises OSError if it doesn't, or the process dies."""
        _get_root(self.http_port, False, timeout, alive=self.is_alive)

    def terminate(self):
        if self._process is not None:
            self._process.terminate()

    def wait_for_exit(self, timeout=10):
        if self._process is None:
            return
        try:
            self._process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self._process.kill()
            self._process.wait()

    def log_lines(self):
        return ["STDOUT:"] + self.stdout_lines + ["", "STDERR:"] + self.stderr_lines


def _request(port, method, path, body=None, timeout=10):
    """Calls the path on 127.0.0.1:port with the optional JSON body, and returns (status, parsed JSON, or text)."""
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    try:
        headers = {}
        payload = None
        if body is not None:
            headers["Content-Type"] = "application/json"
            payload = json.dumps(body)
        conn.request(method, path, body=payload, headers=headers)
        response = conn.getresponse()
        text = response.read().decode("utf-8")
        try:
            return response.status, (json.loads(text) if text.strip() else None)
        except ValueError:
            return response.status, text
    finally:
        conn.close()


def _get_json(port, path, timeout=5):
    """GET the path from localhost:port, and return (status, parsed JSON body)."""
    return _request(port, "GET", path, timeout=timeout)


class GotoRegistryPeersTest(unittest.TestCase):
    """One goto instance in registry mode, and three goto peers registered with it."""

    @classmethod
    def setUpClass(cls):
        cls.instances = []
        cls.had_failure = False
        try:
            binary = _find_binary()
            # 4 instances x (HTTP, GRPC, JSON-RPC) ports, all distinct as everything runs on localhost.
            ports = _find_free_ports(3 * (1 + len(PEER_LABELS)))
            port_sets = [ports[i : i + 3] for i in range(0, len(ports), 3)]

            cls.registry = _GotoInstance(
                REGISTRY_LABEL, binary, *port_sets[0], ["--label=" + REGISTRY_LABEL, "--lockerLogs=true"]
            )
            cls.registry_url = "http://localhost:%d" % cls.registry.http_port
            cls.peers = []
            for label, port_set in zip(PEER_LABELS, port_sets[1:]):
                cls.peers.append(
                    _GotoInstance(
                        label,
                        binary,
                        *port_set,
                        [
                            "--label=" + label,
                            "--locker=true",
                            "--registry",
                            cls.registry_url,
                            "--reminderLogs=false",
                        ]
                    )
                )

            # The registry has to be up before the peers start, as a peer that fails to register retries only
            # after a 10s sleep.
            cls._launch(cls.registry)
            cls.registry.wait_until_serving(10)
            for peer in cls.peers:
                cls._launch(peer)
            for peer in cls.peers:
                peer.wait_until_serving(10)
        except BaseException:
            cls._dump_logs()
            cls._stop_all()
            raise

    @classmethod
    def _launch(cls, instance):
        cls.instances.append(instance)
        instance.start()

    @classmethod
    def _stop_all(cls):
        # Each goto takes a few seconds to shut down, so the peers are stopped together, and only then the
        # registry, which the peers deregister from while shutting down.
        instances = getattr(cls, "instances", [])
        registry = getattr(cls, "registry", None)
        for group in ([i for i in instances if i is not registry], [i for i in instances if i is registry]):
            for instance in group:
                instance.terminate()
            for instance in group:
                instance.wait_for_exit()

    @classmethod
    def _dump_logs(cls):
        title = lambda instance: "%s: %s" % (instance.name, " ".join(instance.args))
        for instance in getattr(cls, "instances", []):
            print("\n".join(_build_overlay(title(instance), instance.log_lines())))

    @classmethod
    def tearDownClass(cls):
        if cls.had_failure:
            cls._dump_logs()
        cls._stop_all()

    def run(self, result=None):
        # Remember whether any test failed, so that the instances' logs get printed in tearDownClass.
        if result is not None:
            before = len(result.failures) + len(result.errors)
        outcome = super().run(result)
        if result is not None and len(result.failures) + len(result.errors) > before:
            type(self).had_failure = True
        return outcome

    def _wait_for_peers(self):
        """Polls the registry until all the peers show up in /registry/peers, and returns the last response."""
        deadline = time.time() + PEER_REGISTRATION_TIMEOUT
        peers = {}
        while True:
            status, body = _get_json(self.registry.http_port, "/registry/peers")
            self.assertEqual(status, 200, "GET /registry/peers returned %s" % status)
            peers = body or {}
            if all(label in peers and peers[label].get("pods") for label in PEER_LABELS):
                return peers
            if time.time() >= deadline:
                return peers
            time.sleep(0.5)

    def test_registry_is_serving(self):
        status, reason = _get_root(self.registry.http_port, False, 5)
        self.assertGreater(status, 0)
        self.assertLess(status, 600)

    def test_all_peers_registered(self):
        peers = self._wait_for_peers()
        self.assertEqual(
            sorted(peers.keys()),
            sorted(PEER_LABELS),
            "Peers registered with the registry don't match the launched peers: %s" % sorted(peers.keys()),
        )

    def test_peer_addresses_match_launched_ports(self):
        peers = self._wait_for_peers()
        for peer in self.peers:
            with self.subTest(peer=peer.name):
                self.assertIn(peer.name, peers, "Peer %s did not register with the registry" % peer.name)
                pods = peers[peer.name].get("pods") or {}
                self.assertEqual(len(pods), 1, "Peer %s should have one pod, found %s" % (peer.name, list(pods)))
                address = next(iter(pods))
                self.assertTrue(
                    address.endswith(":%d" % peer.http_port),
                    "Peer %s registered with address %s, expected port %d" % (peer.name, address, peer.http_port),
                )
                self.assertEqual(pods[address].get("address"), address)
                self.assertFalse(pods[address].get("offline"), "Peer %s is marked offline" % peer.name)

    def test_registry_is_not_registered_as_peer(self):
        peers = self._wait_for_peers()
        self.assertNotIn(REGISTRY_LABEL, peers)

    def test_peer_lockers_created(self):
        # Peers launched with --locker=true get a locker on the registry as soon as they register.
        self._wait_for_peers()
        status, lockers = _get_json(self.registry.http_port, "/registry/peers/lockers")
        self.assertEqual(status, 200)
        for label in PEER_LABELS:
            with self.subTest(peer=label):
                self.assertIn(label, lockers or {}, "No locker for peer %s on the registry" % label)

    def _assert_all_pods_ok(self, what, status, result, peer_names):
        """Checks that the registry's per-pod result of a call made to the peers says every pod of them succeeded."""
        self.assertEqual(status, 200, "%s returned %s: %s" % (what, status, result))
        self.assertIsInstance(result, dict, "%s returned %s" % (what, result))
        self.assertEqual(sorted(result.keys()), sorted(peer_names), "%s returned %s" % (what, result))
        for name, pods in result.items():
            self.assertTrue(pods and all(pods.values()), "%s failed on peer %s: %s" % (what, name, pods))

    def _traffic_results(self):
        """Returns the client results that the peers have reported to the registry, by peer and by target."""
        status, summary = _get_json(self.registry.http_port, "/registry/peers/client/results/summary")
        self.assertEqual(status, 200, "GET /registry/peers/client/results/summary returned %s" % status)
        return {peer: (data or {}).get("byTargets") or {} for peer, data in (summary or {}).items()}

    def test_registry_driven_peer_traffic(self):
        """
        The registry gives each peer targets for the other two peers, with autoInvoke off, so the peers hold the
        traffic until the registry tells them to start. Then every peer sends its traffic for 10s, and reports the
        results to the registry.
        """
        registry_port = self.registry.http_port
        self._wait_for_peers()
        expected_targets = {}  # peer name -> {target name -> request count}

        # 1. Push the targets to the peers via the registry. Each peer gets one target per other peer.
        for peer in self.peers:
            expected_targets[peer.name] = {}
            for other in self.peers:
                if other is peer:
                    continue
                target = {
                    "name": "to-" + other.name,
                    "method": "GET",
                    "url": "http://127.0.0.1:%d/status/200" % other.http_port,
                    "replicas": 1,
                    "requestCount": TRAFFIC_REQUEST_COUNT,
                    "delay": TRAFFIC_DELAY,
                    "autoInvoke": False,
                }
                status, result = _request(
                    registry_port, "POST", "/registry/peers/%s/targets/add" % peer.name, target
                )
                self._assert_all_pods_ok("Adding target %s to %s" % (target["name"], peer.name), status, result, [peer.name])
                expected_targets[peer.name][target["name"]] = TRAFFIC_REQUEST_COUNT

        # 2. The registry has the targets of every peer, and none of them is set to auto invoke.
        for peer in self.peers:
            status, targets = _get_json(registry_port, "/registry/peers/%s/targets" % peer.name)
            self.assertEqual(status, 200)
            self.assertEqual(sorted(targets.keys()), sorted(expected_targets[peer.name].keys()))
            for name, target in targets.items():
                self.assertFalse(target.get("autoInvoke"), "Target %s of %s is set to auto invoke" % (name, peer.name))

        # 3. With autoInvoke off, the peers don't start the traffic on their own.
        time.sleep(2)
        self.assertEqual(
            {name: t for name, t in self._traffic_results().items() if t},
            {},
            "Peers sent traffic before the registry asked them to",
        )

        # 4. The registry asks each peer to start its traffic.
        started_at = time.time()
        for peer in self.peers:
            status, result = _request(registry_port, "POST", "/registry/peers/%s/targets/invoke/all" % peer.name)
            self._assert_all_pods_ok("Invoking targets on %s" % peer.name, status, result, [peer.name])

        # 5. Wait for the peers to finish, which is when all their results are on the registry.
        deadline = started_at + TRAFFIC_TIMEOUT
        results = {}
        while True:
            results = self._traffic_results()
            done = all(
                results.get(peer, {}).get(target, {}).get("invocationCount") == count
                for peer, targets in expected_targets.items()
                for target, count in targets.items()
            )
            if done or time.time() >= deadline:
                break
            time.sleep(0.5)
        elapsed = time.time() - started_at
        self.assertTrue(done, "Registry did not get all the results within %ds: %s" % (TRAFFIC_TIMEOUT, results))
        self.assertGreaterEqual(elapsed, TRAFFIC_MIN_SECONDS, "Traffic finished in %.1fs, expected about 10s" % elapsed)

        # 6. Every peer reported results for its two targets, and only for those, and all the requests succeeded.
        self.assertEqual(sorted(results.keys()), sorted(PEER_LABELS))
        for peer, targets in expected_targets.items():
            self.assertEqual(sorted(results[peer].keys()), sorted(targets.keys()), "Targets reported by %s" % peer)
            for target, count in targets.items():
                with self.subTest(peer=peer, target=target):
                    result = results[peer][target]
                    self.assertEqual(result.get("invocationCount"), count)
                    status_codes = result.get("countsByStatusCodes") or {}
                    self.assertEqual(list(status_codes.keys()), ["200"], "%s/%s: %s" % (peer, target, status_codes))

    def test_print_summary(self):
        peers = self._wait_for_peers()
        lines = ["Registry URL: %s" % self.registry_url]
        for peer in self.peers:
            lines.append("%s args: %s" % (peer.name, " ".join(peer.args)))
        lines.append("GET /registry/peers -> %s" % ", ".join(
            "%s=%s" % (name, ",".join(sorted((data.get("pods") or {}).keys()))) for name, data in sorted(peers.items())
        ))
        print("\n".join(_build_overlay("REGISTRY E2E TEST", lines)))


if __name__ == "__main__":
    unittest.main()

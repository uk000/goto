# Global Package

This package provides global configuration, flags, and shared state management.

## REST APIs

This package does not expose REST APIs. It provides internal global configuration and state management used by other packages. What it holds is set from the [command arguments](../../cmd/README.md), from the environment variables below, and, for the logging flags, from the [log APIs](../log/README.md).

## Environment variables

`goto` reads the identity of the instance from these environment variables when it starts (nothing else of `goto` is configured through the environment, except `SCRIPTS_DIR` of the [scripts](../scripts/README.md) feature):

|Variable|Meaning|When not set|
|---|---|---|
| `POD_NAME` | Pod name | The host name, then `local` if there is none |
| `POD_IP` | IP address of the instance | The local address of an outbound UDP route (probed with a dial to `8.8.8.8:80`, nothing is sent), then `localhost` |
| `NAMESPACE` | Namespace | The namespace in the service account file of a K8s pod (`/var/run/secrets/kubernetes.io/serviceaccount/namespace`), then `local` |
| `NODE_NAME` | Node the pod runs on | `LocalNode` |
| `HOST_IP` | IP address of the node | `0.0.0.0` |
| `CLUSTER` | Cluster name | `LocalCluster` |
| `SCRIPTS_DIR` | Directory where the scripts of the scripts feature are stored | `{temp dir}/scripts` |

They make up the `Goto-Host` header and the default listener label:
- `Goto-Host` is `{pod}.{namespace}[{podIP}]({node}[{hostIP}]@{cluster})`, for example `myhost.local[10.0.0.5](LocalNode[0.0.0.0]@LocalCluster)`. When none of node, cluster and host IP is known the shorter `{pod}.{namespace}[{podIP}]` form is used (in practice the three always have a value, so the long form is what you see).
- The default label of a listener is `[{podIP}:{port}][{pod}@{namespace}@{cluster}]`; the `--label` argument replaces `{podIP}`.

## Shared state

|Variable|What it holds|
|---|---|
| `Version`, `Commit`, `DevTag` | Build version and commit (set at build time with `-ldflags`); `DevTag` (currently `0.9.20`) is used as the version when none was set. Reported by `GET /goto/version` |
| `Self` | Identity of this instance: ports (`ServerPort` 8080, `GRPCPort` 1234, `JSONRPCPort` 3000 by default), `Name` (the `--label`), `Address`, pod, namespace, node, cluster, pod IP, host IP, `HostLabel` and `RegistryURL` |
| `Flags` | The logging switches (server, admin, client, invocation, registry, locker, reminder, peer health, probe, metrics, proxy, request and response headers, minibodies and bodies, MCP verbose) and `UseLocker`. Defaults and the arguments that set them are in the [command arguments](../../cmd/README.md) and in the [log APIs](../log/README.md) |
| `ServerConfig` | `CommonName` (`--cn`), `CertPath` (`--certs`), `KubeConfig`, `StartupDelay`, `ShutdownDelay`, `StartupScript`, `ConfigPaths`, the `Stopping` flag (true from the stop signal on, until the process exits) and the largest MTU of the machine |
| `CmdConfig`, `CmdClientConfig`, `CtlConfig` | What the command line selected: client mode (`--client`), the client traffic arguments, and the `ctx`/`apply` command arguments |
| `Funcs` | Function hooks that features register so that other packages can call them without importing them (listener labels and ports, probe checks, and similar) |
| `Debug` | The `--debug` argument |

## Lifecycle watchers

Features register callbacks with the global package instead of calling each other, and the server fires them as the events happen: HTTP server started (`AddHTTPStartWatcher`), JSON-RPC server started (`AddJSONRPCStartWatcher`), TCP serve function configured (`AddTCPServeWatcher`), gRPC server started and stopped (`AddGRPCStartWatcher`, `AddGRPCStopWatcher`), all initial listeners started (`AddListenersStartWatcher`), connection opened and closed (`AddConnOpenWatcher`, `AddConnCloseWatcher`), and HTTP and JSON-RPC stopped (`AddHTTPStopWatcher`, `AddJSONRPCStopWatcher`). `OnShutdown` callbacks run when the process exits.

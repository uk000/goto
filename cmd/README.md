
The application accepts the following command arguments. Go's `flag` package parses them, so each argument can be written with one or two dashes (`-ports 8080` or `--ports 8080`), and a value can be given as `--ports 8080` or `--ports=8080`.

> **Boolean arguments need the `=`.** Write `--probeLogs=true` or `--serverLogs=false`. A form like `--serverLogs false` does not set the argument to false: `false` is read as the first non-option word, which stops the parsing of the arguments, and `goto` then treats it as a command (see [Commands](#goto-ctl-commands)). Nothing starts and the exit status is `0`.

Durations (`--startupDelay`, `--shutdownDelay`) are Go durations that need a unit (`500ms`, `5s`, `1m`); a bare number is rejected (`invalid value "5" for flag -startupDelay`) and `goto` exits with status 2.

<table style="font-size: 0.9em;">
    <thead>
        <tr>
            <th>Argument</th>
            <th>Description</th>
            <th>Default Value</th>
        </tr>
    </thead>
    <tbody>
        <tr>
          <td rowspan="4"><pre>--ports {ports}</pre></td>
          <td>Initial list of ports that the server should start with. Port list is given as comma-separated list of <pre>{port1},<br/>{port2}/{protocol2}/{option2},<br/>{port3}/{protocol3}/{option3},...</pre>  </td>
          <td rowspan="4">""</td>
        </tr>
        <tr>
          <td>* The first port in the list is used as the primary port and is forced to be HTTP. Protocol is optional, and can be one of <pre>http (default), http1, https, https1, httpss, tcp,<br/> tls (tcp+tls), grpc, grpcs (grpc+tls), grpcss, xds, or rpc/jsonrpc (for JSONRPC protocol e.g. MCP and A2A). </pre> See the <a href="../pkg/server/README.md#listener-protocols">protocols table</a> for what each one serves. A <strong>udp</strong> entry is accepted but no UDP listener is opened for it at startup (add UDP listeners with the listener API).</td>
        </tr>
        <tr>
          <td>* Protocol <strong>https</strong> configures the port to serve HTTP requests with a self-signed TLS cert, whereas protocol <strong>tls</strong> configures a TCP port with self-signed TLS cert, and <strong>grpcs</strong> configures a gRPC port with self-signed TLS cert. The common name of the self-signed certs is the <strong>--cn</strong> argument (default <strong>goto.goto</strong>). Use protocol <strong>http1</strong> or <strong>https1</strong> to configure a listener port that only serves HTTP/1.1 protocol and explicitly disallows HTTP/2 protocol. The optional third part is <strong>mtls</strong> (a TLS port that requires a client cert), <strong>mtlss</strong> (requires a client cert and verifies it) or <strong>-&gt;{port}</strong> (a tcp port that forwards its connections to the listener on that port).</td>
        </tr>
        <tr>
          <td>* For example: <pre>--ports 8080,<br/>8081/http,8083/https,<br/>8443/https/mtls,<br/>8000/tcp,8001/tcp/->8443,<br/>9000/tls,10000/grpc,3000/rpc</pre>  In addition to the startup ports, additional ports can be opened by making listener API calls on this port. See <a href="../pkg/server/README.md#listeners">Listeners</a> feature for more details. An invalid port stops the process (<code>Error: Invalid port [N]</code>), and a repeated port is skipped (<code>Error: Duplicate port [N]</code>).</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--port, -p {port}</pre></td>
          <td>Port of the primary HTTP listener, for when <strong>--ports</strong> is not used. The first port of <strong>--ports</strong> takes precedence over it.</td>
          <td rowspan="1">8080</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--uds {path}</pre></td>
          <td>Unix domain socket path for an additional HTTP listener, e.g. <pre>--uds /tmp/goto.sock</pre> </td>
          <td rowspan="2">""</td>
        </tr>
        <tr>
          <td>* Test with: <pre>curl --unix-socket /tmp/goto.sock http://localhost/version</pre> A listener added via the listeners API can also use a socket by setting its <code>uds</code> field. See <a href="../docs/listeners-example.md#unix-domain-sockets-uds">Unix Domain Sockets</a>.</td>
        </tr>
          <td><pre>--rpcPort, -rpc {port}</pre></td>
          <td>The default RPC port that will be used to serve A2A and MCP JSONRPC protocol traffic. It is always opened: the argument only changes the port number. Additional RPC ports can be opened via ports list above. </td>
          <td>3000</td>
        </tr>
        <tr>
          <td><pre>--grpcPort, -gp {port}</pre></td>
          <td>The default gRPC port that will be used to serve gRPC traffic. It is always opened: the argument only changes the port number. Additional gRPC ports can be opened via ports list above. If the port cannot be bound (it is already in use), the process panics at startup.</td>
          <td>1234</td>
        </tr>
        <tr>
          <td><pre>--config {path}</pre></td>
          <td>A directory path from where startup configs will be loaded. Goto will look for .yaml, .yml and .sh files in the config folder and load those. Shell scripts are executed and expected to produce yaml output. Repeat the argument to give more than one directory (<code>--config /a --config /b</code>; a comma-separated list is not split). See <a href="#startup-config-files">Startup config files</a>. </td>
          <td>-</td>
        </tr>
        <tr>
          <td><pre>--cn {commonName}</pre></td>
          <td>CommonName to be used for Auto-Generated TLS Certs. </td>
          <td>goto.goto</td>
        </tr>
        <tr>
          <td><pre>--label, -l {label}</pre></td>
          <td>Label that this instance will use to identify itself. * This is used both for setting Goto's default response headers as well as when registering with the registry. It takes the place of the IP address in the default listener label.</td>
          <td>`[{IPAddress}:{Port}][{pod}@{namespace}@{cluster}]` per listener, e.g. `[10.0.0.5:8080][myhost@local@LocalCluster]`</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--startupDelay<br/> {delay}</pre></td>
          <td>Sleep this long before starting the HTTP and JSON-RPC servers. A duration with a unit (<code>5s</code>). </td>
          <td rowspan="1">1s</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--shutdownDelay<br/> {delay}</pre></td>
          <td>After receiving SIGTERM (or SIGINT), keep serving for this long before stopping. During this time a request to the readiness probe URI is answered with <code>404</code>. A second signal ends the wait. A duration with a unit (<code>5s</code>). </td>
          <td rowspan="1">1s</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--startupScript<br/> {shell command}</pre></td>
          <td>List of shell commands to execute at goto startup. Multiple commands are specified by passing multiple instances of this arg. <strong>Not executed in this version</strong>: the argument is accepted and stored, but nothing runs the commands. </td>
          <td rowspan="1"></td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--registry {url}</pre></td>
          <td>URL of the Goto Registry instance that this instance should connect to. </td>
          <td rowspan="2"> "" </td>
        </tr>
        <tr>
          <td>* This is used to get initial configs and optionally report results to the Goto registry. See <a href="../pkg/registry/README.md">Registry</a> feature for more details.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--locker<br/>={true|false}</pre></td>
          <td> Whether this instance should report its results back to the Goto Registry. </td>
          <td rowspan="2"> false </td>
        </tr>
        <tr>
          <td>* An instance can be asked to report its results to the Goto registry in case the  instance is transient, e.g. pods.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--certs {path}</pre></td>
          <td> Directory path from where to load TLS root certificates. </td>
          <td rowspan="2"> "/etc/certs" </td>
        </tr>
        <tr>
          <td>* The loaded root certificates are used if available, otherwise system default root certs are used.</td>
        </tr>
        <tr>
          <td><pre>--kubeConfig {path}</pre></td>
          <td>Path of the Kubernetes config file used by the K8s features. If the file cannot be loaded, a <code>K8s: Failed to load kube config</code> line is logged at startup and goto starts anyway. <strong>Known issue:</strong> in this version the value of this arg is not applied (the default K8s client is created before the args are parsed); goto uses the in-cluster config or <code>~/.kube/config</code>. </td>
          <td>~/.kube/config</td>
        </tr>
        <tr>
          <td><pre>--debug<br/>={true|false}</pre></td>
          <td>Enable additional debug logging (currently in the request body handling and the tunnels), and log <code>Debug logging enabled</code> at startup. </td>
          <td>false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--serverLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable all goto server logging. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--adminLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of admin calls to configure goto. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--metricsLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of calls to metrics URIs. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--probeLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of requests received for URIs configured as liveness and readiness probes. See <a href="../pkg/server/probes/README.md">Probes</a> for more details. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--clientLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of client activities. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--invocationLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable client's target invocation logs. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--registryLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable all registry logs. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--lockerLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of locker requests on Registry instance. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--reminderLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable reminder logs received from various peer instances (applicable to goto instances acting as registry). </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--peerHealthLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of requests received from Registry for peer health checks </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--proxyDebugLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable the debug logs of the proxies. Shown by <code>GET /log</code> as <code>proxy</code>; there is no API to change it at runtime. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestHeaders<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request headers </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request body </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRPCRequestBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of the request body of RPC (MCP/A2A JSON-RPC) requests. There is no API to change it at runtime. </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestMiniBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request mini body </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseHeaders<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response headers </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response body </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseMiniBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response mini body </td>
          <td rowspan="1">false</td>
        </tr>
    </tbody>
</table>

The logging arguments can be changed while the server runs with the [log APIs](../pkg/log/README.md) (all except `--proxyDebugLogs` and `--logRPCRequestBody`).

The identity of the instance (pod name, namespace, node, cluster, IPs) comes from environment variables (`POD_NAME`, `NAMESPACE`, `POD_IP`, `NODE_NAME`, `HOST_IP`, `CLUSTER`), see the [global package](../pkg/global/README.md#environment-variables).

Once the server is up and running, rest of the interactions and configurations are done purely via REST APIs.

<br/>

## <a name="startup-config-files"></a>Startup config files

Each directory given with `--config` is read when `goto` starts (the first level of the directory only, not its sub-directories):

- Files ending in `.yaml` or `.yml` are parsed as YAML configs. Files ending in `.sh` are run, and their output is parsed as a YAML config. Any other file is ignored.
- A config has an optional `name` and `order` (a number; the files of a directory are loaded in ascending order) and any of these sections, each loaded independently (a section that fails to load is logged and the other sections still load):

|Section|What it configures|
|---|---|
| `tls` | `certs` (a list of `{port or name, cert, key, autoCert, domain, san, spiffeID}`, where `cert` and `key` of a `certs` entry are paths of PEM files; an entry with a `port` is applied to the listener on that port), `caCerts` (a list of `{name, domain, cert, key}`, where `cert` and `key` are the PEM text itself), `spiffeSocket` (the SPIFFE workload API socket for `spiffeID` entries) |
| `listeners` | A map of port to listener, with the fields of the [Listener JSON schema](../pkg/server/README.md#listener-json-schema) (the port is the key). The bootstrap, JSON-RPC and gRPC ports cannot be reconfigured this way: they are logged as errors and skipped. A listener that is already live as the file wants it is not restarted. See [sample-listeners-config.yaml](../pkg/server/listeners/sample-listeners-config.yaml). |
| `mcp`, `a2a` | MCP servers and A2A agents, see the [MCP server](../pkg/ai/mcp/server/README.md) and [A2A server](../pkg/ai/a2a/server/README.md) docs |
| `http`, `grpc` | HTTP response payloads and gRPC protos and services |
| `proxy` | A list of HTTP, TCP and gRPC proxies, see the [proxy](../pkg/proxy/README.md) docs |

- The sections `scripts`, `registry`, `traffic` and `jobs` are accepted by the parser, but the startup loader ignores them (nothing is applied and nothing is logged).
- `goto` watches each config directory. A changed file is loaded again 5 seconds after its last change; a file that is removed has the configs that it loaded removed (its listeners are closed and removed, or go back to what `--ports` defines; its certs and CAs are removed). Updates of a Kubernetes ConfigMap volume are handled the same way.
- A `tcp:` block inside a listener of the file is not applied (the listener then resets its connections): configure the TCP behaviour with the `/server/tcp/{port}/...` APIs after startup.

```yaml
name: demo
order: 1
tls:
  certs:
    - port: 8443
      domain: foo.com
      autoCert: true
listeners:
  8443:
    label: https-8443
    protocol: https
    open: true
  9000:
    protocol: tcp
    open: true
    forward: 8443
```

<br/>

## <a name="goto-client-mode"></a>Client mode arguments

With `--client`, `goto` does not start a server. It sends the requests described by the arguments below, prints the summary of the results (JSON, in the log) and exits. For the client features that are available through the server, see the [client](../pkg/client/README.md) docs.

|Argument|Short|Default|Description|
|---|---|---|---|
| `--client={true\|false}` | | false | Run in client mode |
| `--urls` | `-u` | | Comma-separated list of URLs (or `host:port`) to send traffic to. One target is created per URL. Without any URL the run ends with `No URL specified`. |
| `--protocol` | `-pr` | | `http`, `grpc` or `tcp`. `tcp` is accepted but no traffic is sent in this version (a TCP target cannot be invoked): the run ends with exit code 0 and no requests. |
| `--method` | `-X` | | Request method. `GET` when not given. |
| `--headers` | `-h` | | Comma-separated list of `name:value` headers, e.g. `x:y,a:b`. An item that does not have exactly one `:` is dropped, so a value that contains `:` cannot be given here. |
| `--payload` | `--data` | | Request body |
| `--autodata` | `-a` | | Generate a request body of this size |
| `--reqCount` | `--req` | 1 | Number of requests per target |
| `--parallel` | `--par` | 1 | Number of parallel calls (replicas) |
| `--delay` | `-d` | | Duration to wait between requests |
| `--retries` | `-r` | 1 | Number of retries of a failed request |
| `--retrydelay` | `-rd` | | Duration to wait between retries |
| `--retryon` | `-ro` | | Comma-separated status codes that are retried |
| `--persist={true\|false}` | | false | Keep the results across runs, in `/tmp/goto_client_summary_results.json` (and `/tmp/goto_client_detailed_results.json` with `--verbose`) |
| `--verbose={true\|false}` | `-v` | false | Print (and persist) the detailed results of every call |

Example: `goto --client --urls http://localhost:8080/foo,http://localhost:8080/echo --reqCount 2 --headers x:y --method POST --payload hello`

<br/>

## <a name="goto-ctl-commands"></a>Commands: `ctx` and `apply`

When `goto` is given a word that is not an argument (`goto ... ctx` or `goto ... apply`), it runs as a command line tool against a running `goto`, and does not start a server. The arguments that come before the command select the context; the command has arguments of its own after it.

|Command line|Description|
|---|---|
| `goto ctx -n {name} -r {url}` | Create the context `{name}` pointing to the `goto` at `{url}`, or change the URL of an existing one. The arguments `-name` and `-remote` are the long forms. |
| `goto [-ctxf {file}] [-ctx {name}] apply -f {config.yaml}` | Send the config file to the `goto` of the context (`-ctx`, default `default`). `-file` is the long form of `-f`. The long forms of the leading arguments are `-contextFile` and `-context`. |

- Contexts are kept in a YAML file, `~/.goto_context/gotoContext.yaml` unless `-ctxf` gives another path. It is created, with a context named `default` that points to `http://localhost:8080`, the first time any command runs. Its content is a map of name to `{name, remoteGotoURL}`.
- When the context given by `-ctx` does not exist, `Context [x] not found. Using default context.` is logged and the **`default` context is used**, so the config can land on a different `goto` than the one you meant.
- `apply` sends these sections of the config file to the remote `goto`: `tls` (only `certs` entries that have a `port`, a `cert` file and a `key` file; they are added to the listener of that port and the listener is reopened), `scripts` (`config` entries are stored with `/scripts/add/{name}` or `/scripts/store/{name}`, `run` entries are run), `mcp`, `a2a` and `proxy`. A `listeners` section is not sent to the remote `goto`: the listeners are opened inside the `apply` process itself, which exits, so nothing changes on the remote `goto`. The sections `http`, `grpc`, `registry`, `traffic`, `jobs` and `tls.caCerts` are accepted and ignored.
- A word that is neither `ctx` nor `apply` is ignored with no message and exit status `0`.

```
goto ctx -n local -r http://localhost:8080
goto -ctx local apply -f myconfig.yaml
```

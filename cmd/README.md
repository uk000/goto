
The application accepts the following command arguments:

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
          <td rowspan="2"><pre>--port {port}</pre></td>
          <td>Primary port the server listens on. Alternately use <strong>--ports</strong> for multiple startup ports. Additional ports can be opened by making listener API calls on this port. See <a href="#-listeners">Listeners</a> feature for more details. </td>
          <td rowspan="2">8080</td>
        </tr>
        <tr>
          <td>* Additional ports can be opened by making listener API calls on this port. See <a href="#-listeners">Listeners</a> feature for more details.</td>
        </tr>
        <tr>
          <td rowspan="4"><pre>--ports {ports}</pre></td>
          <td>Initial list of ports that the server should start with. Port list is given as comma-separated list of <pre>{port1},<br/>{port2}/{protocol2}/{commonName2},<br/>{port3}/{protocol3}/{commonName3},...</pre>  </td>
          <td rowspan="4">""</td>
        </tr>
        <tr>
          <td>* The first port in the list is used as the primary port and is forced to be HTTP. Protocol is optional, and can be one of <pre>http (default), http1, https, https1, tcp,<br/> tls (tcp+tls), grpc, or grpcs (grpc+tls), rpc (for JSONRPC protocol e.g. MCP and A2A). </pre></td>
        </tr>
        <tr>
          <td>* Protocol <strong>https</strong> configures the port to serve HTTP requests with a self-signed TLS cert, whereas protocol <strong>tls</strong> configures a TCP port with self-signed TLS cert, and <strong>grpcs</strong> configures a gRPC port with self-signed TLS cert. <strong>CommonName</strong> is used for generating self-signed certs, and defaults to <strong>goto.goto</strong>. Use protocol <strong>http1</strong> or <strong>https1</strong> to configure a listener port that only serves HTTP/1.1 protocol and explicitly disallows HTTP/2 protocol.</td>
        </tr>
        <tr>
          <td>* For example: <pre>--ports 8080,<br/>8081/http,8083/https,<br/>8443/https/foo.com,<br/>8000/tcp,9000/tls,10000/grpc,3000/rpc</pre>  In addition to the startup ports, additional ports can be opened by making listener API calls on this port. See <a href="#-listeners">Listeners</a> feature for more details.</td>
        </tr>
        <tr>
          <td><pre>--rpcPort {port}</pre></td>
          <td>A mandatory default RPC port that will be used to server A2A and MCP JSONRPC protocol traffic. Additional RPC ports can be opened via ports list above. </td>
          <td>-</td>
        </tr>
        <tr>
          <td><pre>--grpcPort {port}</pre></td>
          <td>A mandatory default gRPC port that will be used to server gRPC traffic. Additional gRPC ports can be opened via ports list above. </td>
          <td>-</td>
        </tr>
        <tr>
          <td><pre>--label `{label}`</pre></td>
          <td>Label that this instance will use to identify itself. * This is used both for setting Goto's default response headers as well as when registering with the registry.</td>
          <td>Goto-`IPAddress:Port` </td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--startupDelay<br/> {delay}</pre></td>
          <td>Delay the startup by this duration. </td>
          <td rowspan="1">1s</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--shutdownDelay<br/> {delay}</pre></td>
          <td>Delay the shutdown by this duration after receiving SIGTERM. </td>
          <td rowspan="1">1s</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--startupScript<br/> {shell command}</pre></td>
          <td>List of shell commands to execute at goto startup. Multiple commands are specified by passing multiple instances of this arg. The commands are joined with ';' as a separator and executed using 'sh -c'. </td>
          <td rowspan="1"></td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--registry {url}</pre></td>
          <td>URL of the Goto Registry instance that this instance should connect to. </td>
          <td rowspan="2"> "" </td>
        </tr>
        <tr>
          <td>* This is used to get initial configs and optionally report results to the Goto registry. See <a href="#registry-features">Registry</a> feature for more details.</td>
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
          <td rowspan="2"><pre>--events<br/>={true|false}</pre></td>
          <td> Whether this instance should generate events and build a timeline locally. </td>
          <td rowspan="2"> true </td>
        </tr>
        <tr>
          <td>* Events timeline can be helpful in observing how various operations and traffic were interleaved, and help reason about some outcome.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--publishEvents<br/>={true|false}</pre></td>
          <td> Whether this instance should publish its events to the registry to let registry build a unified timeline of events collected from various peer instances. This flag takes effect only if a registry URL is specified to let this instance connect to a registry instance. </td>
          <td rowspan="2"> false </td>
        </tr>
        <tr>
          <td>* Events timeline can be helpful in observing how various operations and traffic were interleaved, and help reason about some outcome.</td>
        </tr>
        <tr>
          <td rowspan="2"><pre>--certs `{path}`</pre></td>
          <td> Directory path from where to load TLS root certificates. </td>
          <td rowspan="2"> "/etc/certs" </td>
        </tr>
        <tr>
          <td>* The loaded root certificates are used if available, otherwise system default root certs are used.</td>
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
          <td>Enable/Disable logging of requests received for URIs configured as liveness and readiness probes. See <a href="#server-probes">Probes</a> for more details. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--clientLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of client activities. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--invocationLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable client's target invocation logs. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--registryLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable all registry logs. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--lockerLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of locker requests on Registry instance. </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--eventsLogs<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of store event calls from peers on Registry instance. </td>
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
          <td rowspan="1"><pre>--logRequestHeaders<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request headers </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request body </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logRequestMiniBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of request mini body </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseHeaders<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response headers </td>
          <td rowspan="1">false</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response body </td>
          <td rowspan="1">true</td>
        </tr>
        <tr>
          <td rowspan="1"><pre>--logResponseMiniBody<br/>={true|false}</pre></td>
          <td>Enable/Disable logging of response mini body </td>
          <td rowspan="1">true</td>
        </tr>
    </tbody>
</table>

Once the server is up and running, rest of the interactions and configurations are done purely via REST APIs.

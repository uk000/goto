## TCP Server Feature

#### TCP Listeners
- A TCP listener can be created using protocol `tcp` (plain TCP) or `tls` (TCP+TLS). TCP listeners can be opened at startup via command line arg as well as at runtime via admin APIs.
- Startup TCP listeners are opened with default TCP config (described below). Admin APIs allow reconfiguring TCP listeners with additional configs using the listener's `tcp` config JSON schema. Parameters such as listener mode, read/write/idle timeouts, connection lifetime, packet sizes, etc. can be configured. 
- A listener with a `forward` port (see the [Listener JSON Schema](../README.md#listener-json-schema)) hands its connections to the HTTP server as if they had arrived on the forward port, instead of serving them in one of the TCP modes below.
- All the APIs below must be invoked via an HTTP listener (the `/port={port}/...` prefix is not needed, since the TCP port is part of the URI). The port must be a TCP listener, otherwise the API responds with `400` and one of the messages `No listener for port {port}` or `Port {port} is not a TCP listener`.
- The text that a TCP listener itself sends to a client (the replies of `Conversation`, `SilentLife`, `CloseAtFirstByte` and `Payload Validation` modes) is prefixed with the label of the `goto` host in square brackets, e.g. `[pod.ns[10.0.0.1](node[10.0.0.2]@cluster)]HELLO`. Echoed bytes, streamed bytes and configured response payloads are sent as they are.

> &#x1F4DD; <small> The `tcp` block of the listener JSON given to `/server/listeners/add` (and the `tcp:` key of a listener in a startup config file) is not applied at this time: connections to such a listener are reset. Add the listener without the block, and configure it with `/server/tcp/{port}/configure`. </small>

#### Server TCP Modes
A TCP listener can operate in 7 different modes to facilitate different kinds of testing: `Payload`, `Echo`, `Stream`, `Payload Validation`, `Conversation`, `Silent Life` and `Close At First Byte`. 

Each connection is served in one mode, picked when the connection is accepted from the listener's config. When more than one mode is on in the config, the first of the following that applies wins: 
`Payload` > `Stream` > `Conversation` > `Payload Validation` (`validatePayloadLength` or `validatePayloadContent`) > `Silent Life` (any `connectionLife` greater than zero, whether or not `silentLife` is set) > `Close At First Byte` > `Echo`. 
A listener with no mode on and no `connectionLife` runs in `Echo` mode, and one with no mode on and a `connectionLife` runs in `Silent Life` mode. The mode APIs turn off the other modes when they turn one on. A connection works with the config as it was when the connection arrived, so changes only apply to new connections.

Defaults: `readTimeout`, `writeTimeout`, `connectTimeout` and `connIdleTimeout` are `30s` if not set; `echoResponseSize` is `10`; the server reads client data in pieces of up to 100 bytes.

- <strong>Mode: `SilentLife`</strong>
  - Applies to any connection of a listener whose `connectionLife` is greater than zero (unless `Payload`, `Stream`, `Conversation` or `Payload Validation` is on). 
  - The server reads and counts the bytes received, and never echoes. It waits for the configured lifetime, then sends the number of bytes it received as text (prefixed with the host label, e.g. `[host]21`) and closes the client connection.
- <strong>Mode: `CloseAtFirstByte`</strong>
  - Applies when `closeAtFirstByte` is on and `connectionLife` is not set.
  - The listener waits (with no timeout) for the first byte to arrive, replies `GOODBYE` (prefixed with the host label) and closes the client connection.
- <strong>Mode: `Echo`</strong>
  - The listener echoes back the bytes received from the client. It is the default mode when no other mode applies, i.e. when no mode is on and `connectionLife` is not set.
  - Field `echoResponseSize` configures the echo buffer size, which is the number of bytes that the listener will need to receive from the client before echoing back. If more data is received than the `echoResponseSize`, it'll echo multiple chunks each of `echoResponseSize` size, and keep the left over bytes for the next chunk. With the default size of 10, a client that sends 21 bytes gets 20 back, and the 21st stays buffered until 9 more arrive.
  - Field `echoResponseDelay` configures the delay server should apply before sending each echo response packet.
  - In `echo` mode, the connection enforces `readTimeout` and `connIdleTimeout` based on the activity: any new bytes received reset the read/idle timeouts. It applies `writeTimeout` when sending the echo response to the client. 
  - Echo is not available together with `connectionLife`: a listener with a life set serves its connections in `SilentLife` mode, even if `echo` is on.
- <strong>Mode: `Stream`</strong>
  - The listener starts streaming TCP bytes per the given configuration as soon as a client connects. The chunks are random alphanumeric text, the same text for each chunk of a connection.
  - The connection is not bound by `readTimeout`, `writeTimeout` or `connIdleTimeout`; `connectionLife`, if set, stops the stream when reached. The client connection closes automatically once the streaming completes.
  - The stream behavior is controlled via the following fields: `streamPayloadSize`, `streamChunkSize`, `streamChunkCount`, `streamChunkDelay`, `streamDuration`. Not all of these fields are required: the server computes the missing ones from the given ones (for example chunk count from payload size and chunk size, or from duration and delay). When the mode is on and the fields leave a value open, it falls back to a chunk delay of `100ms`, chunk size of `100`, chunk count of `10`, payload size of `1000` and duration of `1s`. The API that sets a stream reports the resolved chunk count, chunk size, delay and duration in its response, and the config of each connection (see `history` and `active` APIs) carries them.
  - The stream waits for `streamChunkDelay` before sending each chunk.
- <strong>Mode: `Payload`</strong>
  - The listener serves a set of pre-configured response payload(s) with an optional `responseDelay`. A listener in this mode with no `responsePayloads` sends nothing and closes the connection.
  - If more than one payload is configured in `responsePayloads` array, the `responseDelay` gets applied before sending each item in the array (an empty item repeats the previous payload). 
  - Field `respondAfterRead` controls whether response should be sent immediately or if a read should be performed before sending the response, in which case at least 1 byte must be received from the client before each response is sent. 
  - Field `keepOpen` determines whether the connection is kept open after sending the last item in the array, for the rest of the connection life. 
  - If no `connectionLife` is configured explicitly, the connection life defaults to `30s` in this mode, and the connection is kept open for the remaining lifetime (computed from the start of request) when `keepOpen` is on. The payloads that remain to be sent when the life is over are not sent. With `keepOpen`, the server holds the connection for the rest of its life even if the client closes it earlier.
- <strong>Mode: `Payload Validation`</strong>
  - The client should first set the payload expectation by calling either `/server/tcp/{port}/expect/payload/length={length}` or `/server/tcp/{port}/expect/payload` (expected payload in the request body), depending on whether server should just validate payload length or the payload content. Payload validation can also be turned on with `/server/tcp/{port}/mode/validate=y`, which validates the content too if an expected payload was stored.
  - The server then waits for the duration of the connection lifetime (if not set explicitly for the listener, this feature defaults to `30s` of total connection life), and buffers bytes received from the client.
  - If at any point during the connection life the number of received bytes exceed the expected payload length, the server responds with error and closes connection.
  - Once the number of bytes matches the expected length, the server still waits for any excess byte until the connection life ends, and then answers with a success message or an error. Each message is prefixed with the host label. The messages returned by the server are one of the following:
    - `[SUCCESS]: Received payload matches expected payload of length [l] on port [p]`
    - `[ERROR:EXCEEDED] - Payload length [l] exceeded expected length [e] on port [p]`
    - `[ERROR:CONTENT] - Payload content of length [l] didn't match expected payload of length [e] on port [p]`
    - `[ERROR:TIMEOUT] - Timed out before receiving payload of expected length [l] on port [p]`
  - `expectedPayloadLength` must be between `0` and `67108864` (64 MiB). A configuration with any other value is rejected with a `400`, and so are `expect/payload/length={length}` with such a length and `expect/payload` with a body longer than that; the port keeps the expectation it had.
- <strong>Mode: `Conversation`</strong>
  - The listener waits for the client to send a line of text containing `HELLO` (any case) to which server also responds back with `HELLO`.
  - All subsequent lines from the client should follow the format `BEGIN/`{text}`/END`, and the server echoes the received text back in the format of `ACK/`{text}`/END`. A line in any other format gets the reply `ERROR`.
  - The client can initiate connection closure by sending a line containing `GOODBYE` (any case), to which the server replies `GOODBYE`, or else the connection can close based on various timeouts and connection lifetime config.
  - Messages are read line by line, so every client message must end with a newline.

In all cases, the client may close the connection proactively causing the ongoing operation to abort.

#### APIs
###### <small>* TCP configuration APIs are always invoked via an HTTP listener, not on the TCP port that's being configured. </small>


|METHOD|URI|Description|
|---|---|---|
| POST       | /server/tcp/`{port}`/configure   | Reconfigure details of a TCP listener without having to close and restart. Accepts TCP Config JSON as payload (the whole config is replaced by the payload, so fields that are left out get their defaults). Applies to connections that come after the call. |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/read={duration}  | Set TCP read timeout for the port (applies to the modes that read from the client, e.g. echo and conversation) |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/write={duration}  | Set TCP write timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/idle={duration}  | Set TCP connection idle timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/connection/set<br/>/life={duration}  | Set TCP connection lifetime duration for the port (applies to all TCP connection modes except streaming) |
| POST, PUT  | /server/tcp/`{port}`/echo<br/>/response/set<br/>/delay={duration}  | Set response delay for TCP echo mode for the listener |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/payload={payloadSize}<br/>/duration={duration}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with the given total payload size delivered over the given duration with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/chunksize={chunkSize}<br/>/duration={duration}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with chunks of the given chunk size delivered over the given duration with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/chunksize={chunkSize}<br/>/count={chunkCount}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with total chunks matching the given chunk count of the given chunk size delivered with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`<br/>/expect/payload<br/>/length={length}  | Set expected payload length for payload verification mode (to only validate payload length, not content) |
| POST, PUT  | /server/tcp/`{port}`<br/>/expect/payload  | Set expected payload for payload verification mode, to validate both payload length and content. Expected payload must be sent as request body. |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/validate=`{enable}` | Enable/disable payload validation mode on a port to support payload length/content validation over connection lifetime (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/stream=`{enable}`  | Enable or disable streaming on a port without having to restart the listener (useful to disable streaming while retaining the stream configuration) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/echo=`{enable}` | Enable/disable echo mode on a port to let the port be tested in silent mode (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/conversation=`{enable}` | Enable/disable conversation mode on a port to support multiple packets verification (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/silentlife=`{enable}` | Enable/disable silent life mode on a port (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/closeatfirst=`{enable}` | Enable/disable `close at first byte` mode on a port (see overview for details) |
| POST, PUT  | /server/tcp/{port}/set/payload=`{enable}` | Enable/disable `payload` mode on a port, allowing for tcp connection to serve a pre-configured payload and close the connection (see overview for details) |
| GET  | /server/tcp/`{port}`/active | Get a list of active client connections for a TCP listener port |
| GET  | /server/tcp/active | Get a list of active client connections for all TCP listener ports |
| GET  | /server/tcp/`{port}`<br/>/history/{mode} | Get history list of client connections for a TCP listener port for the given mode (one of the supported modes given as text, case-insensitive: `SilentLife`, `CloseAtFirstByte`, `Echo`, `Stream`, `Conversation`, `Payload`. The name is matched as a substring of the connection's mode, and `Payload` matches both the `Payload` and the `Payload Validation` modes) |
| GET  | /server/tcp/`{port}`/history | Get history list of client connections for a TCP listener port |
| GET  | /server/tcp/history/{mode} | Get history list of client connections for all TCP listener ports for the given mode (see above) |
| GET  | /server/tcp/history | Get history list of client connections for all TCP listener ports |
| POST  | /server/tcp/`{port}`<br/>/history/clear | Clear history of client connections for a TCP listener port |
| POST  | /server/tcp/history/clear | Clear history of client connections for all TCP listener ports |

#### API Responses
All the APIs answer with a one-line text message (`200`), and with `400` and a message when the request is not valid. Responses of the `history` and `active` APIs are JSON. Only the methods listed in the table are served (for example `configure` is `POST` only, other methods get `404`).

|API|Status|Response body|
|---|---|---|
| `configure` | 200 | `TCP configuration applied to port {port}` |
| `configure` | 400 | `Failed to parse json with error: {error}` for a payload that is not JSON, or `[Invalid read timeout: -3s]` (similar for the write timeout, connection life, response delay and echo response delay) for a negative duration |
| any `/server/tcp/{port}/...` | 400 | `No listener for port {port}` (no listener, or the bootstrap port), or `Port {port} is not a TCP listener` |
| `timeout/set/...`, `connection/set/life=...`, `echo/response/set/delay=...` | 200 | `Read timeout set to 45s for listener {port}`, `Write timeout set to ...`, `Connection idle timeout set to ...`, `Connection will close 2m after creation for listener {port}`, `Response will be sent 250ms after connection for listener {port}` |
| the same | 400 | `Port [{port}]: Invalid duration: -5s` for a negative duration. Text that is not a duration (e.g. `abc`) is accepted and means zero |
| `stream/...` | 200 | `Connection will stream [5] chunks of size [200] with delay [1s] for a duration of [5s] for listener {port}` (the resolved values; the stream mode is turned on and the other modes off) |
| `stream/...` | 400 | `Invalid parameters for streaming`, when neither a chunk size nor a payload size is given, or neither a duration nor a chunk count is given |
| `expect/payload/length={length}` | 200 | `Stored expected payload length [10]` (turns on length validation, turns off the other modes, and forgets any stored payload) |
| `expect/payload` | 200 | `Stored expected payload content of length [11]` (turns on length and content validation, turns off the other modes) |
| `mode/validate={enable}` | 200 | `Will validate payload length [10]`, `Will validate payload content of size [11]`, or `Payload validation turned off` |
| `mode/{mode}={enable}`, `set/payload={enable}` | 200 | `Echo mode set to [true] for listener {port}`, `Stream mode set to [false] ...`, `Conversation mode ...`, `SilentLife mode ...`, `CloseAtFirstByte mode ...`, `Response Payload mode set to [true] ...` |
| `active`, `history` | 200 | JSON map of port to request ID to `{"config": {...}, "status": {...}}` (`{}` when there is none). `active` has `config` and `status` of the open connections; `history` has them for every connection served since the last clear. |
| `history/clear` | 200 | `TCP Connection History Cleared` (with ` for port {port}` for the port form) |

- `{enable}` is `y`, `yes`, `true` or `1` to turn a mode on; any other value turns it off. Turning a mode on turns the other modes off; turning it off leaves them as they are.
- `{duration}` uses the Go duration format (`500ms`, `30s`, `2m`). `{payloadSize}` and `{chunkSize}` take a number of bytes with an optional unit, where `K`/`KB` are 1000 bytes and `M`/`MB` are 1000000 bytes (units are case-sensitive).
- The connection history keeps one entry per connection for the life of the process, until it is cleared.

<details>
<summary>TCP API Examples</summary>

```
# add a TCP listener and give it an echo config
curl -X POST localhost:8080/server/listeners/add -d '{"port":9000,"protocol":"tcp","open":true}'
curl -X POST localhost:8080/server/tcp/9000/configure -d '{"echo":true,"echoResponseSize":5,"echoResponseDelay":"100ms","readTimeout":"1m"}'
TCP configuration applied to port 9000

# echo, 5 bytes at a time
printf 'hello world\n' | nc -w 2 localhost 9000
hello worl

# stream 1000 bytes in 5 chunks of 200 bytes, a second apart, to every client that connects
curl -X POST localhost:8080/server/tcp/9000/stream/payload=1K/duration=5s/delay=1s
Connection will stream [5] chunks of size [200] with delay [1s] for a duration of [5s] for listener 9000

# serve a canned response and keep the connection for 10 seconds
curl -X POST localhost:8080/server/tcp/9000/configure -d '{"payload":true,"responsePayloads":["HELLO","BYE"],"responseDelay":"200ms","keepOpen":true,"connectionLife":"10s"}'

# validate that clients send exactly these bytes (the verdict comes when the client closes its sending side, as nc does at the end of its input, or when the connection life ends)
curl -X PUT localhost:8080/server/tcp/9000/expect/payload -d 'SomePayload'
Stored expected payload content of length [11]
printf 'SomePayload' | nc -w 3 localhost 9000
[<host label>][SUCCESS]: Received payload matches expected payload of length [11] on port [9000]
```
</details>

<details>
<summary>Connection history and status output</summary>

`history` and `active` report, for each connection, the config that the connection works with (a copy taken when the connection arrived) and its status:

```
curl -s localhost:8080/server/tcp/9000/history
{"9000":{"1393":{"config":{"tls":false,"mtls":false,"readTimeout":"","writeTimeout":"","connectTimeout":"","connIdleTimeout":"","connectionLife":"3s","keepOpen":false,"payload":false,"stream":false,"echo":true,"conversation":false,"silentLife":true,"closeAtFirstByte":false,"validatePayloadLength":false,"validatePayloadContent":false,"expectedPayloadLength":0,"echoResponseSize":10,"echoResponseDelay":"","responsePayloads":null,"responseDelay":"","respondAfterRead":false,"streamPayloadSize":"","streamChunkSize":"0","streamChunkCount":0,"streamChunkDelay":"0s","streamDuration":"0s"},"status":{"port":9000,"listenerID":"9000-1","requestID":1393,"connStartTime":"2026-11-07T15:42:51.317218-08:00","connCloseTime":"2026-11-07 23:42:54.318452 +0000 UTC","firstByteInAt":"2026-11-07 23:42:51.317283 +0000 UTC","lastByteInAt":"2026-11-07 23:42:51.317283 +0000 UTC","firstByteOutAt":"2026-11-07 23:42:54.318346 +0000 UTC","lastByteOutAt":"2026-11-07 23:42:54.318346 +0000 UTC","totalBytesRead":21,"totalBytesSent":70,"totalReads":2,"totalWrites":1,"closed":true,"clientClosed":false,"serverClosed":true,"errorClosed":false,"readTimeout":false,"idleTimeout":false,"lifeTimeout":true,"writeErrors":0}}}}
```

|Status field|Meaning|
|---|---|
| port, listenerID, requestID | The listener port, the listener (`{port}-{generation}`) that accepted the connection, and the connection's sequence number on this `goto` instance |
| connStartTime, connCloseTime | When the connection was accepted and when it closed (empty while it is open) |
| firstByteInAt, lastByteInAt, firstByteOutAt, lastByteOutAt | UTC times of the first and last byte received from and sent to the client |
| totalBytesRead, totalBytesSent, totalReads, totalWrites | Counters of bytes and of read and write calls |
| closed | The connection is closed |
| clientClosed, serverClosed, errorClosed | Who closed it: the client, the server (by life or mode logic, or because the listener closed), or an I/O error |
| readTimeout, idleTimeout, lifeTimeout | Which timeout ended the connection |
| writeErrors | Number of failed writes |

Note that in the example above the config says `echo: true`, and the mode of the connection was `SilentLife` because `connectionLife` was set (`silentLife` is turned on in the connection's copy of the config to say so).
</details>

<br/>
<details>
<summary>TCP Config JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| tls | bool | Reported on the TCP config of a connection. TLS is set up by the listener (protocol `tls`), not by this field. |
| mtls | bool | Reported on the TCP config of a connection. mTLS is set up by the listener (`mTLS` field), not by this field. |
| readTimeout | duration | Read timeout to apply when reading data sent by the client (default `30s`). |
| writeTimeout | duration | Write timeout to apply when sending data to the client (default `30s`). |
| connectTimeout | duration | Max period that the server will wait during connection handshake (default `30s`). |
| connIdleTimeout | duration | Max period of inactivity (no bytes traveled) on the connection that would trigger closure of the client connection (default `30s`). |
| connectionLife | duration | Max lifetime after which the client connection will be terminated proactively by the server. Default none. A value above zero makes the connections `SilentLife` unless the config has `payload`, `stream`, `conversation` or payload validation on (see modes above). |
| keepOpen | bool | Controls whether the server should keep the connection open after sending the response. Currently this configuration is only used in `Payload` mode where the server is configured to send a set of pre-configured payloads. |
| payload | bool | Controls whether the listener should operate in `Payload` mode. |
| stream | bool | Controls whether the listener should operate in `Stream` mode. |
| echo | bool | Controls whether the listener should operate in `Echo` mode. |
| conversation | bool | Controls whether the listener should operate in `Conversation` mode. |
| silentLife | bool | Controls whether the listener should operate in `SilentLife` mode. |
| closeAtFirstByte | bool | Controls whether the listener should operate in `CloseAtFirstByte` mode. |
| validatePayloadLength | bool | Controls whether the listener should operate in `Payload Validation` mode for length. |
| validatePayloadContent | bool | Controls whether the listener should operate in `Payload Validation` mode for both content and length. |
| expectedPayloadLength | int | Set the expected payload length explicitly for length verification. Also used to auto-store the expected payload content length when validating content. See API for providing expected payload content. Must be between `0` and `67108864` (64 MiB), otherwise the configuration is rejected. The expected payload content itself can only be set via API `expect/payload`; a `configure` call clears it. |
| echoResponseSize | int | Configures the size of payload to be echoed back to the client. Server will only echo back when it has these many bytes received from the client. Default `10` (also used when the value is zero or negative). |
| echoResponseDelay | duration | Delay to be applied when sending response back to the client in echo mode. |
| responsePayloads | []string | A list of payloads to be used in `Payload` mode. When more than one payload is configured, each is sent in succession after applying the `responseDelay`. |
| responseDelay | duration | Delay to apply before sending each response payload in `Payload` mode. |
| respondAfterRead | bool | In `Payload` mode, this field controls whether the server should start sending payloads immediately or after waiting to receive at least one byte from the client. |
| streamPayloadSize | string (size) | Configures the total payload size to be streamed via chunks if streaming is enabled for the listener. |
| streamChunkSize | string (size) | Configures the size of each chunk of data to stream if streaming is enabled for the listener. |
| streamChunkCount | int | Configures the total number of chunks to stream if streaming is enabled for the listener. |
| streamChunkDelay | duration | Configures the delay to be added before sending each chunk back if streaming is enabled for the listener. |
| streamDuration | duration | Configures the total duration of stream if streaming is enabled for the listener. |

</details>

See [TCP Example](../../../docs/tcp-example.md)

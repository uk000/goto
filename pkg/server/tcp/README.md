## TCP Server Feature

#### TCP Listeners
- A TCP listener can be created using protocol `tcp` (plain TCP) or `tls` (TCP+TLS). TCP listeners can be opened at startup via command line arg as well as at runtime via admin APIs.
- Startup TCP listeners are opened with default TCP config (described below). Admin APIs allow reconfiguring TCP listeners with additional configs using the listener's `tcp` config JSON schema. Parameters such as listener mode, read/write/idle timeouts, connection lifetime, packet sizes, etc. can be configured. 

#### Server TCP Modes
A TCP listener can operate in 6 different modes to facilitate different kinds of testing: `Payload`, `Echo`, `Stream`, `Payload Validation`, `Conversation`, `Silent Life` and `Close At First Byte`. 

- <strong>Mode: `SilentLife`</strong>
  - If the listener is configured with a `connectionLife` that limits its lifetime, the listener operates in `SilentLife` mode
  - The listener waits for the configured lifetime and closes the client connection. It receives and counts the bytes received, but never responds. 
- <strong>Mode: `CloseAtFirstByte`</strong>
  - If the listener's `connectionLife` is set to zero, the listener operates in `CloseAtFirstByte` mode 
  - The listener waits for the first byte to arrive and then closes the client connection.
- <strong>Mode: `Echo`</strong>
  - The listener echoes back the bytes received from the client. 
  - Field `echoResponseSize` configures the echo buffer size, which is the number of bytes that the listener will need to receive from the client before echoing back. If more data is received than the `echoResponseSize`, it'll echo multiple chunks each of `echoResponseSize` size. 
  - Field `echoResponseDelay` configures the delay server should apply before sending each echo response packet.
  - In `echo` mode, the connection enforces `readTimeout` and `connIdleTimeout` based on the activity: any new bytes received reset the read/idle timeouts. It applies `writeTimeout` when sending the echo response to the client. 
  - If `connectionLife` is set, it controls the overall lifetime of the connection and the connection will close upon reaching the max life regardless of the activity.
- <strong>Mode: `Stream`</strong>
  - The listener starts streaming TCP bytes per the given configuration as soon as a client connects.
  - None of the timeouts or max life applies in streaming mode, and the client connection closes automatically once the streaming completes.
  - The stream behavior is controlled via the following fields: `streamPayloadSize`, `streamChunkSize`, `streamChunkCount`, `streamChunkDelay`, `streamDuration`. Not all of these fields are required, and a combination of some may lead to ambiguity that the server resolves by picking the most sensible combinations of these config params.
- <strong>Mode: `Payload`</strong>
  - The listener serves a set of pre-configured response payload(s) with an optional `responseDelay`.
  - If more than one payload is configured in `responsePayloads` array, the `responseDelay` gets applied before sending each item in the array. 
  - Field `respondAfterRead` controls whether response should be sent immediately or if a read should be performed before sending the response, in which case at least 1 byte must be received from the client before the response(s) are sent. 
  - Field `keepOpen` determines whether the connection is kept open after sending the last item in the array. 
  - If no `connectionLife` is configured explicitly, the connection life defaults to `30s` in this mode, and the connection is kept open for the remaining lifetime (computed from the start of request). 
  - Note that in this mode, the server keeps the connection open even if the client preemptively closes the connection.
- <strong>Mode: `Payload Validation`</strong>
  - The client should first set the payload expectation by calling either `/server/tcp/{port}/expect/payload/{length}` or `/server/tcp/{port}/expect/payload/{length}`, depending on whether server should just validate payload length or the payload content.
  - The server then waits for the duration of the connection lifetime (if not set explicitly for the listener, this feature defaults to `30s` of total connection life), and buffers bytes received from the client.
  - If at any point during the connection life the number of received bytes exceed the expected payload length, the server responds with error and closes connection.
  - If at the end of the connection life, the number of bytes match the payload expectations (either length or both length and content), then the server responds with a success message. The messages returned by the server are one of the following:
    - `[SUCCESS]: Received payload matches expected payload of length [l] on port [p]`
    - `[ERROR:EXCEEDED] - Payload length [l] exceeded expected length [e] on port [p]`
    - `[ERROR:CONTENT] - Payload content of length [l] didn't match expected payload of length [e] on port [p]`
    - `[ERROR:TIMEOUT] - Timed out before receiving payload of expected length [l] on port [p]`
- <strong>Mode: `Conversation`</strong>
  - The listener waits for the client to send a TCP payload with text `HELLO` to which server also responds back with `HELLO`.
  - All subsequent packets from the client should follow the format `BEGIN/`{text}`/END`, and the server echoes the received text back in the format of `ACK/`{text}`/END`.
  - The client can initiate connection closure by sending text `GOODBYE`, or else the connection can close based on various timeouts and connection lifetime config.

In all cases, the client may close the connection proactively causing the ongoing operation to abort.

#### APIs
###### <small>* TCP configuration APIs are always invoked via an HTTP listener, not on the TCP port that's being configured. </small>


|METHOD|URI|Description|
|---|---|---|
| POST, PUT  | /server/tcp/`{port}`/configure   | Reconfigure details of a TCP listener without having to close and restart. Accepts TCP Config JSON as payload. |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/read={duration}  | Set TCP read timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/write={duration}  | Set TCP write timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/timeout/set<br/>/idle={duration}  | Set TCP connection idle timeout for the port (applies to TCP echo mode) |
| POST, PUT  | /server/tcp/`{port}`<br/>/connection/set<br/>/life={duration}  | Set TCP connection lifetime duration for the port (applies to all TCP connection modes except streaming) |
| POST, PUT  | /server/tcp/`{port}`/echo<br/>/response/set<br/>/delay={duration}  | Set response delay for TCP echo mode for the listener |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/payload={payloadSize}<br/>/duration={duration}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with the given total payload size delivered over the given duration with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/chunksize={chunkSize}<br/>/duration={duration}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with chunks of the given chunk size delivered over the given duration with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`/stream<br/>/chunksize={chunkSize}<br/>/count={chunkCount}<br/>/delay={delay}  | Set TCP connection to stream data as soon as a client connects, with total chunks matching the given chunk count of the given chunk size delivered with the given delay per chunk |
| POST, PUT  | /server/tcp/`{port}`<br/>/expect/payload<br/>/length={length}  | Set expected payload length for payload verification mode (to only validate payload length, not content) |
| POST, PUT  | /server/tcp/`{port}`<br/>/expect/payload  | Set expected payload for payload verification mode, to validate both payload length and content. Expected payload must be sent as request body. |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/validate=`[y/n]` | Enable/disable payload validation mode on a port to support payload length/content validation over connection lifetime (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/stream=`[y/n]`  | Enable or disable streaming on a port without having to restart the listener (useful to disable streaming while retaining the stream configuration) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/echo=`[y/n]` | Enable/disable echo mode on a port to let the port be tested in silent mode (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/conversation=`[y/n]}` | Enable/disable conversation mode on a port to support multiple packets verification (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/silentlife=`[y/n]` | Enable/disable silent life mode on a port (see overview for details) |
| POST, PUT  | /server/tcp/`{port}`<br/>/mode/closeatfirst=`[y/n]` | Enable/disable `close at first byte` mode on a port (see overview for details) |
| POST, PUT  | /server/tcp/{port}/set/payload=`{enable}` | Enable/disable `payload` mode on a port, allowing for tcp connection to serve a pre-configured payload and close the connection (see overview for details) |
| GET  | /server/tcp/`{port}`/active | Get a list of active client connections for a TCP listener port |
| GET  | /server/tcp/active | Get a list of active client connections for all TCP listener ports |
| GET  | /server/tcp/`{port}`<br/>/history/{mode} | Get history list of client connections for a TCP listener port for the given mode (one of the supported modes given as text: `SilentLife`, `CloseAtFirstByte`, `Echo`, `Stream`, `Conversation`, `PayloadValidation`) |
| GET  | /server/tcp/`{port}`/history | Get history list of client connections for a TCP listener port |
| GET  | /server/tcp/history/{mode} | Get history list of client connections for all TCP listener ports for the given mode (see above) |
| GET  | /server/tcp/history | Get history list of client connections for all TCP listener ports |
| POST  | /server/tcp/`{port}`<br/>/history/clear | Clear history of client connections for a TCP listener port |
| POST  | /server/tcp/history/clear | Clear history of client connections for all TCP listener ports |

<br/>
<details>
<summary>TCP Config JSON Schema</summary>

|Field|Data Type|Description|
|---|---|---|
| readTimeout | duration | Read timeout to apply when reading data sent by the client. |
| writeTimeout | duration | Write timeout to apply when sending data to the client. |
| connectTimeout | duration | Max period that the server will wait during connection handshake. |
| connIdleTimeout | duration | Max period of inactivity (no bytes traveled) on the connection that would trigger closure of the client connection. |
| connectionLife | duration | Max lifetime after which the client connection will be terminated proactively by the server. |
| keepOpen | bool | Controls whether the server should keep the connection open after sending the response. Currently this configuration is only used in `Payload` mode where the server is configured to send a set of pre-configured payloads. |
| payload | bool | Controls whether the listener should operate in `Payload` mode. |
| stream | bool | Controls whether the listener should operate in `Stream` mode. |
| echo | bool | Controls whether the listener should operate in `Echo` mode. |
| conversation | bool | Controls whether the listener should operate in `Conversation` mode. |
| silentLife | bool | Controls whether the listener should operate in `SilentLife` mode. |
| closeAtFirstByte | bool | Controls whether the listener should operate in `CloseAtFirstByte` mode. |
| validatePayloadLength | bool | Controls whether the listener should operate in `Payload Validation` mode for length. |
| validatePayloadContent | bool | Controls whether the listener should operate in `Payload Validation` mode for both content and length. |
| expectedPayloadLength | int | Set the expected payload length explicitly for length verification. Also used to auto-store the expected payload content length when validating content. See API for providing expected payload content. |
| echoResponseSize | int | Configures the size of payload to be echoed back to the client. Server will only echo back when it has these many bytes received from the client. |
| echoResponseDelay | duration | Delay to be applied when sending response back to the client in echo mode. |
| responsePayloads | []string | A list of payloads to be used in `Payload` mode. When more than payloads are configured, each is sent in succession after applying the `responseDelay`. |
| responseDelay | duration | Delay to apply before sending each response payload in `Payload` mode. |
| respondAfterRead | bool | In `Payload` mode, this field controls whether the server should start sending payloads immediately or after waiting to receive at least one byte from the client. |
| streamPayloadSize | int | Configures the total payload size to be streamed via chunks if streaming is enabled for the listener. |
| streamChunkSize | int | Configures the size of each chunk of data to stream if streaming is enabled for the listener. |
| streamChunkCount | int | Configures the total number of chunks to stream if streaming is enabled for the listener. |
| streamChunkDelay | duration | Configures the delay to be added before sending each chunk back if streaming is enabled for the listener. |
| streamDuration | duration | Configures the total duration of stream if streaming is enabled for the listener. |

</details>

<details>
<summary>TCP Events</summary>

- `TCP Configuration Rejected`
- `TCP Configured`
- `TCP Connection Duration Configured`
- `TCP Streaming Configured`
- `TCP Expected Payload Configured`
- `TCP Payload Validation Configured`
- `TCP Mode Configured`
- `TCP Connection History Cleared`
- `New TCP Client Connection`
- `TCP Client Connection Closed`

</details>

See [TCP Example](tcp-example.md)

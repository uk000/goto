# <a name="-stream-chunked-payload"></a>

## > Stream (Chunked) Payload

This URI responds with either the request body (if one is sent) or a random-generated payload, where response behavior is controlled by the query parameters passed to the API. The feature allows requesting a custom number of chunks (or a custom response duration over which to stream), a custom chunk size, and a custom delay in-between chunked responses. Combination of these parameters define the total payload size and the total duration of the response.

Note: the API is at `/stream` only (there is no `/server/stream` alias, and no `/stream/payload=.../duration=...` path forms; those documented earlier do not exist). To stream a *pre-configured* payload for a URI, see the `set/stream/...` payload APIs in the [Response Payload](../response/payload/README.md) README.

Stream responses carry following headers:

- `Goto-Stream-Length: <total payload size>` (computed as `chunksize x count`; it is an estimate when the request body is shorter than that)
- `Goto-Stream-Duration: <total response duration>` (only when `duration` is used)
- `Goto-Chunk-Count: <total number of chunks>`
- `Goto-Chunk-Length: <per-chunk size>`
- `Goto-Chunk-Delay: <per-chunk delay>` (the `delay` param as given, empty if not given)
- `X-Content-Type-Options: nosniff`
- `Transfer-Encoding: chunked`
- `Content-Type`: the request's `Content-Type`, else `plain/text`

#### Stream APIs
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/stream | Stream 10 chunks of 10 bytes with a 1 second delay between chunks (the defaults below) |
| GET, PUT, POST  |	/stream?count=`{count}`&delay=`{delay}`&chunksize=`{chunk}` | Stream the payload as `count` chunks of `chunksize` bytes, waiting `delay` between chunks. Any of the three query params may be omitted. |
| GET, PUT, POST  |	/stream?duration=`{duration}`&delay=`{delay}`&chunksize=`{chunk}` | Stream for (about) the given duration: the chunk count is `duration / delay + 1`. `duration` takes precedence over `count` if both are given. Any of the params may be omitted. |

Parameters (all query params, all optional; `count`/`duration` and `delay`/`chunksize` combinations are all accepted):

|Param|Type|Default|Meaning|
|---|---|---|---|
| count     | int       | 10 | Number of chunks to send. A non-numeric or missing value gives the default (no error). |
| duration  | duration or `low-high` range | none | Total time to stream. If given, `count = duration / delay + 1`. A range picks a random duration |
| delay     | duration or `low-high` range | 1s | Delay between chunks; a range picks a random delay per chunk. `0`/missing means 1s |
| chunksize | size (`10`, `1K`, `2M`, ...) | 10 | Bytes per chunk |

Behaviour notes:
* Payload source: if the request carries a body, that body is split into `chunksize` pieces and streamed once (the stream stops when the body is used up, even if `count` is larger, although the `Goto-Chunk-Count` header says `count`). With no body, a random payload (starting with the listener label) of `chunksize` bytes is generated and repeated `count` times.
* Each chunk is followed by a newline.
* The server's own write timeout is lifted for the stream, so long durations are fine; the client sees chunks as they are flushed (use `curl --no-buffer`).
* Large `chunksize` (and `count`) values are not capped, they allocate memory for the generated payload.
* Responses are subject to the configured [response delay](../response/delay/README.md), which is applied once up front (`/stream?count=2` with a 200ms response delay and the default 1s chunk delay takes about 1.2s).

<br/>
<details>
<summary>Stream Response API Example</summary>

```
curl -v --no-buffer 'localhost:8080/stream?count=10&delay=300ms'

curl -v --no-buffer 'localhost:8080/stream?chunksize=100&duration=5s&delay=500ms-2s'

curl -v --no-buffer 'localhost:8080/stream?chunksize=100&count=5&delay=200ms'

curl -v --no-buffer 'localhost:8080/stream?duration=5s&delay=100ms-300ms'

# stream a given payload in 4 byte chunks
curl --no-buffer -X POST -d 'hello world' 'localhost:8080/stream?count=5&delay=50ms&chunksize=4'
hell
o wo
rld
```

Headers of `curl -i 'localhost:8080/stream?count=3&delay=100ms&chunksize=5'`:
```
HTTP/1.1 200 OK
Content-Type: plain/text
Goto-Chunk-Count: 3
Goto-Chunk-Delay: 100ms
Goto-Chunk-Length: 5
Goto-Stream-Length: 15
X-Content-Type-Options: nosniff
Transfer-Encoding: chunked
```

</details>

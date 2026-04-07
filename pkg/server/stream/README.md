# <a name="-stream-chunked-payload"></a>

## > Stream (Chunked) Payload

This URI responds with either pre-configured or random-generated payload where response behavior is controlled by the parameters passed to the API. The feature allows requesting a custom payload size, custom response duration over which to stream the payload, custom chunk size to be used for splitting the payload into chunks, and custom delay to be used in-between chunked responses. Combination of these parameters define the total payload size and the total duration of the response.

Stream responses carry following headers:

- `Goto-Stream-Length: <total payload size>`
- `Goto-Stream-Duration: <total response duration>`
- `Goto-Chunk-Count: <total number of chunks>`
- `Goto-Chunk-Length: <per-chunk size>`
- `Goto-Chunk-Delay: <per-chunk delay>`
- `X-Content-Type-Options: nosniff`
- `Transfer-Encoding: chunked`

#### Stream APIs
|METHOD|URI|Description|
|---|---|---|
| GET, PUT, POST  |	/stream/payload=`{size}`<br/>/duration={duration}<br/>/delay={delay} | Respond with a payload of the given size delivered over the given duration with the given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/duration={duration}<br/>/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered over the given duration with given delay per chunk. Both `duration` and `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/chunksize={chunk}<br/>/count={count}/delay={delay} | Respond with either pre-configured default payload or generated random payload split into chunks of given chunk size, delivered the given count of times with given delay per chunk. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/duration={duration}<br/>/delay={delay} | Respond with pre-configured default payload split into enough chunks to spread out over the given duration with given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |
| GET, PUT, POST  |	/stream/count={count}/delay={delay} | Respond with pre-configured default payload split into the given count of chunks with the given delay per chunk. This URI requires a default payload to be set via payload API. `delay` can be either a single duration or a `low-high` range, in which case a random duration will be picked from the given range. |

<br/>
<details>
<summary>Stream Response API Example</summary>

```
curl -v --no-buffer localhost:8080/stream/payload=10K/duration=5s-15s/delay=100ms-1s

curl -v --no-buffer localhost:8080/stream/chunksize=100/duration=5s/delay=500ms-2s

curl -v --no-buffer localhost:8080/stream/chunksize=100/count=5/delay=200ms

curl -v --no-buffer localhost:8080/stream/duration=5s/delay=100ms-300ms

curl -v --no-buffer localhost:8080/stream/count=10/delay=300ms
```

</details>

# connectprobe

Times each phase of a call that goes through an HTTP CONNECT proxy (a `goto` port in [CONNECT mode](../../pkg/proxy/tcp/README.md#http-connect-proxy), or any other proxy), with the DNS resolver and dialer of Go, which are what the `goto` client uses. It exists for the question "the proxy and the server both answer in a few milliseconds, so why does the client take so long?": `curl` has a resolver of its own, and its own way of falling back between IPv4 and IPv6, so what it measures can differ from what a `goto` client gets. **Run it where the client runs** (e.g. in the pod of the client `goto`), with the names that the client uses.

It uses nothing but the Go standard library, so the binary is small and can be copied into a pod.

## Usage

```
connectprobe -proxy <[http[s]://]host:port> -url <[http[s]://]host[:port][/path]> [options]
```

| Option | Default | |
|---|---|---|
| `-proxy` | | The proxy, as `host:port` or a URL. `https://` means that the proxy is reached over TLS. `user:password@` in it is sent as basic proxy authorization, and is not printed. Required. |
| `-url` | | The URL to GET through the proxy. `https://` is a TLS handshake with the target, over the tunnel. Required. |
| `-n` | 5 | Number of calls. Each one is made on a connection of its own, as the `goto` client makes them. |
| `-interval` | 0 | Wait between the calls, e.g. `30s`. Caches that expire (DNS) come back between calls that are far enough apart. |
| `-timeout` | 10s | Limit for a call, and for each connection that the probe opens. |
| `-verify` | false | Verify the certificates of the proxy and the target. The `goto` client doesn't, by default. |
| `-check-addrs` | false | After the calls, resolve the name of the proxy again, and connect to each address on its own. The proxy logs these bare connections as clients that sent no request. |
| `-json` | false | Print one JSON document (durations in milliseconds, `null` for a phase that the call didn't have) instead of the table. |

Exit status: `0` if all calls got a response (of any status), `1` if a call failed or the probe was interrupted, `2` for wrong usage.

## Output

```
$ connectprobe -proxy goto-proxy:9000 -url http://server:8081/echo -n 3 -check-addrs
proxy http://goto-proxy:9000 -> http://server:8081/echo

call  dns        tcp        proxy-tls  connect    tls        ttfb       body       total      status
1     2.81ms     0.52ms     -          87.81ms    -          6.90ms     0.56ms     98.70ms    200
      CONNECT answer: Goto-Connect-Proxy-Took=84.33ms
      response: Goto-Took=172.58µs Goto-Connect-Proxy-Took=5.63ms
2     2.52ms     0.18ms     -          2.13ms     -          2.07ms     0.14ms     7.05ms     200
...
```

| Phase | What it is |
|---|---|
| `dns` | Resolving the name of the proxy. `-` for an IP address. |
| `tcp` | Connecting to the proxy once its name is resolved. It includes the wait for the other address family (Go gives the first one 300ms before it tries the other). |
| `proxy-tls` | TLS handshake with the proxy, if it is an `https` one. |
| `connect` | The CONNECT exchange. The proxy connects to the target (resolving its name, too) before it answers, so this is where a slow lookup or connection **at the proxy** shows. |
| `tls` | TLS handshake with the target, if it is an `https` one. |
| `ttfb` | From sending the request to getting the head of the response. |
| `body` | Reading the body of the response. |
| `total` | From the start of the call to the end of the body. |

The lines under a call have the headers of `goto` that tell where the time went on its side: `Goto-Took` (the server), `Goto-Connect-Proxy-Took` and `Goto-Connect-Proxy-Delay` (a `goto` CONNECT proxy), in the answer to CONNECT and in the response, and `Goto-Connect-Proxy-DNS-Took` and `Goto-Connect-Proxy-Dial-Took` in the answer to CONNECT (how the proxy spent its time on the lookup of the target and the connection to it). `Goto-Connect-Proxy-Took` in the answer to CONNECT is the time that the proxy took from the first byte of the CONNECT request to its answer, which includes its own lookup of the target and the connection to it, so a large `connect` with a large value there is the proxy, and a large `connect` with a small one is the network between the client and the proxy.

After two or more calls there is a summary (min, average and max of each phase, over the calls that got a response). If the first call is much slower than the ones after it, the probe says which phase has the extra time. That is what a cold cache looks like, usually DNS: on the client, or on the proxy for the name of the target. Use `-interval` to see it come back once the entries of the cache expire.

## How to read it

| What you see | Likely cause |
|---|---|
| `dns` is large, mostly on the first call | Cold DNS. Search domains (`ndots`) multiply the lookups of a short name, and each miss is forwarded upstream. Use a fully qualified name with a trailing dot, or set `dnsConfig` `ndots:1` for the pod. Avoid `.local` for names in a cluster. |
| `tcp` is around 300ms, or `-check-addrs` shows one address that is slow or dead | The dialer waits for the other address family. Fix the address family that doesn't work. |
| `connect` is large, and `Goto-Connect-Proxy-Took` of the answer is large too | The proxy is slow to look up or connect to the target: `Goto-Connect-Proxy-DNS-Took` and `Goto-Connect-Proxy-Dial-Took` of the answer say which. The same checks apply on the proxy's side. |
| `connect` or `ttfb` is large, and `Goto-Connect-Proxy-Took` is small | The network between the client and the proxy. |
| Everything is small here, and the client still reports a large time | The time is not in these phases: it is in the client itself. |

## Build

```
make connect-probe PROBE_ARGS="-proxy goto-proxy:9000 -url http://server:8081/echo -n 5"   # builds and runs it
make connect-probe PROBE_ARGS="-h"                                                         # the usage

make connect-probe-build PROBE_OS=linux PROBE_ARCH=arm64    # bin/connectprobe-linux-arm64, static
make connect-probe-build-all                                # linux, amd64 and arm64

# to the pod of the client, and run it there
kubectl cp bin/connectprobe-linux-amd64 <namespace>/<pod>:/tmp/connectprobe
kubectl exec <pod> -- chmod +x /tmp/connectprobe
kubectl exec <pod> -- /tmp/connectprobe -proxy goto-proxy:9000 -url http://server:8081/echo -n 5 -interval 30s
```

`kubectl cp` needs `tar` in the container. Without it, run the probe from any other pod or host on the same network, using the names that the client uses.

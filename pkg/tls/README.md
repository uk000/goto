# TLS Package REST APIs

This package provides REST APIs for managing TLS certificates and configuration.

## TLS Startup Configs
```
tls:
  spiffeSocket: "unix:///tmp/spire-agent/public/api.sock"
  caCerts:
    - name: myca
      domain: "goto.client"
    - name: myca
      domain: "goto.goto"
      key: "$caKey"
      cert: "$caCert"
  certs:
    - port: 8443
      domain: "goto.goto"
      san: ["goto.1", "goto.2"]
      spiffeID: "spiffe://cluster.local/ns/goto/sa/goto-8443"
    - name: clientCert
      domain: "goto.client"
      spiffeID: "spiffe://cluster.local/ns/goto/sa/goto-client"

```

## TLS APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST     | /tls/ca/cert/add/{name}/{domain} | Add CA Certificate. Body: Certificate data (PEM format) |
|POST     | /tls/ca/cert/remove/{name} | Remove CA Certificate |
|POST     | /tls/ca/key/add/{name}/{domain} | Add CA Key |
|POST     | /tls/ca/key/remove/{name} | Remove CA Key |
|GET     | /tls/ca/certs | Get CA Certificates |
|POST     | /tls/cert/add/{name} | Add Certificate under a Name to be referenced for client configs. Note: Server certs are added via listeners. |
|POST     | /tls/cert/remove/{name} | Remove a Named Certificate |
|POST     | /tls/key/add/{name} | Add Key under a Name to be referenced for client configs. Note: Server certs are added via listeners.|
|POST     | /tls/key/remove/{name} | Add a Named Key |
|POST     | /tls/certs | Get summary info for all uploaded certificates |
|POST     | /tls/certs/raw | Get full raw info for all uploaded certificates |
|POST     | /tls/workdir/set | Set directory where uploaded certificates will be stored  |

## For mTLS Usage
### 1. Configure Listener to use mTLS
goto --ports 8080,8443/https/mtls

### 2. Optional: Configure Listener to use Spiffe Certificate via startup config
Listener marked with `https/mtls` to indicate this listener do mTLS handshake with clients (without verifying client certs).
```
goto --ports 8080,8443/https/mtls
```

The mTLS port can either use auto-generated certs, or startup config can supply certs for the port:

```
tls:
  spiffeSocket: "unix:///tmp/spire-agent/public/api.sock"
  certs:
    - port: 8443
      domain: "goto.goto"
      san: ["goto.1", "goto.2"]
      spiffeID: "spiffe://cluster.local/ns/goto/sa/goto-8443"
```

### 3. Upload certificate for client mTLS via startup config
On the client side, the startup config can ask `goto` to load SPIFFE cert and store it under name `mycert`.

```
tls:
  spiffeSocket: "unix:///tmp/spire-agent/public/api.sock"
  certs:
    - name: mycert
      domain: "goto.client"
      spiffeID: "spiffe://cluster.local/ns/goto/sa/goto-client"
```

### 4. Check certs loaded on the client and server
```
curl -s localhost:8080/tls/certs
```

### 5. Ask goto to send an HTTP request using the client cert
```
curl -v localhost:8080/client/http/invoke -d '{"url": "localhost:8443", "tls": true, "authority": "goto.goto", "noSNI": false, "alpn":["istio-h2"], "clientCert": "mycert"}' -H'Accept: yaml'
```

Client will perform mTLS handshake for the above request, and the response will show the mTLS details through headers `Goto-Mtls`, `Request-Goto-Client-Mtls`, `Goto-Client-Cert`, `Goto-Server-Cert`. The server also sends the above headers within the default response payload JSON (unless Goto has been configured with a custom payload).


Server reports the following headers to the client:
`Goto-Mtls`: whether mTLS was performed on this request
`Goto-Sni`: SNI sent by the client
`Goto-Tls`: whether TLS was performed
`Goto-Protocol`: which protoco was used. HTTP/2 or HTTPS indicate TLS, whereas H2C or HTTP/1.1 indicate plain.
`Request-Tls-Sni`: SNI sent by the client
`Request-Tls-Version`: TLS version
`Goto-Client-Cert`: cert presented by the client to the server, including Spiffe ID, SNI, and ALPNs presented and negotiated.
`Goto-Server-Cert`: cert presented by the server to the client, including Spiffe ID, SNI, and ALPNs presented and negotiated.

Client reports the server cert info under `serverCert` for each request, and `serverCerts` as a cumulative list of all server certs. Client also reports its own cert that was used for mTLS under `clientCert`.

```
Results:
  https://localhost:8443[1]:
    Status: 200
    headers:
      Goto-Client-Cert:
      - 'RemoteAddr: [::1]:53848;; Subject: org=[SPIRE],co=[US],cn=[];; URIs: [spiffe://cluster.local/ns/goto/sa/goto-client];; SNI: goto.goto;; ALPN: [h2];; Issuer: org=[SPIFFE],co=[US],sn=[274732312709532820115529507679155363266];; NegotiatedALPN: h2;; Finished: true;;'
      Goto-Host:
      - Goto-Local-Pod.NS-3K[1.1.1.1](LocalNode[0.0.0.0]@local)
      Goto-Mtls:
      - "true"
      Goto-Port:
      - "8443"
      Goto-Protocol:
      - HTTP/2
      Goto-Remote-Address:
      - '[::1]:53848'
      Goto-Response-Status:
      - "200"
      Goto-Server-Cert:
      - 'Subject: org=[SPIRE],co=[US],cn=[];; URIs: [spiffe://cluster.local/ns/goto/sa/goto-8443];; ALPN: [istio istio-http/1.1];; Issuer: org=[SPIFFE],co=[US],sn=[274732312709532820115529507679155363266];; Finished: true;;'
      Goto-Sni:
      - goto.goto
      Goto-Tls:
      - "true"
      Goto-Took:
      - 480.334µs
      Request-Accept:
      - yaml
      Request-Goto-Client-Mtls:
      - "true"
      Request-Goto-Client-Tls:
      - "true"
      Request-Host:
      - goto.goto
      Request-Method:
      - POST
      Request-Tls-Sni:
      - goto.goto
      Request-Tls-Version:
      - "1.3"
      Request-Uri:
      - /
      Request-User-Agent:
      - Goto-Local-Pod.NS-3K[1.1.1.1](LocalNode[0.0.0.0]@local)
      Via-Goto:
      - '[Goto:8443][Goto-Local-Pod@NS-3K@local](HTTP)'
    payload: |+
      Goto-Client-Cert: 'RemoteAddr: [::1]:53848;; Subject: org=[SPIRE],co=[US],cn=[];; URIs: [spiffe://cluster.local/ns/goto/sa/goto-client];; SNI: goto.goto;; ALPN: [h2];; Issuer: org=[SPIFFE],co=[US],sn=[274732312709532820115529507679155363266];; NegotiatedALPN: h2;; Finished: true;;'
      Goto-Host: Goto-Local-Pod.NS-3K[1.1.1.1](LocalNode[0.0.0.0]@local)
      Goto-Listener: '[Goto:8443][Goto-Local-Pod@NS-3K@local]'
      Goto-Port: "8443"
      Goto-Remote-Address: '[::1]:53848'
      Goto-SNI: goto.goto
      Goto-Server-Cert: 'Subject: org=[SPIRE],co=[US],cn=[];; URIs: [spiffe://cluster.local/ns/goto/sa/goto-8443];; ALPN: [istio istio-http/1.1];; Issuer: org=[SPIFFE],co=[US],sn=[274732312709532820115529507679155363266];; Finished: true;;'
      Goto-TLS: true
      Goto-mTLS: true
      Request-Host: goto.goto
      Request-Method: POST
      Request-PayloadSize: 0
      Request-Protocol: HTTP/2.0
      Request-Query: ""
      Request-URI: /
      Response-PayloadSize: 0
      Via-Goto: '[Goto:8443][Goto-Local-Pod@NS-3K@local]'
      headers:
        Request-Accept:
        - yaml
        Request-Accept-Encoding:
        - gzip
        Request-Content-Length:
        - "0"
        Request-Goto-Client-Mtls:
        - "true"
        Request-Goto-Client-Tls:
        - "true"
        Request-User-Agent:
        - Goto-Local-Pod.NS-3K[1.1.1.1](LocalNode[0.0.0.0]@local)
    serverCert: 'RemoteAddr: localhost:8443;; Subject: org=[SPIRE],co=[US],cn=[];; URIs: [spiffe://cluster.local/ns/goto/sa/goto-8443];; ALPN: [istio istio-http/1.1];; Issuer: org=[SPIFFE],co=[US],sn=[274732312709532820115529507679155363266];; Finished: true;;'
Statuses:
- 200
clientCert: 'RemoteAddr: [::1]:53847;; Subject: org=[SPIRE];co=[US],cn=[];; URIs:
  [spiffe://cluster.local/ns/goto/sa/goto-client];; ALPN: [istio-http/1.1 istio-h2];;
  Issuer: org=[SPIFFE];co=[US],sn=[274732312709532820115529507679155363266];;Finished:
  true;;'
serverCerts:
- 'RemoteAddr: localhost:8443;; Subject: org=[SPIRE],co=[US],cn=[];; URIs: [spiffe://cluster.local/ns/goto/sa/goto-8443];; ALPN: [istio istio-http/1.1];; Issuer: org=[SPIFFE],co=[US],sn=[274732312709532820115529507679155363266];; Finished: true;;'
url: localhost:8443

```

#### 6. Check client certs recorded for a server port
A server listener API can be used to see a log of client certs info for recent requests served on this port (since the last time it was cleared).
```
curl -s localhost:8080/server/listeners/8443/client/certs
```
Output:
```
"8443":
  '[::1]:53848':
    alpn:
    - h2
    endAt: "2026-06-18T15:36:49.047915-07:00"
    finished: true
    issuer: org=[SPIFFE];co=[US],sn=[274732312709532820115529507679155363266]
    negotiated: h2
    remoteAddr: '[::1]:53848'
    sni: goto.goto
    startAt: "2026-06-18T15:36:49.044292-07:00"
    status:
    - 'HandleClientHello: Server Reporting ALPNs on Port [8443] Label [Server-[Goto:8443][Goto-Local-Pod@NS-3K@local]]
      RemoteAddr [[::1]:53848] - ALPNs: [h2 istio istio-http/1.1]'
    - 'VerifyConnection: Stored Certificate Info on Port [8443] Label [Server-[Goto:8443][Goto-Local-Pod@NS-3K@local]]
      RemoteAddr [[::1]:53848] - CN: [], SAN: [], URIs: [spiffe://cluster.local/ns/goto/sa/goto-client],
      Issuers: org=[SPIFFE];co=[US],sn=[274732312709532820115529507679155363266],
      SNI: [goto.goto], NegotiatedProtocol: [h2]'
    - Peer Cert Reported
    subject: org=[SPIRE];co=[US],cn=[]
    uris:
    - spiffe://cluster.local/ns/goto/sa/goto-client
```

#### 6. Clear the client cert records
```
curl -XPOST localhost:8080/server/listeners/8443/client/certs/clear
```


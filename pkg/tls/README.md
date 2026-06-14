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
#### 1. Configure Listener to use mTLS
goto --ports 8080,8443/https/mtls

#### 2. Optional: Configure Listener to use Spiffe Certificate via startup config
goto --ports 8080,8443/https/mtls
```
tls:
  spiffeSocket: "unix:///tmp/spire-agent/public/api.sock"
  certs:
    - port: 8443
      domain: "goto.goto"
      san: ["goto.1", "goto.2"]
      spiffeID: "spiffe://cluster.local/ns/goto/sa/goto-8443"
```

#### 3. Upload certificate for client mTLS via startup config
```
tls:
  spiffeSocket: "unix:///tmp/spire-agent/public/api.sock"
  certs:
    - name: clientCert
      domain: "goto.client"
      spiffeID: "spiffe://cluster.local/ns/goto/sa/goto-client"
```

#### 4. Use client API with certificate reference
```
curl -v localhost:8080/client/http/invoke -d '{"url": "localhost:8443", "tls": true, "authority": "goto.goto", "noSNI": false, "alpn":["istio-http/1.1", "istio-h2"], "clientCert": "clientCert"}' -H'Accept: yaml'
```

Client will perform mTLS handshake for the above request, and the response will show the mTLS details:
```
Results:
  https://localhost:8443[1]:
    Status: 200
    headers:
      Goto-Mtls:
      - "true"
      Goto-Peer-Cert-Info:
      - '{"uris":["spiffe://cluster.local/ns/goto/sa/goto-client"],"sni":"goto.goto","alpn":["h2","http/1.1","istio","istio-http/1.1","istio-h2"],"negotiated":"h2"}'
      Goto-Port:
      - "8443"
      Goto-Protocol:
      - HTTP/2
      Goto-Response-Status:
      - "200"
      Goto-Sni:
      - goto.goto
      Goto-Tls:
      - "true"
      Request-Tls-Sni:
      - goto.goto
      Request-Tls-Version:
      - "1.3"
    payload: |+
      Goto-Peer-Cert-Info: '{"uris":["spiffe://cluster.local/ns/goto/sa/goto-client"],"sni":"goto.goto","alpn":["h2","http/1.1","istio","istio-http/1.1","istio-h2"],"negotiated":"h2"}'
      Goto-Port: "8443"
      Goto-SNI: goto.goto
      Goto-TLS: true
      Goto-mTLS: true
    peerCert: '{"uris":["spiffe://cluster.local/ns/goto/sa/goto-8443"],"sni":"goto.goto","negotiated":"h2"}'
Statuses:
- 200
peerCerts:
- '{"uris":["spiffe://cluster.local/ns/goto/sa/goto-8443"],"sni":"goto.goto","negotiated":"h2"}'
```

In the above response example, server on port 8443 reported the following headers to the client:
`Goto-Mtls`: whether mTLS was performed on this request
`Goto-Peer-Cert-Info`: cert presented by the client to the server, including Spiffe ID, SNI, and ALPNs presented and negotiated.
`Goto-Sni`: SNI sent by the client
`Goto-Tls`: whether TLS was performed
`Goto-Protocol`: which protoco was used. HTTP/2 or HTTPS indicate TLS, whereas H2C or HTTP/1.1 indicate plain.
`Request-Tls-Sni`: SNI sent by the client
`Request-Tls-Version`: TLS version

Client reports the server cert info under `peerCerts`, including Spiffe ID, SNI, and negotiated ALPN.
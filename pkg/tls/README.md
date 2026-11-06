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

|Field|Description|
|---|---|
| `spiffeSocket` | Address of the SPIFFE workload API socket that X509-SVIDs are loaded from for the entries that have a `spiffeID` |
| `caCerts[].name`, `caCerts[].domain` | Both are required. The CA is stored under the name, for the domain. The same name can be repeated for more domains |
| `caCerts[].key`, `caCerts[].cert` | PEM text of the CA key and cert |
| `certs[].port` | The listener port that the cert is for. One of `port` and `name` is required |
| `certs[].name` | Name under which the cert is stored, to be used as `clientCert` in client configs |
| `certs[].domain`, `certs[].san` | Common name, and the alternate names, of an auto-generated cert |
| `certs[].spiffeID` | The SPIFFE ID of the cert: loaded from the SPIFFE socket, or put in the URI SAN of an auto-generated cert |
| `certs[].autoCert` | `true` to have the cert generated |
| `certs[].key`, `certs[].cert` | PEM text of the cert and key. Both are required unless `spiffeID` or `autoCert` is given |

## TLS APIs
###### <small>* These APIs can be invoked with prefix `/port={port}/...` to configure/read data of one port via another.</small>

|METHOD|URI|Description|
|---|---|---|
|POST, PUT | /tls/ca/cert/add/{name}/{domain} | Add CA Certificate for a domain. Body: Certificate data (PEM format, or base64 of it). A name that already has a cert keeps the first cert and just gets the domain added |
|POST, PUT | /tls/ca/cert/remove/{name} | Remove CA Certificate |
|POST, PUT | /tls/ca/key/add/{name}/{domain} | Add CA Key for a domain. Body: private key (PEM format, or base64 of it). A name that already has a key keeps the first key |
|POST, PUT | /tls/ca/key/remove/{name} | Remove CA Key |
|POST, PUT | /tls/ca/set/default/{name} | Sets a CA Key to be used as default signing key for signing. Only an RSA key of a name that exists has an effect |
|GET     | /tls/ca/certs | Get CA Certificates, with the domains they are for |
|GET     | /tls/ca/jwks | Serves Default Signing CA's JWKS for client-side signature verification |
|GET     | /tls/ca/pubkey | Serves the public key of the Default Signing CA, in PEM format |
|GET     | /tls/ca/cert | Serves the certificate of the Default Signing CA, in PEM format |
|POST, PUT | /tls/ca/verify | Verifies a given JWT (from request payload). Verification result is reflected in HTTP status code, and the JWT payload is parsed and reported in response body of the API. |
|POST, PUT | /tls/cert/add/{name} | Add Certificate under a Name to be referenced for client configs. Body: PEM, or base64 (URL-safe, unpadded) of it. Note: Server certs are added via listeners. |
|POST, PUT | /tls/cert/remove/{name} | Remove a Named Certificate |
|POST, PUT | /tls/key/add/{name} | Add Key under a Name to be referenced for client configs. Body: PEM, or base64 (URL-safe, unpadded) of it. Note: Server certs are added via listeners.|
|POST, PUT | /tls/key/remove/{name} | Remove a Named Key |
|GET      | /tls/certs | Get summary info for the certificates that were loaded: used from an upload, generated, or loaded from SPIFFE. A cert that was only uploaded is not listed until it is used |
|GET      | /tls/certs/raw | Same output as `/tls/certs` |
|POST, PUT | /tls/workdir/set?dir={dir} | Set directory where generated certificates will be stored. The `dir` query param is required |

Server certificates of a listener are managed with the listener APIs, which are described in the [server README](../server/README.md) and summarized [below](#listener-certificates).

### Named certs and keys
A client config can refer to a cert by the name that it was uploaded under (`"clientCert": "mycert"`). The cert and the key are uploaded with two calls, in any order. The body is the PEM text, or the base64 of the PEM text.
```
curl -X POST localhost:8080/tls/cert/add/mycert --data-binary @cert.pem
Cert stored for name [mycert]

curl -X POST localhost:8080/tls/key/add/mycert --data-binary @key.pem
Key stored for name [mycert]

curl -X POST localhost:8080/tls/cert/add/mycert
No Payload                                (status 400, for an empty body)

curl -X POST localhost:8080/tls/cert/remove/mycert
Cert Removed for name [mycert]            (always 200, also when the name does not exist)
```
- Uploads are not validated: any text is accepted with status 200. The cert and key are only combined and parsed when the name is first used, so a pair that doesn't match shows up as a failure of that use.
- Uploading again, or removing, the cert or the key of a name drops the parsed pair, which is rebuilt from the uploaded data on its next use.
- `GET /tls/certs` lists the pairs that have been parsed. A pair that was just uploaded is not listed until a client config has used it (or it was generated, or loaded from SPIFFE). The listing is in `openssl`-like text, with one block for each name; `/tls/certs/raw` returns the same text:
```
curl -s localhost:8080/tls/certs
Certificate [mycert]:
  Version: 3
  Serial Number: 47204657729844007884600553735063539720227586741
  Signature Algorithm: SHA256-RSA
  Issuer: CN=Test CA
  Validity:
    Not Before: Nov  7 22:43:50 2026 GMT
    Not After : Dec  7 22:43:50 2026 GMT
  Subject: CN=Test CA
  Subject Public Key Info:
    Public Key Algorithm: RSA
  X509v3 Extensions:
    Basic Constraints: CA:true
    Subject Key Identifier: 23:6A:6E:DB:78:D2:F4:DC:5B:2A:24:D7:4F:5F:6F:C7:F4:24:10:15
  SHA-256 Fingerprint: 4C:9C:CB:05:5E:DA:F3:46:AE:CC:25:04:7F:63:5E:12:0B:9C:B3:E8:86:2A:AC:38:01:87:02:CD:01:36:23:08
  Chain Length: 1
```
(Key Usage, Extended Key Usage, Authority Key Identifier and Subject Alternative Name lines are added when the cert has them.)

### CA certs, keys and the default signing CA
CAs are kept by name, with the domains that they are for (a name can be for several domains, by repeating the `add` calls with the same name). CA certs also go into the pool of root CAs that goto uses to verify peers. When `goto` generates a cert for a domain, it signs it with the CA whose cert and key were registered for that domain, and makes it self-signed when there is none.
```
curl -X POST localhost:8080/tls/ca/cert/add/myca/example.com --data-binary @ca-cert.pem
CA Cert stored for name [myca]

curl -X POST localhost:8080/tls/ca/key/add/myca/example.com --data-binary @ca-key.pem
CA Key stored for name [myca]

curl -s localhost:8080/tls/ca/certs
myca:
  map[example.com:true]: |
    -----BEGIN CERTIFICATE-----
    ...
```
(The domains of the CA are printed as a `map[...]` text in the YAML key, for now.)

- Adding a cert or key for a name that has one keeps the first one, and still answers `stored`. Remove the CA cert (or key) first to replace it.
- At startup, `goto` generates an RSA key (2048 bits), with a self-signed cert (`CN=IDP Signing Key`, 10 years), and serves its JWKS. `POST /tls/ca/set/default/{name}` replaces them with the RSA key and cert of a CA that was uploaded under the name. The call answers `CA [name] set as default signing authority` with status 200 also when nothing was changed, e.g. because the name is not known, or its key is not an RSA key in PEM.
```
curl -s localhost:8080/tls/ca/jwks
{
  "keys": [
    {
      "kty": "RSA",
      "use": "sig",
      "alg": "RS256",
      "kid": "Pb41ABdnGe1c_C78ZG2eTkAHTP4",
      "n": "l5tVuTGyExzo...",
      "e": "AQAB",
      "x5c": [ "MIIC5DCCAcyg..." ]
    }
  ]
}

curl -s localhost:8080/tls/ca/pubkey
-----BEGIN PUBLIC KEY-----
...

curl -s localhost:8080/tls/ca/cert
-----BEGIN CERTIFICATE-----
...
```
(`/tls/ca/pubkey` and `/tls/ca/cert` answer 400 with `No CA JWKS configured` / `No CA certificate available` if there is no default key or no cert for it.)

#### Verifying a JWT
`POST /tls/ca/verify` takes a JWT as the body, and checks its RS256 signature against the default signing CA (the key with the `kid` of the token, or the first key if the token has no `kid`). Claims such as `exp` are not checked. The response is the payload of the token, pretty-printed; the status is 200 for a valid signature and 400 for anything else (the payload is still shown if it can be decoded).
```
curl -s -X POST localhost:8080/tls/ca/verify -d "$jwt"          # signed with the default CA key
{
  "sub": "u1",
  "iss": "goto"
}
[200]

curl -s -X POST localhost:8080/tls/ca/verify -d "$tampered"     # payload changed after signing
{
  "sub": "evil"
}
[400]

curl -s -X POST localhost:8080/tls/ca/verify -d ""
No JWT payload                                                   [400]

curl -s -X POST localhost:8080/tls/ca/verify -d "a.b.c"
Payload (base64 decode error): illegal base64 data at input byte 0   [400]
```

### Auto-generated certs
Where a cert is to be generated (a listener asked for `autoCert`, `cert/auto/{domain}` or `cert/autosni` of the listener API, or a startup config entry with `autoCert: true`), `goto` creates an ECDSA P-384 key and a certificate valid for one year, with the common name, organization and organizational unit set to the first domain, all the domains as DNS names, the SPIFFE ID (if given) as a URI name, and server and client auth key usages. It is signed by the CA of the domain (see above) or is self-signed. A cert that is generated for a listener is saved as PEM files `{label}-{port}-cert.pem` and `{label}-{port}-key.pem` in the work directory, which is set with `/tls/workdir/set` (the label of the listener is sanitized, e.g. `172192179723082m-C5VYWYNJHP-local-LocalCluster-23082-cert.pem`; the key file is only readable by the owner).
```
curl -X POST 'localhost:8080/tls/workdir/set?dir=/tmp/goto-certs'
Working directory set to [/tmp/goto-certs]

curl -X POST localhost:8080/tls/workdir/set?dir=
Missing directory path                    (status 200)
```
Without the `dir` param the URI is not found (404). The directory is created when a cert is saved; the path is not checked when it is set.

If the CA that is registered for a domain has a cert or key that can't be parsed, generating the cert fails: the listener API answers 500 `Failed to auto-generate cert for listener {port}`, and the reason is only in the server log (`Failed to parse CA certificate in chain: ...`).

### Listener certificates
The certificates that a listener serves are managed with the listener APIs (`/server/listeners/{port}/...`, also `/server/listener/{port}/...`). These are listed here for reference. See the server documentation for the listeners.

|METHOD|URI|Description|
|---|---|---|
|GET  | /server/listeners/{port}/cert | The cert of the listener, as PEM (generated or uploaded) |
|GET  | /server/listeners/{port}/key | The private key of the listener, as PEM |
|POST, PUT | /server/listeners/{port}/cert/add | Set the cert of the listener, from the body. `Cert added for listener {port}`. Not validated |
|POST, PUT | /server/listeners/{port}/key/add | Set the key of the listener, from the body. `Key added for listener {port}`. Not validated |
|POST, PUT | /server/listeners/{port}/cert/remove | Remove the cert and key of the listener, and reopen it |
|POST, PUT | /server/listeners/{port}/cert/auto/{domain}?spiffeID={spiffeID} | Generate a cert for the domain, with the SPIFFE ID, and reopen the listener. `spiffeID` has to be given (the URI is not found without it); the body is not used. `Cert auto-generated for listener {port}` |
|POST, PUT | /server/listeners/{port}/cert/autosni | Have the listener generate a cert on the fly for the SNI of each client (the cert is for the SNI and for `goto.goto`). `Listener [{port}] configured to auto-generate cert for any SNI` |
|POST, PUT | /server/listeners/{port}/ca/add | Add the CA certs in the body (PEM) to the CAs that the listener uses to verify client certs, and reopen it |
|POST, PUT | /server/listeners/{port}/ca/clear | Clear the CAs of the listener, and reopen it |
|GET  | /server/listeners/client/certs | The certs of the clients that connected to any port (YAML), see the mTLS usage below |
|GET  | /server/listeners/{port}/client/certs | The same for one port |
|POST | /server/listeners/client/certs/clear | Clear the recorded client certs of all ports |
|POST | /server/listeners/{port}/client/certs/clear | Clear the recorded client certs of one port |

An unknown port answers 400 `Port {port}: No listener on the port, or listener not closeable`. `cert/add` and `key/add` do not reopen the listener: a new cert and key are served after the listener is reopened (`cert/remove`, `ca/add`, `ca/clear`, `cert/auto`, `cert/autosni`, or `POST /server/listeners/{port}/open`).

`cert/add` and `key/add` store whatever is sent. A cert and key that can't be parsed make the next reopen of the listener fail: it is tried 5 times, 5 seconds apart, then the port stays closed until a valid cert and key are set or `cert/remove` is called. `/open` answers `reopened` with status 200 even then, so check the server log (`Failed to parse certificate with error: ...`).

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

#### 7. Clear the client cert records
```
curl -XPOST localhost:8080/server/listeners/8443/client/certs/clear
```


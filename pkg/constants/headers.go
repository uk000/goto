/**
 * Copyright 2026 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package constants

const (
	HeaderViaGoto      = "Via-Goto"
	HeaderGotoHost     = "Goto-Host"
	HeaderGotoListener = "Goto-Listener"

	HeaderGotoTunnelHost      = "Goto-Tunnel-Host"
	HeaderViaGotoTunnel       = "Via-Goto-Tunnel"
	HeaderGotoTunnel          = "Goto-Tunnel"
	HeaderGotoRequestedTunnel = "Goto-Requested-Tunnel"
	HeaderGotoViaTunnelCount  = "Goto-Via-Tunnel-Count"
	HeaderGotoTunnelStatus    = "Goto-Tunnel-Status"

	HeaderGotoPort          = "Goto-Port"
	HeaderGotoProtocol      = "Goto-Protocol"
	HeaderGotoTLS           = "Goto-TLS"
	HeaderGotoMTLS          = "Goto-mTLS"
	HeaderGotoClientTLS     = "Goto-Client-TLS"
	HeaderGotoClientMTLS    = "Goto-Client-mTLS"
	HeaderGotoSNI           = "Goto-SNI"
	HeaderGotoClientCert    = "Goto-Client-Cert"
	HeaderGotoServerCert    = "Goto-Server-Cert"
	HeaderGotoRemoteAddress = "Goto-Remote-Address"
	HeaderFromGoto          = "From-Goto"
	HeaderFromGotoHost      = "From-Goto-Host"
	HeaderGotoRequestID     = "Goto-Request-Id"

	HeaderGotoResponseStatus = "Goto-Response-Status"
	HeaderGotoResponseDelay  = "Goto-Response-Delay"
	HeaderGotoUpstreamStatus = "Goto-Upstream-Status"

	HeaderGotoInAt  = "Goto-In-At"
	HeaderGotoOutAt = "Goto-Out-At"
	HeaderGotoTook  = "Goto-Took"

	// The headers that a proxy adds to what it passes on are named for the proxy that adds them, which is what tells them
	// from the headers of the target that the proxy passes on (a goto target has its own Goto-Host, Goto-Took and so on):
	// Goto-Proxy-xxx for the proxies of requests (HTTP, gRPC, MCP), and Goto-Connect-Proxy-xxx for the CONNECT proxy,
	// which tunnels TCP. The two don't share any header, so a client that goes through both can tell which one said what.
	HeaderGotoProxyHost           = "Goto-Proxy-Host"
	HeaderGotoProxyPort           = "Goto-Proxy-Port"
	HeaderGotoProxyUpstreamStatus = "Goto-Proxy-Upstream-Status"
	HeaderGotoProxyUpstreamTook   = "Goto-Proxy-Upstream-Took"
	HeaderGotoProxyDelay          = "Goto-Proxy-Delay"
	// Time that the proxy took for a whole request, from its first byte to the last one of the response, which is told by a
	// trailer, as the time is only known at the end.
	HeaderGotoProxyTook = "Goto-Proxy-Took"
	// When the request got to the proxy and when its response head was sent, and the time between them, which is told by
	// headers of the response: they are about the head, and not the body that comes after it (see Goto-Proxy-Took).
	HeaderGotoProxyInAt     = "Goto-Proxy-In-At"
	HeaderGotoProxyOutAt    = "Goto-Proxy-Out-At"
	HeaderGotoProxyHeadTook = "Goto-Proxy-Head-Took"
	// The status of a response that the proxy sent, which is not the one of the target if the proxy changed it.
	HeaderGotoProxyResponseStatus = "Goto-Proxy-Response-Status"

	// The headers of the CONNECT proxy. The identity ones are in the head of each response that goes through a tunnel that
	// carries HTTP. Took and Delay are in those, for the time of the exchange, and in the answer to the CONNECT request
	// that opens the tunnel, for the time of the answer. The lookup and the connecting that went into the answer are told
	// apart (they are part of the Took of the answer), and are only in it.
	HeaderGotoConnectProxyHost           = "Goto-Connect-Proxy-Host"
	HeaderGotoConnectProxyPort           = "Goto-Connect-Proxy-Port"
	HeaderGotoConnectProxyRemoteAddress  = "Goto-Connect-Proxy-Remote-Address"
	HeaderGotoConnectProxyUpstreamStatus = "Goto-Connect-Proxy-Upstream-Status"
	HeaderGotoConnectProxyDelay          = "Goto-Connect-Proxy-Delay"
	HeaderGotoConnectProxyTook           = "Goto-Connect-Proxy-Took"
	HeaderGotoConnectProxyDNSTook        = "Goto-Connect-Proxy-DNS-Took"
	HeaderGotoConnectProxyDialTook       = "Goto-Connect-Proxy-Dial-Took"

	// Time that the client took for each phase of its calls, which come as a JSON array that has one value per call, as
	// the statuses of Goto-Upstream-Status do. A phase that the call didn't go through (e.g. dns for an IP, or any
	// connecting for a connection that was reused) is "-" in it. What the CONNECT proxy said of the answer to the CONNECT
	// request of the call (Goto-Connect-Proxy-Took, -DNS-Took, -Dial-Took and -Delay) is passed on in the response of the
	// client, in the same form and under the same names.
	HeaderGotoClientDNSTook          = "Goto-Client-DNS-Took"
	HeaderGotoClientTCPTook          = "Goto-Client-TCP-Took"
	HeaderGotoClientProxyTLSTook     = "Goto-Client-Proxy-TLS-Took"
	HeaderGotoClientTunnelTook       = "Goto-Client-Tunnel-Took"
	HeaderGotoClientTLSTook          = "Goto-Client-TLS-Took"
	HeaderGotoClientResponseHeadTook = "Goto-Client-Response-Head-Took"
	HeaderGotoClientBodyTook         = "Goto-Client-Body-Took"
	HeaderGotoClientTotalTook        = "Goto-Client-Total-Took"

	HeaderGotoURIStatus             = "Goto-URI-Status"
	HeaderGotoURIStatusRemaining    = "Goto-URI-Status-Remaining"
	HeaderGotoRequestedStatus       = "Goto-Requested-Status"
	HeaderGotoForcedStatus          = "Goto-Forced-Status"
	HeaderGotoForcedStatusRemaining = "Goto-Forced-Status-Remaining"
	HeaderGotoStatusFlip            = "Goto-Status-Flip"

	HeaderProxyRequestDropped    = "Goto-Proxy-Request-Dropped"
	HeaderProxyResponseDropped   = "Goto-Proxy-Response-Dropped"
	XRequestID                   = "x-request-id"
	HeaderGotoTargetID           = "Goto-Target-ID"
	HeaderGotoTargetURL          = "Goto-Target-URL"
	HeaderGotoRetryCount         = "Goto-Retry-Count"
	HeaderReadinessRequestCount  = "Readiness-Request-Count"
	HeaderReadinessOverflowCount = "Readiness-Overflow-Count"
	HeaderLivenessRequestCount   = "Liveness-Request-Count"
	HeaderLivenessOverflowCount  = "Liveness-Overflow-Count"
	HeaderGotoFilteredRequest    = "Goto-Filtered-Request"
	HeaderGotoPayloadContentType = "Goto-Payload-Content-Type"
	HeaderGotoPayloadLength      = "Goto-Payload-Length"
	HeaderGotoPayloadCount       = "Goto-Payload-Count"
	HeaderGotoChunkCount         = "Goto-Chunk-Count"
	HeaderGotoChunkLength        = "Goto-Chunk-Length"
	HeaderGotoChunkDelay         = "Goto-Chunk-Delay"
	HeaderGotoStreamLength       = "Goto-Stream-Length"
	HeaderGotoStreamDuration     = "Goto-Stream-Duration"
	HeaderGotoClientInfo         = "Goto-Client-Info"
	HeaderGotoServerInfo         = "Goto-Server-Info"
	HeaderGotoMCPServer          = "Goto-MCP-Server"
	HeaderGotoMCPTool            = "Goto-MCP-Tool"
	HeaderGotoMCPSessionID       = "Goto-MCP-Session-ID"
	HeaderGotoMCPRequestID       = "Goto-MCP-Request-ID"
	HeaderGotoMCPCallerID        = "Goto-MCP-Caller-ID"
	HeaderGotoA2AServer          = "Goto-A2A-Server"
	HeaderGotoA2AAgent           = "Goto-A2A-Agent"
	HeaderGotoA2ARequestID       = "Goto-A2A-Request-ID"
	HeaderGotoServerTimeline     = "Goto-Server-Timeline"

	HeaderProxyConnection     = "Proxy-Connection"
	HeaderXContentTypeOptions = "X-Content-Type-Options"
	HeaderContentType         = "Content-Type"
	HeaderResponseContentType = "ResponseContent-Type"
	HeaderContentTypeLower    = "content-type"
	HeaderContentLength       = "Content-Length"
	HeaderMethod              = "Method"
	HeaderPayloadSize         = "Payload-Size"
	HeaderCacheControl        = "Cache-Control"
	HeaderTransferEncoding    = "Transfer-Encoding"
	HeaderAuthority           = ":authority"
	ContentTypeJSON           = "application/json"
	ContentTypeYAML           = "application/yaml"
	ContentTypeMarkdown       = "text/markdown; charset=utf-8"

	HeaderGotoRPCMethod = "Goto-RPC-Method"

	HeaderStoppingReadinessRequest = "Stopping-Readiness-Request"
)

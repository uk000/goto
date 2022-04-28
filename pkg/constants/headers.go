/**
 * Copyright 2021 uk
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
  HeaderViaGoto                   = "Via-Goto"
  HeaderGotoHost                  = "Goto-Host"
  HeaderGotoTunnelHost            = "Goto-Tunnel-Host"
  HeaderViaGotoTunnel             = "Via-Goto-Tunnel"
  HeaderGotoTunnel                = "Goto-Tunnel"
  HeaderGotoRequestedTunnel       = "Goto-Requested-Tunnel"
  HeaderGotoViaTunnelCount        = "Goto-Via-Tunnel-Count"
  HeaderGotoTunnelStatus          = "Goto-Tunnel-Status"
  HeaderGotoPort                  = "Goto-Port"
  HeaderGotoProtocol              = "Goto-Protocol"
  HeaderGotoRemoteAddress         = "Goto-Remote-Address"
  HeaderGotoResponseStatus        = "Goto-Response-Status"
  HeaderGotoResponseDelay         = "Goto-Response-Delay"
  HeaderGotoURIStatus             = "Goto-URI-Status"
  HeaderGotoURIStatusRemaining    = "Goto-URI-Status-Remaining"
  HeaderGotoRequestedStatus       = "Goto-Requested-Status"
  HeaderGotoForcedStatus          = "Goto-Forced-Status"
  HeaderGotoForcedStatusRemaining = "Goto-Forced-Status-Remaining"
  HeaderGotoStatusFlip            = "Goto-Status-Flip"
  HeaderGotoInAt                  = "Goto-In-At"
  HeaderGotoOutAt                 = "Goto-Out-At"
  HeaderGotoTook                  = "Goto-Took"
  HeaderFromGoto                  = "From-Goto"
  HeaderFromGotoHost              = "From-Goto-Host"
  HeaderGotoRequestID             = "Goto-Request-ID"
  HeaderGotoTargetID              = "Goto-Target-ID"
  HeaderGotoTargetURL             = "Goto-Target-URL"
  HeaderGotoRetryCount            = "Goto-Retry-Count"
  HeaderReadinessRequestCount     = "Readiness-Request-Count"
  HeaderReadinessOverflowCount    = "Readiness-Overflow-Count"
  HeaderLivenessRequestCount      = "Liveness-Request-Count"
  HeaderLivenessOverflowCount     = "Liveness-Overflow-Count"
  HeaderGotoFilteredRequest       = "Goto-Filtered-Request"

  HeaderProxyConnection  = "Proxy-Connection"
  HeaderContentType      = "Content-Type"
  HeaderContentTypeLower = "content-type"
  HeaderContentLength    = "Content-Length"
  HeaderAuthority        = ":authority"
  ContentTypeJSON        = "application/json"
  ContentTypeYAML        = "application/yaml"

  HeaderStoppingReadinessRequest = "Stopping-Readiness-Request"

  HeaderUpstreamStatus = "Goto-Upstream-Status"
  HeaderUpstreamTook = "Goto-Upstream-Took"
)

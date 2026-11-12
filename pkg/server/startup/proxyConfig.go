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

package startup

import (
	"fmt"
	"goto/pkg/global"
	grpcproxy "goto/pkg/proxy/grpc"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"time"
)

// removeHTTPProxy clears the HTTP proxy config of the port.
func removeHTTPProxy(port int) {
	httpproxy.ClearPortProxy(port)
}

// loadHTTPProxy sets the responses of the HTTP proxy of the port and adds its targets.
func loadHTTPProxy(p *httpproxy.Proxy) {
	if p == nil {
		return
	}
	proxy := httpproxy.GetPortProxy(p.Port)
	proxy.Enabled = p.Enabled
	proxy.ProxyResponses = p.ProxyResponses
	log.Printf("Proxy [%d] will use responses: %+v", p.Port, util.ToJSONText(proxy.ProxyResponses))
	for name, target := range p.Targets {
		if target == nil {
			log.Printf("[*** ERROR ***] HTTP Proxy target [%s] has no config", name)
			continue
		}
		target.Name = name
		target.Port = p.Port
		log.Printf("Loading HTTP Proxy Target [%s]\n", name)
		if err := proxy.AddTarget(target); err != nil {
			log.Printf("[*** ERROR ***] Failed to process HTTP Proxy target [%s] with error: %s", name, err.Error())
		} else {
			log.Println("------------------------------------")
			log.Printf("HTTP Proxy target [%s] loaded successfully", name)
			log.Println("------------------------------------")
		}
	}
}

// removeTCPProxy takes the config of a TCP proxy port away: its upstreams, and its CONNECT proxy mode and delay. The TCP
// listener that the port may have got for the CONNECT proxy mode stays, as the one that the API makes does when the mode
// is turned off.
func removeTCPProxy(port int) {
	tcpproxy.ClearPortProxy(port)
}

// loadTCPProxy loads the config of a TCP proxy port: its upstreams, and the http part of it that makes the port an HTTP
// CONNECT proxy. A port can have either of them, or both.
func loadTCPProxy(p *tcpproxy.TCPProxy) {
	if p == nil {
		return
	}
	connect := p.HTTP.Connect || p.HTTP.ConnectDelay != nil
	// a port that is only a CONNECT proxy has no upstreams to load
	if len(p.Upstreams) > 0 || !connect {
		loadTCPUpstreams(p)
	}
	if connect {
		loadConnectProxy(p)
	}
}

// loadTCPUpstreams validates the upstreams of a TCP proxy and adds them to the proxy of its port.
func loadTCPUpstreams(p *tcpproxy.TCPProxy) {
	if err := tcpproxy.ValidateUpstreams(p.Upstreams); err != nil {
		log.Printf("[*** ERROR ***] TCP Proxy [%d] Upstreams failed validation: %s\n", p.Port, err.Error())
		return
	}
	log.Printf("Loading TCP Proxy [%d]\n", p.Port)
	tcpproxy.GetPortProxy(p.Port).AddUpstreams(p.Upstreams)
	log.Println("------------------------------------")
	log.Printf("TCP Proxy [%d] loaded [%d] upstreams successfully", p.Port, len(p.Upstreams))
	log.Println("------------------------------------")
}

// loadConnectProxy loads the http part of a TCP proxy: the delay of the CONNECT proxy, and the CONNECT proxy mode, which
// makes the port a TCP port (it keeps the TCP listener that it has, e.g. a tls one that the listeners of the config made).
// The ports that serve the admin APIs, which can't stop serving HTTP, can't be CONNECT proxy ports.
func loadConnectProxy(p *tcpproxy.TCPProxy) {
	port := p.Port
	if port <= 0 || port > 65535 {
		log.Printf("[*** ERROR ***] TCP Proxy [%d] has an invalid port for an HTTP CONNECT proxy\n", port)
		return
	}
	log.Printf("Loading HTTP CONNECT Proxy [%d]\n", port)
	// the delay is set first, for it to apply to the first connection
	if c := p.HTTP.ConnectDelay; c != nil {
		min, max, count := delayRange(c)
		// the delay that the proxy counts down is a copy of its own, not the one that the config keeps
		if delay := types.NewDelay(min, max, count); delay != nil {
			tcpproxy.SetConnectDelay(port, delay)
		} else {
			log.Printf("[*** ERROR ***] HTTP CONNECT Proxy [%d] delay has no max, so it is not a delay. Ignoring it.\n", port)
			tcpproxy.SetConnectDelay(port, nil)
		}
	}
	if !p.HTTP.Connect {
		return
	}
	switch port {
	case global.Self.ServerPort, global.Self.JSONRPCPort, global.Self.GRPCPort:
		log.Printf("[*** ERROR ***] Default Port [%d] cannot be an HTTP CONNECT Proxy port\n", port)
		return
	}
	if err := tcpproxy.EnableConnectProxy(port); err != nil {
		log.Printf("[*** ERROR ***] Failed to enable HTTP CONNECT Proxy [%d]: %s\n", port, err.Error())
		return
	}
	log.Println("------------------------------------")
	log.Printf("HTTP CONNECT Proxy [%d] enabled, the port is a TCP port", port)
	log.Println("------------------------------------")
}

// removeGRPCProxy removes the gRPC proxy of the port.
func removeGRPCProxy(port int) {
	grpcproxy.RemovePortProxy(port)
}

// loadGRPCProxy adds an enabled gRPC proxy and sets up its service proxies, which are validated first.
func loadGRPCProxy(p *grpcproxy.GRPCProxy) {
	if p == nil {
		return
	}
	if !p.Enabled {
		log.Printf("GRPC Proxy [%d] not enabled. Skipping.\n", p.Port)
		return
	}
	grpcproxy.AddPortProxy(p)
	success := 0
	failure := 0
	for from, sp := range p.ServiceProxies {
		if sp == nil {
			log.Printf("[*** ERROR ***] GRPC Proxy [%d] Service [%s] has no config\n", p.Port, from)
			failure++
			continue
		}
		sp.FromService = from
		if err := sp.Validate(); err != nil {
			log.Printf("[*** ERROR ***] GRPC Proxy [%d] Service [%s] Failed validation: %s\n", p.Port, from, err.Error())
			failure++
			continue
		}
		delayMin, delayMax, delayCount := delayRange(sp.Config.Delay)
		if err := p.SetupGRPCServiceProxy(sp.FromService, sp.ToService, sp.Methods, sp.Upstream.Endpoint, sp.Upstream.Authority, 0, delayMin, delayMax, delayCount, sp); err != nil {
			log.Printf("[*** ERROR ***] GRPC Proxy [%d] Service [%s] Failed to be set up: %s\n", p.Port, from, err.Error())
			failure++
			continue
		}
		log.Printf("GRPC Proxy [%d] configured from Service [%s] to Service [%s]\n", p.Port, from, sp.ToService)
		success++
	}
	log.Println("------------------------------------")
	msg := fmt.Sprintf("GRPC Proxy [%d]", p.Port)
	if success > 0 {
		msg = fmt.Sprintf("%s %d services successfully loaded, ", msg, success)
	}
	if failure > 0 {
		msg = fmt.Sprintf("%s %d services failed to load", msg, failure)
	}
	log.Println(msg)
	log.Println("------------------------------------")
}

// delayRange gives the bounds and the count of a delay. A delay that has no bound (a config of only a max, or none at all)
// has no time to delay by for that bound.
func delayRange(d *types.Delay) (min, max time.Duration, count int) {
	if d == nil {
		return 0, 0, 0
	}
	if d.Min != nil {
		min = d.Min.Duration
	}
	if d.Max != nil {
		max = d.Max.Duration
	}
	return min, max, d.Count
}

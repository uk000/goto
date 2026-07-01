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
	grpcproxy "goto/pkg/proxy/grpc"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/util"
	"log"
)

func removeHTTPProxy(port int) {
	httpproxy.ClearPortProxy(port)
}

func loadHTTPProxy(p *httpproxy.Proxy) {
	if p == nil {
		return
	}
	proxy := httpproxy.GetPortProxy(p.Port)
	proxy.Enabled = p.Enabled
	proxy.ProxyResponses = p.ProxyResponses
	log.Printf("Proxy [%d] will use responses: %+v", p.Port, util.ToJSONText(proxy.ProxyResponses))
	for name, target := range p.Targets {
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

func removeTCPProxy(port int) {
	tcpproxy.ClearPortProxy(port)
}

func loadTCPProxy(p *tcpproxy.TCPProxy) {
	if p == nil {
		return
	}
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

func removeGRPCProxy(port int) {
	grpcproxy.RemovePortProxy(port)
}

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
		sp.FromService = from
		if err := sp.Validate(); err != nil {
			log.Printf("[*** ERROR ***] GRPC Proxy [%d] Service [%s] Failed validation: %s\n", p.Port, from, err.Error())
			failure++
			continue
		}
		p.SetupGRPCServiceProxy(sp.FromService, sp.ToService, sp.Methods, sp.Upstream.Endpoint, sp.Upstream.Authority, 0, sp.Config.Delay.Min.Duration, sp.Config.Delay.Max.Duration, sp.Config.Delay.Count, sp)
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

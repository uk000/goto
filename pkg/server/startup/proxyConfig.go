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
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/util"
	"log"
)

func removeHTTPProxy(p *httpproxy.Proxy) {
	httpproxy.ClearPortProxy(p.Port)
}

func loadHTTPProxy(p *httpproxy.Proxy) {
	removeHTTPProxy(p)
	proxy := httpproxy.GetPortProxy(p.Port)
	proxy.Enabled = p.Enabled
	proxy.ProxyResponses = p.ProxyResponses
	log.Printf("Proxy [%d] will use responses: %+v", p.Port, util.ToJSONText(proxy.ProxyResponses))
	for name, target := range p.Targets {
		target.Name = name
		log.Printf("Loading HTTP Proxy Target [%s]\n", name)
		if err := proxy.AddTarget(target); err != nil {
			log.Printf("Failed to process HTTP Proxy target [%s] with error: %s", name, err.Error())
		} else {
			log.Println("============================================================")
			log.Printf("HTTP Proxy target [%s] loaded successfully", name)
			log.Println("============================================================")
		}
	}
}

func removeTCPProxy(p *tcpproxy.TCPProxy) {
	tcpproxy.ClearPortProxy(p.Port)
}

func loadTCPProxy(p *tcpproxy.TCPProxy) {
	removeTCPProxy(p)
	if err := tcpproxy.ValidateUpstreams(p.Upstreams); err != nil {
		log.Printf("TCP Proxy [%d] Upstreams failed validation: %s\n", p.Port, err.Error())
	}
	log.Printf("Loading TCP Proxy [%d]\n", p.Port)
	tcpproxy.GetPortProxy(p.Port).AddUpstreams(p.Upstreams)
	log.Println("============================================================")
	log.Printf("TCP Proxy [%d] loaded [%d] upstreams successfully", p.Port, len(p.Upstreams))
	log.Println("============================================================")
}

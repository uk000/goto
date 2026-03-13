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

package ctl

import (
	"bytes"
	"fmt"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/util"
	"log"
	"net/http"
)

type Proxy struct {
	HTTP *httpproxy.Proxy   `yaml:"http"`
	TCP  *tcpproxy.TCPProxy `yaml:"tcp"`
}

func processProxy(config *GotoConfig) {
	if config.Proxies == nil {
		log.Println("No Proxy to configure")
		return
	}
	for _, proxy := range config.Proxies {
		if proxy.HTTP != nil {
			sendHTTPProxy(proxy.HTTP)
		}
	}
}

func sendHTTPProxy(httpProxy *httpproxy.Proxy) {
	url := fmt.Sprintf("%s/proxy/http/responses/set", currentContext.RemoteGotoURL)
	json := util.ToJSONBytes(httpProxy.ProxyResponses)
	if json == nil {
		log.Printf("JSON marshalling failed for HTTP Proxy [%d] Responses.", httpProxy.Port)
		return
	}
	log.Printf("Sending HTTP Proxy [%d] Responses to URL [%s]\n", httpProxy.Port, url)
	resp, err := http.Post(url, "application/json", bytes.NewReader(json))
	if err != nil {
		log.Printf("Failed to send HTTP Proxy Responses. Error [%s]n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Non-OK status for HTTP Proxy [%d] Responses: %s\n", httpProxy.Port, resp.Status)
		log.Println(string(json))
	} else {
		log.Printf("HTTP Proxy [%d] Responses sent successfully. Response: [%s]\n", httpProxy.Port, util.Read(resp.Body))
	}
	for name, target := range httpProxy.Targets {
		target.Name = name
		url := fmt.Sprintf("%s/proxy/http/targets/add", currentContext.RemoteGotoURL)
		json := util.ToJSONBytes(target)
		if json == nil {
			log.Printf("JSON marshalling failed for HTTP Proxy Target [%s] JSON: %+v", name, target)
			return
		}
		log.Printf("Sending HTTP Proxy Target [%s] to URL [%s]\n", name, url)
		resp, err := http.Post(url, "application/json", bytes.NewReader(json))
		if err != nil {
			log.Printf("Failed to send HTTP Proxy. Error [%s]n", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("Non-OK status for HTTP Proxy Target [%s]: %s\n", name, resp.Status)
			log.Println(string(json))
		} else {
			log.Printf("HTTP Proxy Target [%s] sent successfully. Response: [%s]\n", name, util.Read(resp.Body))
		}
	}
}

func sendTCPPProxy(tcpProxy *tcpproxy.TCPProxy) {
	for name, target := range tcpProxy.Upstreams {
		target.Name = name
		url := fmt.Sprintf("%s/proxy/tcp/upstreams/add", currentContext.RemoteGotoURL)
		json := util.ToJSONBytes(target)
		if json == nil {
			log.Printf("JSON marshalling failed for TCP Proxy Target [%s] JSON: %+v", name, target)
			return
		}
		log.Printf("Sending TCP Proxy Target [%s] to URL [%s]\n", name, url)
		resp, err := http.Post(url, "application/json", bytes.NewReader(json))
		if err != nil {
			log.Printf("Failed to send TCP Proxy. Error [%s]n", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("Non-OK status for TCP Proxy Target [%s]: %s\n", name, resp.Status)
			log.Println(string(json))
		} else {
			log.Printf("TCP Proxy Target [%s] sent successfully. Response: [%s]\n", name, util.Read(resp.Body))
		}
	}
}

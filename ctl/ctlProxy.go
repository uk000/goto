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
	grpcproxy "goto/pkg/proxy/grpc"
	httpproxy "goto/pkg/proxy/http"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"strconv"
	"time"
)

type Proxy struct {
	HTTP *httpproxy.Proxy     `yaml:"http"`
	TCP  *tcpproxy.TCPProxy   `yaml:"tcp"`
	GRPC *grpcproxy.GRPCProxy `yaml:"grpc"`
}

func processProxy(config *GotoConfig) {
	if config.Proxies == nil {
		log.Println("No Proxy to configure")
		return
	}
	for _, proxy := range config.Proxies {
		if proxy == nil {
			continue
		}
		if proxy.HTTP != nil {
			sendHTTPProxy(proxy.HTTP)
		}
		if proxy.TCP != nil {
			sendTCPPProxy(proxy.TCP)
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
		if target == nil {
			continue
		}
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

// tcpProxyURL is the URL of a TCP proxy API of the remote goto, for the port of the proxy that is passed as the port
// override. The port is required in the proxy.tcp config, so it's always part of the URL.
func tcpProxyURL(tcpProxy *tcpproxy.TCPProxy, api string) string {
	return fmt.Sprintf("%s/port=%d/proxy/tcp%s", currentContext.RemoteGotoURL, tcpProxy.Port, api)
}

// sendTCPPProxy sends the config of a TCP proxy port: its upstreams, and the http part of it, which makes the port an HTTP
// CONNECT proxy. The port is required: a TCP proxy config without one is skipped.
func sendTCPPProxy(tcpProxy *tcpproxy.TCPProxy) {
	if tcpProxy.Port <= 0 {
		log.Println("TCP Port is required for TCP Proxy config. Skipping.")
		return
	}
	sendTCPUpstreams(tcpProxy)
	sendTCPConnectProxy(tcpProxy)
}

func sendTCPUpstreams(tcpProxy *tcpproxy.TCPProxy) {
	for name, target := range tcpProxy.Upstreams {
		if target == nil {
			continue
		}
		target.Name = name
		url := tcpProxyURL(tcpProxy, "/upstreams/add")
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

// sendTCPConnectProxy sends the http part of a TCP proxy to the remote: the delay of the CONNECT proxy, and then the mode
// that makes the port one. The delay goes first, for it to apply to the first connection of the proxy.
func sendTCPConnectProxy(tcpProxy *tcpproxy.TCPProxy) {
	connect := tcpProxy.HTTP.Connect
	delay := tcpProxy.HTTP.ConnectDelay
	if !connect && delay == nil {
		return
	}
	if delay != nil {
		if val := connectDelayParam(delay); val == "" {
			log.Printf("HTTP CONNECT Proxy [%d] delay has no max, so it is not a delay. Skipping it.\n", tcpProxy.Port)
		} else {
			postTCPConnectAPI(tcpProxy, "/http/connect/delay/"+val, "HTTP CONNECT Proxy ["+strconv.Itoa(tcpProxy.Port)+"] delay ["+val+"]")
		}
	}
	if connect {
		postTCPConnectAPI(tcpProxy, "/http/connect/enable", "HTTP CONNECT Proxy ["+strconv.Itoa(tcpProxy.Port)+"] mode")
	}
}

func postTCPConnectAPI(tcpProxy *tcpproxy.TCPProxy, api, what string) {
	url := tcpProxyURL(tcpProxy, api)
	log.Printf("Sending %s to URL [%s]\n", what, url)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		log.Printf("Failed to send %s. Error [%s]\n", what, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Non-OK status for %s: %s. Response: [%s]\n", what, resp.Status, util.Read(resp.Body))
	} else {
		log.Printf("%s sent successfully. Response: [%s]\n", what, util.Read(resp.Body))
	}
}

// connectDelayParam gives the delay as the API of the CONNECT proxy takes it: min-max, followed by :count when the delay
// is for a number of events. It is empty for a delay that has no max, which isn't a delay (a missing min is no time, as it
// is for the startup of the server).
func connectDelayParam(d *types.Delay) string {
	var min, max time.Duration
	if d.Min != nil {
		min = d.Min.Duration
	}
	if d.Max != nil {
		max = d.Max.Duration
	}
	if max <= 0 {
		return ""
	}
	if min < 0 {
		min = 0
	}
	val := min.String() + "-" + max.String()
	if d.Count > 0 {
		val += ":" + strconv.Itoa(d.Count)
	}
	return val
}

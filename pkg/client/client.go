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

package client

import (
	"fmt"
	"goto/pkg/client/results"
	"goto/pkg/client/target"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	gototls "goto/pkg/tls"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type CallSpec struct {
	URL          string               `json:"url"`
	Authority    string               `json:"authority"`
	Method       string               `json:"method"`
	Count        int                  `json:"count"`
	RequestID    bool                 `json:"requestID"`
	H2           bool                 `json:"h2"`
	TLS          bool                 `json:"tls"`
	NoSNI        bool                 `json:"noSNI"`
	VerifyTLS    bool                 `json:"verifyTLS"`
	TLSVersion   uint16               `json:"tlsVersion"`
	ClientCert   string               `json:"clientCert"`
	ALPN         []string             `json:"alpn"`
	Payload      []string             `json:"payload"`
	StreamDelay  string               `json:"streamDelay"`
	Headers      *types.HeadersConfig `json:"headers"`
	request      *http.Request
	client       transport.ClientTransport
	streamWriter io.WriteCloser
	streamDelayD time.Duration
}

type CallResult struct {
	Headers      http.Header `json:"headers"`
	Payload      string      `json:"payload"`
	PeerCertInfo string      `json:"peerCert"`
	Status       int
}

type CallResults struct {
	URL       string `json:"url"`
	Results   map[string]*CallResult
	PeerCerts []string `json:"peerCerts"`
	Statuses  []int
}

var (
	Middleware        = middleware.NewMiddleware("client", setRoutes, nil)
	clientMiddlewares = []*middleware.Middleware{&target.Middleware}
)

func setRoutes(r *mux.Router) {
	clientRouter := middleware.RootPath("/client")
	middleware.AddRoutes(clientRouter, clientMiddlewares...)
	util.AddRoute(clientRouter, "/http/{q:invoke|call}", invokeHTTP, "GET", "POST", "PUT", "OPTIONS")
}

func (c *CallSpec) PrepareAuthority(r *http.Request) {
	if !strings.HasPrefix(c.URL, "http") {
		if c.TLS {
			c.URL = "https://" + c.URL
		} else {
			c.URL = "http://" + c.URL
		}
	}
	if strings.HasPrefix(c.URL, "https") {
		c.TLS = true
	}
	if c.Authority == "" {
		u, e := url.Parse(c.URL)
		if e == nil {
			c.Authority = u.Host
		}
	}
	if c.Authority == "" {
		for h, v := range r.Header {
			if strings.EqualFold(h, "host") {
				c.Authority = v[0]
			}
		}
	}
}

func (c *CallSpec) PrepareRequest(r *http.Request) error {
	var bodyReader io.Reader
	if len(c.Payload) > 1 {
		bodyReader, c.streamWriter = io.Pipe()
	} else if len(c.Payload) == 1 {
		bodyReader = strings.NewReader(c.Payload[0])
	}
	req, err := http.NewRequest(r.Method, c.URL, bodyReader)
	if err != nil {
		return err
	}
	if c.Headers != nil {
		if c.Headers.Forward != nil {
			types.ForwardHeaders(r.Header, req.Header, slices.Values(c.Headers.Forward))
		}
		if c.Headers.Add != nil {
			for h, v := range c.Headers.Add {
				req.Header.Add(h, v)
			}
		}
	}
	if a := r.Header.Get("Accept"); a != "" {
		req.Header.Add("Accept", a)
	}
	req.Header.Add("User-Agent", global.Self.HostLabel)
	if c.Authority != "" {
		req.Host = c.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	if c.Authority == "" {
		c.Authority = req.Host
	}
	if c.RequestID {
		uuid := uuid.New().String()
		req.Header.Add("x-request-id", uuid)
		q := url.Values{}
		q.Add("x-request-id", uuid)
		req.URL.RawQuery = q.Encode()
	}
	c.request = req
	if c.StreamDelay != "" {
		c.streamDelayD = util.ParseDuration(c.StreamDelay)
	}
	return nil
}

func (c *CallSpec) Invoke(r *http.Request) (*CallResults, error) {
	c.PrepareAuthority(r)
	sni := c.Authority
	if c.NoSNI {
		sni = ""
	}
	port := util.GetRequestOrListenerPortNum(r)
	label := util.GetCurrentListenerLabel(r)
	client := transport.CreateDefaultHTTPClient(port, label, c.H2, c.TLS, c.NoSNI, c.Authority, metrics.ConnTracker)
	client.UpdateTLSConfig(sni, c.TLSVersion, c.VerifyTLS, c.ALPN)
	if c.ClientCert != "" {
		cert, err := gototls.GetCert(c.ClientCert)
		if err != nil {
			log.Printf("Invocation: [ERROR] Client Certificate Name given but Certificate not uploaded")
			return nil, err
		}
		client.UpdateTLSCerts(gototls.RootCAs, cert)
	}
	callResults := &CallResults{URL: c.URL, Results: map[string]*CallResult{}}
	if c.Count == 0 {
		c.Count = 1
	}
	for i := 1; i <= c.Count; i++ {
		requestID := fmt.Sprintf("%s[%d]", c.URL, i)
		result := &CallResult{}
		callResults.Results[requestID] = result
		err := c.PrepareRequest(r)
		if err != nil {
			return nil, err
		}
		if len(c.Payload) > 0 && c.streamWriter != nil {
			go func() {
				defer c.streamWriter.Close()
				for _, payload := range c.Payload {
					if c.streamDelayD > 0 {
						time.Sleep(c.streamDelayD)
					}
					log.Printf("[HTTP Client] Streaming input: %s", payload)
					if _, err := c.streamWriter.Write([]byte(payload)); err != nil {
						log.Printf("[HTTP Client] Error sending data to remote: %v", err)
						return
					}
				}
				log.Println("[HTTP Client] Finished streaming data chunks.")
			}()
		}
		resp, err := client.HTTP().Do(c.request)
		if err != nil {
			return nil, err
		}
		msg := fmt.Sprintf("Call to URL [%s] Authority [%s] succeeded with response [%s]", c.URL, c.Authority, resp.Status)
		util.AddLogMessage(msg, r)
		result.Headers = resp.Header
		defer resp.Body.Close()
		b := util.Read(resp.Body)
		result.Payload = b
		pci := client.GetPeerCertInfo()
		if pci != nil {
			v := util.ToJSONText(pci)
			result.PeerCertInfo = v
			callResults.PeerCerts = append(callResults.PeerCerts, v)
		}
		result.Status = resp.StatusCode
		callResults.Statuses = append(callResults.Statuses, resp.StatusCode)
	}
	return callResults, nil
}

func invokeHTTP(w http.ResponseWriter, r *http.Request) {
	rs := util.GetRequestStore(r)
	call := &CallSpec{}
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, call)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("[HTTP Client] Failed to parse call spec with error: %s", err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	callResults, err := call.Invoke(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("[HTTP Client] Failed to invoke URL [%s] Authority [%s] with error: %s", call.URL, call.Authority, err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	w.Header().Add(constants.HeaderGotoUpstreamStatus, util.ToJSONText(callResults.Statuses))
	if len(callResults.PeerCerts) > 0 {
		w.Header().Add(constants.HeaderGotoPeerCertInfo, util.ToJSONText(callResults.PeerCerts))
	}
	for _, cr := range callResults.Results {
		viaGotos := cr.Headers[constants.HeaderViaGoto]
		for _, v := range viaGotos {
			if v != "" {
				rs.ViaGotos = append(rs.ViaGotos, v)
			}
		}
	}
	util.WriteJsonOrYAMLPayload(w, callResults, true)
}

func Run() {
	if global.CmdClientConfig.Verbose {
		results.EnableInvocationResults(true)
	}
	if global.CmdClientConfig.Persist {
		content, err := util.LoadFile("/tmp/goto_client_summary_results.json")
		if err == nil {
			err = results.LoadTargetsResultsJSON(content)
		}
		if err != nil {
			log.Printf("Ignoring error while loading saved summary results: %v\n", err)
		}
		if global.CmdClientConfig.Verbose {
			content, err := util.LoadFile("/tmp/goto_client_detailed_results.json")
			if err == nil {
				err = results.LoadInvocationResultsJSON(content)
			}
			if err != nil {
				log.Printf("Ignoring error while loading saved detailed results: %v\n", err)
			}
		}
	}

	if len(global.CmdClientConfig.URLs) == 0 {
		log.Println("No URL specified")
		return
	}
	for _, url := range global.CmdClientConfig.URLs {
		is := &invocation.InvocationSpec{
			Name:                 url,
			Protocol:             global.CmdClientConfig.Protocol,
			Method:               global.CmdClientConfig.Method,
			URL:                  url,
			Headers:              global.CmdClientConfig.Headers,
			Body:                 global.CmdClientConfig.Payload,
			AutoPayload:          global.CmdClientConfig.AutoPayload,
			Replicas:             global.CmdClientConfig.Parallel,
			RequestCount:         global.CmdClientConfig.RequestCount,
			Delay:                global.CmdClientConfig.Delay,
			Retries:              global.CmdClientConfig.Retries,
			RetryDelay:           global.CmdClientConfig.RetryDelay,
			RetriableStatusCodes: global.CmdClientConfig.RetryOn,
			AutoInvoke:           false,
			LowerHeaders:         true,
		}
		if err := target.Client.AddTarget(&target.Target{is}); err != nil {
			log.Printf("Invalid target spec: %s", err.Error())
		}
	}
	target.Client.InvokeAll()
	json := results.GetTargetsResultsJSON(true)
	log.Println(json)
	if global.CmdClientConfig.Persist {
		util.StoreFile("/tmp/", "goto_client_summary_results.json", []byte(json))
	}
	if global.CmdClientConfig.Verbose {
		json = results.GetInvocationResultsJSON(true)
		log.Println(json)
		if global.CmdClientConfig.Persist {
			util.StoreFile("/tmp/", "goto_client_detailed_results.json", []byte(json))
		}
	}
}

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
	"errors"
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
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type CallSpec struct {
	URL            string               `json:"url"`
	Proxy          string               `json:"proxy"`
	Authority      string               `json:"authority"`
	Method         string               `json:"method"`
	Count          int                  `json:"count"`
	RequestID      bool                 `json:"requestID"`
	IsH2           bool                 `json:"h2"`
	TLS            bool                 `json:"tls"`
	NoSNI          bool                 `json:"noSNI"`
	VerifyTLS      bool                 `json:"verifyTLS"`
	TLSVersion     uint16               `json:"tlsVersion"`
	ClientCert     string               `json:"clientCert"`
	ALPN           *gototls.ALPN        `json:"alpn"`
	Payload        []string             `json:"payload"`
	StreamDelay    string               `json:"streamDelay"`
	Headers        *types.HeadersConfig `json:"headers"`
	RequestTimeout time.Duration        `json:"requestTimeout"`
	request        *http.Request
	client         transport.ClientTransport
	streamWriter   io.WriteCloser
	streamDelayD   time.Duration
}

type CallResult struct {
	Headers    http.Header `json:"headers"`
	Payload    string      `json:"payload"`
	ServerCert string      `json:"serverCert"`
	Status     int
}

type CallResults struct {
	URL         string `json:"url"`
	Results     map[string]*CallResult
	ServerCerts []string `json:"serverCerts"`
	ClientCert  string   `json:"clientCert"`
	Statuses    []int
	// what each call went through, which is reported in the headers of the response and is not part of its payload
	timings []*transport.CallTimings
}

var (
	Middleware        = middleware.NewMiddleware("client", setRoutes, nil)
	clientMiddlewares = []*middleware.Middleware{&target.Middleware}
)

// setRoutes registers the /client routes: the target client middleware and the HTTP invoke/call endpoint.
func setRoutes(r *mux.Router) {
	clientRouter := middleware.RootPath("/client")
	middleware.AddRoutes(clientRouter, clientMiddlewares...)
	util.AddRoute(clientRouter, "/http/{q:invoke|call}", invokeHTTP, "GET", "POST", "PUT", "OPTIONS")
}

// PrepareAuthority gives the call URL an http(s) scheme (setting TLS for https) and defaults the authority to the URL host or the request's Host header.
func (c *CallSpec) PrepareAuthority(r *http.Request) {
	// it's the scheme that is looked for, and not just text that starts like one, as the name of a host may be httpbin
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		if c.TLS {
			c.URL = "https://" + c.URL
		} else {
			c.URL = "http://" + c.URL
		}
	}
	if strings.HasPrefix(c.URL, "https://") {
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

// PrepareRequest builds the outgoing request from the spec: payload (streamed via a pipe if chunked), headers, authority, TLS marker headers and optional x-request-id.
func (c *CallSpec) PrepareRequest(r *http.Request) error {
	var bodyReader io.Reader
	if len(c.Payload) > 1 {
		bodyReader, c.streamWriter = io.Pipe()
	} else if len(c.Payload) == 1 {
		bodyReader = strings.NewReader(c.Payload[0])
	}
	req, err := http.NewRequest(c.Method, c.URL, bodyReader)
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
	if c.TLS {
		req.Header.Add(constants.HeaderGotoClientTLS, "true")
		if c.ClientCert != "" {
			req.Header.Add(constants.HeaderGotoClientMTLS, "true")
		} else {
			req.Header.Add(constants.HeaderGotoClientMTLS, "false")
		}
	} else {
		req.Header.Add(constants.HeaderGotoClientTLS, "false")
	}
	if c.RequestID {
		requestID := uuid.New().String()
		req.Header.Add("x-request-id", requestID)
		// the query that the URL has is kept, with the request id added to it
		q := req.URL.Query()
		q.Set("x-request-id", requestID)
		req.URL.RawQuery = q.Encode()
	}
	c.request = req
	if c.StreamDelay != "" {
		c.streamDelayD = util.ParseDuration(c.StreamDelay)
	}
	return nil
}

// streamPayloads sends the chunks of the payload one after the other, with the delay before each, and closes the writer
// after the last one.
func streamPayloads(w io.WriteCloser, payloads []string, delay time.Duration) {
	defer w.Close()
	for _, payload := range payloads {
		if delay > 0 {
			time.Sleep(delay)
		}
		log.Printf("[HTTP Client] Streaming input: %s", payload)
		if _, err := w.Write([]byte(payload)); err != nil {
			log.Printf("[HTTP Client] Error sending data to remote: %v", err)
			return
		}
	}
	log.Println("[HTTP Client] Finished streaming data chunks.")
}

// proxyURL validates the proxy of the call, and returns its URL, which is nil if the call has no proxy. What a call asks for
// that can't be done for a request that goes via a proxy is an error, and not left to be ignored: net/http does the TLS
// handshake with the target on its own after the proxy has set up the tunnel, with no say for h2 (which has no proxy
// support), SNI to be left out, or a handshake preamble.
func (c *CallSpec) proxyURL() (*url.URL, error) {
	if c.Proxy == "" {
		return nil, nil
	}
	if c.IsH2 {
		return nil, errors.New("h2 calls can't be sent via a proxy")
	}
	if c.TLS && c.NoSNI {
		return nil, errors.New("TLS calls via a proxy are always sent with SNI, noSNI can't be used")
	}
	if c.TLS && c.ALPN != nil && c.ALPN.Handshake != nil {
		return nil, errors.New("TLS calls via a proxy can't have an ALPN handshake")
	}
	return transport.ParseProxyURL(c.Proxy)
}

// Invoke sends the call Count times via a per-call HTTP client (optional proxy, TLS, client cert, ALPN) and collects each response's headers, payload, status, certs and timings.
func (c *CallSpec) Invoke(r *http.Request) (*CallResults, error) {
	callResults := &CallResults{URL: c.URL, Results: map[string]*CallResult{}}
	c.PrepareAuthority(r)
	proxy, err := c.proxyURL()
	if err != nil {
		return nil, err
	}
	sni := c.Authority
	if host, _, err := net.SplitHostPort(c.Authority); err == nil {
		sni = host
	}
	if c.NoSNI {
		sni = ""
	}
	port := util.GetRequestOrListenerPortNum(r)
	label := util.GetCurrentListenerLabel(r)
	client := transport.CreateHTTPClient(port, label, c.IsH2, true, c.TLS, c.NoSNI, c.Authority,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	// the client is the call's own, so what it keeps open for reuse (the tunnels of a proxy, for one) goes with the call
	defer client.Close()
	if proxy != nil {
		// the proxy is set before the TLS settings are, as these are applied to the tunneled connections along with it
		pc, ok := client.(transport.ProxyClient)
		if !ok {
			return nil, errors.New("the client can't be sent via a proxy")
		}
		if err := pc.SetProxy(proxy); err != nil {
			return nil, err
		}
	}
	client.UpdateTLSConfig(sni, c.TLSVersion, c.VerifyTLS, c.ALPN)
	if c.ClientCert != "" {
		certs, err := gototls.GetCerts(c.ClientCert)
		if err != nil {
			log.Printf("Invocation: [ERROR] Client Certificate Name given but Certificate not uploaded")
			return nil, err
		}
		client.UpdateTLSCerts(gototls.RootCAs, certs)
		leaf := gototls.IdentifyLeaf(certs)
		if leaf != nil {
			uris := []string{}
			for _, uri := range leaf.URIs {
				uris = append(uris, uri.String())
			}
			alpn := []string{}
			if c.ALPN != nil {
				alpn = c.ALPN.Protos
			}
			pci := &gototls.PeerCertInfo{
				StartAt:    time.Now(),
				RemoteAddr: r.RemoteAddr,
				Subject:    gototls.SubjectToString(leaf),
				DNSNames:   leaf.DNSNames,
				URIs:       uris,
				SNI:        c.Authority,
				Issuer:     gototls.IssuerToString(leaf),
				ALPN:       alpn,
			}
			callResults.ClientCert = pci.Summary()
		}
	}
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
			// the writer of this request is what the chunks are for, and not the one that the next request gets
			go streamPayloads(c.streamWriter, c.Payload, c.streamDelayD)
		}
		timings := transport.NewCallTimings()
		c.request = c.request.WithContext(timings.Context(c.request.Context()))
		resp, err := client.HTTP().Do(c.request)
		if err != nil {
			log.Printf("[HTTP Client] Call failed with error: %v", err)
			return nil, err
		}
		timings.HeadReceived()
		msg := fmt.Sprintf("Call to URL [%s] Authority [%s] succeeded with response [%s]", c.URL, c.Authority, resp.Status)
		if proxy != nil {
			msg = fmt.Sprintf("Call to URL [%s] Authority [%s] via proxy [%s] succeeded with response [%s]", c.URL, c.Authority, proxy.Redacted(), resp.Status)
		}
		util.AddLogMessage(msg, r)
		result.Headers = resp.Header
		defer resp.Body.Close()
		b := util.Read(resp.Body)
		timings.BodyRead()
		callResults.timings = append(callResults.timings, timings)
		result.Payload = b
		pci := client.GetPeerCertInfo()
		if pci != nil {
			result.ServerCert = pci.Summary()
			callResults.ServerCerts = append(callResults.ServerCerts, result.ServerCert)
		}
		result.Status = resp.StatusCode
		callResults.Statuses = append(callResults.Statuses, resp.StatusCode)
	}
	return callResults, nil
}

// timingHeaders are the headers that report the time that the calls took, in the order that they are added to the response.
var timingHeaders = []string{
	constants.HeaderGotoClientDNSTook,
	constants.HeaderGotoClientTCPTook,
	constants.HeaderGotoClientProxyTLSTook,
	constants.HeaderGotoClientTunnelTook,
	constants.HeaderGotoClientTLSTook,
	constants.HeaderGotoClientResponseHeadTook,
	constants.HeaderGotoClientBodyTook,
	constants.HeaderGotoClientTotalTook,
	constants.HeaderGotoConnectProxyTook,
	constants.HeaderGotoConnectProxyDNSTook,
	constants.HeaderGotoConnectProxyDialTook,
	constants.HeaderGotoConnectProxyDelay,
}

// reportTimings adds a header for each phase that the calls went through, which has the time of the phase for each call, as
// a JSON array. The proxy that the calls were sent through reports the phases of its answer to the CONNECT request in
// the same way. Nothing is reported for the headers that no call has a time for.
func reportTimings(h http.Header, calls []*transport.CallTimings) {
	if len(calls) == 0 {
		return
	}
	perCall := make([]map[string]string, len(calls))
	for i, call := range calls {
		perCall[i] = call.Headers()
	}
	for _, name := range timingHeaders {
		values := make([]string, len(calls))
		reported := false
		for i := range calls {
			values[i] = perCall[i][name]
			reported = reported || values[i] != "-"
		}
		if reported {
			h.Add(name, util.ToJSONText(values))
		}
	}
}

// invokeHTTP is the client invoke API: it parses the call spec from the body, invokes it, and replies with the results plus upstream status, cert and timing headers.
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
	log.Printf("HTTP Client: Invoking [%s], Authority: [%s], TLS Cert: [%s], ALPN: [%s]\n", call.URL, call.Authority, call.ClientCert, util.ToJSONText(call.ALPN))
	callResults, err := call.Invoke(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("[HTTP Client] Failed to invoke URL [%s] Authority [%s] with error: %s", call.URL, call.Authority, err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	w.Header().Add(constants.HeaderGotoUpstreamStatus, util.ToJSONText(callResults.Statuses))
	if len(callResults.ServerCerts) > 0 {
		w.Header().Add(constants.HeaderGotoServerCert, util.ToJSONText(callResults.ServerCerts))
	}
	w.Header().Add(constants.HeaderGotoClientCert, callResults.ClientCert)
	reportTimings(w.Header(), callResults.timings)
	for _, cr := range callResults.Results {
		viaGotos := cr.Headers[constants.HeaderViaGoto]
		for _, v := range viaGotos {
			if v != "" {
				rs.ViaGotos = append(rs.ViaGotos, v)
			}
		}
	}
	util.WriteJsonOrYAMLPayload(w, callResults, !util.IsAcceptJSON(r))
}

// Run is the CLI client mode: it adds a target per configured URL, invokes them all, and logs (and optionally persists and reloads) the summary and detailed results.
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
		if err := target.Client.AddTarget(&target.Target{InvocationSpec: is}); err != nil {
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

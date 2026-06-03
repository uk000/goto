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
	Payload      []string             `json:"payload"`
	StreamDelay  string               `json:"streamDelay"`
	Headers      *types.HeadersConfig `json:"headers"`
	request      *http.Request
	client       transport.ClientTransport
	streamWriter io.WriteCloser
	streamDelayD time.Duration
}

var (
	Middleware        = middleware.NewMiddleware("client", setRoutes, nil)
	clientMiddlewares = []*middleware.Middleware{&target.Middleware}
)

func setRoutes(r *mux.Router) {
	clientRouter := middleware.RootPath("/client")
	middleware.AddRoutes(clientRouter, clientMiddlewares...)
	util.AddRoute(clientRouter, "/http/invoke", invokeHTTP, "GET", "POST", "PUT", "OPTIONS")
}

func (c *CallSpec) PrepareRequest(r *http.Request) error {
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
	req.Header.Add("User-Agent", global.Self.HostLabel)
	if c.Authority != "" {
		req.Host = c.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
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

func (c *CallSpec) Invoke(r *http.Request) ([]string, map[string]int, error) {
	client := transport.CreateDefaultHTTPClient(global.Self.Name, c.H2, c.TLS, c.Authority, metrics.ConnTracker)
	output := []string{}
	statuses := map[string]int{}
	for i := 1; i <= c.Count; i++ {
		err := c.PrepareRequest(r)
		if err != nil {
			return nil, nil, err
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
			return nil, nil, err
		}
		msg := fmt.Sprintf("Call to URL [%s] Authority [%s] succeeded with response [%s]", c.URL, c.Authority, resp.Status)
		util.AddLogMessage(msg, r)
		defer resp.Body.Close()
		b := util.Read(resp.Body)
		output = append(output, b)
		key := fmt.Sprintf("%s[%d]", c.URL, i)
		statuses[key] = resp.StatusCode
	}
	return output, statuses, nil
}

func invokeHTTP(w http.ResponseWriter, r *http.Request) {
	call := &CallSpec{}
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, call)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("[HTTP Client] Failed to parse call spec with error: %s", err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	output, statuses, err := call.Invoke(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("[HTTP Client] Failed to invoke URL [%s] Authority [%s] with error: %s", call.URL, call.Authority, err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	w.Header().Add(constants.HeaderGotoUpstreamStatus, util.ToJSONText(statuses))
	util.WriteJsonOrYAMLPayload(w, output, true)
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

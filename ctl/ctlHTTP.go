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
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
	"log"
	"net/http"
)

type HTTPResponse struct {
	Payloads []*payload.ResponsePayload `yaml:"payloads"`
}

type HTTPServerConfig struct {
	Port     int           `yaml:"port"`
	Response *HTTPResponse `yaml:"response"`
}

type HTTP struct {
	Servers []*HTTPServerConfig `yaml:"servers"`
}

func processHTTP(config *GotoConfig) {
	if config.HTTP != nil {
		for _, server := range config.HTTP.Servers {
			processHTTPResponse(server.Port, server.Response)
		}
	}
}

func processHTTPResponse(port int, r *HTTPResponse) {
	if r == nil {
		log.Printf("no HTTP response")
		return
	}
	for _, rp := range r.Payloads {
		//TBD: send payload over API
		url := fmt.Sprintf("%s/port=%d/server/response/payload/set/matches", currentContext.RemoteGotoURL, port)
		json := util.ToJSONBytes(rp)
		if json == nil {
			log.Printf("JSON marshalling failed for Response Payload: %+v\n", rp)
			return
		}
		log.Printf("Sending Response Payload to URL [%s]\n", url)
		resp, err := http.Post(url, "application/json", bytes.NewReader(json))
		if err != nil {
			log.Printf("JSON marshalling failed for Response Payload: %+v\n", rp)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("Non-OK status for  Response Payload: %s\n", resp.Status)
			log.Println(string(json))
		} else {
			log.Printf(" Response Payload sent successfully. Response: [%s]\n", util.Read(resp.Body))
		}
	}
}

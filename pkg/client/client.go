/**
 * Copyright 2025 uk
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
	"goto/pkg/client/results"
	"goto/pkg/client/target"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"

	"github.com/gorilla/mux"
)

var (
	Middleware        = middleware.NewMiddleware("client", SetRoutes, nil)
	clientMiddlewares = []*middleware.Middleware{&target.Middleware}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	clientRouter := r.PathPrefix("/client").Subrouter()
	middleware.AddRoutes(clientRouter, r, root, clientMiddlewares...)
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

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
	"goto/ctl"
	"goto/pkg/server/response/payload"
	"log"
)

func clearHTTP(h *ctl.HTTP) {
	if h != nil {
		for _, s := range h.Servers {
			payload.PayloadManager.ClearRPCResponsePayloads(s.Port)
		}
	}
}

func loadHTTP(h *ctl.HTTP) {
	if h == nil {
		return
	}
	for _, s := range h.Servers {
		processHTTPResponse(s.Port, s.Response)
	}
}

func processHTTPResponse(port int, hr *ctl.HTTPResponse) {
	if hr == nil {
		log.Println("No HTTP response")
		return
	}
	for _, rp := range hr.Payloads {
		err := payload.PayloadManager.SetURIResponsePayloadWithMatches(port, rp, false)
		if err != nil {
			log.Printf("Error processing HTTP response: %s\n", err.Error())
		}
	}
	log.Println("============================================================")
	log.Printf("[%d] HTTP Response Payloads loaded successfully", len(hr.Payloads))
	log.Println("============================================================")
}

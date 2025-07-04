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

package body

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	Middleware = middleware.NewMiddleware("body", nil, MiddlewareHandler)
)

func MiddlewareHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if global.Debug {
			log.Println("Enter Request.Body Middleware")
		}
		if !util.IsAdminRequest(r) && r.ProtoMajor == 1 {
			if global.Debug {
				log.Println("Reading Request.Body")
			}
			rs := util.GetRequestStore(r)
			body := util.Read(r.Body)
			bodyLength := len(body)
			rs.RequestPayload = body
			rs.RequestPayloadSize = bodyLength
			if global.Debug {
				log.Println("Finished Reading Request.Body")
			}
			util.AddLogMessage(fmt.Sprintf("Request Body Length: [%d]", bodyLength), r)
			if global.Flags.LogRequestMiniBody || global.Flags.LogRequestBody {
				bodyLog := ""
				if global.Flags.LogRequestMiniBody && len(body) > 50 {
					bodyLog = fmt.Sprintf("%s...", body[:50])
					bodyLog += fmt.Sprintf("%s", body[bodyLength-50:])
				} else {
					bodyLog = body
				}
				bodyLog = strings.ReplaceAll(bodyLog, "\n", "\\n")
				if global.Flags.LogRequestMiniBody {
					util.AddLogMessage(fmt.Sprintf("Request Mini Body: [%s]", bodyLog), r)
				} else {
					util.AddLogMessage(fmt.Sprintf("Request Body: [%s]", bodyLog), r)
				}
			}
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
		if global.Debug {
			log.Println("Discarding Request.Body")
		}
		if r.Body != nil {
			io.Copy(io.Discard, r.Body)
		}
		if global.Debug {
			log.Println("Exit Request.Body Middleware")
		}
	})
}

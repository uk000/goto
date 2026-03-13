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

package util

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
)

func AddLogMessage(msg string, r *http.Request) {
	rs := GetRequestStore(r)
	rs.LogMessages = append(rs.LogMessages, msg)
}

func LogMessage(ctx context.Context, msg string) {
	_, rs := GetRequestStoreFromContext(ctx)
	rs.LogMessages = append(rs.LogMessages, msg)
}

func UpdateTrafficEventStatusCode(r *http.Request, statusCode int) {
	rs := GetRequestStore(r)
	if rs != nil && !rs.IsTrafficEventReported {
		rs.StatusCode = statusCode
	}
}

func UpdateTrafficEventDetails(r *http.Request, details string) {
	rs := GetRequestStore(r)
	if !rs.IsTrafficEventReported {
		rs.TrafficDetails = append(rs.TrafficDetails, details)
	}
}

func ReportTrafficEvent(r *http.Request) (int, []string) {
	rs := GetRequestStore(r)
	if !rs.IsTrafficEventReported {
		rs.IsTrafficEventReported = true
		return rs.StatusCode, rs.TrafficDetails
	}
	return 0, nil
}

func PrintRequest(context string, r *http.Request) {
	log.Printf("======== %s ==========\n", context)
	if b, err := httputil.DumpRequest(r, true); err == nil {
		log.Println(string(b))
	}
	log.Printf(">> Method: %s", ToJSONText(r.Method))
	log.Printf(">> URI: %s", ToJSONText(r.RequestURI))
	log.Printf(">> Headers: %s", ToJSONText(r.Header))
	log.Printf(">> Query: %s", ToJSONText(r.URL.Query()))
	if rr, ok := r.Body.(*ReReader); ok {
		log.Printf(">> Body: %s", string(rr.Content))
	}
}

func PrintResponse(w http.ResponseWriter) {
	log.Println(ToJSONText(w))
}

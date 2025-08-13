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

package payload

import (
	"context"
	"fmt"
	"goto/pkg/server/intercept"
	"goto/pkg/util"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if util.IsKnownNonTraffic(r) {
			if next != nil {
				next.ServeHTTP(w, r)
			}
			return
		}
		var payload *ResponsePayload
		p := PayloadManager.getPortResponse(r)
		pr := p.protoPayload(util.IsGRPC(r) || util.IsJSONRPC(r))
		if !util.IsPayloadRequest(r) && pr.HasAnyPayload() {
			body := util.Read(r.Body)
			newBody, rp, captures, found := pr.GetResponsePayload(r.RequestURI, r.Header, r.URL.Query(), io.NopCloser(strings.NewReader(body)))
			if found {
				if newBody != nil {
					r.Body = newBody
				} else {
					r.Body = io.NopCloser(strings.NewReader(body))
				}
				payload = rp
				r = r.WithContext(context.WithValue(context.WithValue(r.Context(), payloadKey, payload), captureKey, captures))
				processPayload(w, r, rp, captures)
			}
		}
		if next != nil && (payload == nil || util.IsStatusRequest(r) || util.IsDelayRequest(r)) {
			next.ServeHTTP(w, r)
		}
	})
}

func processPayload(w http.ResponseWriter, r *http.Request, rp *ResponsePayload, captures map[string]string) {
	var payload []byte
	contentType := ""
	if !rp.IsBinary {
		payload = getFilledPayload(rp, r, captures)
	} else {
		payload = rp.Payload
	}
	contentType = rp.ContentType
	length := strconv.Itoa(len(payload))
	w.Header().Set("Content-Length", length)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Goto-Payload-Content-Type", contentType)
	msg := fmt.Sprintf("Responding with configured payload of length [%s], content type [%s], stream [%t] for URI [%s]",
		length, contentType, rp.IsStream, r.RequestURI)
	util.AddLogMessage(msg, r)

	payloadSent := false
	if rp.IsStream {
		w.Header().Set("Goto-Payload-Count", strconv.Itoa(len(rp.StreamPayload)))
		if fw := intercept.NewFlushWriter(r, w); fw != nil {
			failed := false
			for _, b := range rp.StreamPayload {
				if n, err := fw.Write(b); err != nil {
					msg = fmt.Sprintf("Failed to write stream payload of length [%d] with error: %s", n, err.Error())
					failed = true
					break
				}
				fw.Flush()
			}
			if !failed {
				msg = fmt.Sprintf("Written stream payload, count [%d]", len(rp.StreamPayload))
			}
			util.AddLogMessage(msg, r)
			payloadSent = true
		}
	} else {
		w.Header().Set("Goto-Payload-Length", length)
	}
	if !payloadSent {
		if n, err := w.Write(payload); err != nil {
			msg = fmt.Sprintf("Failed to write payload of length [%s] with error: %s", length, err.Error())
		} else {
			msg = fmt.Sprintf("Written payload of length [%d] compared to configured size [%s]", n, length)
		}
	}
	util.AddLogMessage(msg, r)
	util.UpdateTrafficEventDetails(r, "Response Payload Applied")
}

func getFilledPayload(rp *ResponsePayload, r *http.Request, captures map[string]string) []byte {
	vars := mux.Vars(r)
	payload := string(rp.Payload)
	payload = util.SubstitutePayloadMarkers(payload, rp.URICaptureKeys, vars)
	if rp.HeaderCaptureKey != "" {
		if value := r.Header.Get(rp.HeaderMatch); value != "" {
			payload = strings.Replace(payload, rp.HeaderCaptureKey, value, -1)
		}
	}
	if rp.QueryCaptureKey != "" {
		for k, values := range r.URL.Query() {
			if rp.queryMatchRegexp.MatchString(k) && len(values) > 0 {
				payload = strings.Replace(payload, rp.QueryCaptureKey, values[0], -1)
			}
		}
	}
	if len(rp.Transforms) > 0 {
		payload = util.TransformPayload(util.Read(r.Body), rp.Transforms, util.IsYAMLContentType(r.Header))
	}
	for k, v := range captures {
		payload = strings.Replace(payload, util.MarkFiller(k), v, -1)
	}
	return []byte(payload)
}

func handleURI(w http.ResponseWriter, r *http.Request) {
	processPayload(w, r, r.Context().Value(payloadKey).(*ResponsePayload), r.Context().Value(captureKey).(map[string]string))
}

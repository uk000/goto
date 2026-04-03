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

package payload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/echo"
	"goto/pkg/server/intercept"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsKnownNonTraffic {
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
	var jsonPayload any
	contentType := ""
	if !rp.IsBinary {
		payload = getFilledPayload(rp, r, captures)
	} else {
		payload = rp.Payload
	}
	if rp.Base64Encode {
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(payload)))
		base64.StdEncoding.Encode(encoded, payload)
		payload = encoded
	} else if rp.Base64Decode {
		decoded := make([]byte, base64.StdEncoding.DecodedLen(len(payload)))
		base64.StdEncoding.Decode(decoded, payload)
		decoded = util.CleanJSONBytes(decoded)
		if rp.IsJSON {
			var err error
			if err = json.Unmarshal(decoded, &jsonPayload); err == nil {
				jsonPayload = util.CleanJSON(jsonPayload)
				payload, _ = json.Marshal(jsonPayload)
			} else {
				payload = decoded
			}
		}
	}
	contentType = rp.ContentType
	w.Header().Set(constants.HeaderGotoPayloadContentType, contentType)
	w.Header().Set(constants.HeaderContentType, contentType)
	msg := fmt.Sprintf("Responding with configured payload content type [%s], stream [%t] for URI [%s]",
		contentType, rp.IsStream, r.RequestURI)
	util.AddLogMessage(msg, r)

	payloadSent := false
	if rp.IsStream {
		w.Header().Set(constants.HeaderGotoPayloadCount, strconv.Itoa(len(rp.StreamPayload)))
		if fw := intercept.NewFlushWriter(w); fw != nil {
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
	}
	if !payloadSent {
		if rp.IsJSON && !rp.EscapeJSON {
			payload = util.CleanJSONBytes(payload)
			if parsed, ok := util.TryUnmarshalString(string(payload)); ok {
				if v := util.Normalize(parsed); v != nil {
					jsonPayload = v
				}
			}
		}
		if jsonPayload != nil {
			if err := util.WriteJson(w, jsonPayload); err != nil {
				msg = fmt.Sprintf("Failed to write JSON payload of length [%d] with error: %s. Will send as non-JSON.", len(payload), err.Error())
				util.AddLogMessage(msg, r)
			} else {
				payloadSent = true
			}
		}
	}
	if !payloadSent {
		w.Header().Set(constants.HeaderGotoPayloadLength, strconv.Itoa(len(payload)))
		if n, err := w.Write(payload); err != nil {
			msg = fmt.Sprintf("Failed to write payload of length [%s] with error: %s", len(payload), err.Error())
		} else {
			msg = fmt.Sprintf("Written payload of length [%d] compared to configured size [%s]", n, len(payload))
		}
	}
	util.AddLogMessage(msg, r)
	util.UpdateTrafficEventDetails(r, "Response Payload Applied")
}

func processCaptures(payload string, value string, pair *types.Pair[*regexp.Regexp, []string], detectJSON, escapteJSON bool) string {
	if value != "" && pair.Left != nil {
		captures := util.GetCaptureGroupValues(pair.Left, pair.Right, value)
		for k, v := range captures {
			isJSONValue := strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") || strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}")
			if isJSONValue {
				if detectJSON {
					k = "\"" + k + "\""
				} else if escapteJSON {
					if v2, err := json.Marshal(v); err == nil {
						v = string(v2)
					}
					k = "\"" + k + "\""
				}
			}
			payload = strings.Replace(payload, k, v, -1)
		}
	}
	return payload
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
	if rp.RequestCapture != nil {
		for k, pair := range rp.RequestCapture.headerCaptureKeys {
			payload = processCaptures(payload, r.Header.Get(k), pair, rp.DetectJSON, rp.EscapeJSON)
		}
		for k, pair := range rp.RequestCapture.queryCaptureKeys {
			payload = processCaptures(payload, r.Header.Get(k), pair, rp.DetectJSON, rp.EscapeJSON)
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
		} else {
			util.WriteJsonPayload(w, echo.GetEchoResponseFromRS(util.GetRequestStore(r)))
		}
	}
}

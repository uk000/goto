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
	"bytes"
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/echo"
	"goto/pkg/server/intercept"
	"goto/pkg/util"
	"io"
	"net/http"
	"strconv"
)

// middlewareFunc responds with the configured payload matching the request (if any), and calls next only if none matched or the request is a status or delay request.
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
			body := readAndRestoreBody(r)
			_, rp, captures, found := pr.GetMatchingResponsePayload(r.RequestURI, r.Header, r.URL.Query(), io.NopCloser(bytes.NewReader(body)))
			if found {
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

// readAndRestoreBody reads the body of the request to look for a payload that it matches, and leaves it to be read from
// the start by whoever handles the request after that, whether or not a payload is found for it. The body of a request
// is kept as a re-reader, which the handlers that come after this one look for, so it stays one.
func readAndRestoreBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	rr := util.AsReReader(r.Body)
	rr.Rewind()
	r.Body = rr
	return rr.Content
}

// processPayload writes the matching payload as the response, as a flushed stream, JSON or raw bytes, with its content type and length headers.
func processPayload(w http.ResponseWriter, r *http.Request, rp *ResponsePayload, captures map[string]string) {
	var payload []byte
	contentType := ""
	if !rp.IsBinary {
		payload = rp.getFilledPayload(r, captures)
	} else {
		payload = rp.Payload
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
	var jsonPayload any
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
			msg = fmt.Sprintf("Failed to write payload of length [%d] with error: %s", len(payload), err.Error())
		} else {
			msg = fmt.Sprintf("Written payload of length [%d] compared to configured size [%d]", n, len(payload))
		}
	}
	util.AddLogMessage(msg, r)
}

// handleURI responds with the payload matching the request, or with the echo of the request if none matches.
func handleURI(w http.ResponseWriter, r *http.Request) {
	var payload *ResponsePayload
	p := PayloadManager.getPortResponse(r)
	pr := p.protoPayload(util.IsGRPC(r) || util.IsJSONRPC(r))
	if !util.IsPayloadRequest(r) && pr.HasAnyPayload() {
		body := readAndRestoreBody(r)
		_, rp, captures, found := pr.GetMatchingResponsePayload(r.RequestURI, r.Header, r.URL.Query(), io.NopCloser(bytes.NewReader(body)))
		if found {
			payload = rp
			r = r.WithContext(context.WithValue(context.WithValue(r.Context(), payloadKey, payload), captureKey, captures))
			processPayload(w, r, rp, captures)
		} else {
			util.WriteJsonPayload(w, echo.GetEchoResponseFromRS(util.GetRequestStore(r)))
		}
	}
}

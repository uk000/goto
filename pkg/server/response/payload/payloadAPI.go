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
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("response.payload", setRoutes, middlewareFunc)
)

func setRoutes(r *mux.Router) {
	payloadRouter := util.PathRouter(r, "/payload")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/stream/count={count}/delay={delay}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/stream/count={count}/delay={delay}/header/{header}", setResponsePayload, "uri", "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default/binary/{size}", setResponsePayload, "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default/binary", setResponsePayload, "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default/{size}", setResponsePayload, "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default", setResponsePayload, "POST")
	util.AddRouteQ(payloadRouter, "/set/{grpc}?", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/header/{header}={value}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/header/{header}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/query/{q}={value}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/query/{q}", setResponsePayload, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/set/{grpc}?/body~{regexes}", setResponsePayload, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/set/{grpc}?/body/paths/{paths}", setResponsePayload, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/{grpc}?/transform", setPayloadTransform, "uri", "POST")
	util.AddRoute(payloadRouter, "/clear", clearResponsePayload, "POST")
	util.AddRoute(payloadRouter, "", getResponsePayload, "GET")
	util.AddRoute(payloadRouter, "/{size}", respondWithPayload, "GET", "PUT", "POST")
}

func setResponsePayload(w http.ResponseWriter, r *http.Request) {
	msg := ""
	port := util.GetRequestOrListenerPort(r)
	payload := util.ReadBytes(r.Body)
	pr := PayloadManager.getPortResponse(r)
	isGRPC := util.GetStringParamValue(r, "grpc") != ""
	binary := util.IsBinaryContentHeader(r.Header) || strings.Contains(r.RequestURI, "binary")
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	query := util.GetStringParamValue(r, "q")
	value := util.GetStringParamValue(r, "value")
	regexes := util.GetStringParamValue(r, "regexes")
	paths := util.GetStringParamValue(r, "paths")
	isStream := strings.Contains(r.RequestURI, "stream")
	count := util.GetIntParamValue(r, "count")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	contentType := r.Header.Get(constants.HeaderResponseContentType)
	if contentType == "" {
		if binary {
			contentType = "application/octet-stream"
		} else {
			contentType = "plain/text"
		}
	}
	if isStream {
		if err := pr.setStreamResponsePayload(isGRPC, payload, contentType, uri, header, value, count, delayMin, delayMax); err == nil {
			msg = fmt.Sprintf("Port [%s] Stream Payload set with content-type: [%s], URI [%s] and header [%s], count: [%d], delay: [%s-%s]",
				port, contentType, uri, header, count, delayMin, delayMax)
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set Default Stream Payload with error: %s", port, err)
		}
	} else if header != "" && uri != "" {
		if err := pr.setResponsePayloadForURIWithHeader(isGRPC, payload, binary, uri, header, value, contentType); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and header [%s : %s] : content-type [%s], length [%d]",
				port, uri, header, value, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and header [%s : %s] : content-type [%s], length [%d] with error [%s]",
				port, uri, header, value, contentType, len(payload), err.Error())
		}
	} else if query != "" && uri != "" {
		if err := pr.setResponsePayloadForURIWithQuery(isGRPC, payload, binary, uri, query, value, contentType); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and query [%s : %s] : content-type [%s], length [%d]",
				port, uri, query, value, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and query [%s : %s] : content-type [%s], length [%d] with error [%s]",
				port, uri, query, value, contentType, len(payload), err.Error())
		}
	} else if uri != "" && (regexes != "" || paths != "") {
		match := regexes
		if match == "" {
			match = paths
		}
		if err := pr.setResponsePayloadForURIWithBodyMatch(isGRPC, payload, binary, uri, match, contentType, paths != ""); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and match [%+v] : content-type [%s], length [%d]",
				port, uri, match, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and match [%+v] : content-type [%s], length [%d] with error [%s]",
				port, uri, match, contentType, len(payload), err.Error())
		}
	} else if uri != "" {
		pr.setURIResponsePayload(isGRPC, false, payload, binary, uri, contentType, nil)
		msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] : content-type [%s], length [%d]",
			port, uri, contentType, len(payload))
	} else if header != "" {
		pr.setHeaderResponsePayload(isGRPC, payload, binary, header, value, contentType)
		msg = fmt.Sprintf("Port [%s] Payload set for header [%s : %s] : content-type [%s], length [%d]",
			port, header, value, contentType, len(payload))
	} else if query != "" {
		pr.setQueryResponsePayload(isGRPC, payload, binary, query, value, contentType)
		msg = fmt.Sprintf("Port [%s] Payload set for query [%s : %s] : content-type [%s], length [%d]",
			port, query, value, contentType, len(payload))
	} else {
		size := util.GetSizeParam(r, "size")
		if err := pr.setDefaultResponsePayload(isGRPC, payload, contentType, size); err == nil {
			if size > 0 {
				msg = fmt.Sprintf("Port [%s] Default Payload set with content-type: %s, size: %d",
					port, contentType, size)
			} else {
				msg = fmt.Sprintf("Port [%s] Default Payload set with content-type: %s",
					port, contentType)
			}
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set Default Payload with error: %s", port, err)
		}
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEvent("Response Payload Configured", msg, r)
}

func setPayloadTransform(w http.ResponseWriter, r *http.Request) {
	msg := ""
	port := util.GetRequestOrListenerPort(r)
	pr := PayloadManager.getPortResponse(r)
	isGRPC := util.GetStringParamValue(r, "grpc") != ""
	isStream := strings.Contains(r.RequestURI, "stream")
	contentType := r.Header.Get(constants.HeaderResponseContentType)
	if contentType == "" {
		contentType = constants.ContentTypeJSON
	}
	var transforms []*util.Transform
	if err := util.ReadJsonPayload(r, &transforms); err == nil {
		uri := util.GetStringParamValue(r, "uri")
		if uri != "" && transforms != nil {
			pr.setURIResponsePayload(isGRPC, isStream, nil, false, uri, contentType, transforms)
			msg = fmt.Sprintf("Port [%s] transform paths set for URI [%s] : [%s: %+v]",
				port, uri, contentType, util.ToJSONText(transforms))
			events.SendRequestEvent("Response Payload Configured", msg, r)
		} else {
			msg = "Invalid transformation. Missing URI or payload."
		}
	} else {
		msg = fmt.Sprintf("Invalid transformations: %s", err.Error())
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func clearResponsePayload(w http.ResponseWriter, r *http.Request) {
	PayloadManager.getPortResponse(r).init()
	msg := fmt.Sprintf("Port [%s] Response Payload Cleared", util.GetRequestOrListenerPort(r))
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEvent("Response Payload Cleared", msg, r)
}

func getResponsePayload(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, PayloadManager.getPortResponse(r))
}

func respondWithPayload(w http.ResponseWriter, r *http.Request) {
	sizeV := util.GetStringParamValue(r, "size")
	size := util.GetSizeParam(r, "size")
	if size <= 0 {
		size = 100
	}
	payload := types.GenerateRandomString(size)
	fmt.Fprint(w, payload)
	w.Header().Set(constants.HeaderContentLength, sizeV)
	w.Header().Set(constants.HeaderContentType, "plain/text")
	w.Header().Set(constants.HeaderGotoPayloadLength, sizeV)
	util.AddLogMessage(fmt.Sprintf("Responding with requested payload of length %d", size), r)
}

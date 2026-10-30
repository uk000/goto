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
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("response.payload", setRoutes, middlewareFunc)
)

func setRoutes(r *mux.Router) {
	payloadRouter := util.PathRouter(r, "/payload")
	util.AddRoute(payloadRouter, "/set/{grpc}?/matches", setResponsePayloadWithMatches, "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/stream/count={count}/delay={delay}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/stream/count={count}/delay={delay}/header/{header}", setResponsePayload, "uri", "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default/binary/{size}", setResponsePayload, "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default/binary", setResponsePayload, "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default/{size}", setResponsePayload, "POST")
	util.AddRoute(payloadRouter, "/set/{grpc}?/default", setResponsePayload, "POST")
	// The body match is registered before /set/{grpc}?, which takes any one segment after /set as the protocol, so it
	// would otherwise take /set/body~{regexes} for a URI payload of a protocol named "body~{regexes}".
	util.AddRouteQ(payloadRouter, "/set/{grpc}?/body~{regexes}", setResponsePayload, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/set/{grpc}?", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/header/{header}={value}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/header/{header}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/query/{q}={value}", setResponsePayload, "uri", "POST")
	util.AddRouteQO(payloadRouter, "/set/{grpc}?/query/{q}", setResponsePayload, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/set/{grpc}?/body/paths/{paths}", setResponsePayload, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/{grpc}?/transform", setPayloadTransform, "uri", "POST")
	util.AddRouteQ(payloadRouter, "/{grpc}?/transform/delay={delay}", setPayloadTransform, "uri", "POST")
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
	binary := util.IsBinaryContentHeader(r.Header) || routeHasSegment(r, "binary")
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	query := util.GetStringParamValue(r, "q")
	value := util.GetStringParamValue(r, "value")
	regexes := util.GetStringParamValue(r, "regexes")
	paths := util.GetStringParamValue(r, "paths")
	isStream := routeHasSegment(r, "stream")
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
		if err := pr.setResponsePayloadForURIWithHeader(isGRPC, payload, binary, uri, header, value, contentType, delayMin, delayMax); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and header [%s : %s] : content-type [%s], length [%d]",
				port, uri, header, value, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and header [%s : %s] : content-type [%s], length [%d] with error [%s]",
				port, uri, header, value, contentType, len(payload), err.Error())
		}
	} else if query != "" && uri != "" {
		if err := pr.setResponsePayloadForURIWithQuery(isGRPC, payload, binary, uri, query, value, contentType, delayMin, delayMax); err == nil {
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
		if err := pr.setResponsePayloadForURIWithBodyMatch(isGRPC, payload, binary, uri, match, contentType, paths != "", delayMin, delayMax); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and match [%+v] : content-type [%s], length [%d]",
				port, uri, match, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and match [%+v] : content-type [%s], length [%d] with error [%s]",
				port, uri, match, contentType, len(payload), err.Error())
		}
	} else if uri != "" {
		if err := pr.setURIResponsePayload(isGRPC, false, payload, binary, uri, contentType, nil, delayMin, delayMax); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] : content-type [%s], length [%d]",
				port, uri, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] : content-type [%s], length [%d] with error [%s]",
				port, uri, contentType, len(payload), err.Error())
		}
	} else if header != "" {
		if err := pr.setHeaderResponsePayload(isGRPC, payload, binary, header, value, contentType, delayMin, delayMax); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for header [%s : %s] : content-type [%s], length [%d]",
				port, header, value, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for header [%s : %s] : content-type [%s], length [%d] with error [%s]",
				port, header, value, contentType, len(payload), err.Error())
		}
	} else if query != "" {
		if err := pr.setQueryResponsePayload(isGRPC, payload, binary, query, value, contentType, delayMin, delayMax); err == nil {
			msg = fmt.Sprintf("Port [%s] Payload set for query [%s : %s] : content-type [%s], length [%d]",
				port, query, value, contentType, len(payload))
		} else {
			msg = fmt.Sprintf("Port [%s] Failed to set payload for query [%s : %s] : content-type [%s], length [%d] with error [%s]",
				port, query, value, contentType, len(payload), err.Error())
		}
	} else {
		size := util.GetSizeParam(r, "size")
		if err := pr.setDefaultResponsePayload(isGRPC, payload, contentType, size, delayMin, delayMax); err == nil {
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
}

// routeHasSegment tells whether the path of the route that serves the request has the given segment (e.g. "stream" in
// /set/stream/count={count}/delay={delay}). The URI that is being configured (the "uri" query param) and the values in
// the path are not looked at, so a URI or a header with such a name doesn't change what the request does.
func routeHasSegment(r *http.Request, segment string) bool {
	path := r.URL.Path
	if route := mux.CurrentRoute(r); route != nil {
		if template, err := route.GetPathTemplate(); err == nil {
			path = template
		}
	}
	for _, s := range strings.Split(path, "/") {
		if s == segment {
			return true
		}
	}
	return false
}

func setPayloadTransform(w http.ResponseWriter, r *http.Request) {
	msg := ""
	port := util.GetRequestOrListenerPort(r)
	pr := PayloadManager.getPortResponse(r)
	isGRPC := util.GetStringParamValue(r, "grpc") != ""
	isStream := routeHasSegment(r, "stream")
	contentType := r.Header.Get(constants.HeaderResponseContentType)
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	if contentType == "" {
		contentType = constants.ContentTypeJSON
	}
	var transforms []*util.Transform
	if err := util.ReadJsonPayload(r, &transforms); err == nil {
		uri := util.GetStringParamValue(r, "uri")
		if uri != "" && transforms != nil {
			pr.setURIResponsePayload(isGRPC, isStream, nil, false, uri, contentType, transforms, delayMin, delayMax)
			msg = fmt.Sprintf("Port [%s] transform paths set for URI [%s] : [%s: %+v]",
				port, uri, contentType, util.ToJSONText(transforms))
		} else {
			msg = "Invalid transformation. Missing URI or payload."
		}
	} else {
		msg = fmt.Sprintf("Invalid transformations: %s", err.Error())
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func setResponsePayloadWithMatches(w http.ResponseWriter, r *http.Request) {
	var err error
	msg := ""
	port := util.GetRequestOrListenerPortNum(r)
	isGRPC := util.GetStringParamValue(r, "grpc") != ""
	contentType := r.Header.Get(constants.HeaderResponseContentType)
	if contentType == "" {
		contentType = constants.ContentTypeJSON
	}
	rp := &ResponsePayload{}
	rps := []*ResponsePayload{}
	data := util.ReadBytes(r.Body)
	util.ReadJsonFromBytes(data, &rps)
	if len(rps) == 0 {
		err = util.ReadJsonFromBytes(data, rp)
	}
	if err == nil {
		if len(rps) > 0 {
			for _, rp := range rps {
				err = PayloadManager.SetURIResponsePayloadWithMatches(port, rp, isGRPC)
				if err != nil {
					break
				}
			}
		} else {
			err = PayloadManager.SetURIResponsePayloadWithMatches(port, rp, isGRPC)
		}
		if err == nil {
			var configured any = rp
			if len(rps) > 0 {
				configured = rps
			}
			msg = fmt.Sprintf("Port [%d] Response Payload set: %+v", port, util.ToJSONText(configured))
		} else {
			msg = fmt.Sprintf("Invalid Response Payload: %s", err.Error())
		}
	} else {
		msg = fmt.Sprintf("Failed to parse Response Payload: %s", err.Error())
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
}

func getResponsePayload(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, PayloadManager.getPortResponse(r))
}

func respondWithPayload(w http.ResponseWriter, r *http.Request) {
	size := util.GetSizeParam(r, "size")
	if size <= 0 {
		size = 100
	}
	payload := types.GenerateRandomString(size)
	// The headers describe the payload that is sent, and they have to be set before it is written. The size in the URI
	// can be given with units (e.g. 2K), which is not the length of the payload.
	length := strconv.Itoa(len(payload))
	w.Header().Set(constants.HeaderContentLength, length)
	w.Header().Set(constants.HeaderContentType, "plain/text")
	w.Header().Set(constants.HeaderGotoPayloadLength, length)
	fmt.Fprint(w, payload)
	util.AddLogMessage(fmt.Sprintf("Responding with requested payload of length %d", size), r)
}

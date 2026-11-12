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

package rpc

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
	"net/http"
	"reflect"
	"strings"
	"time"
)

type RPCMethod interface {
	GetName() string
	GetURI() string
	SetStreamCount(int)
	SetStreamDelay(min, max time.Duration, count int)
	SetResponsePayload([]byte)
}

type RPCService interface {
	IsGRPC() bool
	IsJSONRPC() bool
	GetName() string
	GetURI() string
	HasMethod(string) bool
	GetMethodCount() int
	ForEachMethod(f func(RPCMethod))
	GetMethod(name string) RPCMethod
}

type RPCServiceRegistry interface {
	GetRPCService(name string) RPCService
}

// CheckService resolves the service (and optional method) named in the request from the registry, writing a 400 with a message when invalid.
func CheckService(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) (service RPCService, method RPCMethod, serviceType string, msg string, ok bool) {
	s := util.GetStringParamValue(r, "service")
	m := util.GetStringParamValue(r, "method")
	if s == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No service"
		ok = false
	} else if isNil(reg) {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Service proto not defined [%s]", s)
		ok = false
	} else if service = reg.GetRPCService(s); isNil(service) {
		service = nil
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Service proto not defined [%s]", s)
		ok = false
	} else if m != "" && !service.HasMethod(m) {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Method [%s] not defined for service [%s]", m, s)
		ok = false
	} else {
		if m != "" {
			method = service.GetMethod(m)
		}
		if service.IsGRPC() {
			serviceType = "GRPC"
		} else {
			serviceType = "JSONRPC"
		}
		ok = true
	}
	return
}

// isNil tells if an interface holds nothing: it is nil, or it holds a nil pointer (or map, slice, etc.), which is what
// the registries return for a service that they don't have. A value that can't be nil is not nil.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

// checkMethod is for the APIs that work on a method of the service, which the request must have named. It writes the
// response when it doesn't.
func checkMethod(w http.ResponseWriter, method RPCMethod) (msg string, ok bool) {
	if isNil(method) {
		w.WriteHeader(http.StatusBadRequest)
		return "No method", false
	}
	return "", true
}

// ClearServiceResponsePayload clears the RPC response payloads on the port after validating the service and method of the request.
func ClearServiceResponsePayload(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) {
	service, method, serviceType, msg, ok := CheckService(w, r, reg)
	if ok {
		msg, ok = checkMethod(w, method)
	}
	if ok {
		port := util.GetRequestOrListenerPortNum(r)
		payload.PayloadManager.ClearRPCResponsePayloads(port)
		msg = fmt.Sprintf("Port [%d]: Cleared %s payloads for service [%s] method [%s]",
			port, serviceType, service.GetName(), method.GetName())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// SetServiceResponsePayload stores the request body as the response payload of a service method, including stream count, delay, and header/body match criteria.
func SetServiceResponsePayload(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) {
	service, method, serviceType, msg, ok := CheckService(w, r, reg)
	if ok {
		msg, ok = checkMethod(w, method)
	}
	if ok {
		content := util.ReadBytes(r.Body)
		port := util.GetRequestOrListenerPortNum(r)
		isStream := strings.Contains(r.RequestURI, "stream")
		count := util.GetIntParamValue(r, "count")
		delayMin, delayMax, delayCount, _ := util.GetDurationParam(r, "delay")
		if delayCount == 0 {
			delayCount = -1
		}
		header := util.GetStringParamValue(r, "header")
		value := util.GetStringParamValue(r, "value")
		regexes := util.GetStringParamValue(r, "regexes")
		paths := util.GetStringParamValue(r, "paths")
		contentType := r.Header.Get(constants.HeaderResponseContentType)
		if contentType == "" {
			contentType = "plain/text"
		}
		methodURI := method.GetURI()
		if err := payload.PayloadManager.SetRPCResponsePayload(port, isStream, content, contentType, methodURI, header, value, regexes, paths, count, delayMin, delayMax); err != nil {
			msg = fmt.Sprintf("Port [%d]: Failed to set %s payload for service [%s] method [%s], header [%s:%s], regexes [%s], paths [%s], content-type [%s], length [%d], count [%d], delay [%s-%s], with error [%s]",
				port, serviceType, service.GetName(), method.GetName(), header, value, regexes, paths, contentType, len(content), count, delayMin, delayMax, err.Error())
		} else {
			method.SetStreamCount(count)
			method.SetStreamDelay(delayMin, delayMax, delayCount)
			method.SetResponsePayload(content)
			msg = fmt.Sprintf("Port [%d]: Set %s payload for service [%s] method [%s], header [%s:%s], regexes [%s], paths [%s], content-type [%s], length [%d], count [%d], delay [%s-%s]",
				port, serviceType, service.GetName(), method.GetName(), header, value, regexes, paths, contentType, len(content), count, delayMin, delayMax)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// SetServicePayloadTransform reads transforms from the request body and applies them to the response payload of a service method.
func SetServicePayloadTransform(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) {
	service, method, serviceType, msg, ok := CheckService(w, r, reg)
	if ok {
		msg, ok = checkMethod(w, method)
	}
	if ok {
		isStream := strings.Contains(r.RequestURI, "stream")
		contentType := r.Header.Get(constants.HeaderResponseContentType)
		if contentType == "" {
			contentType = constants.ContentTypeJSON
		}
		var transforms []*util.Transform
		if err := util.ReadJsonPayload(r, &transforms); err == nil {
			if transforms != nil {
				methodURI := method.GetURI()
				port := util.GetRequestOrListenerPortNum(r)
				if err := payload.PayloadManager.SetRPCResponsePayloadTransform(port, isStream, contentType, methodURI, transforms, 0, 0); err != nil {
					msg = fmt.Sprintf("Port [%d]: Failed to set %s payload transform for service [%s] method [%s], content-type [%s], transforms: [%+v] with error [%s]",
						port, serviceType, service.GetName(), method.GetName(), contentType, util.ToJSONText(transforms), err.Error())
				} else {
					msg = fmt.Sprintf("Port [%d]: Configured Transform paths for %s service [%s] method [%s], content-type [%s], transforms: [%+v]",
						port, serviceType, service.GetName(), method.GetName(), contentType, util.ToJSONText(transforms))
				}
			} else {
				msg = "Missing transforms."
			}
		} else {
			msg = fmt.Sprintf("Failed to read transformations: %s", err.Error())
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

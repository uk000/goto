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

package rpc

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
	"net/http"
	"strings"
	"time"
)

type RPCMethod interface {
	GetName() string
	GetURI() string
	SetStreamCount(int)
	SetStreamDelayMin(time.Duration)
	SetStreamDelayMax(time.Duration)
}

type RPCService interface {
	GetName() string
	HasMethod(string) bool
	GetMethodCount() int
}

type RPCServiceRegistry interface {
	GetRPCService(name string) RPCService
}

func CheckService(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) (RPCService, RPCMethod, string) {
	msg := ""
	s := util.GetStringParamValue(r, "service")
	m := util.GetStringParamValue(r, "method")
	var service RPCService
	var method RPCMethod
	if s == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No service"
	} else if service = reg.GetRPCService(s); service == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Service proto not defined [%s]", s)
	} else if service.HasMethod(m) {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Method [%s] not defined for service [%s]", m, s)
	}
	return service, method, msg
}

func ClearServiceResponsePayload(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) {
	service, method, msg := CheckService(w, r, reg)
	if service != nil && method != nil {
		port := util.GetRequestOrListenerPortNum(r)
		payload.PayloadManager.ClearRPCResponsePayloads(port)
		msg = fmt.Sprintf("Port [%d]: Cleared GRPC payloads for service [%s] method [%s]",
			port, service.GetName(), method.GetName())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func SetServiceResponsePayload(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) {
	service, method, msg := CheckService(w, r, reg)
	if service != nil && method != nil {
		content := util.ReadBytes(r.Body)
		port := util.GetRequestOrListenerPortNum(r)
		isStream := strings.Contains(r.RequestURI, "stream")
		count := util.GetIntParamValue(r, "count")
		delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
		header := util.GetStringParamValue(r, "header")
		value := util.GetStringParamValue(r, "value")
		regexes := util.GetStringParamValue(r, "regexes")
		paths := util.GetStringParamValue(r, "paths")
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "plain/text"
		}
		if err := payload.PayloadManager.SetRPCResponsePayload(port, isStream, content, contentType, method.GetURI(), header, value, regexes, paths, count, delayMin, delayMax); err != nil {
			msg = fmt.Sprintf("Port [%d]: Failed to set GRPC payload for service [%s] method [%s], header [%s:%s], regexes [%s], paths [%s], content-type [%s], length [%d], count [%d], delay [%s-%s], with error [%s]",
				port, service.GetName(), method.GetName(), header, value, regexes, paths, contentType, len(content), count, delayMin, delayMax, err.Error())
		} else {
			method.SetStreamCount(count)
			method.SetStreamDelayMin(delayMin)
			method.SetStreamDelayMax(delayMax)
			msg = fmt.Sprintf("Port [%d]: Set GRPC payload for service [%s] method [%s], header [%s:%s], regexes [%s], paths [%s], content-type [%s], length [%d], count [%d], delay [%s-%s]",
				port, service.GetName(), method.GetName(), header, value, regexes, paths, contentType, len(content), count, delayMin, delayMax)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func SetServicePayloadTransform(w http.ResponseWriter, r *http.Request, reg RPCServiceRegistry) {
	service, method, msg := CheckService(w, r, reg)
	if service != nil {
		contentType := r.Header.Get("Content-Type")
		if contentType == "" {
			contentType = constants.ContentTypeJSON
		}
		var transforms []*util.Transform
		if err := util.ReadJsonPayload(r, &transforms); err == nil {
			if transforms != nil {
				port := util.GetRequestOrListenerPortNum(r)
				if err := payload.PayloadManager.SetRPCResponsePayloadTransform(port, contentType, method.GetURI(), transforms); err != nil {
					msg = fmt.Sprintf("Port [%d]: Failed to set GRPC payload transform for service [%s] method [%s], content-type [%s], transforms: [%+v] with error [%s]",
						port, service.GetName(), method.GetName(), contentType, util.ToJSONText(transforms), err.Error())
				} else {
					msg = fmt.Sprintf("Port [%d]: Configured Transform paths for GRPC service [%s] method [%s], content-type [%s], transforms: [%+v]",
						port, service.GetName(), method.GetName(), contentType, util.ToJSONText(transforms))
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

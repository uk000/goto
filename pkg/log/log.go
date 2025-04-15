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

package log

import (
	"fmt"
	"goto/pkg/global"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Handler = util.ServerHandler{Name: "log", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	logRouter := util.PathRouter(r, "/log")
	util.AddRoute(logRouter, "/server/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/admin/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/client/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/invocation/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/invocation/response/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/registry/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/registry/locker/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/registry/events/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/registry/reminder/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/health/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/probe/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/metrics/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/request/headers/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/request/minibody/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/request/body/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/response/headers/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/response/minibody/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "/response/body/{enable}", setLogLevel, "POST", "PUT")
	util.AddRoute(logRouter, "", getLogLevels, "GET")
}

func setLogLevel(w http.ResponseWriter, r *http.Request) {
	msg := ""
	enable := util.GetBoolParamValue(r, "enable")
	server := strings.Contains(r.RequestURI, "server")
	admin := strings.Contains(r.RequestURI, "admin")
	client := strings.Contains(r.RequestURI, "client")
	invocation := strings.Contains(r.RequestURI, "invocation")
	invocationResponse := strings.Contains(r.RequestURI, "invocation/response")
	registry := strings.Contains(r.RequestURI, "registry")
	locker := strings.Contains(r.RequestURI, "locker")
	events := strings.Contains(r.RequestURI, "events")
	reminder := strings.Contains(r.RequestURI, "reminder")
	health := strings.Contains(r.RequestURI, "health")
	probe := strings.Contains(r.RequestURI, "probe")
	metrics := strings.Contains(r.RequestURI, "metrics")
	request := strings.Contains(r.RequestURI, "request")
	response := strings.Contains(r.RequestURI, "response")
	headers := strings.Contains(r.RequestURI, "headers")
	minibody := strings.Contains(r.RequestURI, "minibody")
	body := strings.Contains(r.RequestURI, "body")
	if server {
		global.EnableServerLogs = enable
		msg = fmt.Sprintf("All Server logging set to [%t]", enable)
	} else if admin {
		global.EnableAdminLogs = enable
		msg = fmt.Sprintf("All Admin logging set to [%t]", enable)
	} else if client {
		global.EnableClientLogs = enable
		msg = fmt.Sprintf("Client logging set to [%t]", enable)
	} else if invocationResponse {
		global.EnableInvocationResponseLogs = enable
		msg = fmt.Sprintf("Invocation Response logging set to [%t]", enable)
	} else if invocation {
		global.EnableInvocationLogs = enable
		msg = fmt.Sprintf("Invocation logging set to [%t]", enable)
	} else if registry {
		if locker {
			global.EnableRegistryLockerLogs = enable
			msg = fmt.Sprintf("Registry Locker logging set to [%t]", enable)
		} else if events {
			global.EnableRegistryEventsLogs = enable
			msg = fmt.Sprintf("Registry Events logging set to [%t]", enable)
		} else if reminder {
			global.EnableRegistryReminderLogs = enable
			msg = fmt.Sprintf("Registry Reminder logging set to [%t]", enable)
		} else {
			global.EnableRegistryLogs = enable
			msg = fmt.Sprintf("All Registry logging set to [%t]", enable)
		}
	} else if health {
		global.EnablePeerHealthLogs = enable
		msg = fmt.Sprintf("Health logging set to [%t]", enable)
	} else if probe {
		global.EnableProbeLogs = enable
		msg = fmt.Sprintf("Probe logging set to [%t]", enable)
	} else if metrics {
		global.EnableMetricsLogs = enable
		msg = fmt.Sprintf("Metrics logging set to [%t]", enable)
	} else if request {
		if headers {
			global.LogRequestHeaders = enable
			msg = fmt.Sprintf("Request Headers logging set to [%t]", enable)
		} else if minibody {
			global.LogRequestMiniBody = enable
			if enable && global.LogRequestBody {
				global.LogRequestBody = false
			}
			msg = fmt.Sprintf("Request Mini Body logging set to [%t]", enable)
		} else if body {
			global.LogRequestBody = enable
			if enable && global.LogRequestMiniBody {
				global.LogRequestMiniBody = false
			}
			msg = fmt.Sprintf("Request Body logging set to [%t]", enable)
		}
	} else if response {
		if headers {
			global.LogResponseHeaders = enable
			msg = fmt.Sprintf("Response Headers logging set to [%t]", enable)
		} else if minibody {
			global.LogResponseMiniBody = enable
			if enable && global.LogResponseBody {
				global.LogResponseBody = false
			}
			msg = fmt.Sprintf("Response Mini Body logging set to [%t]", enable)
		} else if body {
			global.LogResponseBody = enable
			if enable && global.LogResponseMiniBody {
				global.LogResponseMiniBody = false
			}
			msg = fmt.Sprintf("Response Body logging set to [%t]", enable)
		}
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getLogLevels(w http.ResponseWriter, r *http.Request) {
	levels := map[string]bool{
		"server":     global.EnableServerLogs,
		"admin":      global.EnableAdminLogs,
		"client":     global.EnableClientLogs,
		"invocation": global.EnableInvocationLogs,
		"registry":   global.EnableRegistryLogs,
		"locker":     global.EnableRegistryLockerLogs,
		"events":     global.EnableRegistryEventsLogs,
		"reminder":   global.EnableRegistryReminderLogs,
		"health":     global.EnablePeerHealthLogs,
		"probe":      global.EnableProbeLogs,
		"metrics":    global.EnableMetricsLogs,
		"request":    global.LogRequestHeaders,
		"response":   global.LogResponseHeaders,
	}
	util.WriteJsonPayload(w, levels)
}

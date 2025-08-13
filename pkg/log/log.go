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
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("log", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
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
		global.Flags.EnableServerLogs = enable
		msg = fmt.Sprintf("All Server logging set to [%t]", enable)
	} else if admin {
		global.Flags.EnableAdminLogs = enable
		msg = fmt.Sprintf("All Admin logging set to [%t]", enable)
	} else if client {
		global.Flags.EnableClientLogs = enable
		msg = fmt.Sprintf("Client logging set to [%t]", enable)
	} else if invocationResponse {
		global.Flags.EnableInvocationResponseLogs = enable
		msg = fmt.Sprintf("Invocation Response logging set to [%t]", enable)
	} else if invocation {
		global.Flags.EnableInvocationLogs = enable
		msg = fmt.Sprintf("Invocation logging set to [%t]", enable)
	} else if registry {
		if locker {
			global.Flags.EnableRegistryLockerLogs = enable
			msg = fmt.Sprintf("Registry Locker logging set to [%t]", enable)
		} else if events {
			global.Flags.EnableRegistryEventsLogs = enable
			msg = fmt.Sprintf("Registry Events logging set to [%t]", enable)
		} else if reminder {
			global.Flags.EnableRegistryReminderLogs = enable
			msg = fmt.Sprintf("Registry Reminder logging set to [%t]", enable)
		} else {
			global.Flags.EnableRegistryLogs = enable
			msg = fmt.Sprintf("All Registry logging set to [%t]", enable)
		}
	} else if health {
		global.Flags.EnablePeerHealthLogs = enable
		msg = fmt.Sprintf("Health logging set to [%t]", enable)
	} else if probe {
		global.Flags.EnableProbeLogs = enable
		msg = fmt.Sprintf("Probe logging set to [%t]", enable)
	} else if metrics {
		global.Flags.EnableMetricsLogs = enable
		msg = fmt.Sprintf("Metrics logging set to [%t]", enable)
	} else if request {
		if headers {
			global.Flags.LogRequestHeaders = enable
			msg = fmt.Sprintf("Request Headers logging set to [%t]", enable)
		} else if minibody {
			global.Flags.LogRequestMiniBody = enable
			if enable && global.Flags.LogRequestBody {
				global.Flags.LogRequestBody = false
			}
			msg = fmt.Sprintf("Request Mini Body logging set to [%t]", enable)
		} else if body {
			global.Flags.LogRequestBody = enable
			if enable && global.Flags.LogRequestMiniBody {
				global.Flags.LogRequestMiniBody = false
			}
			msg = fmt.Sprintf("Request Body logging set to [%t]", enable)
		}
	} else if response {
		if headers {
			global.Flags.LogResponseHeaders = enable
			msg = fmt.Sprintf("Response Headers logging set to [%t]", enable)
		} else if minibody {
			global.Flags.LogResponseMiniBody = enable
			if enable && global.Flags.LogResponseBody {
				global.Flags.LogResponseBody = false
			}
			msg = fmt.Sprintf("Response Mini Body logging set to [%t]", enable)
		} else if body {
			global.Flags.LogResponseBody = enable
			if enable && global.Flags.LogResponseMiniBody {
				global.Flags.LogResponseMiniBody = false
			}
			msg = fmt.Sprintf("Response Body logging set to [%t]", enable)
		}
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getLogLevels(w http.ResponseWriter, r *http.Request) {
	levels := map[string]bool{
		"server":     global.Flags.EnableServerLogs,
		"admin":      global.Flags.EnableAdminLogs,
		"client":     global.Flags.EnableClientLogs,
		"invocation": global.Flags.EnableInvocationLogs,
		"registry":   global.Flags.EnableRegistryLogs,
		"locker":     global.Flags.EnableRegistryLockerLogs,
		"events":     global.Flags.EnableRegistryEventsLogs,
		"reminder":   global.Flags.EnableRegistryReminderLogs,
		"health":     global.Flags.EnablePeerHealthLogs,
		"probe":      global.Flags.EnableProbeLogs,
		"metrics":    global.Flags.EnableMetricsLogs,
		"proxy":      global.Flags.EnableProxyDebugLogs,
		"request":    global.Flags.LogRequestHeaders,
		"response":   global.Flags.LogResponseHeaders,
	}
	util.WriteJsonPayload(w, levels)
}

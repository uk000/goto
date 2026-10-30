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

package log

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// saveFlags puts the flags of the server back when the test is done, and the headers that the test excluded from logs
// back in the logs.
func saveFlags(t *testing.T, excluded ...string) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	flags := global.Flags
	t.Cleanup(func() {
		global.Flags = flags
		for _, h := range excluded {
			util.IncludeHeader(h)
		}
	})
}

// admin sends a request to the admin API of the log settings.
func admin(method, target string) *httptest.ResponseRecorder {
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	req := httptest.NewRequest(method, target, nil)
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	middleware.RootRouters["/log"].ServeHTTP(rr, req)
	return rr
}

type levelRoute struct {
	path string
	flag func(*types.Flags) *bool
	text string
}

var levelRoutes = []levelRoute{
	{"server", func(f *types.Flags) *bool { return &f.EnableServerLogs }, "All Server logging set to [%t]"},
	{"admin", func(f *types.Flags) *bool { return &f.EnableAdminLogs }, "All Admin logging set to [%t]"},
	{"client", func(f *types.Flags) *bool { return &f.EnableClientLogs }, "Client logging set to [%t]"},
	{"invocation", func(f *types.Flags) *bool { return &f.EnableInvocationLogs }, "Invocation logging set to [%t]"},
	{"invocation/response", func(f *types.Flags) *bool { return &f.EnableInvocationResponseLogs }, "Invocation Response logging set to [%t]"},
	{"registry", func(f *types.Flags) *bool { return &f.EnableRegistryLogs }, "All Registry logging set to [%t]"},
	{"registry/locker", func(f *types.Flags) *bool { return &f.EnableRegistryLockerLogs }, "Registry Locker logging set to [%t]"},
	{"registry/reminder", func(f *types.Flags) *bool { return &f.EnableRegistryReminderLogs }, "Registry Reminder logging set to [%t]"},
	{"health", func(f *types.Flags) *bool { return &f.EnablePeerHealthLogs }, "Health logging set to [%t]"},
	{"probe", func(f *types.Flags) *bool { return &f.EnableProbeLogs }, "Probe logging set to [%t]"},
	{"metrics", func(f *types.Flags) *bool { return &f.EnableMetricsLogs }, "Metrics logging set to [%t]"},
	{"request/headers", func(f *types.Flags) *bool { return &f.LogRequestHeaders }, "Request Headers logging set to [%t]"},
	{"request/minibody", func(f *types.Flags) *bool { return &f.LogRequestMiniBody }, "Request Mini Body logging set to [%t]"},
	{"request/body", func(f *types.Flags) *bool { return &f.LogRequestBody }, "Request Body logging set to [%t]"},
	{"response/headers", func(f *types.Flags) *bool { return &f.LogResponseHeaders }, "Response Headers logging set to [%t]"},
	{"response/minibody", func(f *types.Flags) *bool { return &f.LogResponseMiniBody }, "Response Mini Body logging set to [%t]"},
	{"response/body", func(f *types.Flags) *bool { return &f.LogResponseBody }, "Response Body logging set to [%t]"},
}

func TestSetLogLevelRoutes(t *testing.T) {
	for _, lr := range levelRoutes {
		for _, enable := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s=%t", lr.path, enable), func(t *testing.T) {
				saveFlags(t)
				// Start from the opposite so that the call has to change it, and from all others being off so that a
				// call that changes another setting is seen.
				global.Flags = types.Flags{}
				*lr.flag(&global.Flags) = !enable
				rr := admin("POST", fmt.Sprintf("/log/%s/%t", lr.path, enable))
				if rr.Code != http.StatusOK {
					t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
				}
				want := types.Flags{}
				*lr.flag(&want) = enable
				if global.Flags != want {
					t.Errorf("flags = %+v, want only [%s] to be %t", global.Flags, lr.path, enable)
				}
				if got := strings.TrimSpace(rr.Body.String()); got != fmt.Sprintf(lr.text, enable) {
					t.Errorf("body = %q, want %q", got, fmt.Sprintf(lr.text, enable))
				}
			})
		}
	}
}

func TestSetLogLevelAcceptsPutAndValueForms(t *testing.T) {
	saveFlags(t)
	for value, want := range map[string]bool{"y": true, "yes": true, "TRUE": true, "1": true, "n": false, "no": false, "0": false, "junk": false} {
		global.Flags.EnableServerLogs = !want
		if rr := admin("PUT", "/log/server/"+value); rr.Code != http.StatusOK {
			t.Fatalf("PUT %s answered %d", value, rr.Code)
		}
		if global.Flags.EnableServerLogs != want {
			t.Errorf("value %q set the flag to %t, want %t", value, global.Flags.EnableServerLogs, want)
		}
	}
}

func TestSetLogLevelRejectsOtherMethods(t *testing.T) {
	saveFlags(t)
	global.Flags.EnableServerLogs = true
	for _, method := range []string{"GET", "DELETE"} {
		if rr := admin(method, "/log/server/false"); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s answered %d, want it not to be served", method, rr.Code)
		}
	}
	if !global.Flags.EnableServerLogs {
		t.Error("a method that is not allowed changed the setting")
	}
}

// Only one of the body logs is on at a time: the log of a part of the body, and the one of the whole body, replace each
// other.
func TestBodyLogsReplaceEachOther(t *testing.T) {
	for _, side := range []string{"request", "response"} {
		t.Run(side, func(t *testing.T) {
			saveFlags(t)
			mini := func() bool {
				if side == "request" {
					return global.Flags.LogRequestMiniBody
				}
				return global.Flags.LogResponseMiniBody
			}
			body := func() bool {
				if side == "request" {
					return global.Flags.LogRequestBody
				}
				return global.Flags.LogResponseBody
			}
			admin("POST", "/log/"+side+"/body/true")
			if !body() || mini() {
				t.Fatalf("body = %t, minibody = %t, want only the body", body(), mini())
			}
			admin("POST", "/log/"+side+"/minibody/true")
			if body() || !mini() {
				t.Fatalf("body = %t, minibody = %t, want the minibody to replace the body", body(), mini())
			}
			admin("POST", "/log/"+side+"/body/true")
			if !body() || mini() {
				t.Fatalf("body = %t, minibody = %t, want the body to replace the minibody", body(), mini())
			}
			admin("POST", "/log/"+side+"/body/false")
			if body() || mini() {
				t.Fatalf("body = %t, minibody = %t, want both off", body(), mini())
			}
			// Turning one off leaves the other alone.
			admin("POST", "/log/"+side+"/minibody/true")
			admin("POST", "/log/"+side+"/body/false")
			if !mini() {
				t.Error("turning the body log off turned the minibody log off")
			}
		})
	}
}

func TestSetMCPVerbose(t *testing.T) {
	saveFlags(t)
	global.Flags.VerboseMCP = false
	if rr := admin("POST", "/log/mcp/verbose=true"); rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	if !global.Flags.VerboseMCP {
		t.Error("verbose MCP logging was not turned on")
	}
	admin("PUT", "/log/mcp/verbose=false")
	if global.Flags.VerboseMCP {
		t.Error("verbose MCP logging was not turned off")
	}
}

func TestExcludeHeaderFromLogs(t *testing.T) {
	saveFlags(t, "x-secret-token", "x-other")
	rr := admin("POST", "/log/request/headers/ignore/X-Secret-Token")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "Header [X-Secret-Token] will not be logged" {
		t.Errorf("answered %d %q", rr.Code, rr.Body.String())
	}
	for _, h := range []string{"x-secret-token", "X-SECRET-TOKEN", "X-Secret-Token"} {
		if !util.IsHeaderExcluded(h) {
			t.Errorf("header %q is not excluded", h)
		}
	}
	if util.IsHeaderExcluded("x-other") {
		t.Error("a header that was not excluded is excluded")
	}
	if rr := admin("PUT", "/log/request/headers/ignore/x-other"); rr.Code != http.StatusOK || !util.IsHeaderExcluded("X-Other") {
		t.Errorf("PUT answered %d, header excluded = %t", rr.Code, util.IsHeaderExcluded("X-Other"))
	}
}

// The path of the ignore API has the name of a header. The settings of logs must not be taken from the name.
func TestExcludeHeaderLeavesLogSettingsAlone(t *testing.T) {
	saveFlags(t, "server-client-admin")
	before := global.Flags
	admin("POST", "/log/request/headers/ignore/server-client-admin")
	if global.Flags != before {
		t.Errorf("flags = %+v, want them unchanged from %+v", global.Flags, before)
	}
}

func TestGetLogLevels(t *testing.T) {
	saveFlags(t)
	global.Flags = types.Flags{
		EnableServerLogs:           true,
		EnableClientLogs:           true,
		EnableRegistryLockerLogs:   true,
		EnableRegistryReminderLogs: true,
		EnableProbeLogs:            true,
		EnableProxyDebugLogs:       true,
		LogResponseHeaders:         true,
	}
	rr := admin("GET", "/log")
	if rr.Code != http.StatusOK || rr.Header().Get(constants.HeaderContentType) != constants.ContentTypeJSON {
		t.Fatalf("answered %d with content type %q", rr.Code, rr.Header().Get(constants.HeaderContentType))
	}
	var got map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rr.Body.String(), err)
	}
	want := map[string]bool{
		"server": true, "admin": false, "client": true, "invocation": false, "registry": false, "locker": true,
		"reminder": true, "health": false, "probe": true, "metrics": false, "proxy": true,
		"request": false, "response": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("levels = %v, want %v", got, want)
	}
}

func TestGetLogLevelsFollowsChanges(t *testing.T) {
	saveFlags(t)
	global.Flags = types.Flags{}
	admin("POST", "/log/probe/true")
	admin("POST", "/log/registry/reminder/true")
	admin("POST", "/log/request/headers/true")
	var got map[string]bool
	if err := json.Unmarshal(admin("GET", "/log").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"probe", "reminder", "request"} {
		if !got[k] {
			t.Errorf("level %q is off after turning it on: %v", k, got)
		}
	}
	for _, k := range []string{"server", "admin", "client", "locker", "response"} {
		if got[k] {
			t.Errorf("level %q is on without being turned on: %v", k, got)
		}
	}
}

func TestLogMiddlewareHasRoutesOnly(t *testing.T) {
	if Middleware == nil || Middleware.Name != "log" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler != nil {
		t.Errorf("middleware = %+v, want one named log that has routes and no handler", Middleware)
	}
}

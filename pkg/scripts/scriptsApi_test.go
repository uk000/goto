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

package scripts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	stubOnce   sync.Once
	routesOnce sync.Once
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

// router is the router that serves the APIs of the scripts, which the middleware package keeps for the /scripts path.
func router() http.Handler {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/scripts"]
}

// useAPI makes the scripts of the package-wide manager go to a directory of the test, and leaves the manager empty
// when the test is done.
func useAPI(t *testing.T) string {
	t.Helper()
	dir := useScriptsDir(t)
	Scripts.RemoveAll()
	t.Cleanup(Scripts.RemoveAll)
	return dir
}

// call serves a request, the way the server does.
func call(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := router()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	within(t, method+" "+target, func() { h.ServeHTTP(rr, req) })
	return rr
}

func wantStatus(t *testing.T, rr *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, status, rr.Body.String())
	}
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "script" || Middleware.SetRoutes == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
	if Middleware.MiddlewareHandler != nil {
		t.Error("the scripts have only APIs, and no handler of requests")
	}
}

func TestAPIAddScriptAndRunIt(t *testing.T) {
	useAPI(t)
	rr := call(t, "POST", "/scripts/add/api1", "echo from-api")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "Script [api1] added successfully\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
	rr = call(t, "POST", "/scripts/run/api1", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "from-api\n" {
		t.Errorf("output = %q, want the output of the script", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content type = %q, want the output to be sent as bytes", ct)
	}
}

func TestAPIAddScriptWithPUT(t *testing.T) {
	useAPI(t)
	wantStatus(t, call(t, "PUT", "/scripts/add/put1", "echo put"), 200)
	rr := call(t, "POST", "/scripts/put1/run", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "put\n" {
		t.Errorf("output = %q", rr.Body.String())
	}
}

func TestAPIStoreScriptWritesAFile(t *testing.T) {
	dir := useAPI(t)
	rr := call(t, "POST", "/scripts/store/stored1", "#!/bin/sh\necho stored-by-api\n")
	wantStatus(t, rr, 200)
	data, err := os.ReadFile(filepath.Join(dir, "stored1.sh"))
	if err != nil || string(data) != "#!/bin/sh\necho stored-by-api\n" {
		t.Fatalf("file = %q, %v, want the body of the request", data, err)
	}
	rr = call(t, "POST", "/scripts/run/stored1", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "stored-by-api\n" {
		t.Errorf("output = %q", rr.Body.String())
	}
	rr = call(t, "POST", "/scripts/stored1/remove", "")
	wantStatus(t, rr, 200)
	if _, err := os.Stat(filepath.Join(dir, "stored1.sh")); !os.IsNotExist(err) {
		t.Errorf("the file of the removed script is still there: %v", err)
	}
}

func TestAPIRunScriptWithArgs(t *testing.T) {
	useAPI(t)
	wantStatus(t, call(t, "POST", "/scripts/store/args1", "#!/bin/sh\necho \"[$#:$1:$2]\"\n"), 200)
	for _, tt := range []struct{ target, want string }{
		{"/scripts/args1/run", "[0::]\n"},
		{"/scripts/args1/run?args=a,b", "[2:a:b]\n"},
		{"/scripts/args1/run?args=a&args=b", "[2:a:b]\n"},
		{"/scripts/args1/run?args=only", "[1:only:]\n"},
	} {
		rr := call(t, "POST", tt.target, "")
		wantStatus(t, rr, 200)
		if rr.Body.String() != tt.want {
			t.Errorf("POST %s = %q, want %q", tt.target, rr.Body.String(), tt.want)
		}
	}
	// The other route of running does not take the args.
	rr := call(t, "POST", "/scripts/run/args1", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "[0::]\n" {
		t.Errorf("output = %q", rr.Body.String())
	}
}

func TestAPIRunScriptReadsTheBodyAsInput(t *testing.T) {
	useAPI(t)
	wantStatus(t, call(t, "POST", "/scripts/add/upper", "tr a-z A-Z"), 200)
	rr := call(t, "POST", "/scripts/run/upper", "shout")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "SHOUT" {
		t.Errorf("output = %q, want the body of the request as the input", rr.Body.String())
	}
}

func TestAPIRunFailingScriptHasTheFailureInTheBody(t *testing.T) {
	useAPI(t)
	wantStatus(t, call(t, "POST", "/scripts/add/failing", "echo partial; exit 2"), 200)
	rr := call(t, "POST", "/scripts/run/failing", "")
	wantStatus(t, rr, 200)
	if !strings.HasPrefix(rr.Body.String(), "partial\n") || !strings.Contains(rr.Body.String(), "exit status 2") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestAPIRemoveScripts(t *testing.T) {
	useAPI(t)
	for _, name := range []string{"r1", "r2", "r3", "r4"} {
		wantStatus(t, call(t, "POST", "/scripts/add/"+name, "echo "+name), 200)
	}
	for _, target := range []string{"/scripts/remove/r1", "/scripts/r2/remove"} {
		rr := call(t, "POST", target, "")
		wantStatus(t, rr, 200)
	}
	if has(Scripts, "r1") || has(Scripts, "r2") || !has(Scripts, "r3") || !has(Scripts, "r4") {
		t.Errorf("scripts left: r1=%v r2=%v r3=%v r4=%v", has(Scripts, "r1"), has(Scripts, "r2"), has(Scripts, "r3"), has(Scripts, "r4"))
	}
	rr := call(t, "POST", "/scripts/remove/r3", "")
	if rr.Body.String() != "Script [r3] removed successfully\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
	rr = call(t, "POST", "/scripts/remove/all", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "All scripts removed successfully\n" {
		t.Errorf("body = %q", rr.Body.String())
	}
	if has(Scripts, "r4") {
		t.Error("remove all left a script")
	}
	// Removing what is not there is not an error.
	wantStatus(t, call(t, "POST", "/scripts/remove/nothing", ""), 200)
}

func TestAPIRemoveAllByTheNameAll(t *testing.T) {
	useAPI(t)
	wantStatus(t, call(t, "POST", "/scripts/add/x1", "echo x"), 200)
	rr := call(t, "POST", "/scripts/all/remove", "")
	wantStatus(t, rr, 200)
	if rr.Body.String() != "All scripts removed successfully\n" || has(Scripts, "x1") {
		t.Errorf("body = %q, x1 present = %v", rr.Body.String(), has(Scripts, "x1"))
	}
}

func TestAPIGetScripts(t *testing.T) {
	useAPI(t)
	rr := call(t, "GET", "/scripts", "")
	wantStatus(t, rr, 200)
	if strings.TrimSpace(rr.Body.String()) != "{}" {
		t.Errorf("body = %q, want no scripts", rr.Body.String())
	}
	wantStatus(t, call(t, "POST", "/scripts/add/listed", "echo listed"), 200)
	rr = call(t, "GET", "/scripts", "")
	wantStatus(t, rr, 200)
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("content type = %q", ct)
	}
	var got map[string]struct {
		Name     string `json:"name"`
		FilePath string `json:"filePath"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rr.Body.String())
	}
	if len(got) != 1 || got["listed"].Name != "listed" {
		t.Errorf("scripts = %v", got)
	}
}

func TestAPIMethodsAreChecked(t *testing.T) {
	useAPI(t)
	for _, tt := range []struct{ method, target string }{
		{"GET", "/scripts/add/m1"},
		{"GET", "/scripts/run/m1"},
		{"GET", "/scripts/store/m1"},
		{"GET", "/scripts/remove/m1"},
		{"POST", "/scripts"},
		{"DELETE", "/scripts/add/m1"},
	} {
		rr := call(t, tt.method, tt.target, "")
		if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want it to be refused", tt.method, tt.target, rr.Code)
		}
	}
	if has(Scripts, "m1") {
		t.Error("a refused request added a script")
	}
}

func TestAPIRunUnknownScript(t *testing.T) {
	useAPI(t)
	rr := call(t, "POST", "/scripts/run/never-added", "")
	wantStatus(t, rr, http.StatusNotFound)
	if !strings.Contains(rr.Body.String(), "[never-added]") || !strings.Contains(rr.Body.String(), "not found") {
		t.Errorf("body = %q, want the name of the script that is not there", rr.Body.String())
	}
}

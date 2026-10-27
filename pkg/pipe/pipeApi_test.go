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

package pipe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/scripts"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var routesOnce sync.Once

// router is the router that the middleware package keeps for the /pipes path.
func router() http.Handler {
	routesOnce.Do(func() {
		stubGlobalFuncs()
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/pipes"]
}

// call serves a request the way the server does, and doesn't wait for it any longer than a few seconds.
func call(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	h := router()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		h.ServeHTTP(rr, req)
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s %s panicked: %v", method, target, p)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("%s %s did not return in time", method, target)
	}
	return rr
}

func wantBody(t *testing.T, rr *httptest.ResponseRecorder, status int, contains string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, status, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), contains) {
		t.Fatalf("body = %q, want it to contain %q", rr.Body.String(), contains)
	}
}

func listedPipes(t *testing.T) map[string]*Pipe {
	t.Helper()
	rr := call(t, "GET", "/pipes", "")
	wantBody(t, rr, 200, "")
	pipes := map[string]*Pipe{}
	if err := json.Unmarshal(rr.Body.Bytes(), &pipes); err != nil {
		t.Fatalf("pipes %q: %v", rr.Body.String(), err)
	}
	return pipes
}

func TestAPICreateAndListPipes(t *testing.T) {
	a, b := uniqueName("apicreate"), uniqueName("apicreate")
	t.Cleanup(func() { removePipeForTest(a); removePipeForTest(b) })
	wantBody(t, call(t, "POST", "/pipes/create/"+a, ""), 200, fmt.Sprintf("Pipe [%s] created", a))
	wantBody(t, call(t, "PUT", "/pipes/create/"+b, ""), 200, fmt.Sprintf("Pipe [%s] created", b))
	pipes := listedPipes(t)
	if pipes[a] == nil || pipes[a].Name != a || pipes[b] == nil || pipes[b].Name != b {
		t.Errorf("pipes = %v", pipes)
	}
	if rr := call(t, "GET", "/pipes/create/"+a, ""); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
		t.Errorf("a pipe is not created by GET, status = %d", rr.Code)
	}
}

func TestAPIAddPipeFromPayload(t *testing.T) {
	name := uniqueName("apiadd")
	t.Cleanup(func() { removePipeForTest(name) })
	payload := fmt.Sprintf(`{"name":%q,"transforms":{"k":{"type":"Template","spec":"v"}},"outs":["k"]}`, name)
	wantBody(t, call(t, "POST", "/pipes/add", payload), 200, fmt.Sprintf("Pipe [%s] added", name))
	pipes := listedPipes(t)
	p := pipes[name]
	if p == nil || len(p.Stages) != 1 || p.Stages[0].Label != "default" || p.Transforms["k"] == nil || p.Transforms["k"].Type != TransformTemplate {
		t.Fatalf("pipe = %+v", p)
	}
	wantBody(t, call(t, "PUT", "/pipes/add", payload), 200, "added")
	wantBody(t, call(t, "POST", "/pipes/add", `{"name":`), 200, "Failed to parse pipe with error")
	if _, ok := listedPipes(t)[""]; ok {
		t.Errorf("a pipe that failed to parse was added")
	}
}

func TestAPIClearAndRemovePipe(t *testing.T) {
	a, b, c := uniqueName("apiclear"), uniqueName("apiclear"), uniqueName("apiclear")
	t.Cleanup(func() { removePipeForTest(a); removePipeForTest(b); removePipeForTest(c) })
	for _, n := range []string{a, b, c} {
		call(t, "POST", "/pipes/add", fmt.Sprintf(`{"name":%q,"sources":{"s":{"type":"HTTPRequest","spec":"x"}}}`, n))
	}
	wantBody(t, call(t, "POST", "/pipes/"+a+"/clear", ""), 200, fmt.Sprintf("Pipe [%s] cleared", a))
	wantBody(t, call(t, "PUT", "/pipes/clear/"+b, ""), 200, fmt.Sprintf("Pipe [%s] cleared", b))
	pipes := listedPipes(t)
	if len(pipes[a].Sources) != 0 || len(pipes[b].Sources) != 0 || len(pipes[c].Sources) != 1 {
		t.Errorf("sources after clearing: a=%d b=%d c=%d", len(pipes[a].Sources), len(pipes[b].Sources), len(pipes[c].Sources))
	}
	wantBody(t, call(t, "POST", "/pipes/remove/"+a, ""), 200, fmt.Sprintf("Pipe [%s] removed", a))
	wantBody(t, call(t, "PUT", "/pipes/"+b+"/remove", ""), 200, fmt.Sprintf("Pipe [%s] removed", b))
	pipes = listedPipes(t)
	if pipes[a] != nil || pipes[b] != nil || pipes[c] == nil {
		t.Errorf("pipes after removing: %v", pipes)
	}
	// clearing without a name takes all of the pipes away
	wantBody(t, call(t, "POST", "/pipes/clear", ""), 200, "All pipes cleared")
	if pipes = listedPipes(t); len(pipes) != 0 {
		t.Errorf("pipes after clearing all: %v", pipes)
	}
}

func TestAPIAddAndRemoveSources(t *testing.T) {
	name := uniqueName("apisources")
	t.Cleanup(func() { removePipeForTest(name) })
	call(t, "POST", "/pipes/create/"+name, "")
	wantBody(t, call(t, "POST", "/pipes/"+name+"/sources/add", `{"name":"req","type":"HTTPRequest","spec":"trig"}`), 200,
		fmt.Sprintf("Added Source [req] with Resource Spec [trig] to Pipe [%s]", name))
	wantBody(t, call(t, "POST", "/pipes/"+name+"/sources/add/k8s/pods?spec=/v1/pods", ""), 200,
		fmt.Sprintf("Added K8s Source [pods] with Resource Spec [/v1/pods] to Pipe [%s]", name))
	scriptName := uniqueName("apiscript")
	t.Cleanup(func() { scripts.Scripts.RemoveScript(scriptName) })
	wantBody(t, call(t, "PUT", "/pipes/"+name+"/sources/add/script/"+scriptName, "echo from-api"), 200,
		fmt.Sprintf("Added Script Source [%s] to Pipe [%s]", scriptName, name))
	p := listedPipes(t)[name]
	if p == nil || len(p.Sources) != 3 || p.Sources["req"].Type != SourceHTTPRequest || p.Sources["pods"].Type != SourceK8s ||
		p.Sources["pods"].Spec != "/v1/pods" || p.Sources[scriptName].Type != SourceScript || p.Sources[scriptName].Spec != scriptName {
		t.Fatalf("pipe = %+v", p)
	}
	wantBody(t, call(t, "POST", "/pipes/"+name+"/sources/remove/pods", ""), 200,
		fmt.Sprintf("Removed Source [pods] from Pipe [%s]", name))
	if p = listedPipes(t)[name]; len(p.Sources) != 2 || p.Sources["pods"] != nil {
		t.Errorf("sources after removing = %v", p.Sources)
	}
}

func TestAPISourceErrors(t *testing.T) {
	name := uniqueName("apierrors")
	missing := uniqueName("apimissing")
	t.Cleanup(func() { removePipeForTest(name) })
	call(t, "POST", "/pipes/create/"+name, "")
	wantBody(t, call(t, "POST", "/pipes/"+name+"/sources/add", `{"name":`), 200, "Failed to parse source with error")
	wantBody(t, call(t, "POST", "/pipes/"+missing+"/sources/add", `{"name":"s","type":"HTTPRequest"}`), 200,
		fmt.Sprintf("Failed to add source with error: Pipe [%s] doesn't exist.", missing))
	wantBody(t, call(t, "POST", "/pipes/"+missing+"/sources/add/k8s/s?spec=/v1/pods", ""), 200, "Failed to add K8s source [s]")
	wantBody(t, call(t, "POST", "/pipes/"+missing+"/sources/remove/s", ""), 200, "Failed to remove source [s]")
	// the script source is added even when there is no pipe for it, but the pipe is what reports the error in the log
	scriptName := uniqueName("apiscript")
	t.Cleanup(func() { scripts.Scripts.RemoveScript(scriptName) })
	wantBody(t, call(t, "POST", "/pipes/"+missing+"/sources/add/script/"+scriptName, "echo x"), 200, "Added Script Source")
	// without the spec the k8s route is not there
	if rr := call(t, "POST", "/pipes/"+name+"/sources/add/k8s/s", ""); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("k8s source without spec, status = %d", rr.Code)
	}
	if p := listedPipes(t)[name]; len(p.Sources) != 0 {
		t.Errorf("sources of the pipe = %v", p.Sources)
	}
}

func TestAPIRunPipeAndReadRuns(t *testing.T) {
	name := uniqueName("apirun")
	t.Cleanup(func() { removePipeForTest(name) })
	call(t, "POST", "/pipes/add", fmt.Sprintf(`{"name":%q,"transforms":{"k":{"type":"Template","spec":"value"}}}`, name))

	rr := call(t, "POST", "/pipes/"+name+"/run", "")
	wantBody(t, rr, 200, "")
	out := map[string]interface{}{}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out["k"] != "value" {
		t.Errorf("run gave %q, %v", rr.Body.String(), err)
	}
	rr = call(t, "POST", "/pipes/"+name+"/run", "", "Accept", "application/yaml")
	wantBody(t, rr, 200, "k: value")
	if rr := call(t, "GET", "/pipes/"+name+"/run", ""); rr.Code == 200 {
		t.Errorf("a pipe is not run by GET")
	}

	rr = call(t, "GET", "/pipes/"+name, "")
	wantBody(t, rr, 200, "")
	var reported struct {
		Pipe *Pipe     `json:"pipe"`
		Runs []PipeRun `json:"runs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &reported); err != nil {
		t.Fatalf("runs %q: %v", rr.Body.String(), err)
	}
	if reported.Pipe == nil || reported.Pipe.Name != name || len(reported.Runs) != 2 {
		t.Fatalf("reported = %s", rr.Body.String())
	}
	if reported.Runs[0].Out["k"] != "value" || reported.Runs[1].ID <= reported.Runs[0].ID || reported.Runs[0].Name != name {
		t.Errorf("runs = %+v", reported.Runs)
	}
}

func TestAPIRunAndReadRunsOfMissingPipe(t *testing.T) {
	missing := uniqueName("apimissing")
	rr := call(t, "POST", "/pipes/"+missing+"/run", "")
	if !strings.Contains(rr.Body.String(), fmt.Sprintf("Failed to run pipeline [%s] with error: Pipe [%s] doesn't exist.", missing, missing)) {
		t.Errorf("run of a missing pipe gave %d %q", rr.Code, rr.Body.String())
	}
	rr = call(t, "GET", "/pipes/"+missing, "")
	wantBody(t, rr, 200, "")
	if got := strings.TrimSpace(rr.Body.String()); got != `{"pipe":null,"runs":null}` {
		t.Errorf("runs of a missing pipe = %q", got)
	}
}

func TestAPIConcurrentUse(t *testing.T) {
	const workers = 6
	names := make([]string, workers)
	for i := range names {
		names[i] = uniqueName("apiconc")
	}
	t.Cleanup(func() {
		for _, n := range names {
			removePipeForTest(n)
		}
	})
	var wg sync.WaitGroup
	failures := make(chan string, workers*20)
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			do := func(method, target, body, want string) {
				h := router()
				req := httptest.NewRequest(method, target, strings.NewReader(body))
				req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
				_, req, _ = util.WithRequestStore(req, nil)
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				if rr.Code != 200 || !strings.Contains(rr.Body.String(), want) {
					failures <- fmt.Sprintf("%s %s = %d %q, want %q", method, target, rr.Code, rr.Body.String(), want)
				}
			}
			do("POST", "/pipes/add", fmt.Sprintf(`{"name":%q,"transforms":{"k":{"type":"Template","spec":"v"}}}`, name), "added")
			for i := 0; i < 3; i++ {
				do("POST", "/pipes/"+name+"/sources/add", fmt.Sprintf(`{"name":"s%d","type":"HTTPRequest","spec":"t"}`, i), "Added Source")
				do("POST", "/pipes/"+name+"/run", "", `"k"`)
				do("GET", "/pipes", "", name)
				do("GET", "/pipes/"+name, "", name)
				do("POST", "/pipes/"+name+"/sources/remove/s"+fmt.Sprint(i), "", "Removed Source")
			}
		}(name)
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	for _, n := range names {
		if got := runCount(n); got != 3 {
			t.Errorf("pipe %s has %d runs, want 3", n, got)
		}
	}
}

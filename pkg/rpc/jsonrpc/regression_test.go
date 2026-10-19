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

package jsonrpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/rpc"

	"github.com/gorilla/mux"
)

// A service that was given methods was refused: the methods are held by their interface, which the decoding can't make.
func TestRegressionServiceWithMethodsCanBeAdded(t *testing.T) {
	r := newRegistry()
	s, err := r.NewJSONRPCService(body(`{"name":"Calc","methods":{"add":{"name":"add","streamCount":2}}}`))
	if err != nil || s == nil {
		t.Fatalf("NewJSONRPCService with methods: %v", err)
	}
	m, ok := s.GetMethod("add").(*JSONRPCMethod)
	if !ok || m.URI != "/calc/add" || m.Service != s || m.StreamCount != 2 {
		t.Errorf("method = %+v", s.GetMethod("add"))
	}
}

// The registry that the rpc package finds for a port that has none yet was a nil registry in an interface, which gave a
// nil pointer panic to whatever called it.
func TestRegressionRegistryOfAPortThatWasNotUsedYet(t *testing.T) {
	port := uniquePort()
	t.Cleanup(func() {
		lock.Lock()
		delete(PortRegistry, port)
		lock.Unlock()
	})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("the registry of a new port panicked: %v", r)
		}
	}()
	reg := rpc.GetServiceRegistry["jsonrpc"](port)
	if reg == nil {
		t.Fatalf("no registry for a port that wasn't used before")
	}
	if got := reg.GetRPCService("Calc"); got != nil {
		t.Errorf("GetRPCService on a new registry = %#v, want a nil interface", got)
	}
}

// An API call without a name wrote its answer and then went on to read the service that was never made.
func TestRegressionAddServiceWithoutANameDoesNotPanic(t *testing.T) {
	e := newAPIEnv(t)
	req := mux.SetURLVars(e.request("POST", apiRoot+"/add/", `{"name":"Calc"}`), map[string]string{"service": ""})
	rr := httptest.NewRecorder()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("addService panicked: %v", r)
			}
		}()
		addService(rr, req)
	}()
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No name") {
		t.Errorf("add = %d %q, want 400 No name", rr.Code, rr.Body.String())
	}
	if e.registry().GetService("Calc") != nil {
		t.Errorf("a request that was refused must not register the service")
	}
}

// The delay count of a gRPC method wasn't copied to the service made from it.
func TestRegressionFromGRPCServiceCopiesTheDelayCount(t *testing.T) {
	g := grpcHealth(t)
	g.Methods["Check"].SetStreamDelay(time.Millisecond, 2*time.Millisecond, 6)
	s, err := newRegistry().FromGRPCService(g.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, delayCount := s.Methods["Check"].(*JSONRPCMethod).GetStreamConfig(); delayCount != 6 {
		t.Errorf("delay count = %d, want 6", delayCount)
	}
}

// The error for a service that isn't there ended with a line break, which made the answers of the API that report it
// have an empty line.
func TestRegressionUnknownGRPCServiceErrorIsOneLine(t *testing.T) {
	_, err := newRegistry().FromGRPCService("no.such.Service")
	if err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("error = %q, want one without a line break", err)
	}
}

// The registry was listed, and the methods were configured, while services were being added: the maps and the fields were
// read without the locks that the writers take. Run with -race.
func TestRegressionListingWhileServicesChange(t *testing.T) {
	r := newRegistry()
	// made by hand, so that the test is about the locks and not about how the body of a service is decoded
	first := &JSONRPCService{Name: "first", URI: "/first", Methods: map[string]rpc.RPCMethod{"m": &JSONRPCMethod{Name: "m", URI: "/first/m"}}}
	r.Services["first"] = first
	const rounds = 100
	var wg sync.WaitGroup
	errs := make(chan error, 3*rounds)
	run := func(f func(i int) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := f(i); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	run(func(i int) error {
		_, err := r.NewJSONRPCService(body(fmt.Sprintf(`{"name":"svc%d"}`, i)))
		return err
	})
	run(func(i int) error {
		m := first.GetMethod("m")
		m.SetStreamCount(i)
		m.SetStreamDelay(time.Duration(i), time.Duration(i), i)
		m.SetResponsePayload([]byte(fmt.Sprint(i)))
		return nil
	})
	run(func(int) error {
		if _, err := json.Marshal(r); err != nil {
			return err
		}
		_, err := json.Marshal(first)
		return err
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := len(r.Services); n != rounds+1 {
		t.Errorf("services = %d, want %d", n, rounds+1)
	}
}

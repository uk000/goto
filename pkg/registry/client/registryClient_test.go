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

package registryclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/global"
)

type recordedRequest struct {
	method      string
	path        string
	contentType string
	body        string
}

// fakeRegistry records the requests that it gets, and answers them with the status and the body that were set for the
// path of the request, or 200 and an empty body.
type fakeRegistry struct {
	srv       *httptest.Server
	lock      sync.Mutex
	requests  []recordedRequest
	responses map[string]fakeResponse
}

type fakeResponse struct {
	status int
	body   string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	fr := &fakeRegistry{responses: map[string]fakeResponse{}}
	fr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fr.lock.Lock()
		fr.requests = append(fr.requests, recordedRequest{method: r.Method, path: r.URL.Path,
			contentType: r.Header.Get("Content-Type"), body: string(b)})
		resp, found := fr.responses[r.URL.Path]
		fr.lock.Unlock()
		if !found {
			resp = fakeResponse{status: http.StatusOK}
		}
		w.WriteHeader(resp.status)
		io.WriteString(w, resp.body)
	}))
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *fakeRegistry) respond(path string, status int, body string) {
	fr.lock.Lock()
	fr.responses[path] = fakeResponse{status: status, body: body}
	fr.lock.Unlock()
}

func (fr *fakeRegistry) all() []recordedRequest {
	fr.lock.Lock()
	defer fr.lock.Unlock()
	return append([]recordedRequest(nil), fr.requests...)
}

func (fr *fakeRegistry) last(t *testing.T) recordedRequest {
	t.Helper()
	all := fr.all()
	if len(all) == 0 {
		t.Fatal("the registry got no requests")
	}
	return all[len(all)-1]
}

// configure points the global configuration at the registry, as it is when the server is started with a registry.
func configure(t *testing.T, url string, useLocker bool) {
	t.Helper()
	prevSelf, prevLocker := global.Self, global.Flags.UseLocker
	global.Self.RegistryURL = url
	global.Self.Name = "peer1"
	global.Self.Address = "10.1.2.3:8080"
	global.Self.ServerPort = 8080
	global.Flags.UseLocker = useLocker
	t.Cleanup(func() {
		global.Self = prevSelf
		global.Flags.UseLocker = prevLocker
	})
}

func openLocker(t *testing.T, fr *fakeRegistry, context, locker string) *RegistryLockerClient {
	t.Helper()
	configure(t, fr.srv.URL, true)
	rc, err := NewRegistryClient().OpenLocker(context, locker)
	if err != nil {
		t.Fatalf("OpenLocker: %v", err)
	}
	return rc
}

func TestNewRegistryClientBuildsRegistryURLs(t *testing.T) {
	configure(t, "http://registry:9090", true)
	rc := NewRegistryClient()
	if rc.baseURL != "http://registry:9090/registry" {
		t.Errorf("baseURL = %q", rc.baseURL)
	}
	if rc.basePeerURL != "http://registry:9090/registry/peers/peer1/10.1.2.3:8080" {
		t.Errorf("basePeerURL = %q", rc.basePeerURL)
	}
	if rc.client == nil || rc.client.HTTP() == nil {
		t.Errorf("the client has no transport")
	}
}

func TestInitRegistryClientSetsDefaultClient(t *testing.T) {
	prev := DefaultClient
	t.Cleanup(func() { DefaultClient = prev })
	configure(t, "http://registry:9090", true)
	DefaultClient = nil
	InitRegistryClient()
	if DefaultClient == nil || DefaultClient.baseURL != "http://registry:9090/registry" {
		t.Fatalf("DefaultClient = %+v", DefaultClient)
	}
	first := DefaultClient
	InitRegistryClient()
	if DefaultClient == first {
		t.Errorf("a new client must be created by every init")
	}
}

func TestOpenLockerRequiresConfiguredRegistryAndLocker(t *testing.T) {
	fr := newFakeRegistry(t)
	for name, tc := range map[string]struct {
		url       string
		useLocker bool
	}{
		"no registry":     {url: "", useLocker: true},
		"locker not used": {url: fr.srv.URL, useLocker: false},
		"neither":         {url: "", useLocker: false},
	} {
		configure(t, tc.url, tc.useLocker)
		rc, err := NewRegistryClient().OpenLocker("", "l1")
		if err == nil || rc != nil {
			t.Errorf("%s: OpenLocker = %v, %v, want an error", name, rc, err)
		}
	}
	if n := len(fr.all()); n != 0 {
		t.Errorf("the registry got %d requests, want none", n)
	}
}

func TestOpenLockerPostsToLockerOrContextLocker(t *testing.T) {
	fr := newFakeRegistry(t)
	configure(t, fr.srv.URL, true)
	rc := NewRegistryClient()

	locker, err := rc.OpenLocker("", "l1")
	if err != nil {
		t.Fatal(err)
	}
	if locker.locker != "l1" || locker.context != "" || locker.RegistryClient != rc {
		t.Errorf("locker client = %+v", locker)
	}
	if r := fr.last(t); r.method != http.MethodPost || r.path != "/registry/lockers/l1/open" || r.contentType != "application/json" || r.body != "" {
		t.Errorf("request = %+v", r)
	}

	locker, err = rc.OpenLocker("ctx", "l2")
	if err != nil {
		t.Fatal(err)
	}
	if locker.locker != "l2" || locker.context != "ctx" {
		t.Errorf("locker client = %+v", locker)
	}
	if r := fr.last(t); r.method != http.MethodPost || r.path != "/registry/context/ctx/lockers/l2/open" {
		t.Errorf("request = %+v", r)
	}
}

func TestOpenLockerReportsRegistryFailures(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.respond("/registry/lockers/bad/open", http.StatusInternalServerError, "no way")
	fr.respond("/registry/lockers/gone/open", http.StatusBadRequest, "")
	configure(t, fr.srv.URL, true)
	rc := NewRegistryClient()
	for locker, code := range map[string]string{"bad": "500", "gone": "400"} {
		l, err := rc.OpenLocker("", locker)
		if err == nil || l != nil {
			t.Fatalf("%s: OpenLocker = %v, %v, want an error", locker, l, err)
		}
		if !strings.Contains(err.Error(), "status code: "+code) || !strings.Contains(err.Error(), "/registry/lockers/"+locker+"/open") {
			t.Errorf("%s: error = %q", locker, err)
		}
	}
	// the registry answers with a redirect or something else that is not an error
	fr.respond("/registry/lockers/ok/open", http.StatusAccepted, "")
	if _, err := rc.OpenLocker("", "ok"); err != nil {
		t.Errorf("202 must be accepted: %v", err)
	}
}

func TestOpenLockerWithUnreachableRegistry(t *testing.T) {
	fr := newFakeRegistry(t)
	url := fr.srv.URL
	fr.srv.Close()
	configure(t, url, true)
	if l, err := NewRegistryClient().OpenLocker("", "l1"); err == nil || l != nil {
		t.Errorf("OpenLocker = %v, %v, want an error", l, err)
	}
}

func TestStoreAndStorePeerPostJSONToTheRightLocker(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	for _, tc := range []struct {
		name     string
		context  string
		peer     bool
		wantPath string
	}{
		{"locker", "", false, "/registry/lockers/l1/store/a,b"},
		{"context locker", "ctx", false, "/registry/context/ctx/lockers/l1/store/a,b"},
		{"peer", "", true, "/registry/peers/peer1/10.1.2.3:8080/locker/store/a,b"},
		{"peer in context", "ctx", true, "/registry/peers/peer1/10.1.2.3:8080/locker/store/a,b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRegistry(t)
			l := openLocker(t, fr, tc.context, "l1")
			var err error
			if tc.peer {
				err = l.StorePeer([]string{"a", "b"}, payload{Name: "x", Count: 3})
			} else {
				err = l.Store([]string{"a", "b"}, payload{Name: "x", Count: 3})
			}
			if err != nil {
				t.Fatal(err)
			}
			r := fr.last(t)
			if r.method != http.MethodPost || r.path != tc.wantPath || r.contentType != "application/json" || r.body != `{"name":"x","count":3}` {
				t.Errorf("request = %+v, want path %s", r, tc.wantPath)
			}
		})
	}
}

func TestStoreEncodesDifferentKindsOfData(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	for data, want := range map[string]struct {
		data any
		body string
	}{
		"string": {"text", `"text"`},
		"map":    {map[string]any{"k": []int{1, 2}}, `{"k":[1,2]}`},
		"number": {42, `42`},
		"nil":    {nil, ``},
	} {
		if err := l.Store([]string{data}, want.data); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		if r := fr.last(t); r.body != want.body {
			t.Errorf("%s: body = %q, want %q", data, r.body, want.body)
		}
	}
}

func TestStoreWithEmptyAndMultipleKeys(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	if err := l.Store(nil, "x"); err != nil {
		t.Fatal(err)
	}
	if r := fr.last(t); r.path != "/registry/lockers/l1/store/" {
		t.Errorf("path = %q", r.path)
	}
	if err := l.Store([]string{"a", "b", "c"}, "x"); err != nil {
		t.Fatal(err)
	}
	if r := fr.last(t); r.path != "/registry/lockers/l1/store/a,b,c" {
		t.Errorf("path = %q", r.path)
	}
}

func TestStoreReportsFailures(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	if err := l.Store([]string{"a"}, make(chan int)); err == nil {
		t.Errorf("data that can't be encoded must be an error")
	}
	if n := len(fr.all()); n != 1 { // the open request only
		t.Errorf("requests = %d, want only the one that opened the locker", n)
	}
	fr.respond("/registry/lockers/l1/store/a", http.StatusForbidden, "denied")
	err := l.Store([]string{"a"}, "x")
	if err == nil || !strings.Contains(err.Error(), "status code: 403") {
		t.Errorf("error = %v, want one about status code 403", err)
	}
	global.Flags.UseLocker = false
	if err := l.Store([]string{"a"}, "x"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want one about the registry not being configured", err)
	}
	if err := l.StorePeer([]string{"a"}, "x"); err == nil {
		t.Errorf("StorePeer must fail while the locker is disabled")
	}
}

func TestGetDataAndPeerDataReadFromTheRightLocker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		context  string
		peer     bool
		wantPath string
	}{
		{"locker", "", false, "/registry/lockers/l1/get/a,b"},
		{"context locker", "ctx", false, "/registry/context/ctx/lockers/l1/get/a,b"},
		{"peer", "", true, "/registry/peers/peer1/10.1.2.3:8080/locker/get/a,b"},
		{"peer in context", "ctx", true, "/registry/peers/peer1/10.1.2.3:8080/locker/get/a,b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRegistry(t)
			l := openLocker(t, fr, tc.context, "l1")
			fr.respond(tc.wantPath, http.StatusOK, "raw bytes\nline 2")
			var b []byte
			var err error
			if tc.peer {
				b, err = l.GetPeerData([]string{"a", "b"})
			} else {
				b, err = l.GetData([]string{"a", "b"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != "raw bytes\nline 2" {
				t.Errorf("data = %q", b)
			}
			if r := fr.last(t); r.method != http.MethodGet || r.path != tc.wantPath {
				t.Errorf("request = %+v", r)
			}
		})
	}
}

func TestGetJSONAndPeerJSON(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	fr.respond("/registry/lockers/l1/get/k", http.StatusOK, `{"a":1,"b":{"c":"d"}}`)
	fr.respond("/registry/peers/peer1/10.1.2.3:8080/locker/get/k", http.StatusOK, `{"peer":true}`)
	j, err := l.GetJSON([]string{"k"})
	if err != nil || !reflect.DeepEqual(j, map[string]any{"a": 1.0, "b": map[string]any{"c": "d"}}) {
		t.Errorf("GetJSON = %v, %v", j, err)
	}
	j, err = l.GetPeerJSON([]string{"k"})
	if err != nil || !reflect.DeepEqual(j, map[string]any{"peer": true}) {
		t.Errorf("GetPeerJSON = %v, %v", j, err)
	}
}

func TestGetJSONOfMissingKeyIsAnError(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	fr.respond("/registry/lockers/l1/get/missing", http.StatusNotFound, "not found")
	fr.respond("/registry/peers/peer1/10.1.2.3:8080/locker/get/missing", http.StatusNotFound, "not found")
	if j, err := l.GetJSON([]string{"missing"}); err == nil || j != nil || !strings.Contains(err.Error(), "status code: 404") {
		t.Errorf("GetJSON = %v, %v, want an error about 404", j, err)
	}
	if j, err := l.GetPeerJSON([]string{"missing"}); err == nil || j != nil {
		t.Errorf("GetPeerJSON = %v, %v, want an error", j, err)
	}
	if b, err := l.GetData([]string{"missing"}); err == nil || b != nil {
		t.Errorf("GetData = %q, %v, want an error", b, err)
	}
	if b, err := l.GetPeerData([]string{"missing"}); err == nil || b != nil {
		t.Errorf("GetPeerData = %q, %v, want an error", b, err)
	}
}

func TestLoadJSONDecodesIntoGivenValue(t *testing.T) {
	type item struct {
		Name  string   `json:"name"`
		Tags  []string `json:"tags"`
		Count int      `json:"count"`
	}
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	fr.respond("/registry/lockers/l1/get/item", http.StatusOK, `{"name":"n","tags":["a","b"],"count":5}`)
	fr.respond("/registry/lockers/l1/get/bad", http.StatusOK, `{"name":`)
	fr.respond("/registry/lockers/l1/get/gone", http.StatusInternalServerError, "")
	got := item{}
	if err := l.LoadJSON([]string{"item"}, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, item{Name: "n", Tags: []string{"a", "b"}, Count: 5}) {
		t.Errorf("loaded = %+v", got)
	}
	if err := l.LoadJSON([]string{"bad"}, &got); err == nil {
		t.Errorf("invalid JSON must be an error")
	}
	if err := l.LoadJSON([]string{"gone"}, &got); err == nil || !strings.Contains(err.Error(), "status code: 500") {
		t.Errorf("error = %v, want one about status code 500", err)
	}
}

func TestGetFromLockerWhileLockerIsDisabled(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	before := len(fr.all())
	global.Flags.UseLocker = false
	if _, err := l.GetData([]string{"a"}); err == nil {
		t.Errorf("GetData must fail")
	}
	if _, err := l.GetJSON([]string{"a"}); err == nil {
		t.Errorf("GetJSON must fail")
	}
	if _, err := l.GetPeerData([]string{"a"}); err == nil {
		t.Errorf("GetPeerData must fail")
	}
	if err := l.LoadJSON([]string{"a"}, &struct{}{}); err == nil {
		t.Errorf("LoadJSON must fail")
	}
	global.Flags.UseLocker = true
	global.Self.RegistryURL = ""
	if _, err := l.GetData([]string{"a"}); err == nil {
		t.Errorf("GetData must fail without a registry")
	}
	if len(fr.all()) != before {
		t.Errorf("the registry was called while the locker was not available")
	}
}

func TestStoredDataCanBeReadBack(t *testing.T) {
	// a registry that keeps what it is given, as the real one does
	var lock sync.Mutex
	store := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/store/"):
			b, _ := io.ReadAll(r.Body)
			store[strings.Split(r.URL.Path, "/store/")[1]] = string(b)
		case strings.Contains(r.URL.Path, "/get/"):
			v, ok := store[strings.Split(r.URL.Path, "/get/")[1]]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			io.WriteString(w, v)
		}
	}))
	t.Cleanup(srv.Close)
	configure(t, srv.URL, true)
	l, err := NewRegistryClient().OpenLocker("", "l1")
	if err != nil {
		t.Fatal(err)
	}
	type cfg struct {
		A string `json:"a"`
		B []int  `json:"b"`
	}
	if err := l.Store([]string{"x", "y"}, cfg{A: "v", B: []int{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	var got cfg
	if err := l.LoadJSON([]string{"x", "y"}, &got); err != nil || !reflect.DeepEqual(got, cfg{A: "v", B: []int{1, 2, 3}}) {
		t.Errorf("LoadJSON = %+v, %v", got, err)
	}
	j, err := l.GetJSON([]string{"x", "y"})
	if err != nil || j["a"] != "v" {
		t.Errorf("GetJSON = %v, %v", j, err)
	}
	if _, err := l.GetJSON([]string{"x", "missing"}); err == nil {
		t.Errorf("a key that wasn't stored must be an error")
	}
}

func TestLockerClientsShareTheRegistryClient(t *testing.T) {
	fr := newFakeRegistry(t)
	configure(t, fr.srv.URL, true)
	rc := NewRegistryClient()
	a, err := rc.OpenLocker("", "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := rc.OpenLocker("ctx", "b")
	if err != nil {
		t.Fatal(err)
	}
	if a.RegistryClient != b.RegistryClient || a.locker == b.locker {
		t.Errorf("lockers don't share the client or share the locker name")
	}
	if err := a.Store([]string{"k"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Store([]string{"k"}, 2); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range fr.all() {
		paths = append(paths, r.path)
	}
	want := []string{"/registry/lockers/a/open", "/registry/context/ctx/lockers/b/open", "/registry/lockers/a/store/k", "/registry/context/ctx/lockers/b/store/k"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestConcurrentStoreAndGet(t *testing.T) {
	fr := newFakeRegistry(t)
	l := openLocker(t, fr, "", "l1")
	const workers, per = 8, 20
	for w := 0; w < workers; w++ {
		fr.respond(fmt.Sprintf("/registry/lockers/l1/get/k%d", w), http.StatusOK, fmt.Sprintf(`{"w":%d}`, w))
	}
	errs := make(chan error, workers*per*2)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := l.Store([]string{fmt.Sprintf("k%d", w)}, map[string]int{"i": i}); err != nil {
					errs <- err
				}
				j, err := l.GetJSON([]string{fmt.Sprintf("k%d", w)})
				if err != nil {
					errs <- err
				} else if j["w"] != float64(w) {
					errs <- fmt.Errorf("worker %d read %v", w, j)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	stores, gets := 0, 0
	for _, r := range fr.all() {
		switch {
		case r.method == http.MethodPost && strings.Contains(r.path, "/store/"):
			stores++
			var m map[string]int
			if err := json.Unmarshal([]byte(r.body), &m); err != nil {
				t.Errorf("stored body %q is not what was sent", r.body)
			}
		case r.method == http.MethodGet:
			gets++
		}
	}
	if stores != workers*per || gets != workers*per {
		t.Errorf("stores = %d, gets = %d, want %d each", stores, gets, workers*per)
	}
}

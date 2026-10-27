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

package k8sApi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	k8sClient "goto/pkg/k8s/client"
	"goto/pkg/k8s/k8stest"
	"goto/pkg/util"
)

// watchServer serves the APIs of k8s on a listener, which the watch tests need as a watch does not end with the call.
func watchServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := router()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, 8080))
		_, r, _ = util.WithRequestStore(r, nil)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type event struct {
	Type   string                 `json:"type"`
	Object map[string]interface{} `json:"object"`
}

func (e event) name() string { return metadataName(e.Object) }

// stream is a watch that is being read: the events that it got, and a way to end it.
type stream struct {
	resp   *http.Response
	events chan event
	cancel context.CancelFunc
}

func openStream(t *testing.T, srv *httptest.Server, target string) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	s := &stream{resp: resp, events: make(chan event, 100), cancel: cancel}
	go func() {
		defer close(s.events)
		dec := json.NewDecoder(resp.Body)
		for {
			var e event
			if err := dec.Decode(&e); err != nil {
				return
			}
			s.events <- e
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return s
}

func (s *stream) next(t *testing.T) event {
	t.Helper()
	select {
	case e, ok := <-s.events:
		if !ok {
			t.Fatal("the watch ended")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event in time")
	}
	return event{}
}

func (s *stream) wantNext(t *testing.T, eventType, name string) event {
	t.Helper()
	e := s.next(t)
	if e.Type != eventType || e.name() != name {
		t.Fatalf("event = %s of %q, want %s of %q", e.Type, e.name(), eventType, name)
	}
	return e
}

// wantNone checks that nothing is sent for a while, which is as long as it takes to be sure that it is not about to be.
func (s *stream) wantNone(t *testing.T) {
	t.Helper()
	select {
	case e, ok := <-s.events:
		if ok {
			t.Fatalf("event = %s of %q, want none", e.Type, e.name())
		}
		t.Fatal("the watch ended")
	case <-time.After(150 * time.Millisecond):
	}
}

func (s *stream) wantEnd(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case _, ok := <-s.events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("the watch did not end within %v", within)
		}
	}
}

func TestWatchStreamsWhatExistsAndWhatChanges(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "w1", map[string]interface{}{"k": "one"}))
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/api-ns/all")
	if s.resp.StatusCode != 200 || !strings.HasPrefix(s.resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("response = %d %q, want 200 JSON", s.resp.StatusCode, s.resp.Header.Get("Content-Type"))
	}
	s.wantNext(t, "ADDED", "w1")
	api.Put(configMap("api-ns", "w2", nil))
	s.wantNext(t, "ADDED", "w2")
	api.Put(configMap("api-ns", "w1", map[string]interface{}{"k": "two"}))
	e := s.wantNext(t, "MODIFIED", "w1")
	if data, _ := e.Object["data"].(map[string]interface{}); data["k"] != "two" {
		t.Errorf("object = %v, want the changed one", e.Object)
	}
	api.Delete("v1", "ConfigMap", "api-ns", "w1")
	s.wantNext(t, "DELETED", "w1")
	// Another namespace is not what is watched.
	api.Put(configMap("other-ns", "w3", nil))
	s.wantNone(t)
}

func TestWatchOfAName(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "w1", nil))
	api.Put(configMap("api-ns", "w2", nil))
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/api-ns/w1")
	s.wantNext(t, "ADDED", "w1")
	api.Put(configMap("api-ns", "w2", map[string]interface{}{"k": "changed"}))
	s.wantNone(t)
	api.Put(configMap("api-ns", "w1", map[string]interface{}{"k": "changed"}))
	s.wantNext(t, "MODIFIED", "w1")
	if reqs := api.WatchRequests(); len(reqs) != 1 || !strings.Contains(reqs[0].RawQuery, "fieldSelector=metadata.name%3Dw1") {
		t.Errorf("watch requests = %+v, want one for the name", reqs)
	}
}

func TestWatchOfAllNamespaces(t *testing.T) {
	api := setup(t)
	api.Put(configMap("ns-a", "a", nil))
	api.Put(configMap("ns-b", "b", nil))
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps")
	got := map[string]bool{s.next(t).name(): true, s.next(t).name(): true}
	if !got["a"] || !got["b"] {
		t.Errorf("watched = %v, want both namespaces", got)
	}
	if reqs := api.WatchRequests(); len(reqs) != 1 || reqs[0].Path != "/api/v1/configmaps" {
		t.Errorf("watch requests = %+v, want one of the config maps of all namespaces", reqs)
	}
}

func TestWatchOfAResourceThatIsNotInANamespace(t *testing.T) {
	api := setup(t)
	api.Put(map[string]interface{}{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": "ns-w"}})
	s := openStream(t, watchServer(t), "/k8s/watch/namespaces/ns-w")
	s.wantNext(t, "ADDED", "ns-w")
	s = openStream(t, watchServer(t), "/k8s/watch/ns/ns-w")
	s.wantNext(t, "ADDED", "ns-w")
}

func TestWatchWithLabelSelector(t *testing.T) {
	api := setup(t)
	labeled := configMap("api-ns", "mine", nil)
	labeled["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"app": "x"}
	api.Put(labeled)
	api.Put(configMap("api-ns", "other", nil))
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/api-ns/all?labelSelector=app%3Dx")
	s.wantNext(t, "ADDED", "mine")
	s.wantNone(t)
}

func TestWatchEndsWithItsTimeout(t *testing.T) {
	setup(t)
	srv := watchServer(t)
	for _, timeout := range []string{"1s", "1"} {
		start := time.Now()
		s := openStream(t, srv, "/k8s/watch/configmaps/api-ns/all?timeout="+timeout)
		s.wantEnd(t, 5*time.Second)
		if time.Since(start) < 500*time.Millisecond {
			t.Errorf("timeout=%s: the watch ended after %v, want it to last the timeout", timeout, time.Since(start))
		}
	}
}

func TestWatchWithBadTimeoutIsRefused(t *testing.T) {
	setup(t)
	rr := call(t, "GET", "/k8s/watch/configmaps/api-ns/all?timeout=soon")
	wantStatus(t, rr, 400)
	if !strings.Contains(rr.Body.String(), "timeout") {
		t.Errorf("body = %q, want it to say what is wrong", rr.Body.String())
	}
}

func TestWatchOfAKindThatIsNotKnownIsRefused(t *testing.T) {
	setup(t)
	wantStatus(t, call(t, "GET", "/k8s/watch/nothings/api-ns/all"), 400)
	wantStatus(t, call(t, "GET", "/k8s/watch/nothings"), 400)
}

func TestWatchTheServerFailsIsReported(t *testing.T) {
	api := setup(t)
	api.FailResources(500)
	rr := call(t, "GET", "/k8s/watch/configmaps/api-ns/all")
	wantStatus(t, rr, 500)
}

func TestWatchOnlyAcceptsGET(t *testing.T) {
	setup(t)
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if rr := call(t, method, "/k8s/watch/configmaps/api-ns/all"); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want the watch to be for GET only", method, rr.Code)
		}
	}
}

func TestWatchEndsWhenTheClientGoesAway(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "w1", nil))
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/api-ns/all")
	s.wantNext(t, "ADDED", "w1")
	if api.Watchers() != 1 {
		t.Fatalf("watches on the server = %d, want 1", api.Watchers())
	}
	s.cancel()
	deadline := time.Now().Add(5 * time.Second)
	for api.Watchers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the watch of the server is still open after the client went away")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatchLastsLongerThanTheTimeoutOfTheClient(t *testing.T) {
	api := setup(t)
	api.Put(configMap("api-ns", "w1", nil))
	c, err := k8sClient.NewK8sClientForConfig(api.URL(), "")
	if err != nil {
		t.Fatal(err)
	}
	// The clients that the config API makes have a timeout of 5 seconds, which is cut short here.
	c.Config.Timeout = 200 * time.Millisecond
	c, err = k8sClient.NewK8sClientForRestConfig(c.Config)
	if err != nil {
		t.Fatal(err)
	}
	k8sClient.ReplaceCurrentClient(c)
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/api-ns/all")
	s.wantNext(t, "ADDED", "w1")
	time.Sleep(700 * time.Millisecond)
	api.Put(configMap("api-ns", "w1", map[string]interface{}{"k": "late"}))
	s.wantNext(t, "MODIFIED", "w1")
}

// Switching the context was a no-op for the reads: it changed a pointer that nothing used.
func TestContextSwitchMakesReadsAndWatchesGoToTheOtherCluster(t *testing.T) {
	first := setup(t)
	first.Put(configMap("api-ns", "which", map[string]interface{}{"cluster": "first"}))
	second := k8stest.New(t)
	second.Put(configMap("api-ns", "which", map[string]interface{}{"cluster": "second"}))
	c, err := k8sClient.NewK8sClientForConfig(second.URL(), "")
	if err != nil {
		t.Fatal(err)
	}
	k8sClient.Clients["second-cluster"] = c

	read := func() string {
		rr := call(t, "GET", "/k8s/resources/configmaps/api-ns/which", "Accept", "application/json")
		wantStatus(t, rr, 200)
		data, _ := decode(t, rr)["data"].(map[string]interface{})
		cluster, _ := data["cluster"].(string)
		return cluster
	}
	if got := read(); got != "first" {
		t.Fatalf("read before the switch = %q, want first", got)
	}
	wantStatus(t, call(t, "POST", "/k8s/context/second-cluster"), 200)
	if got := read(); got != "second" {
		t.Errorf("read after the switch = %q, want second", got)
	}
	s := openStream(t, watchServer(t), "/k8s/watch/configmaps/api-ns/which")
	e := s.wantNext(t, "ADDED", "which")
	if data, _ := e.Object["data"].(map[string]interface{}); data["cluster"] != "second" {
		t.Errorf("watched object = %v, want the one of the second cluster", e.Object)
	}
	if len(second.WatchRequests()) != 1 || len(first.WatchRequests()) != 0 {
		t.Errorf("watch requests: second = %d, first = %d, want the watch to be of the second only", len(second.WatchRequests()), len(first.WatchRequests()))
	}
}

func TestConfigClusterWithTheSchemeInTheURL(t *testing.T) {
	setup(t)
	rr := call(t, "POST", "/k8s/config/https-cluster/https://127.0.0.1:6443/"+caData(t))
	wantStatus(t, rr, 200)
	c := k8sClient.GetClient("https-cluster")
	if c == nil || c.Config.Host != "https://127.0.0.1:6443" {
		t.Errorf("client = %+v, want one for the url with its scheme", c)
	}
	// The form without a scheme is still taken.
	wantStatus(t, call(t, "POST", "/k8s/config/plain-cluster/127.0.0.1:6443/"+caData(t)), 200)
	if c := k8sClient.GetClient("plain-cluster"); c == nil || c.Config.Host != "127.0.0.1:6443" {
		t.Errorf("client = %+v, want one for the host and port", c)
	}
}

func TestConfigClusterWithCAThatIsNotBase64IsRefused(t *testing.T) {
	setup(t)
	wantStatus(t, call(t, "POST", "/k8s/config/bad-ca/127.0.0.1:6443/not*base64"), 400)
	if k8sClient.GetClient("bad-ca") != nil {
		t.Error("a client was made for CA data that is not base 64")
	}
}

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

package peer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/client/target"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/job"
	"goto/pkg/registry"
	"goto/pkg/server/probes"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	seq      atomic.Int32
	stubOnce sync.Once
)

// stubGlobalFuncs is for what the requests of the admin API ask the server of, and there is no server here.
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

type recorded struct {
	method      string
	path        string
	contentType string
	body        string
}

// fakeRegistry answers the registration of a peer with the statuses and the body that are set, and records everything
// that it is asked.
type fakeRegistry struct {
	srv         *httptest.Server
	lock        sync.Mutex
	requests    []recorded
	addStatuses []int // the status for the first registration, the second one and so on; the last one is repeated
	addBody     string
	addCalls    int
}

func newFakeRegistry(t *testing.T, addBody string, addStatuses ...int) *fakeRegistry {
	t.Helper()
	fr := &fakeRegistry{addBody: addBody, addStatuses: addStatuses}
	fr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fr.lock.Lock()
		fr.requests = append(fr.requests, recorded{method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: string(b)})
		status, body := http.StatusOK, ""
		if r.URL.Path == "/registry/peers/add" {
			if len(fr.addStatuses) > 0 {
				status = fr.addStatuses[min(fr.addCalls, len(fr.addStatuses)-1)]
			}
			fr.addCalls++
			body = fr.addBody
		}
		fr.lock.Unlock()
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *fakeRegistry) count(path string) int {
	fr.lock.Lock()
	defer fr.lock.Unlock()
	n := 0
	for _, r := range fr.requests {
		if r.path == path {
			n++
		}
	}
	return n
}

func (fr *fakeRegistry) find(path string) []recorded {
	fr.lock.Lock()
	defer fr.lock.Unlock()
	var found []recorded
	for _, r := range fr.requests {
		if r.path == path {
			found = append(found, r)
		}
	}
	return found
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitBackground waits until the startup tasks and the reminders have ended, and tells whether they did.
func waitBackground() bool {
	done := make(chan struct{})
	go func() {
		background.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(10 * time.Second):
		return false
	}
}

// configure makes the peer a peer of the registry, with short pauses for retries and reminders. Whatever the test left
// running is stopped before the configuration is restored.
func configure(t *testing.T, registryURL string) (name, address string) {
	t.Helper()
	n := seq.Add(1)
	prevSelf, prevDelay, prevInterval := global.Self, registerRetryDelay, reminderInterval
	global.Self.RegistryURL = registryURL
	global.Self.Name = fmt.Sprintf("peer%d", n)
	global.Self.Address = fmt.Sprintf("10.9.8.%d:8080", n)
	global.Self.PodName = "pod-1"
	global.Self.Namespace = "ns-1"
	global.Self.NodeName = "node-1"
	global.Self.Cluster = "cluster-1"
	global.Self.ServerPort = 41000 + int(n)
	registerRetryDelay = time.Millisecond
	reminderInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		global.Self, registerRetryDelay, reminderInterval = prevSelf, prevDelay, prevInterval
	})
	t.Cleanup(func() {
		stopReminders()
		if !waitBackground() {
			t.Errorf("the reminders or the startup tasks didn't end")
		}
	})
	return global.Self.Name, global.Self.Address
}

func api(t *testing.T) *mux.Router {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	r := mux.NewRouter()
	target.SetRoutes(r.PathPrefix("/client").Subrouter())
	return r
}

func callAPI(t *testing.T, r *mux.Router, method, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s = %d: %s", method, path, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// compact is the text without the white space, which is what the JSON of the API is indented with.
func compact(s string) string { return strings.Join(strings.Fields(s), "") }

func targetSpec(name string) *invocation.InvocationSpec {
	return &invocation.InvocationSpec{Name: name, Method: "GET", URL: "http://127.0.0.1:1/" + name, Replicas: 1, RequestCount: 1}
}

func startupData(t *testing.T, data *registry.PeerData) string {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRegisterPeerPostsPeerDetailsAndSendsReminders(t *testing.T) {
	fr := newFakeRegistry(t, startupData(t, &registry.PeerData{}), http.StatusOK)
	name, address := configure(t, fr.srv.URL)
	RegisterPeer(name, address)

	adds := fr.find("/registry/peers/add")
	if len(adds) != 1 {
		t.Fatalf("registrations = %d, want 1", len(adds))
	}
	if adds[0].method != http.MethodPost || adds[0].contentType != "application/json" {
		t.Errorf("registration = %+v", adds[0])
	}
	var sent registry.Peer
	if err := json.Unmarshal([]byte(adds[0].body), &sent); err != nil {
		t.Fatalf("registration body %q: %v", adds[0].body, err)
	}
	want := registry.Peer{Name: name, Address: address, Pod: "pod-1", Namespace: "ns-1", Node: "node-1", Cluster: "cluster-1"}
	if sent != want {
		t.Errorf("peer = %+v, want %+v", sent, want)
	}

	rememberPath := "/registry/peers/" + name + "/remember"
	waitFor(t, "reminders", func() bool { return fr.count(rememberPath) >= 3 })
	for _, r := range fr.find(rememberPath) {
		var reminded registry.Peer
		if r.method != http.MethodPost || r.contentType != "application/json" || json.Unmarshal([]byte(r.body), &reminded) != nil || reminded != want {
			t.Errorf("reminder = %+v", r)
		}
	}

	DeregisterPeer(name, address)
	if !waitBackground() {
		t.Fatalf("the reminders didn't stop")
	}
	removes := fr.find("/registry/peers/" + name + "/remove/" + address)
	if len(removes) != 1 || removes[0].method != http.MethodPost {
		t.Errorf("deregistrations = %+v", removes)
	}
	after := fr.count(rememberPath)
	time.Sleep(50 * time.Millisecond)
	if fr.count(rememberPath) != after {
		t.Errorf("reminders were sent after the peer was deregistered")
	}
}

func TestRegisterPeerAcceptsAcceptedStatus(t *testing.T) {
	fr := newFakeRegistry(t, startupData(t, &registry.PeerData{}), http.StatusAccepted)
	name, address := configure(t, fr.srv.URL)
	RegisterPeer(name, address)
	if n := fr.count("/registry/peers/add"); n != 1 {
		t.Errorf("registrations = %d, want 1", n)
	}
	waitFor(t, "a reminder", func() bool { return fr.count("/registry/peers/"+name+"/remember") >= 1 })
}

func TestRegisterPeerWithoutRegistryDoesNothing(t *testing.T) {
	fr := newFakeRegistry(t, "{}", http.StatusOK)
	name, address := configure(t, "")
	RegisterPeer(name, address)
	DeregisterPeer(name, address)
	if n := len(fr.find("/registry/peers/add")) + len(fr.find("/registry/peers/"+name+"/remove/"+address)); n != 0 {
		t.Errorf("the registry was called %d times without a registry URL", n)
	}
}

func TestRegisterPeerRetriesUntilRegistryAccepts(t *testing.T) {
	fr := newFakeRegistry(t, startupData(t, &registry.PeerData{}), http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusOK)
	name, address := configure(t, fr.srv.URL)
	RegisterPeer(name, address)
	if n := fr.count("/registry/peers/add"); n != 3 {
		t.Errorf("registration attempts = %d, want 3", n)
	}
	waitFor(t, "reminders after the registration succeeded", func() bool { return fr.count("/registry/peers/"+name+"/remember") >= 1 })
}

func TestRegisterPeerGivesUpAfterSixAttempts(t *testing.T) {
	for name, status := range map[string]int{"server error": http.StatusInternalServerError, "bad request": http.StatusBadRequest} {
		t.Run(name, func(t *testing.T) {
			fr := newFakeRegistry(t, "failed", status)
			peerName, address := configure(t, fr.srv.URL)
			RegisterPeer(peerName, address)
			if n := fr.count("/registry/peers/add"); n != 6 {
				t.Errorf("registration attempts = %d, want 6", n)
			}
			time.Sleep(40 * time.Millisecond)
			if n := fr.count("/registry/peers/" + peerName + "/remember"); n != 0 {
				t.Errorf("%d reminders were sent by a peer that was not registered", n)
			}
		})
	}
}

func TestRegisterPeerWithUnreachableRegistry(t *testing.T) {
	fr := newFakeRegistry(t, "{}", http.StatusOK)
	url := fr.srv.URL
	fr.srv.Close()
	name, address := configure(t, url)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RegisterPeer(name, address)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("RegisterPeer didn't give up")
	}
	if n := fr.count("/registry/peers/add"); n != 0 {
		t.Errorf("a closed registry got %d registrations", n)
	}
	// deregistering from a registry that can't be reached is not a failure either
	DeregisterPeer(name, address)
}

func TestRegisterPeerAppliesStartupDataFromRegistry(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	n := seq.Load() + 1
	jobName := fmt.Sprintf("peerjob%d", n)
	scriptName := fmt.Sprintf("peerscript%d", n)
	targetName := fmt.Sprintf("peertarget%d", n)
	j, err := job.ParseJobFromPayload(fmt.Sprintf(`{"name":%q,"task":{"cmd":"echo","args":["from-peer-job"]}}`, jobName))
	if err != nil {
		t.Fatal(err)
	}
	data := &registry.PeerData{
		Targets:             registry.PeerTargets{targetName: {InvocationSpec: targetSpec(targetName)}},
		Jobs:                registry.PeerJobs{jobName: {Job: j}},
		JobScripts:          registry.PeerJobScripts{scriptName + ".sh": []byte("#!/bin/sh\necho scripted\n")},
		Files:               registry.PeerFiles{"data.txt": []byte("file content")},
		TrackingHeaders:     "x-peer-h1,x-peer-h2",
		TrackingTimeBuckets: "0-100,101-500",
		Probes: &registry.PeerProbes{
			ReadinessProbe: "/peer-ready", ReadinessStatus: 503,
			LivenessProbe: "/peer-live", LivenessStatus: 500,
		},
	}
	fr := newFakeRegistry(t, startupData(t, data), http.StatusOK)
	name, address := configure(t, fr.srv.URL)
	RegisterPeer(name, address)
	// the targets are added last, so everything else was done when they are there
	waitFor(t, "the target from the registry", func() bool { return target.Client.PrepareTarget(targetName) != nil })

	spec := target.Client.PrepareTarget(targetName)
	if spec.URL != "http://127.0.0.1:1/"+targetName || spec.Method != "GET" {
		t.Errorf("target = %+v", spec)
	}
	routes := api(t)
	if got := callAPI(t, routes, "GET", "/client/track/headers"); !strings.Contains(got, "x-peer-h1") || !strings.Contains(got, "x-peer-h2") {
		t.Errorf("tracking headers = %q", got)
	}
	if got := callAPI(t, routes, "GET", "/client/track/time"); compact(got) != "[[0,100],[101,500]]" {
		t.Errorf("tracking time buckets = %q", got)
	}
	pp := probes.GetPortProbes(strconv.Itoa(global.Self.ServerPort))
	if pp.ReadinessProbe != "/peer-ready" || pp.ReadinessStatus != 503 || pp.LivenessProbe != "/peer-live" || pp.LivenessStatus != 500 {
		t.Errorf("probes = %+v", pp)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "data.txt")); err != nil || string(b) != "file content" {
		t.Errorf("file = %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, scriptName+".sh")); err != nil || !strings.Contains(string(b), "scripted") {
		t.Errorf("job script = %q, %v", b, err)
	}
	// the job that the registry gave can be run, and so can the one that was made of the script
	if !jobOutputs(jobName, "from-peer-job") {
		t.Errorf("job %s didn't run with the output that its command has", jobName)
	}
	if !job.RunJobWithInputAndWait(scriptName, nil, nil, 10*time.Second) {
		t.Errorf("the job of script %s was not added", scriptName)
	}
}

// jobOutputs runs the job and tells whether it gave the output.
func jobOutputs(jobName, output string) bool {
	if !job.RunJobWithInputAndWait(jobName, nil, nil, 10*time.Second) {
		return false
	}
	for _, r := range job.GetLatestJobResults(jobName) {
		if s, ok := r.Data.(string); ok && strings.Contains(s, output) {
			return true
		}
	}
	return false
}

func TestSetupStartupTasksWithOnlySomeData(t *testing.T) {
	configure(t, "")
	routes := api(t)
	callAPI(t, routes, "POST", "/client/track/headers/clear")
	callAPI(t, routes, "POST", "/client/track/time/clear")
	port := strconv.Itoa(global.Self.ServerPort)

	setupStartupTasks(&registry.PeerData{})
	if got := callAPI(t, routes, "GET", "/client/track/headers"); got != "Tracking headers: \n" {
		t.Errorf("tracking headers = %q", got)
	}
	pp := probes.GetPortProbes(port)
	if pp.ReadinessProbe != "/ready" || pp.LivenessProbe != "/live" {
		t.Errorf("default probes were changed: %+v", pp)
	}

	// the probe of one kind doesn't touch the other
	setupStartupTasks(&registry.PeerData{Probes: &registry.PeerProbes{ReadinessProbe: "/r", ReadinessStatus: 404}})
	if pp.ReadinessProbe != "/r" || pp.ReadinessStatus != 404 || pp.LivenessProbe != "/live" || pp.LivenessStatus != 200 {
		t.Errorf("probes = %+v", pp)
	}
	setupStartupTasks(&registry.PeerData{Probes: &registry.PeerProbes{LivenessProbe: "/l", LivenessStatus: 418}})
	if pp.ReadinessProbe != "/r" || pp.LivenessProbe != "/l" || pp.LivenessStatus != 418 {
		t.Errorf("probes = %+v", pp)
	}
	setupStartupTasks(&registry.PeerData{Probes: &registry.PeerProbes{}})
	if pp.ReadinessProbe != "/r" || pp.LivenessProbe != "/l" {
		t.Errorf("empty probes changed the probes: %+v", pp)
	}

	// time buckets that can't be used are ignored
	setupStartupTasks(&registry.PeerData{TrackingTimeBuckets: "10-20"})
	setupStartupTasks(&registry.PeerData{TrackingTimeBuckets: "not-buckets"})
	if got := callAPI(t, routes, "GET", "/client/track/time"); compact(got) != "[[10,20]]" {
		t.Errorf("tracking time buckets = %q", got)
	}
	setupStartupTasks(&registry.PeerData{TrackingHeaders: "x-only"})
	if got := callAPI(t, routes, "GET", "/client/track/headers"); !strings.Contains(got, "x-only") {
		t.Errorf("tracking headers = %q", got)
	}
}

func TestSetupStartupTasksIgnoresInvalidTargets(t *testing.T) {
	configure(t, "")
	n := seq.Load()
	good := fmt.Sprintf("goodtarget%d", n)
	bad := fmt.Sprintf("badtarget%d", n)
	badSpec := targetSpec(bad)
	badSpec.Replicas = -1
	setupStartupTasks(&registry.PeerData{Targets: registry.PeerTargets{
		bad:  {InvocationSpec: badSpec},
		good: {InvocationSpec: targetSpec(good)},
	}})
	if target.Client.PrepareTarget(good) == nil {
		t.Errorf("the valid target was not added")
	}
	if target.Client.PrepareTarget(bad) != nil {
		t.Errorf("the invalid target was added")
	}
}

func TestSetupStartupTasksConcurrently(t *testing.T) {
	configure(t, "")
	n := seq.Load()
	const workers, per = 8, 10
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			targets := registry.PeerTargets{}
			for i := 0; i < per; i++ {
				name := fmt.Sprintf("conc%d-%d-%d", n, w, i)
				targets[name] = &registry.PeerTarget{InvocationSpec: targetSpec(name)}
			}
			setupStartupTasks(&registry.PeerData{Targets: targets, TrackingHeaders: fmt.Sprintf("x-h%d", w)})
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		for i := 0; i < per; i++ {
			name := fmt.Sprintf("conc%d-%d-%d", n, w, i)
			if target.Client.PrepareTarget(name) == nil {
				t.Errorf("target %s is missing", name)
			}
		}
	}
}

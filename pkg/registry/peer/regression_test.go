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
	"net/http"
	"testing"
	"time"

	"goto/pkg/client/target"
	"goto/pkg/job"
	"goto/pkg/registry"
)

func jsonRoundTrip(from, to interface{}) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// The jobs that the registry sends have the tasks as JSON only, and a job that wasn't parsed has nothing to run.
func TestRegressionJobsFromRegistryCanRun(t *testing.T) {
	configure(t, "")
	jobName := fmt.Sprintf("regjob%d", seq.Load())
	j, err := job.ParseJobFromPayload(fmt.Sprintf(`{"name":%q,"task":{"cmd":"echo","args":["from-registry"]}}`, jobName))
	if err != nil {
		t.Fatal(err)
	}
	// the registry sends the data as JSON
	var data registry.PeerData
	if err := jsonRoundTrip(&registry.PeerData{Jobs: registry.PeerJobs{jobName: {Job: j}}}, &data); err != nil {
		t.Fatal(err)
	}
	setupStartupTasks(&data)
	if !jobOutputs(jobName, "from-registry") {
		t.Errorf("job %s didn't run as the registry meant it to", jobName)
	}
}

// Deregistering when the reminders were not running left a stop signal for the one that ran next, and a second
// deregistration waited forever to leave another.
func TestRegressionDeregisterDoesNotWaitForReminders(t *testing.T) {
	fr := newFakeRegistry(t, "{}", http.StatusOK)
	name, address := configure(t, fr.srv.URL)
	done := make(chan struct{})
	go func() {
		defer close(done)
		DeregisterPeer(name, address)
		DeregisterPeer(name, address)
		DeregisterPeer(name, address)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DeregisterPeer is waiting for a reminder to take the stop signal")
	}
	if n := len(fr.find("/registry/peers/" + name + "/remove/" + address)); n != 3 {
		t.Errorf("deregistrations = %d, want 3", n)
	}
}

func TestRegressionRemindersStartAfterDeregistrationOfUnregisteredPeer(t *testing.T) {
	fr := newFakeRegistry(t, "{}", http.StatusOK)
	name, address := configure(t, fr.srv.URL)
	DeregisterPeer(name, address)
	RegisterPeer(name, address)
	waitFor(t, "reminders of the peer that registered after it had been deregistered", func() bool {
		return fr.count("/registry/peers/"+name+"/remember") >= 2
	})
}

// A registry that accepted the peer but gave nothing to read had no reminders sent to it.
func TestRegressionRemindersWithoutStartupData(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "not JSON": "welcome"} {
		t.Run(name, func(t *testing.T) {
			fr := newFakeRegistry(t, body, http.StatusOK)
			peerName, address := configure(t, fr.srv.URL)
			RegisterPeer(peerName, address)
			waitFor(t, "reminders", func() bool { return fr.count("/registry/peers/"+peerName+"/remember") >= 2 })
		})
	}
}

// Registering again must not leave the reminders of the first registration running, as only one set was stopped.
func TestRegressionRegisteringAgainReplacesReminders(t *testing.T) {
	fr := newFakeRegistry(t, "{}", http.StatusOK)
	name, address := configure(t, fr.srv.URL)
	RegisterPeer(name, address)
	RegisterPeer(name, address)
	waitFor(t, "reminders", func() bool { return fr.count("/registry/peers/"+name+"/remember") >= 2 })
	DeregisterPeer(name, address)
	if !waitBackground() {
		t.Fatal("a reminder is still running after the peer was deregistered")
	}
}

// An entry without a target or a job in the data of the registry is skipped.
func TestRegressionEntriesWithoutDetailsAreIgnored(t *testing.T) {
	configure(t, "")
	good := fmt.Sprintf("regtarget%d", seq.Load())
	data := &registry.PeerData{
		Targets: registry.PeerTargets{"nil": nil, "empty": &registry.PeerTarget{}, good: {InvocationSpec: targetSpec(good)}},
		Jobs:    registry.PeerJobs{"nil": nil, "empty": &registry.PeerJob{}},
	}
	mustNotPanic(t, "setting up what the registry gave", func() { setupStartupTasks(data) })
	if target.Client.PrepareTarget(good) == nil {
		t.Errorf("the target next to the ones without details was not added")
	}
}

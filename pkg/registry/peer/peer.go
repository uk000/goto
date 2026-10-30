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
	"goto/pkg/client/target"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/job"
	"goto/pkg/registry"
	"goto/pkg/server/probes"
	"goto/pkg/util"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxRegisterAttempts = 6

var (
	// reminderStop is closed to stop the reminders of the registration that is current
	reminderStop chan struct{}
	reminderLock sync.Mutex
	// registerRetryDelay is the wait between attempts to register with the registry, and reminderInterval is the
	// period of the reminders that are sent to the registry once the peer is registered.
	registerRetryDelay = 10 * time.Second
	reminderInterval   = 5 * time.Second
	// background is for waiting until the startup tasks and the reminders, which run on their own, have ended
	background sync.WaitGroup
)

func RegisterPeer(peerName, peerAddress string) {
	peer := &registry.Peer{
		Name:      peerName,
		Address:   peerAddress,
		Pod:       global.Self.PodName,
		Namespace: global.Self.Namespace,
		Node:      global.Self.NodeName,
		Cluster:   global.Self.Cluster,
	}
	if global.Self.RegistryURL == "" {
		return
	}
	for attempt := 1; attempt <= maxRegisterAttempts; attempt++ {
		if registerWithRegistry(peerName, peer) {
			return
		}
		if attempt < maxRegisterAttempts {
			log.Printf("Will retry registering with registry, retries: %d\n", attempt)
			time.Sleep(registerRetryDelay)
		} else {
			log.Printf("Failed to register as peer to registry after %d retries. Giving up.\n", attempt)
		}
	}
}

// registerWithRegistry makes one attempt to register, and tells whether the registry accepted the peer.
func registerWithRegistry(peerName string, peer *registry.Peer) bool {
	resp, err := http.Post(global.Self.RegistryURL+"/registry/peers/add", constants.ContentTypeJSON,
		strings.NewReader(util.ToJSONText(peer)))
	if err != nil {
		log.Printf("Failed to register as peer to registry due to error: %s\n", err.Error())
		return false
	}
	defer util.CloseResponse(resp)
	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		log.Printf("Failed to register as peer to registry due to response code %d\n", resp.StatusCode)
		return false
	}
	log.Printf("Registered as peer [%s] with registry [%s]\n", global.Self.Name, global.Self.RegistryURL)
	data := &registry.PeerData{}
	if err := util.ReadJsonPayloadFromBody(resp.Body, data); err == nil {
		log.Println("Read startup data from registry")
		background.Add(1)
		go func() {
			defer background.Done()
			setupStartupTasks(data)
		}()
	} else {
		log.Printf("Failed to read peer targets with error: %s\n", err.Error())
	}
	// the registry has accepted the peer, so it needs to be reminded whether or not it gave any startup data
	startReminders(peer)
	return true
}

func DeregisterPeer(peerName, peerAddress string) {
	if global.Self.RegistryURL != "" {
		stopReminders()
		url := global.Self.RegistryURL + "/registry/peers/" + peerName + "/remove/" + peerAddress
		if resp, err := http.Post(url, "plain/text", nil); err == nil {
			util.CloseResponse(resp)
		} else {
			log.Printf("Failed to deregister from registry as peer %s address %s, error: %s\n", peerName, peerAddress, err.Error())
		}
	}
}

// startReminders starts the reminders to the registry, in place of the ones that were started by an earlier registration.
func startReminders(peer *registry.Peer) {
	stop := make(chan struct{})
	reminderLock.Lock()
	if reminderStop != nil {
		close(reminderStop)
	}
	reminderStop = stop
	reminderLock.Unlock()
	background.Add(1)
	go func() {
		defer background.Done()
		startRegistryReminder(peer, stop)
	}()
}

// stopReminders stops the reminders, if there are any. It never waits for anyone, and doesn't leave anything behind
// for the reminders that are started later.
func stopReminders() {
	reminderLock.Lock()
	if reminderStop != nil {
		close(reminderStop)
		reminderStop = nil
	}
	reminderLock.Unlock()
}

func startRegistryReminder(peer *registry.Peer, stop <-chan struct{}) {
	ticker := time.NewTicker(reminderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			url := global.Self.RegistryURL + "/registry/peers/" + peer.Name + "/remember"
			if resp, err := http.Post(url, constants.ContentTypeJSON, strings.NewReader(util.ToJSONText(peer))); err == nil {
				util.CloseResponse(resp)
				if global.Flags.EnableRegistryReminderLogs {
					log.Printf("Sent reminder to registry at [%s] as peer %s address %s\n", global.Self.RegistryURL, peer.Name, peer.Address)
				}
			} else {
				log.Printf("Failed to remind registry as peer %s address %s, error: %s\n", peer.Name, peer.Address, err.Error())
			}
		}
	}
}

func setupStartupTasks(peerData *registry.PeerData) {
	log.Printf("Got %d targets, %d jobs, %d job scripts\n", len(peerData.Targets), len(peerData.Jobs), len(peerData.JobScripts))

	if peerData.TrackingHeaders != "" {
		log.Printf("Got %s tracking headers\n", peerData.TrackingHeaders)
		target.Client.AddTrackingHeaders(peerData.TrackingHeaders)
	}

	if peerData.TrackingTimeBuckets != "" {
		log.Printf("Got %s tracking time buckets\n", peerData.TrackingTimeBuckets)
		target.Client.AddTrackingTimeBuckets(peerData.TrackingTimeBuckets)
	}

	if peerData.Probes != nil {
		probeStatus := probes.GetPortProbes(strconv.Itoa(global.Self.ServerPort))
		if peerData.Probes.ReadinessProbe != "" {
			log.Printf("Got Readiness probe %s, status: %d\n", peerData.Probes.ReadinessProbe, peerData.Probes.ReadinessStatus)
			probeStatus.ReadinessProbe = peerData.Probes.ReadinessProbe
			probeStatus.ReadinessStatus = peerData.Probes.ReadinessStatus
		}

		if peerData.Probes.LivenessProbe != "" {
			log.Printf("Got Liveness probe: %s, status: %d\n", peerData.Probes.LivenessProbe, peerData.Probes.LivenessStatus)
			probeStatus.LivenessProbe = peerData.Probes.LivenessProbe
			probeStatus.LivenessStatus = peerData.Probes.LivenessStatus
		}
	}

	for fileName, content := range peerData.Files {
		log.Printf("File: %s\n", fileName)
		job.Manager.StoreJobScriptOrFile("", fileName, content, false)
	}

	for fileName, content := range peerData.JobScripts {
		log.Printf("Job Script: %s\n", fileName)
		job.Manager.StoreJobScriptOrFile("", fileName, content, true)
	}

	for name, j := range peerData.Jobs {
		log.Printf("%+v\n", j)
		if j == nil || j.Job == nil {
			log.Printf("Ignoring job [%s] that has no details\n", name)
			continue
		}
		// The task of the job is not a part of its JSON, but is made from it when the job is parsed, which a job that
		// came from the registry as JSON has not been.
		parsed, err := job.ParseJobFromPayload(util.ToJSONText(j.Job))
		if err != nil {
			log.Printf("Ignoring invalid job [%s]: %s\n", name, err.Error())
			continue
		}
		job.Manager.AddJob(parsed)
	}

	for name, t := range peerData.Targets {
		log.Printf("%+v\n", t)
		if t == nil || t.InvocationSpec == nil {
			log.Printf("Ignoring target [%s] that has no details\n", name)
			continue
		}
		if err := target.Client.AddTarget(&target.Target{InvocationSpec: t.InvocationSpec}); err != nil {
			log.Printf("Ignoring invalid target [%s]: %s\n", name, err.Error())
		}
	}
}

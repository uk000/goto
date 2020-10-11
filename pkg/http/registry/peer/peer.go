package peer

import (
	"goto/pkg/global"
	"goto/pkg/http/client/target"
	"goto/pkg/http/registry"
	"goto/pkg/http/server/probe"
	"goto/pkg/job"
	"goto/pkg/util"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
  chanStopReminder chan bool = make(chan bool, 1)
)

func RegisterPeer(peerName, peerAddress string) {
  peer := &registry.Peer{
    Name:      peerName,
    Address:   peerAddress,
    Pod:       util.GetPodName(),
    Namespace: util.GetNamespace(),
  }
  if global.RegistryURL != "" {
    registered := false
    retries := 0
    for !registered && retries < 3 {
      if resp, err := http.Post(global.RegistryURL+"/registry/peers/add", "application/json",
        strings.NewReader(util.ToJSON(peer))); err == nil {
        defer resp.Body.Close()
        log.Printf("Registered as peer [%s] with registry [%s]\n", global.PeerName, global.RegistryURL)
        registered = true
        data := &registry.PeerData{}
        if err := util.ReadJsonPayloadFromBody(resp.Body, data); err == nil {
          log.Printf("Read startup data from registry: %+v\n", *data)
          go setupStartupTasks(data)
        } else {
          log.Printf("Failed to read peer targets with error: %s\n", err.Error())
        }
      } else {
        retries++
        log.Printf("Failed to register as peer to registry, retries: %d, error: %s\n", retries, err.Error())
        if retries < 3 {
          time.Sleep(10 * time.Second)
        }
      }
    }
    if registered {
      go startRegistryReminder(peer)
    } else {
      log.Printf("Failed to register as peer to registry after %d retries\n", retries)
    }
  }
}

func DeregisterPeer(peerName, peerAddress string) {
  if global.RegistryURL != "" {
    chanStopReminder <- true
    url := global.RegistryURL + "/registry/peers/" + peerName + "/remove/" + peerAddress
    if resp, err := http.Post(url, "plain/text", nil); err == nil {
      util.CloseResponse(resp)
    } else {
      log.Printf("Failed to deregister from registry as peer %s address %s, error: %s\n", peerName, peerAddress, err.Error())
    }
  }
}

func startRegistryReminder(peer *registry.Peer) {
  for {
    select {
    case <-chanStopReminder:
      return
    case <-time.Tick(5 * time.Second):
      url := global.RegistryURL + "/registry/peers/" + peer.Name + "/remember"
      if resp, err := http.Post(url, "application/json", strings.NewReader(util.ToJSON(peer))); err == nil {
        util.CloseResponse(resp)
      } else {
        log.Printf("Failed to remind registry as peer %s address %s, error: %s\n", peer.Name, peer.Address, err.Error())
      }
    }
  }
}

func setupStartupTasks(peerData *registry.PeerData) {
  targets := registry.PeerTargets{}
  if peerData.Targets != nil {
    targetsData := util.ToJSON(peerData.Targets)
    if err := util.ReadJson(targetsData, &targets); err != nil {
      log.Println(err.Error())
      return
    }
  }
  jobs := registry.PeerJobs{}
  if peerData.Jobs != nil {
    for _, peerJob := range peerData.Jobs {
      jobs[peerJob.ID] = peerJob
    }
  }
  port := strconv.Itoa(global.ServerPort)
  pc := target.GetClientForPort(port)
  pj := job.GetPortJobs(port)

  log.Printf("Got %d targets, %d jobs\n", len(targets), len(jobs))

  if peerData.TrackingHeaders != "" {
    log.Printf("Got %s trackingHeaders\n", peerData.TrackingHeaders)
    pc.AddTrackingHeaders(peerData.TrackingHeaders)
  }

  if peerData.Probes != nil {
    if peerData.Probes.ReadinessProbe != "" {
      log.Printf("Got Readiness probe %s, status: %d\n", peerData.Probes.ReadinessProbe, peerData.Probes.ReadinessStatus)
      global.ReadinessProbe = peerData.Probes.ReadinessProbe
      probe.ReadinessStatus = peerData.Probes.ReadinessStatus
    }

    if peerData.Probes.LivenessProbe != "" {
      log.Printf("Got Liveness probe: %s, status: %d\n", peerData.Probes.LivenessProbe, peerData.Probes.LivenessStatus)
      global.LivenessProbe = peerData.Probes.LivenessProbe
      probe.LivenessStatus = peerData.Probes.LivenessStatus
    }
  }

  for _, job := range jobs {
    log.Printf("%+v\n", job)
    pj.AddJob(&job.Job)
  }

  for _, t := range targets {
    log.Printf("%+v\n", t)
    pc.AddTarget(&target.Target{t.InvocationSpec})
  }
}

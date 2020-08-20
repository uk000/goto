package peer

import (
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/http/client/target"
	"goto/pkg/http/registry"
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
        data := map[string]interface{}{}
        if err := util.ReadJsonPayloadFromBody(resp.Body, &data); err == nil {
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

func setupStartupTasks(data map[string]interface{}) {
  targets := registry.PeerTargets{}
  if data[constants.PeerDataTargets] != nil {
    targetsData := util.ToJSON(data[constants.PeerDataTargets])
    if err := util.ReadJson(targetsData, &targets); err != nil {
      log.Println(err.Error())
      return
    }
  }
  jobs := registry.PeerJobs{}
  if data[constants.PeerDataJobs] != nil {
    for _, jobData := range data[constants.PeerDataJobs].(map[string]interface{}) {
      if job, err := job.ParseJobFromPayload(util.ToJSON(jobData)); err != nil {
        log.Println(err.Error())
        return
      } else {
        jobs[job.ID] = &registry.PeerJob{*job}
      }
    }
  }
  trackingHeaders := data[constants.PeerDataTrackingHeaders].(string)
  log.Printf("Got %d targets, %d jobs, %s trackingHeaders from registry:\n", len(targets), len(jobs), trackingHeaders)
  port := strconv.Itoa(global.ServerPort)
  pc := target.GetClientForPort(port)
  pj := job.GetPortJobs(port)

  if trackingHeaders != "" {
    pc.AddTrackingHeaders(trackingHeaders)
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

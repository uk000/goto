package peer

import (
  "fmt"
  "goto/pkg/client/target"
  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/job"
  "goto/pkg/registry"
  "goto/pkg/server/probes"
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
    Node:      util.GetNodeName(),
    Cluster:   util.GetCluster(),
  }
  if global.RegistryURL != "" {
    registered := false
    retries := 0
    for !registered && retries < 6 {
      if resp, err := http.Post(global.RegistryURL+"/registry/peers/add", ContentTypeJSON,
        strings.NewReader(util.ToJSON(peer))); err == nil {
        defer resp.Body.Close()
        if resp.StatusCode == 200 || resp.StatusCode == 202 {
          events.SendEventJSONDirect("Peer Registered", peerName, peer)
          registered = true
          log.Printf("Registered as peer [%s] with registry [%s]\n", global.PeerName, global.RegistryURL)
          data := &registry.PeerData{}
          if err := util.ReadJsonPayloadFromBody(resp.Body, data); err == nil {
            events.SendEventJSONDirect("Peer Startup Data", peerName, data)
            log.Println("Read startup data from registry")
            go setupStartupTasks(data)
            go startRegistryReminder(peer)
          } else {
            log.Printf("Failed to read peer targets with error: %s\n", err.Error())
          }
        } else {
          log.Printf("Failed to register as peer to registry due to response code %d\n", resp.StatusCode)
        }
      } else {
        log.Printf("Failed to register as peer to registry due to error: %s\n", err.Error())
      }
      if !registered {
        retries++
        if retries < 6 {
          log.Printf("Will retry registering with registry, retries: %d\n", retries)
          time.Sleep(10 * time.Second)
        } else {
          log.Printf("Failed to register as peer to registry after %d retries. Giving up.\n", retries)
        }
      }
    }
  }
}

func DeregisterPeer(peerName, peerAddress string) {
  events.SendEventDirect("Peer Deregistered", fmt.Sprintf("%s - %s", peerName, peerAddress))
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
      if resp, err := http.Post(url, ContentTypeJSON, strings.NewReader(util.ToJSON(peer))); err == nil {
        util.CloseResponse(resp)
        if global.EnableRegistryReminderLogs {
          log.Printf("Sent reminder to registry at [%s] as peer %s address %s\n", global.RegistryURL, peer.Name, peer.Address)
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
    probeStatus := probes.GetPortProbes(strconv.Itoa(global.ServerPort))
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
    job.Jobs.StoreJobScriptOrFile("", fileName, content, false)
  }

  for fileName, content := range peerData.JobScripts {
    log.Printf("Job Script: %s\n", fileName)
    job.Jobs.StoreJobScriptOrFile("", fileName, content, true)
  }

  for _, j := range peerData.Jobs {
    log.Printf("%+v\n", j)
    job.Jobs.AddJob(&j.Job)
  }

  for _, t := range peerData.Targets {
    log.Printf("%+v\n", t)
    target.Client.AddTarget(&target.Target{t.InvocationSpec})
  }
}

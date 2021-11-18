/**
 * Copyright 2021 uk
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

package cmd

import (
  "flag"
  "goto/pkg/global"
  "goto/pkg/server"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "log"
  "strings"
  "time"
)

var (
  Version string
  Commit  string
)

type ListArg []string

func (l *ListArg) String() string {
  return strings.Join(*l, " ")
}

func (l *ListArg) Set(value string) error {
  *l = append(*l, value)
  return nil
}

func Execute() {
  log.SetFlags(log.LstdFlags | log.Lmicroseconds)
  log.Printf("Version: %s, Commit: %s\n", Version, Commit)
  global.ServerPort = 8080
  global.Version = Version
  global.Commit = Commit
  portsList := ""
  var startupScript ListArg
  flag.IntVar(&global.ServerPort, "port", 8080, "Primary HTTP Server Listen Port")
  flag.StringVar(&portsList, "ports", "", "Comma-separated list of <port/protocol>. First port acts as primary HTTP port")
  flag.StringVar(&global.PeerName, "label", "", "Default Server Label")
  flag.DurationVar(&global.StartupDelay, "startupDelay", 1*time.Second, "Delay Server Startup (seconds)")
  flag.DurationVar(&global.ShutdownDelay, "shutdownDelay", 1*time.Second, "Delay Server Shutdown (seconds)")
  flag.StringVar(&global.RegistryURL, "registry", "", "Registry URL for Peer Registration")
  flag.BoolVar(&global.UseLocker, "locker", false, "Store Results in Registry Locker")
  flag.BoolVar(&global.EnableEvents, "events", true, "Generate and store events on local instance")
  flag.BoolVar(&global.PublishEvents, "publishEvents", false, "Publish events to registry (if events are enabled)")
  flag.StringVar(&global.CertPath, "certs", "/etc/certs", "Directory Path for TLS Certificates")
  flag.BoolVar(&global.EnableServerLogs, "serverLogs", true, "Enable/Disable All Server Logs")
  flag.BoolVar(&global.EnableAdminLogs, "adminLogs", true, "Enable/Disable Admin Logs")
  flag.BoolVar(&global.EnableMetricsLogs, "metricsLogs", true, "Enable/Disable Metrics Logs")
  flag.BoolVar(&global.EnableProbeLogs, "probeLogs", false, "Enable/Disable Probe Logs")
  flag.BoolVar(&global.EnableClientLogs, "clientLogs", true, "Enable/Disable Client Logs")
  flag.BoolVar(&global.EnableInvocationLogs, "invocationLogs", true, "Enable/Disable Client's Target Invocation Logs")
  flag.BoolVar(&global.EnableRegistryLogs, "registryLogs", true, "Enable/Disable All Registry Logs")
  flag.BoolVar(&global.EnableRegistryLockerLogs, "lockerLogs", false, "Enable/Disable Registry Locker Logs")
  flag.BoolVar(&global.EnableRegistryEventsLogs, "eventsLogs", false, "Enable/Disable Registry Peer Events Logs")
  flag.BoolVar(&global.EnableRegistryReminderLogs, "reminderLogs", false, "Enable/Disable Registry Reminder Logs")
  flag.BoolVar(&global.EnablePeerHealthLogs, "peerHealthLogs", true, "Enable/Disable Registry-to-Peer Health Check Logs")
  flag.BoolVar(&global.LogRequestHeaders, "logRequestHeaders", true, "Enable/Disable logging of request headers")
  flag.BoolVar(&global.LogRequestBody, "logRequestBody", false, "Enable/Disable logging of request body")
  flag.BoolVar(&global.LogRequestMiniBody, "logRequestMiniBody", false, "Enable/Disable logging of request mini body")
  flag.BoolVar(&global.LogResponseHeaders, "logResponseHeaders", false, "Enable/Disable logging of response headers")
  flag.BoolVar(&global.LogResponseBody, "logResponseBody", false, "Enable/Disable logging of response body")
  flag.BoolVar(&global.LogResponseMiniBody, "logResponseMiniBody", false, "Enable/Disable logging of response mini body")
  flag.Var(&startupScript, "startupScript", "Script to execute at startup")
  flag.StringVar(&global.KubeConfig, "kubeConfig", "", "Path to Kubernetes config file")
  flag.BoolVar(&global.Debug, "debug", false, "Debug logs")
  flag.Parse()

  if portsList != "" {
    listeners.AddInitialListeners(strings.Split(portsList, ","))
  }
  if global.PeerName == "" {
    global.PeerName = util.BuildListenerLabel(global.ServerPort)
  }
  listeners.DefaultLabel = global.PeerName
  log.Printf("Server [%s] will listen on port [%d]\n", global.PeerName, global.ServerPort)

  if global.EnableEvents {
    log.Println("Will generate and store events locally")
  }
  if global.RegistryURL != "" {
    log.Printf("Registry [%s]\n", global.RegistryURL)
    if global.UseLocker {
      log.Printf("Will Store Results in Locker at Registry [%s]\n", global.RegistryURL)
    }
    if global.EnableEvents && global.PublishEvents {
      log.Println("Will publish events to registry")
    }
  }
  if global.EnableRegistryLockerLogs {
    log.Println("Will Print Registry Locker Logs")
  } else {
    log.Println("Will Not Print Registry Locker Logs")
  }
  if global.EnableRegistryEventsLogs {
    log.Println("Will Print Registry Peer Events Logs")
  } else {
    log.Println("Will Not Print Registry Peer Events Logs")
  }
  if global.EnableRegistryReminderLogs {
    log.Println("Will Print Registry Reminder Logs")
  } else {
    log.Println("Will Not Print Registry Reminder Logs")
  }
  if global.EnableProbeLogs {
    log.Println("Will Print Probe Logs")
  } else {
    log.Println("Will Not Print Probe Logs")
  }
  if global.LogRequestHeaders {
    log.Println("Will Log Request Headers")
  } else {
    log.Println("Will Not Log Request Headers")
  }
  if global.LogRequestMiniBody {
    log.Println("Will Log Request Mini Body")
  } else if global.LogRequestBody {
    log.Println("Will Log Request Body")
  } else {
    log.Println("Will Not Log Request Body")
  }
  if global.LogResponseHeaders {
    log.Println("Will Log Response Headers")
  } else {
    log.Println("Will Not Log Response Headers")
  }
  if global.LogResponseMiniBody {
    log.Println("Will Log Response Mini Body")
  } else if global.LogResponseBody {
    log.Println("Will Log Response Body")
  } else {
    log.Println("Will Not Log Response Body")
  }
  if global.CertPath != "" {
    log.Printf("Will read certs from [%s]\n", global.CertPath)
  }
  if global.Debug {
    log.Println("Debug logging enabled")
  }
  log.Printf("Server startupDelay [%s] shutdownDelay [%s]\n", global.StartupDelay, global.ShutdownDelay)
  if startupScript != nil {
    global.StartupScript = startupScript
  }
  server.Run()
}

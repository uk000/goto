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

func Execute() {
  log.SetFlags(log.LstdFlags | log.Lmicroseconds)
  global.ServerPort = 8080
  portsList := ""
  flag.IntVar(&global.ServerPort, "port", 8080, "Primary HTTP Server Listen Port")
  flag.StringVar(&portsList, "ports", "", "Comma-separated list of <port/protocol>. First port acts as primary HTTP port")
  flag.StringVar(&global.PeerName, "label", "", "Default Server Label")
  flag.DurationVar(&global.StartupDelay, "startupDelay", 1*time.Second, "Delay Server Startup (seconds)")
  flag.DurationVar(&global.ShutdownDelay, "shutdownDelay", 5*time.Second, "Delay Server Shutdown (seconds)")
  flag.StringVar(&global.RegistryURL, "registry", "", "Registry URL for Peer Registration")
  flag.BoolVar(&global.UseLocker, "locker", false, "Store Results in Registry Locker")
  flag.BoolVar(&global.EnableEvents, "events", true, "Generate and store events on local instance")
  flag.BoolVar(&global.PublishEvents, "publishEvents", false, "Publish events to registry (if events are enabled)")
  flag.StringVar(&global.CertPath, "certs", "/etc/certs", "Directory Path for TLS Certificates")
  flag.BoolVar(&global.EnableServerLogs, "serverLogs", true, "Enable/Disable All Server Logs")
  flag.BoolVar(&global.EnableAdminLogs, "adminLogs", true, "Enable/Disable Admin Logs")
  flag.BoolVar(&global.EnableMetricsLogs, "metricsLogs", true, "Enable/Disable Metrics Logs")
  flag.BoolVar(&global.EnableProbeLogs, "probeLogs", false, "Enable/Disable Probe Logs")
  flag.BoolVar(&global.EnablePeerHealthLogs, "peerHealthLogs", true, "Enable/Disable Registry-to-Peer Health Check Logs")
  flag.BoolVar(&global.EnableRegistryLockerLogs, "lockerLogs", false, "Enable/Disable Registry Locker Logs")
  flag.BoolVar(&global.EnableRegistryEventsLogs, "eventsLogs", false, "Enable/Disable Registry Peer Events Logs")
  flag.BoolVar(&global.EnableRegistryReminderLogs, "reminderLogs", false, "Enable/Disable Registry Reminder Logs")
  flag.Parse()
  if global.PeerName == "" {
    global.PeerName = "Goto-" + util.GetHostIP()
  }
  listeners.DefaultLabel = global.PeerName
  log.Printf("Version: %s, Commit: %s\n", Version, Commit)

  if portsList != "" {
    listeners.AddInitialListeners(strings.Split(portsList, ","))
  }
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
  if global.CertPath != "" {
    log.Printf("Will read certs from [%s]\n", global.CertPath)
  }
  log.Printf("Server startupDelay [%s] shutdownDelay [%s]\n", global.StartupDelay, global.ShutdownDelay)
  server.Run()
}

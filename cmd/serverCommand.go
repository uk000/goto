package cmd

import (
	"flag"
	"goto/pkg/global"
	"goto/pkg/http/server"
	"goto/pkg/http/server/listeners"
	"goto/pkg/util"
	"log"
)

var (
  Version string
  Commit  string
)

func Execute() {
  global.ServerPort = 8080
  flag.IntVar(&global.ServerPort, "port", 8080, "Main HTTP Server Listen Port")
  flag.StringVar(&global.PeerName, "label", "", "Default Server Label")
  flag.StringVar(&global.RegistryURL, "registry", "", "Registry URL for Peer Registration")
  flag.StringVar(&global.CertPath, "certs", "/etc/certs", "Directory Path for TLS Certificates")
  flag.BoolVar(&global.UseLocker, "locker", false, "Store Results in Registry Locker")
  flag.Parse()
  if global.PeerName == "" {
    global.PeerName = "Goto-" + util.GetHostIP()
  }
  listeners.DefaultLabel = global.PeerName
  log.Printf("Version: %s, Commit: %s\n", Version, Commit)
  log.Printf("Server [%s] Listen on port [%d]\n", global.PeerName, global.ServerPort)
  if global.RegistryURL != "" {
    log.Printf("Registry [%s]\n", global.RegistryURL)
  }
  if global.UseLocker {
    log.Printf("Will Store Results in Locker at Registry [%s]\n", global.RegistryURL)
  }
  if global.CertPath != "" {
    log.Printf("Will read certs from [%s]\n", global.CertPath)
  }
  server.Run()
}

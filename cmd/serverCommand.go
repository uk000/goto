package cmd

import (
	"flag"
	"goto/pkg/http/registry"
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
  var serverListenPort int = 8080
  flag.IntVar(&serverListenPort, "port", 8080, "Main HTTP Server Listen Port")
  flag.StringVar(&registry.PeerName, "label", "", "Default Server Label")
  flag.StringVar(&registry.RegistryURL, "registry", "", "Registry URL for Peer Registration")
  flag.Parse()
  if registry.PeerName == "" {
    registry.PeerName = "Goto-" + util.GetHostIP()
  }
  listeners.DefaultLabel = registry.PeerName
  log.Printf("Version: %s, Commit: %s\n", Version, Commit)
  log.Printf("Server [%s] Listen on port [%d]\n", registry.PeerName, serverListenPort)
  if registry.RegistryURL != "" {
    log.Printf("Registry [%s]\n", registry.RegistryURL)
  }
  server.Run(serverListenPort)
}

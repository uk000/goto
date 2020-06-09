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
  var serverListenPort int = 8080
  flag.IntVar(&serverListenPort, "port", 8080, "Main HTTP Server Listen Port")
  flag.StringVar(&global.PeerName, "label", "", "Default Server Label")
  flag.StringVar(&global.RegistryURL, "registry", "", "Registry URL for Peer Registration")
  flag.Parse()
  if global.PeerName == "" {
    global.PeerName = "Goto-" + util.GetHostIP()
  }
  listeners.DefaultLabel = global.PeerName
  log.Printf("Version: %s, Commit: %s\n", Version, Commit)
  log.Printf("Server [%s] Listen on port [%d]\n", global.PeerName, serverListenPort)
  if global.RegistryURL != "" {
    log.Printf("Registry [%s]\n", global.RegistryURL)
  }
  server.Run(serverListenPort)
}

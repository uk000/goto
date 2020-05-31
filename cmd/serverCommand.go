package cmd

import (
  "flag"
  "goto/pkg/http/server"
  "log"
)

var (
  Version string
  Commit  string
)

func Execute() {
  var serverListenPort int = 8080
  flag.IntVar(&serverListenPort, "port", 8080, "Main HTTP Server Listen Port")
  flag.Parse()
  log.Printf("Version: %v, Commit: %v\n", Version, Commit)
  log.Printf("Listen on port: %v\n", serverListenPort)
  server.Run(serverListenPort)
}

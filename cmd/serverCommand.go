package cmd

import (
	"flag"
	"goto/pkg/http/server"
)

func Execute() {
  var serverListenPort int = 8080
  flag.IntVar(&serverListenPort, "port", 8080, "Main HTTP Server Listen Port")
  flag.Parse()
  server.Run(serverListenPort)
}

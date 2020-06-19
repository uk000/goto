package server

import (
	"os"
	"strconv"

	"goto/pkg/global"
	"goto/pkg/http/client"
	"goto/pkg/http/invocation"
	"goto/pkg/http/registry"
	"goto/pkg/http/server/catchall"
	"goto/pkg/http/server/conn"
	"goto/pkg/http/server/echo"
	"goto/pkg/http/server/listeners"
	"goto/pkg/http/server/listeners/label"
	"goto/pkg/http/server/request"
	"goto/pkg/http/server/response"
	"goto/pkg/http/server/runner"
	"goto/pkg/job"
	"goto/pkg/util"
)

func Run() {
	global.PeerAddress = util.GetHostIP()+":"+strconv.Itoa(global.ServerPort)
	global.GetPeers = registry.GetPeers
  listeners.SetListenerServer(runner.ServeListener)
  invocation.LoadCerts()
  runner.RunHttpServer("/", label.Handler, conn.Handler, job.Handler, request.Handler,
    response.Handler, listeners.Handler, registry.Handler, client.Handler, echo.Handler, catchall.Handler)
  os.Exit(0)
}

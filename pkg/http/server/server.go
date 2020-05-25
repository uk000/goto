package server

import (
	"os"

	"goto/pkg/http/client"
	"goto/pkg/http/server/catchall"
	"goto/pkg/http/server/conn"
	"goto/pkg/http/server/echo"
	"goto/pkg/http/server/listeners"
	"goto/pkg/http/server/listeners/label"
	"goto/pkg/http/server/request"
	"goto/pkg/http/server/response"
	"goto/pkg/http/server/runner"
)

func Run(listenPort int) {
	runner.RunHttpServer(listenPort, "/", label.Handler, conn.Handler, request.Handler,
		response.Handler, listeners.Handler, client.Handler, echo.Handler, catchall.Handler)
	os.Exit(0)
}

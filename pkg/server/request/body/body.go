package body

import (
  "fmt"
  "io"
  "io/ioutil"
  "log"
  "net/http"
  "strings"

  "goto/pkg/global"
  "goto/pkg/util"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "body", Middleware: Middleware}
)

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if global.Debug {
      log.Println("Enter Request.Body Middleware")
    }
    if (global.LogRequestBody || global.LogRequestMiniBody) &&
      !util.IsAdminRequest(r) && r.ProtoMajor == 1 {
      if global.Debug {
        log.Println("Reading Request.Body")
      }
      body := util.Read(r.Body)
      if global.Debug {
        log.Println("Finished Reading Request.Body")
      }
      bodyLength := len(body)
      r.Body.Close()
      bodyLog := ""
      if global.LogRequestMiniBody && len(body) > 50 {
        bodyLog = fmt.Sprintf("%s...", body[:50])
        bodyLog += fmt.Sprintf("%s", body[bodyLength-50:])
      } else {
        bodyLog = body
      }
      bodyLog = strings.ReplaceAll(bodyLog, "\n", "\\n")
      util.AddLogMessage(fmt.Sprintf("Request Body Length: [%d]", bodyLength), r)
      if global.LogRequestMiniBody {
        util.AddLogMessage(fmt.Sprintf("Request Mini Body: [%s]", bodyLog), r)
      } else {
        util.AddLogMessage(fmt.Sprintf("Request Body: [%s]", bodyLog), r)
      }
      r.Body = ioutil.NopCloser(strings.NewReader(body))
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
    if global.Debug {
      log.Println("Discarding Request.Body")
    }
    io.Copy(ioutil.Discard, r.Body)
    if global.Debug {
      log.Println("Exit Request.Body Middleware")
    }
  })
}

package body

import (
  "fmt"
  "io"
  "io/ioutil"
  "net/http"
  "strings"

  "goto/pkg/util"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "header", Middleware: Middleware}
)

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    body := util.Read(r.Body)
    miniBody := ""
    if len(body) > 100 {
      miniBody = body[:100]
      miniBody += "..."
    } else {
      miniBody = body
    }
    util.AddLogMessage(fmt.Sprintf("Request Body: [%s]", miniBody), r)
    r.Body = ioutil.NopCloser(strings.NewReader(body))
    next.ServeHTTP(w, r)
    io.Copy(ioutil.Discard, r.Body)
  })
}

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
    if !util.IsAdminRequest(r) && r.ProtoMajor == 1 {
      body := util.Read(r.Body)
      r.Body.Close()
      miniBody := ""
      if len(body) > 20 {
        miniBody = fmt.Sprintf("%s...[length:%d]", body[:20], len(body))
      } else {
        miniBody = body
      }
      miniBody = strings.ReplaceAll(miniBody, "\n", " ")
      util.AddLogMessage(fmt.Sprintf("Request Body: [%s]", miniBody), r)
      r.Body = ioutil.NopCloser(strings.NewReader(body))
      if next != nil {
        next.ServeHTTP(w, r)
      }
    } else {
      next.ServeHTTP(w, r)
    }
    io.Copy(ioutil.Discard, r.Body)
  })
}

package intercept

import (
	"bufio"
	"net"
	"net/http"
)

type InterceptResponseWriter struct {
  http.ResponseWriter
  http.Hijacker
  StatusCode int
  Data       []byte
  Hold       bool
  Hijacked   bool
}

func (rw *InterceptResponseWriter) WriteHeader(statusCode int) {
  rw.StatusCode = statusCode
  if !rw.Hijacked && !rw.Hold {
    rw.ResponseWriter.WriteHeader(statusCode)
  }
}

func (rw *InterceptResponseWriter) Write(b []byte) (int, error) {
  rw.Data = append(rw.Data, b...)
  if !rw.Hold {
    return rw.ResponseWriter.Write(b)
  }
  return 0, nil
}

func (rw *InterceptResponseWriter) Flush() {
  rw.ResponseWriter.(http.Flusher).Flush()
}

func (rw *InterceptResponseWriter) Proceed() {
  if !rw.Hijacked && rw.Hold {
    if rw.StatusCode <= 0 {
      rw.StatusCode = 200
    }
    rw.ResponseWriter.WriteHeader(rw.StatusCode)
    rw.ResponseWriter.Write(rw.Data)
  }
}

func (rw *InterceptResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
  rw.Hijacked = true
  return rw.Hijacker.Hijack()
}

func NewInterceptResponseWriter(w http.ResponseWriter, hold bool) *InterceptResponseWriter {
  if h, ok := w.(http.Hijacker); ok {
    return &InterceptResponseWriter{ResponseWriter: w, Hijacker: h, Hold: hold}
  }
  return &InterceptResponseWriter{ResponseWriter: w, Hold: hold}
}

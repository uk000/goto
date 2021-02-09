package intercept

import (
  "bufio"
  "net"
  "net/http"
)

type ResponseInterceptor interface {
  SetChunked()
}

type InterceptResponseWriter struct {
  http.ResponseWriter
  http.Hijacker
  http.Flusher
  parent     ResponseInterceptor
  StatusCode int
  Data       []byte
  Hold       bool
  Hijacked   bool
  Chunked    bool
  BodyLength int
}

func (rw *InterceptResponseWriter) WriteHeader(statusCode int) {
  rw.StatusCode = statusCode
  if !rw.Hijacked && !rw.Hold {
    rw.ResponseWriter.WriteHeader(statusCode)
  }
}

func (rw *InterceptResponseWriter) Write(b []byte) (int, error) {
  rw.BodyLength += len(b)
  if !rw.Hold || rw.Chunked {
    if len(rw.Data) > 0 {
      rw.ResponseWriter.Write(rw.Data)
      rw.Data = []byte{}
    }
    return rw.ResponseWriter.Write(b)
  } else {
    rw.Data = append(rw.Data, b...)
  }
  return 0, nil
}

func (rw *InterceptResponseWriter) Flush() {
  rw.SetChunked()
  if rw.Flusher != nil {
    rw.Flusher.Flush()
  }
}

func (rw *InterceptResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
  rw.Hijacked = true
  return rw.Hijacker.Hijack()
}

func (rw *InterceptResponseWriter) SetChunked() {
  rw.Chunked = true
  if rw.parent != nil {
    rw.parent.SetChunked()
  }
}

func (rw *InterceptResponseWriter) Proceed() {
  if !rw.Hijacked && !rw.Chunked && rw.Hold {
    if rw.StatusCode <= 0 {
      rw.StatusCode = 200
    }
    rw.ResponseWriter.WriteHeader(rw.StatusCode)
    rw.ResponseWriter.Write(rw.Data)
  }
}

func NewInterceptResponseWriter(w http.ResponseWriter, hold bool) *InterceptResponseWriter {
  parent, _ := w.(ResponseInterceptor)
  hijacker, _ := w.(http.Hijacker)
  flusher, _ := w.(http.Flusher)
  return &InterceptResponseWriter{
    ResponseWriter: w,
    Hijacker:       hijacker,
    Flusher:        flusher,
    parent:         parent,
    Hold:           hold,
  }
}

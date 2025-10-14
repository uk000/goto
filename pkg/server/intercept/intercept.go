/**
 * Copyright 2025 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package intercept

import (
	"bufio"
	"fmt"
	"goto/pkg/util"
	"io"
	"net"
	"net/http"
)

type ResponseInterceptor interface {
	SetChunked()
	SetHijacked()
}

type InterceptResponseWriter struct {
	http.ResponseWriter
	http.Hijacker
	http.Flusher
	conn       net.Conn
	parent     ResponseInterceptor
	rs         *util.RequestStore
	StatusCode int
	Data       []byte
	Hold       bool
	Hijacked   bool
	Chunked    bool
	IsH2C      bool
	BodyLength int
}

type HeaderInterceptResponseWriter struct {
	http.ResponseWriter
	rs      *util.RequestStore
	Headers http.Header
}

type FlushWriter struct {
	flusher http.Flusher
	w       io.Writer
	h2c     bool
}

type BodyTracker struct {
	io.ReadCloser
}

func GetConn(r *http.Request) net.Conn {
	if conn := r.Context().Value(util.ConnectionKey); conn != nil {
		return conn.(net.Conn)
	}
	return nil
}

func NewFlushWriter(r *http.Request, w io.Writer) *FlushWriter {
	rs := util.GetRequestStore(r)
	if f, ok := w.(http.Flusher); ok {
		if irw, ok := w.(*InterceptResponseWriter); ok {
			irw.SetChunked()
			return &FlushWriter{w: w, h2c: rs.IsH2C, flusher: f}
		}
	}
	return nil
}

func (fw FlushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil {
		fw.Flush()
	}
	return n, err
}

func (fw FlushWriter) Flush() {
	if fw.flusher != nil {
		fw.flusher.Flush()
	}
}

func trackRequestBody(r *http.Request) {
	r.Body = &BodyTracker{r.Body}
}

func (b *BodyTracker) Read(p []byte) (n int, err error) {
	// util.PrintCallers(3, "BodyTracker.Read")
	return b.ReadCloser.Read(p)
}

func (b *BodyTracker) Close() error {
	//util.PrintCallers(3, "BodyTracker.Close")
	return b.ReadCloser.Close()
}

func (b *BodyTracker) Rewind() {
	// util.PrintCallers(3, "BodyTracker.Close")
	if rr, ok := b.ReadCloser.(*util.ReReader); ok {
		rr.Rewind()
	}
}

func (rw *InterceptResponseWriter) WriteHeader(statusCode int) {
	rw.StatusCode = statusCode
	rw.rs.StatusCode = statusCode
	if !rw.Hijacked && !rw.Hold {
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *InterceptResponseWriter) Write(b []byte) (int, error) {
	// util.PrintCallers(3, "InterceptResponseWriter.Write")
	l := len(b)
	rw.BodyLength += l
	if !rw.Hijacked {
		if !rw.Hold || rw.Chunked {
			if len(rw.Data) > 0 {
				if n, err := rw.ResponseWriter.Write(rw.Data); err != nil {
					return n, err
				}
				rw.Data = []byte{}
			}
			if n, err := rw.ResponseWriter.Write(b); err != nil {
				return n, err
			} else {
				return n, nil
			}
		} else {
			rw.Data = append(rw.Data, b...)
		}
	}
	return l, nil
}

func (rw *InterceptResponseWriter) Flush() {
	// util.PrintCallers(3, "InterceptResponseWriter.Flush")
	rw.SetChunked()
	if rw.Flusher != nil {
		rw.Flusher.Flush()
	}
}

func (rw *InterceptResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// util.PrintCallers(3, "InterceptResponseWriter.Hijack")
	if rw.Hijacker != nil {
		rw.Hijacked = true
		return rw.Hijacker.Hijack()
	}
	return rw.conn, bufio.NewReadWriter(bufio.NewReader(rw.conn), bufio.NewWriter(rw.conn)), nil

}

func (rw *InterceptResponseWriter) SetHijacked() {
	rw.Hijacked = true
	if rw.parent != nil {
		rw.parent.SetHijacked()
	}
}

func (rw *InterceptResponseWriter) SetChunked() {
	rw.Chunked = true
	if rw.parent != nil {
		rw.parent.SetChunked()
	}
}

func (rw *InterceptResponseWriter) Proceed() {
	if rw.Hijacked || rw.Hold || !rw.Chunked {
		if rw.StatusCode <= 0 {
			rw.StatusCode = 200
			rw.rs.StatusCode = rw.StatusCode
		}
		rw.ResponseWriter.WriteHeader(rw.StatusCode)
		if _, err := rw.ResponseWriter.Write(rw.Data); err == http.ErrHijacked {
			rw.Hijacked = true
			if _, err := rw.conn.Write(rw.Data); err != nil {
				fmt.Printf("InterceptResponseWriter.Proceed: failed to write [%d] bytes with error: %s\n", len(rw.Data), err.Error())
			}
		}
	}
}

func NewInterceptResponseWriter(r *http.Request, w http.ResponseWriter, hold bool) *InterceptResponseWriter {
	parent, _ := w.(ResponseInterceptor)
	hijacker, _ := w.(http.Hijacker)
	flusher, _ := w.(http.Flusher)
	//trackRequestBody(r)
	return &InterceptResponseWriter{
		rs:             r.Context().Value(util.RequestStoreKey).(*util.RequestStore),
		ResponseWriter: w,
		Hijacker:       hijacker,
		Flusher:        flusher,
		parent:         parent,
		Hold:           hold,
		IsH2C:          util.IsH2C(r),
		conn:           GetConn(r),
	}
}

func WithIntercept(r *http.Request, w http.ResponseWriter) (http.ResponseWriter, *InterceptResponseWriter) {
	var irw *InterceptResponseWriter
	rs := util.GetRequestStore(r)
	if !rs.IsKnownNonTraffic {
		irw = NewInterceptResponseWriter(r, w, true)
		r.Context().Value(util.RequestStoreKey).(*util.RequestStore).InterceptResponseWriter = irw
		w = irw
	}
	return w, irw
}

func WithHeadersIntercept(r *http.Request, w http.ResponseWriter) (http.ResponseWriter, *HeaderInterceptResponseWriter) {
	var irw *HeaderInterceptResponseWriter
	rs := util.GetRequestStore(r)
	if !rs.IsKnownNonTraffic {
		irw = NewHeadersInterceptResponseWriter(w)
		r.Context().Value(util.RequestStoreKey).(*util.RequestStore).HeadersInterceptRW = irw
		w = irw
	}
	return w, irw
}

func NewHeadersInterceptResponseWriter(w http.ResponseWriter) *HeaderInterceptResponseWriter {
	return &HeaderInterceptResponseWriter{
		ResponseWriter: w,
		Headers:        w.Header(),
	}
}

func (rw *HeaderInterceptResponseWriter) HeadersSent() bool {
	return len(rw.Headers) > 0
}

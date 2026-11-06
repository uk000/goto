/**
 * Copyright 2026 uk
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

package middleware

import (
	"bytes"
	"context"
	"fmt"
	"goto/pkg/constants"
	"io"
	"net/http"
	"net/url"
	"strings"

	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type GrpcHTTPRequestAdapter struct {
	*http.Request
	Ctx context.Context
}

// NewGrpcHTTPRequestAdapter builds an HTTP/2 request from the method, host, URI, headers and body of a gRPC call, taking the remote address from the peer in ctx.
func NewGrpcHTTPRequestAdapter(ctx context.Context, method, host, uri string, headers map[string][]string, body []byte) *GrpcHTTPRequestAdapter {
	u, err := url.Parse(uri)
	if err != nil {
		// A uri that doesn't parse is still the path of the request, and the request must have a URL.
		u = &url.URL{Path: uri}
	}
	h := http.Header{}
	for k, v := range headers {
		for _, vv := range v {
			h.Add(k, vv)
		}
	}
	h.Add("uri", uri)
	h.Add("method", method)
	h.Add(constants.HeaderContentType, "application/grpc")
	remoteAddr := ""
	if p, ok := peer.FromContext(ctx); ok && p != nil {
		remoteAddr = p.Addr.String()
	}
	return &GrpcHTTPRequestAdapter{
		&http.Request{
			Method:        method,
			URL:           u,
			Header:        h,
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: int64(len(body)),
			Host:          host,
			RemoteAddr:    remoteAddr,
			RequestURI:    uri,
			ProtoMajor:    2,
		},
		ctx,
	}
}

// Read reads from the request body.
func (g *GrpcHTTPRequestAdapter) Read(p []byte) (n int, err error) {
	return g.Body.Read(p)
}

// Close closes the request body.
func (g *GrpcHTTPRequestAdapter) Close() error {
	return g.Body.Close()
}

// Context returns the context of the adapter, or a background context if it has none.
func (g *GrpcHTTPRequestAdapter) Context() context.Context {
	if g.Ctx != nil {
		return g.Ctx
	}
	return context.Background()
}

// ToHTTPRequest returns the underlying http.Request bound to the context of the adapter.
func (g *GrpcHTTPRequestAdapter) ToHTTPRequest() *http.Request {
	if g.Ctx == nil {
		g.Ctx = context.Background()
	}
	return g.Request.WithContext(g.Ctx)
}

type GrpcHTTPResponseWriterAdapter struct {
	HeaderMap   http.Header
	Responses   []proto.Message
	StatusCode  int
	wroteHeader bool
	desc        protoreflect.MessageDescriptor
}

// NewGrpcHTTPResponseWriterAdapter creates a response writer that collects what is written as messages of the given protobuf type.
func NewGrpcHTTPResponseWriterAdapter(desc protoreflect.MessageDescriptor) *GrpcHTTPResponseWriterAdapter {
	return &GrpcHTTPResponseWriterAdapter{
		HeaderMap:  make(http.Header),
		Responses:  []proto.Message{},
		StatusCode: http.StatusOK,
		desc:       desc,
	}
}

// Header returns the response headers.
func (g *GrpcHTTPResponseWriterAdapter) Header() http.Header {
	return g.HeaderMap
}

// Write unmarshals the JSON response data into a dynamic message of the response type and collects it, setting status 200 if no status was written.
func (g *GrpcHTTPResponseWriterAdapter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if len(b) > 0 {
		if g.desc == nil {
			return 0, fmt.Errorf("failed to unmarshal response: the type of the response is not known")
		}
		dmsg := dynamicpb.NewMessage(g.desc)
		if err := protojson.Unmarshal(b, dmsg); err != nil {
			return 0, fmt.Errorf("failed to unmarshal response to dynamic message: %w", err)
		}
		g.Responses = append(g.Responses, dmsg)
	}
	return len(b), nil
}

// WriteHeader records the status code, only the first time it is called.
func (g *GrpcHTTPResponseWriterAdapter) WriteHeader(statusCode int) {
	if g.wroteHeader {
		return
	}
	g.StatusCode = statusCode
	g.wroteHeader = true
}

// Status returns the response status code.
func (g *GrpcHTTPResponseWriterAdapter) Status() int {
	return g.StatusCode
}

// HTTPToMDHeaders converts HTTP headers to gRPC metadata keys, lower-cased and without colons.
func HTTPToMDHeaders(h http.Header) map[string][]string {
	md := make(map[string][]string)
	for k, v := range h {
		k = strings.ToLower(strings.ReplaceAll(k, ":", ""))
		md[k] = v
	}
	return md
}

// ToMetadata returns the response headers as gRPC metadata.
func (g *GrpcHTTPResponseWriterAdapter) ToMetadata() map[string][]string {
	return HTTPToMDHeaders(g.HeaderMap)
}

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

func NewGrpcHTTPRequestAdapter(ctx context.Context, method, host, uri string, headers map[string][]string, body []byte) *GrpcHTTPRequestAdapter {
	u, _ := url.Parse(uri)
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

func (g *GrpcHTTPRequestAdapter) Read(p []byte) (n int, err error) {
	return g.Body.Read(p)
}

func (g *GrpcHTTPRequestAdapter) Close() error {
	return g.Body.Close()
}

func (g *GrpcHTTPRequestAdapter) Context() context.Context {
	if g.Ctx != nil {
		return g.Ctx
	}
	return context.Background()
}

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

func NewGrpcHTTPResponseWriterAdapter(desc protoreflect.MessageDescriptor) *GrpcHTTPResponseWriterAdapter {
	return &GrpcHTTPResponseWriterAdapter{
		HeaderMap:  make(http.Header),
		Responses:  []proto.Message{},
		StatusCode: http.StatusOK,
		desc:       desc,
	}
}

func (g *GrpcHTTPResponseWriterAdapter) Header() http.Header {
	return g.HeaderMap
}

func (g *GrpcHTTPResponseWriterAdapter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if len(b) > 0 {
		dmsg := dynamicpb.NewMessage(g.desc)
		if err := protojson.Unmarshal(b, dmsg); err != nil {
			return 0, fmt.Errorf("failed to unmarshal response to dynamic message: %w", err)
		}
		g.Responses = append(g.Responses, dmsg)
	}
	return len(b), nil
}

func (g *GrpcHTTPResponseWriterAdapter) WriteHeader(statusCode int) {
	if g.wroteHeader {
		return
	}
	g.StatusCode = statusCode
	g.wroteHeader = true
}

func (g *GrpcHTTPResponseWriterAdapter) Status() int {
	return g.StatusCode
}

func HTTPToMDHeaders(h http.Header) map[string][]string {
	md := make(map[string][]string)
	for k, v := range h {
		k = strings.ToLower(strings.ReplaceAll(k, ":", ""))
		md[k] = v
	}
	return md
}

func (g *GrpcHTTPResponseWriterAdapter) ToMetadata() map[string][]string {
	return HTTPToMDHeaders(g.HeaderMap)
}

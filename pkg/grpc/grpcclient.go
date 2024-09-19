/**
 * Copyright 2024 uk
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

package grpc

import (
  "bytes"
  "context"
  "crypto/tls"
  "encoding/base64"
  "errors"
  "fmt"
  "goto/pkg/constants"
  "goto/pkg/metrics"
  "goto/pkg/transport"
  "goto/pkg/util"
  "io"
  "log"
  "net"
  "net/http"
  "sort"
  "strings"
  "sync"
  "time"

  "github.com/gogo/protobuf/jsonpb"
  "github.com/jhump/protoreflect/desc"
  "github.com/jhump/protoreflect/dynamic"
  "github.com/jhump/protoreflect/dynamic/grpcdynamic"
  "github.com/jhump/protoreflect/grpcreflect"
  "google.golang.org/grpc"
  "google.golang.org/grpc/codes"
  "google.golang.org/grpc/connectivity"
  "google.golang.org/grpc/credentials"
  "google.golang.org/grpc/keepalive"
  "google.golang.org/grpc/metadata"
  grpc_reflect "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
  "google.golang.org/grpc/status"
  "google.golang.org/protobuf/runtime/protoiface"
)

type DescriptorSource interface {
  ListServices() ([]string, error)
  FindSymbol(fullyQualifiedName string) (desc.Descriptor, error)
  AllExtensionsForType(typeName string) ([]*desc.FieldDescriptor, error)
}

type GRPCServiceMethod struct {
  Name              string
  IsUnary           bool
  IsClientStreaming bool
  IsServerStreaming bool
  IsJSONResponse    bool
  mf                *dynamic.MessageFactory
  md                *desc.MethodDescriptor
}

type GRPCClient struct {
  transport.BaseTransportIntercept
  Service        string        `json:"service"`
  URL            string        `json:"url"`
  ServerName     string        `json:"serverName"`
  Authority      string        `json:"authority"`
  IsTLS          bool          `json:"isTLS"`
  VerifyTLS      bool          `json:"verifyTLS"`
  TLSVersion     uint16        `json:"tlsVersion"`
  ConnectTimeout time.Duration `json:"connectTimeout"`
  IdleTimeout    time.Duration `json:"idleTimeout"`
  RequestTimeout time.Duration `json:"requestTimeout"`
  KeepOpen       time.Duration `json:"keepOpen"`
  Methods        map[string]*GRPCServiceMethod
  tlsConfig      *tls.Config
  tlsCredentials credentials.TransportCredentials
  baseDialOpts   []grpc.DialOption
  dialOpts       []grpc.DialOption
  sd             *desc.ServiceDescriptor
  mf             *dynamic.MessageFactory
  conn           *grpc.ClientConn
}

type GRPCClientStream struct {
  clientStream *grpcdynamic.ClientStream
  bidiStream   *grpcdynamic.BidiStream
}

type GRPCServerStream struct {
  serverStream *grpcdynamic.ServerStream
  bidiStream   *grpcdynamic.BidiStream
}

type GRPCResponse struct {
  Status                   int
  EquivalentHTTPStatusCode int
  ResponseHeaders          map[string][]string
  ResponseTrailers         map[string][]string
  ResponsePayload          []byte
  ClientStreamCount        int
  ServerStreamCount        int
}

type GRPCManager struct {
  targets        map[string]*GRPCClient
  connectTimeout time.Duration
  idleTimeout    time.Duration
  requestTimeout time.Duration
  tlsVersion     uint16
  baseDialOpts   []grpc.DialOption
}

var (
  Manager = newGRPCManager()
)

func newGRPCManager() *GRPCManager {
  g := &GRPCManager{}
  g.targets = map[string]*GRPCClient{}
  g.connectTimeout = 30 * time.Second
  g.idleTimeout = 5 * time.Minute
  g.requestTimeout = 1 * time.Minute
  g.tlsVersion = tls.VersionTLS13
  g.configureDialOptions()
  return g
}

func CreateGRPCClient(name, url, authority, serverName string, isTLS, verifyTLS bool) (transport.TransportClient, error) {
  ep, err := NewGRPCClient(name, url, authority, serverName)
  if err != nil {
    return nil, err
  }
  ep.SetTLS(isTLS, verifyTLS)
  if err := ep.Connect(); err != nil {
    return nil, err
  }
  return ep, nil
}

func NewGRPCClient(service, url, authority, serverName string) (*GRPCClient, error) {
  if serverName == "" {
    serverName = authority
  }
  sd := grpcParser.GetService(service)
  if sd == nil {
    return nil, fmt.Errorf("No proto configured for service [%s]", service)
  }
  c := &GRPCClient{
    Service:        service,
    URL:            url,
    ServerName:     serverName,
    Authority:      authority,
    TLSVersion:     Manager.tlsVersion,
    ConnectTimeout: Manager.connectTimeout,
    IdleTimeout:    Manager.idleTimeout,
    RequestTimeout: Manager.requestTimeout,
    Methods:        map[string]*GRPCServiceMethod{},
    baseDialOpts:   Manager.baseDialOpts,
    sd:             grpcParser.GetService(service),
    mf:             dynamic.NewMessageFactoryWithDefaults(),
  }
  for _, md := range c.sd.GetMethods() {
    c.Methods[md.GetName()] = &GRPCServiceMethod{
      Name:              md.GetFullyQualifiedName(),
      md:                md,
      IsClientStreaming: md.IsClientStreaming(),
      IsServerStreaming: md.IsServerStreaming(),
      IsUnary:           !md.IsClientStreaming() && !md.IsServerStreaming(),
      IsJSONResponse:    true,
      mf:                c.mf,
    }
  }
  contextDialer := func(ctx context.Context, address string) (net.Conn, error) {
    if conn, err := c.Dialer.DialContext(ctx, "tcp", address); err == nil {
      metrics.ConnTracker <- service
      return transport.NewConnTracker(conn, &c.BaseTransportIntercept)
    } else {
      log.Printf("GRPCClient.NewGRPCClient: Failed to dial address [%s] with error: %s\n", address, err.Error())
      return nil, err
    }
  }
  c.baseDialOpts = append(c.baseDialOpts, grpc.WithContextDialer(contextDialer))
  return c, nil
}

func (g *GRPCManager) AddTarget(name, url, authority, serverName string) error {
  if c, err := NewGRPCClient(name, url, authority, serverName); err == nil {
    g.targets[name] = c
    return nil
  } else {
    return err
  }
}

func (g *GRPCManager) RemoveTarget(name string) {
  if t := g.targets[name]; t != nil {
    t.Close()
    delete(g.targets, name)
  }
}

func (g *GRPCManager) ClearTargets() {
  for _, t := range g.targets {
    t.Close()
  }
  g.targets = map[string]*GRPCClient{}
}

func (g *GRPCManager) SetConnectionParams(connectTimeout, idleTimeout, requestTimeout time.Duration) {
  g.connectTimeout = connectTimeout
  g.idleTimeout = idleTimeout
  g.requestTimeout = requestTimeout
  g.configureDialOptions()
}

func (g *GRPCManager) configureDialOptions() {
  g.baseDialOpts = []grpc.DialOption{
    grpc.WithBlock(),
    grpc.WithKeepaliveParams(keepalive.ClientParameters{
      Time:    g.idleTimeout / 2,
      Timeout: g.idleTimeout / 2,
    }),
    grpc.FailOnNonTempDialError(true),
    grpc.WithUserAgent("goto/"),
  }
}

func (g *GRPCManager) SetTargetTLS(name string, isTLS, verifyTLS bool) {
  t := g.targets[name]
  if t == nil {
    log.Printf("GRPCClient.SetTargetTLS: Target [%s] not found", name)
    return
  }
  t.SetTLS(isTLS, verifyTLS)
}

func (g *GRPCManager) ConnectTarget(name string) {
  if t := g.targets[name]; t != nil {
    t.Connect()
  } else {
    log.Printf("GRPCClient.ConnectTarget: Target [%s] not found", name)
  }
}

func (g *GRPCManager) ConnectTargetWithHeaders(name string, headers map[string]string) {
  if t := g.targets[name]; t != nil {
    t.ConnectWithHeaders(headers)
  } else {
    log.Printf("GRPCClient.ConnectTargetWithHeaders: Target [%s] not found", name)
  }
}

func (g *GRPCManager) reflect(name string) {
  t := g.targets[name]
  if t == nil {
    log.Printf("GRPCClient.reflect: Target [%s] not found", name)
    return
  }
  t.Connect()
  refClient := grpcreflect.NewClient(context.Background(), grpc_reflect.NewServerReflectionClient(t.GRPC()))
  descSource := DescriptorSourceFromServer(context.Background(), refClient)
  ListServices(descSource)
}

func (c *GRPCClient) SetConnectionParams(connectTimeout, idleTimeout, requestTimeout, keepOpen time.Duration) {
  c.ConnectTimeout = connectTimeout
  c.IdleTimeout = idleTimeout
  c.RequestTimeout = requestTimeout
  c.KeepOpen = keepOpen
  c.configureDialOptions()
}

func (c *GRPCClient) SetTLS(isTLS, verifyTLS bool) {
  c.IsTLS = isTLS
  c.VerifyTLS = verifyTLS
  c.configureTLS()
}

func (c *GRPCClient) configureTLS() {
  if c.IsTLS {
    tlsConfig := &tls.Config{
      InsecureSkipVerify: !c.VerifyTLS,
      ServerName:         c.ServerName,
      MinVersion:         c.TLSVersion,
      MaxVersion:         c.TLSVersion,
    }
    if c.VerifyTLS {
      tlsConfig.RootCAs = util.RootCAs
    }
    if cert, err := util.CreateCertificate(constants.DefaultCommonName, ""); err == nil {
      tlsConfig.Certificates = []tls.Certificate{*cert}
    }
    c.SetTLSConfig(tlsConfig)
  } else {
    c.tlsConfig = nil
    c.tlsCredentials = nil
    c.configureDialOptions()
  }
}

func (c *GRPCClient) SetTLSConfig(tlsConfig *tls.Config) {
  c.IsTLS = true
  c.tlsConfig = tlsConfig
  c.tlsCredentials = credentials.NewTLS(c.tlsConfig)
  if c.ServerName != "" {
    if err := c.tlsCredentials.OverrideServerName(c.ServerName); err != nil {
      log.Printf("GRPCClient.SetTLSConfig: Failed to override server name [%q] with error: %s", c.ServerName, err.Error())
    }
  }
  c.configureDialOptions()
}

func (c *GRPCClient) UpdateTLSConfig(serverName string, tlsVersion uint16) {
  c.ServerName = serverName
  c.TLSVersion = tlsVersion
  c.configureTLS()
}

func (c *GRPCClient) configureDialOptions() {
  c.dialOpts = append(c.baseDialOpts, grpc.WithAuthority(c.Authority),
    grpc.WithKeepaliveParams(keepalive.ClientParameters{
      Time:    c.IdleTimeout / 2,
      Timeout: c.IdleTimeout / 2,
    }))
  if c.IsTLS {
    c.dialOpts = append(c.baseDialOpts, grpc.WithTransportCredentials(c.tlsCredentials))
  } else {
    c.dialOpts = append(c.baseDialOpts, grpc.WithInsecure())
  }
}

func (c *GRPCClient) GetOpenConnectionCount() int {
  return c.ConnCount
}

func (c *GRPCClient) GetDialer() *net.Dialer {
  return &c.Dialer
}

func (c *GRPCClient) Transport() transport.TransportIntercept {
  return &c.BaseTransportIntercept
}

func (c *GRPCClient) Close() {
  if c.conn != nil {
    c.conn.Close()
  }
}

func (c *GRPCClient) HTTP() *http.Client {
  return nil
}

func (c *GRPCClient) GRPC() *grpc.ClientConn {
  return c.conn
}

func (c *GRPCClient) IsGRPC() bool {
  return true
}

func (c *GRPCClient) IsHTTP() bool {
  return false
}

func (c *GRPCClient) Connect() error {
  return c.ConnectWithHeaders(nil)
}

func (c *GRPCClient) ConnectWithHeaders(headers map[string]string) error {
  if c.conn != nil {
    connState := c.conn.GetState()
    if connState != connectivity.Connecting && connState != connectivity.Idle && connState != connectivity.Ready {
      c.conn.Close()
      c.conn = nil
    }
  }
  if c.conn == nil {
    var err error
    ctx := context.Background()
    if len(headers) > 0 {
      ctx = metadata.NewOutgoingContext(ctx, metadata.New(headers))
    }
    ctx, cancel := context.WithTimeout(ctx, c.ConnectTimeout)
    defer cancel()
    if c.conn, err = grpc.DialContext(ctx, c.URL, c.dialOpts...); err != nil {
      log.Printf("GRPCClient.ConnectWithHeaders: Failed to connect to target [%s] url [%s] with error: %s\n", c.Service, c.URL, err.Error())
      return err
    }
  }
  return nil
}

func (c *GRPCClient) Invoke(method string, headers map[string]string, payloads [][]byte) (response *GRPCResponse, err error) {
  if c.sd == nil || c.mf == nil {
    return nil, fmt.Errorf("Service [%s] not configured", c.Service)
  }
  grpcMethod := c.Methods[method]
  if grpcMethod == nil {
    return nil, fmt.Errorf("Method [%s] not found in Service [%s]", method, c.Service)
  }
  if err = c.ConnectWithHeaders(headers); err != nil {
    return
  }
  ctx := context.Background()
  if len(headers) > 0 {
    ctx = metadata.NewOutgoingContext(ctx, metadata.New(headers))
  }
  stub := grpcdynamic.NewStubWithMessageFactory(c.GRPC(), c.mf)
  return grpcMethod.Invoke(ctx, stub, payloads, c.RequestTimeout, c.KeepOpen)
}

func (m *GRPCServiceMethod) Invoke(ctx context.Context, stub grpcdynamic.Stub, payloads [][]byte, requestTimeout, keepOpen time.Duration) (response *GRPCResponse, err error) {
  ctx, cancel := context.WithTimeout(ctx, requestTimeout)
  defer cancel()
  if m.IsUnary {
    return m.InvokeUnary(ctx, stub, payloads)
  } else if m.IsClientStreaming && m.IsServerStreaming {
    return m.InvokeBidiStream(ctx, stub, payloads, keepOpen)
  } else if m.IsClientStreaming {
    return m.InvokeClientStream(ctx, stub, payloads, keepOpen)
  } else if m.IsServerStreaming {
    return m.InvokeServerStream(ctx, stub, payloads)
  }
  return nil, nil
}

func (m *GRPCServiceMethod) newInput() protoiface.MessageV1 {
  return m.mf.NewMessage(m.md.GetInputType())
}

func (m *GRPCServiceMethod) fillInput(input protoiface.MessageV1, payload []byte) error {
  if err := jsonpb.Unmarshal(bytes.NewReader(payload), input); err != nil {
    log.Printf("GRPCClient.newInput: Failed to unmarshal payload into method input type [%s] with error: %s\n", m.md.GetInputType().GetFullyQualifiedName(), err.Error())
    return err
  }
  return nil
}

func (m *GRPCServiceMethod) InvokeUnary(ctx context.Context, stub grpcdynamic.Stub, payloads [][]byte) (response *GRPCResponse, err error) {
  input := m.newInput()
  if len(payloads) > 0 {
    if err = m.fillInput(input, payloads[0]); err != nil {
      return
    }
  }
  var respHeaders metadata.MD
  var respTrailers metadata.MD
  output, err := stub.InvokeRpc(ctx, m.md, input, grpc.Trailer(&respTrailers), grpc.Header(&respHeaders))
  response = newGRPCResponse(respHeaders, respTrailers, []protoiface.MessageV1{output}, 0, 0, m.IsJSONResponse)
  if m.processResponseStatus(response, err) {
    return response, nil
  } else {
    return response, err
  }
}

func (m *GRPCServiceMethod) InvokeClientStream(ctx context.Context, stub grpcdynamic.Stub, payloads [][]byte, keepOpen time.Duration) (response *GRPCResponse, err error) {
  stream, err := stub.InvokeRpcClientStream(ctx, m.md)
  if err != nil {
    log.Printf("GRPCClient.InvokeClientStream: Method [%s] Failed to initiate Client stream with error: %s\n", m.Name, err.Error())
    return nil, err
  }
  var output protoiface.MessageV1
  output, err = m.sendClientStream(&GRPCClientStream{clientStream: stream}, payloads, keepOpen, nil)
  var respHeaders metadata.MD
  respHeaders, err = stream.Header()
  respTrailers := stream.Trailer()
  response = newGRPCResponse(respHeaders, respTrailers, []protoiface.MessageV1{output}, len(payloads), 0, m.IsJSONResponse)
  if m.processResponseStatus(response, err) {
    return response, nil
  } else {
    return response, err
  }
}

func (m *GRPCServiceMethod) InvokeServerStream(ctx context.Context, stub grpcdynamic.Stub, payloads [][]byte) (response *GRPCResponse, err error) {
  input := m.newInput()
  if len(payloads) > 0 {
    if err = m.fillInput(input, payloads[0]); err != nil {
      return
    }
  }
  stream, err := stub.InvokeRpcServerStream(ctx, m.md, input)
  if err != nil {
    log.Printf("GRPCClient.InvokeServerStream: Method [%s] Failed to initiate Server stream with error: %s\n", m.Name, err.Error())
    return
  }
  var output []protoiface.MessageV1
  output, err = m.receiveServerStream(&GRPCServerStream{serverStream: stream}, nil)
  var respHeaders metadata.MD
  respHeaders, err = stream.Header()
  respTrailers := stream.Trailer()
  response = newGRPCResponse(respHeaders, respTrailers, output, 0, len(output), m.IsJSONResponse)
  if m.processResponseStatus(response, err) {
    return response, nil
  } else {
    return response, err
  }
}

func (m *GRPCServiceMethod) InvokeBidiStream(ctx context.Context, stub grpcdynamic.Stub, payloads [][]byte, keepOpen time.Duration) (response *GRPCResponse, err error) {
  stream, err := stub.InvokeRpcBidiStream(ctx, m.md)
  if err != nil {
    log.Printf("GRPCClient.InvokeBidiStream: Method [%s] Failed to initiate Bidi stream with error: %s\n", m.Name, err.Error())
    return nil, err
  }
  var output []protoiface.MessageV1
  wg := &sync.WaitGroup{}
  wg.Add(2)
  go func() {
    if out, e := m.sendClientStream(&GRPCClientStream{bidiStream: stream}, payloads, keepOpen, wg); e != nil {
      if out != nil {
        output = append(output, out)
      }
      err = e
    }
  }()
  go func() {
    output, err = m.receiveServerStream(&GRPCServerStream{bidiStream: stream}, wg)
  }()
  wg.Wait()
  var respHeaders metadata.MD
  respHeaders, err = stream.Header()
  respTrailers := stream.Trailer()
  response = newGRPCResponse(respHeaders, respTrailers, output, len(payloads), len(output), m.IsJSONResponse)
  if m.processResponseStatus(response, err) {
    return response, nil
  } else {
    return response, err
  }
}

func (m *GRPCServiceMethod) sendClientStream(stream *GRPCClientStream, payloads [][]byte, keepOpen time.Duration, wg *sync.WaitGroup) (output protoiface.MessageV1, err error) {
  defer func() {
    if wg != nil {
      wg.Done()
    }
  }()
  startTime := time.Now()
  input := m.newInput()
  for _, payload := range payloads {
    if err = m.fillInput(input, payload); err != nil {
      log.Printf("GRPCClient.sendClientStream: Method [%s] Closing stream due to send error: %s\n", m.Name, err.Error())
      break
    }
    if err = stream.SendMsg(input); err != nil {
      log.Printf("GRPCClient.sendClientStream: Method [%s] Closing stream due to send error: %s\n", m.Name, err.Error())
      break
    }
    input.Reset()
  }
  if keepOpen > 0 {
    sleep := time.Now().Sub(startTime)
    if sleep > 0 {
      time.Sleep(sleep)
    }
  }
  if err == nil || err == io.EOF {
    output, err = stream.Close()
  }
  if err != nil {
    log.Printf("GRPCClient.sendClientStream: Method [%s] Error while sending client stream: %s\n", m.Name, err.Error())
  }
  return
}

func (m *GRPCServiceMethod) receiveServerStream(stream *GRPCServerStream, wg *sync.WaitGroup) (output []protoiface.MessageV1, err error) {
  defer func() {
    if wg != nil {
      wg.Done()
    }
  }()
  for {
    if out, e := stream.RecvMsg(); e == nil {
      if out != nil {
        output = append(output, out)
      }
    } else {
      if e != io.EOF {
        fmt.Printf("Invocation: [ERROR] %s\n", e.Error())
        err = e
      }
      break
    }
  }
  return
}

func newGRPCResponse(respHeaders metadata.MD, respTrailers metadata.MD, responsePayloads []protoiface.MessageV1, clientStreamCount, serverStreamCount int, isJSON bool) *GRPCResponse {
  r := &GRPCResponse{
    ResponseHeaders:  respHeaders,
    ResponseTrailers: respTrailers,
  }
  r.ClientStreamCount = clientStreamCount
  r.ServerStreamCount = serverStreamCount
  if isJSON {
    r.ResponsePayload = []byte(util.ToJSONText(responsePayloads))
  }
  return r
}

func (m *GRPCServiceMethod) processResponseStatus(r *GRPCResponse, err error) bool {
  if status, ok := status.FromError(err); ok {
    r.Status = int(status.Code())
    if r.Status == 0 {
      r.EquivalentHTTPStatusCode = http.StatusOK
    }
    return true
  } else {
    if err != nil {
      log.Printf("GRPCClient.processResponseStatus: Method [%s] GRPC call failed with error: %s\n", m.Name, err.Error())
    } else {
      log.Printf("GRPCClient.processResponseStatus: Method [%s] GRPC call failed without any error!\n", m.Name)
    }
    if status != nil {
      r.Status = int(status.Code())
    }
  }
  return false
}

func (s *GRPCClientStream) Header() (metadata.MD, error) {
  if s.clientStream != nil {
    return s.clientStream.Header()
  } else if s.bidiStream != nil {
    return s.bidiStream.Header()
  }
  return nil, errors.New("No stream")
}

func (s *GRPCClientStream) Trailer() metadata.MD {
  if s.clientStream != nil {
    return s.clientStream.Trailer()
  } else if s.bidiStream != nil {
    return s.bidiStream.Trailer()
  }
  return nil
}

func (s *GRPCClientStream) SendMsg(m protoiface.MessageV1) error {
  if s.clientStream != nil {
    return s.clientStream.SendMsg(m)
  } else if s.bidiStream != nil {
    return s.bidiStream.SendMsg(m)
  }
  return errors.New("No stream")
}

func (s *GRPCClientStream) Close() (protoiface.MessageV1, error) {
  if s.clientStream != nil {
    return s.clientStream.CloseAndReceive()
  } else if s.bidiStream != nil {
    return nil, s.bidiStream.CloseSend()
  }
  return nil, errors.New("No stream")
}

func (s *GRPCServerStream) Header() (metadata.MD, error) {
  if s.serverStream != nil {
    return s.serverStream.Header()
  } else if s.bidiStream != nil {
    return s.bidiStream.Header()
  }
  return nil, errors.New("No stream")
}

func (s *GRPCServerStream) Trailer() metadata.MD {
  if s.serverStream != nil {
    return s.serverStream.Trailer()
  } else if s.bidiStream != nil {
    return s.bidiStream.Trailer()
  }
  return nil
}

func (s *GRPCServerStream) RecvMsg() (protoiface.MessageV1, error) {
  if s.serverStream != nil {
    return s.serverStream.RecvMsg()
  } else if s.bidiStream != nil {
    return s.bidiStream.RecvMsg()
  }
  return nil, errors.New("No stream")
}

func reset(refClient *grpcreflect.Client, cc *grpc.ClientConn) {
  if refClient != nil {
    refClient.Reset()
    refClient = nil
  }
  if cc != nil {
    cc.Close()
    cc = nil
  }
}

func ListServices(descSource DescriptorSource) error {
  svcs, err := descSource.ListServices()
  if err != nil {
    return err
  }
  sort.Strings(svcs)
  if len(svcs) == 0 {
    fmt.Println("(No services)")
  } else {
    for _, svc := range svcs {
      fmt.Printf("%s\n", svc)
    }
  }
  return nil
}

func ListMethods(source DescriptorSource, serviceName string) error {
  dsc, err := source.FindSymbol(serviceName)
  if err != nil {
    return err
  }
  if sd, ok := dsc.(*desc.ServiceDescriptor); !ok {
    return errors.New(fmt.Sprintf("Service [%s] not found", serviceName))
  } else {
    methods := make([]string, 0, len(sd.GetMethods()))
    for _, method := range sd.GetMethods() {
      methods = append(methods, method.GetFullyQualifiedName())
      fmt.Printf("%s\n", method.GetFullyQualifiedName())
    }
    sort.Strings(methods)
  }
  return nil
}

var base64Codecs = []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding}

func decode(val string) (string, error) {
  var firstErr error
  var b []byte
  // we are lenient and can accept any of the flavors of base64 encoding
  for _, d := range base64Codecs {
    var err error
    b, err = d.DecodeString(val)
    if err != nil {
      if firstErr == nil {
        firstErr = err
      }
      continue
    }
    return string(b), nil
  }
  return "", firstErr
}

type serverSource struct {
  client *grpcreflect.Client
}

func (ss serverSource) ListServices() ([]string, error) {
  svcs, err := ss.client.ListServices()
  return svcs, reflectionSupport(err)
}

func (ss serverSource) FindSymbol(fullyQualifiedName string) (desc.Descriptor, error) {
  file, err := ss.client.FileContainingSymbol(fullyQualifiedName)
  if err != nil {
    return nil, reflectionSupport(err)
  }
  d := file.FindSymbol(fullyQualifiedName)
  if d == nil {
    return nil, errors.New(fmt.Sprintf("Symbol [%s] not found", fullyQualifiedName))
  }
  return d, nil
}

func (ss serverSource) AllExtensionsForType(typeName string) ([]*desc.FieldDescriptor, error) {
  var exts []*desc.FieldDescriptor
  nums, err := ss.client.AllExtensionNumbersForType(typeName)
  if err != nil {
    return nil, reflectionSupport(err)
  }
  for _, fieldNum := range nums {
    ext, err := ss.client.ResolveExtension(typeName, fieldNum)
    if err != nil {
      return nil, reflectionSupport(err)
    }
    exts = append(exts, ext)
  }
  return exts, nil
}

func DescriptorSourceFromServer(_ context.Context, refClient *grpcreflect.Client) DescriptorSource {
  return serverSource{client: refClient}
}

func reflectionSupport(err error) error {
  if err == nil {
    return nil
  }
  if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
    return errors.New("server does not support the reflection API")
  }
  return err
}
func parseSymbol(svcAndMethod string) (string, string) {
  pos := strings.LastIndex(svcAndMethod, "/")
  if pos < 0 {
    pos = strings.LastIndex(svcAndMethod, ".")
    if pos < 0 {
      return "", ""
    }
  }
  return svcAndMethod[:pos], svcAndMethod[pos+1:]
}

/**
 * Copyright 2022 uk
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

package invocation

import (
  "crypto/tls"
  "encoding/base64"
  "fmt"
  "goto/pkg/global"
  "goto/pkg/grpc"
  "goto/pkg/metrics"
  "goto/pkg/transport"
  "goto/pkg/util"
  "io"
  "log"
  "net/http"
  "net/url"
  "regexp"
  "strings"
  "sync"
  "sync/atomic"
  "time"
)

type InvocationSpec struct {
  Name                 string         `json:"name"`
  Protocol             string         `json:"protocol"`
  Method               string         `json:"method"`
  Service              string         `json:"service"`
  URL                  string         `json:"url"`
  BURLS                []string       `json:"burls"`
  Headers              RequestHeaders `json:"headers"`
  Body                 string         `json:"body"`
  AutoPayload          string         `json:"autoPayload"`
  Replicas             int            `json:"replicas"`
  RequestCount         int            `json:"requestCount"`
  WarmupCount          int            `json:"warmupCount"`
  InitialDelay         string         `json:"initialDelay"`
  Delay                string         `json:"delay"`
  Retries              int            `json:"retries"`
  RetryDelay           string         `json:"retryDelay"`
  RetriableStatusCodes []int          `json:"retriableStatusCodes"`
  KeepOpen             string         `json:"keepOpen"`
  SendID               bool           `json:"sendID"`
  ConnTimeout          string         `json:"connTimeout"`
  ConnIdleTimeout      string         `json:"connIdleTimeout"`
  RequestTimeout       string         `json:"requestTimeout"`
  AutoInvoke           bool           `json:"autoInvoke"`
  Fallback             bool           `json:"fallback"`
  AB                   bool           `json:"ab"`
  Random               bool           `json:"random"`
  StreamPayload        []string       `json:"streamPayload"`
  StreamDelay          string         `json:"streamDelay"`
  Binary               bool           `json:"binary"`
  CollectResponse      bool           `json:"collectResponse"`
  TrackPayload         bool           `json:"trackPayload"`
  Assertions           Assertions     `json:"assertions"`
  AutoUpgrade          bool           `json:"autoUpgrade"`
  VerifyTLS            bool           `json:"verifyTLS"`
  TLS                  bool           `json:"tls"`
  BodyReader           io.Reader      `json:"-"`
  httpVersionMajor     int
  httpVersionMinor     int
  tcp                  bool
  grpc                 bool
  http                 bool
  h2                   bool
  authority            string
  connTimeoutD         time.Duration
  connIdleTimeoutD     time.Duration
  requestTimeoutD      time.Duration
  initialDelayD        time.Duration
  delayD               time.Duration
  streamDelayD         time.Duration
  retryDelayD          time.Duration
  keepOpenD            time.Duration
  autoPayloadSize      int
}

type RequestHeaders [][]string

type Assert struct {
  StatusCode    int               `json:"statusCode"`
  PayloadSize   int               `json:"payloadSize"`
  Payload       string            `json:"payload"`
  Headers       map[string]string `json:"headers"`
  Retries       int               `json:"retries"`
  FailedURL     string            `json:"failedURL"`
  SuccessURL    string            `json:"successURL"`
  headersRegexp map[string]*regexp.Regexp
  payload       []byte
}

type Assertions []*Assert

type InvocationTracker struct {
  ID       uint32              `json:"id"`
  Target   *InvocationSpec     `json:"target"`
  Status   *InvocationStatus   `json:"status"`
  Payloads [][]byte            `json:"-"`
  Channels *InvocationChannels `json:"-"`
  CustomID int                 `json:"customID"`
  client   *InvocationClient
}

type InvocationChannels struct {
  StopChannel   chan bool
  DoneChannel   chan bool
  ResultChannel chan *InvocationResult
  Sinks         []ResultSink
  Lock          sync.RWMutex
}

type TargetInvocations struct {
  trackers map[uint32]*InvocationTracker
  lock     sync.RWMutex
}

type InvocationClient struct {
  tracker         *InvocationTracker
  transportClient transport.TransportClient
  lock            sync.RWMutex
}

type ResultSink func(*InvocationResult)
type ResultSinkFactory func(*InvocationTracker) ResultSink

const (
  maxIdleClientDuration    = 60 * time.Second
  clientConnReportDuration = 5 * time.Second
)

var (
  invocationCounter uint32
  hostLabel         string
  chanStopCleanup   = make(chan bool, 2)
  activeInvocations = map[uint32]*InvocationTracker{}
  activeTargets     = map[string]*TargetInvocations{}
  targetClients     = map[string]transport.TransportClient{}
  invocationsLock   sync.RWMutex
)

func Startup() {
  hostLabel = util.GetHostLabel()
  go monitorHttpClients()
}

func Shutdown() {
  chanStopCleanup <- true
}

func ValidateSpec(spec *InvocationSpec) error {
  var err error
  if err = spec.validateTrafficConfig(); err != nil {
    return err
  }
  if err = spec.validateConnectionAndRequestConfigs(); err != nil {
    return err
  }
  if err = spec.validatePayload(); err != nil {
    return err
  }
  spec.processProtocol()
  spec.processAuthority()
  if spec.Assertions != nil {
    spec.prepareAssertions()
  }
  return nil
}

func (spec *InvocationSpec) processProtocol() {
  if spec.Protocol != "" {
    lowerProto := strings.ToLower(spec.Protocol)
    if strings.EqualFold(lowerProto, "tcp") {
      spec.tcp = true
      spec.httpVersionMajor = 0
      spec.httpVersionMinor = 0
    } else if strings.EqualFold(lowerProto, "grpc") {
      spec.grpc = true
      spec.httpVersionMajor = 2
      spec.httpVersionMinor = 0
    } else if major, minor, ok := http.ParseHTTPVersion(spec.Protocol); ok {
      if major == 1 && (minor == 0 || minor == 1) {
        spec.httpVersionMajor = major
        spec.httpVersionMinor = minor
      } else if major == 2 {
        spec.httpVersionMajor = major
        spec.httpVersionMinor = 0
      }
    } else if strings.EqualFold(spec.Protocol, "HTTP/2") || strings.EqualFold(spec.Protocol, "HTTP/2.0") ||
              strings.EqualFold(spec.Protocol, "H2") || strings.EqualFold(spec.Protocol, "H2C") {
      spec.httpVersionMajor = 2
      spec.httpVersionMinor = 0
    }
  }
  if !spec.tcp && spec.httpVersionMajor == 0 {
    spec.httpVersionMajor = 1
    spec.httpVersionMinor = 1
    spec.Protocol = fmt.Sprintf("HTTP/%d.%d", spec.httpVersionMajor, spec.httpVersionMinor)
  } else if spec.httpVersionMajor == 2 {
    spec.h2 = true
  }
  if !spec.tcp && !spec.grpc {
    spec.http = true
  }
  if strings.HasPrefix(strings.ToLower(spec.URL), "https") {
    spec.TLS = true
  }
  if spec.http && !strings.HasPrefix(spec.URL, "http") {
    spec.URL = "http://" + spec.URL
  }
}

func (spec *InvocationSpec) processAuthority() {
  authority := ""
  for _, h := range spec.Headers {
    if strings.EqualFold(h[0], "host") {
      authority = h[1]
    }
  }
  if authority == "" {
    if u, e := url.Parse(spec.URL); e == nil {
      authority = u.Host
    }
  }
  if authority != "" {
    spec.authority = authority
  }
}

func (spec *InvocationSpec) validatePayload() error {
  if spec.AutoPayload != "" {
    spec.autoPayloadSize = util.ParseSize(spec.AutoPayload)
    if spec.autoPayloadSize <= 0 {
      return fmt.Errorf("Invalid AutoPayload, must be a valid size like 100, 10K, etc.")
    }
  }
  if spec.StreamDelay != "" {
    var err error
    if spec.streamDelayD, err = time.ParseDuration(spec.StreamDelay); err != nil {
      return fmt.Errorf("Invalid delay")
    }
  } else {
    spec.streamDelayD = 10 * time.Millisecond
    spec.StreamDelay = "10ms"
  }
  return nil
}

func (spec *InvocationSpec) validateConnectionAndRequestConfigs() error {
  var err error
  if spec.ConnTimeout != "" {
    if spec.connTimeoutD, err = time.ParseDuration(spec.ConnTimeout); err != nil {
      return fmt.Errorf("Invalid ConnectionTimeout")
    }
  } else {
    spec.connTimeoutD = 5 * time.Second
    spec.ConnTimeout = "5s"
  }
  if spec.ConnIdleTimeout != "" {
    if spec.connIdleTimeoutD, err = time.ParseDuration(spec.ConnIdleTimeout); err != nil {
      return fmt.Errorf("Invalid ConnectionIdleTimeout")
    }
  } else {
    spec.connIdleTimeoutD = 5 * time.Minute
    spec.ConnIdleTimeout = "5m"
  }
  if spec.RequestTimeout != "" {
    if spec.requestTimeoutD, err = time.ParseDuration(spec.RequestTimeout); err != nil {
      return fmt.Errorf("Invalid RequestIdleTimeout")
    }
  } else {
    spec.requestTimeoutD = 30 * time.Second
    spec.RequestTimeout = "30s"
  }
  return nil
}

func (spec *InvocationSpec) validateTrafficConfig() error {
  var err error
  if spec.Name == "" {
    return fmt.Errorf("Name is required")
  }
  if spec.Method == "" {
    return fmt.Errorf("Method is required")
  }
  if spec.URL == "" {
    return fmt.Errorf("URL is required")
  }
  if (spec.AB || spec.Fallback || spec.Random) && len(spec.BURLS) == 0 {
    return fmt.Errorf("At least one B-URL is required for Fallback, ABMode or RandomMode")
  }
  if spec.Replicas < 0 {
    return fmt.Errorf("Invalid replicas")
  } else if spec.Replicas == 0 {
    spec.Replicas = 1
  }
  if spec.RequestCount < 0 {
    return fmt.Errorf("Invalid requestCount")
  } else if spec.RequestCount == 0 {
    spec.RequestCount = 1
  }
  if spec.InitialDelay != "" {
    if spec.initialDelayD, err = time.ParseDuration(spec.InitialDelay); err != nil {
      return fmt.Errorf("Invalid initial delay")
    }
  }
  if spec.Delay != "" {
    if spec.delayD, err = time.ParseDuration(spec.Delay); err != nil {
      return fmt.Errorf("Invalid delay")
    }
  } else {
    spec.delayD = 10 * time.Millisecond
    spec.Delay = "10ms"
  }
  if spec.RetryDelay != "" {
    if spec.retryDelayD, err = time.ParseDuration(spec.RetryDelay); err != nil {
      return fmt.Errorf("Invalid retryDelay")
    }
  } else {
    spec.retryDelayD = 1 * time.Second
    spec.RetryDelay = "1s"
  }
  if spec.KeepOpen != "" {
    if spec.keepOpenD, err = time.ParseDuration(spec.KeepOpen); err != nil {
      return fmt.Errorf("Invalid keepOpen")
    }
  }
  return nil
}

func (spec *InvocationSpec) prepareAssertions() {
  for _, a := range spec.Assertions {
    if len(a.Payload) > 0 {
      if spec.Binary {
        if b, err := base64.RawStdEncoding.DecodeString(a.Payload); err == nil {
          a.payload = b
        } else {
          a.payload = []byte(a.Payload)
        }
      } else {
        a.payload = []byte(a.Payload)
      }
      a.PayloadSize = len(a.payload)
    }
    if a.PayloadSize > 0 {
      spec.CollectResponse = true
      spec.TrackPayload = true
    }
    if len(a.Headers) > 0 {
      a.headersRegexp = map[string]*regexp.Regexp{}
      for h, hv := range a.Headers {
        if h != "" {
          h := strings.ToLower(h)
          a.headersRegexp[h] = nil
          if hv != "" {
            a.headersRegexp[h] = regexp.MustCompile("(?i)" + hv)
          }
        }
      }
    }
  }
}

func GetActiveInvocations() map[string]map[uint32]*InvocationStatus {
  results := map[string]map[uint32]*InvocationStatus{}
  currentActiveTargets := map[string]*TargetInvocations{}
  invocationsLock.RLock()
  for target, targetInvocations := range activeTargets {
    currentActiveTargets[target] = targetInvocations
  }
  invocationsLock.RUnlock()
  for target, targetInvocations := range currentActiveTargets {
    results[target] = map[uint32]*InvocationStatus{}
    targetInvocations.lock.RLock()
    for _, tracker := range targetInvocations.trackers {
      results[target][tracker.ID] = tracker.Status
    }
    targetInvocations.lock.RUnlock()
  }
  return results
}

func IsAnyTargetActive(targets []string) bool {
  invocationsLock.RLock()
  defer invocationsLock.RUnlock()
  for _, target := range targets {
    if activeTargets[target] != nil {
      return true
    }
  }
  return false
}

func StopTarget(target string) {
  invocationsLock.RLock()
  targetInvocations := activeTargets[target]
  invocationsLock.RUnlock()
  if targetInvocations != nil {
    targetInvocations.lock.RLock()
    for _, tracker := range targetInvocations.trackers {
      done := false
      tracker.Channels.Lock.RLock()
      doneChannel := tracker.Channels.DoneChannel
      stopChannel := tracker.Channels.StopChannel
      stopRequested := tracker.Status.StopRequested
      stopped := tracker.Status.Stopped
      tracker.Channels.Lock.RUnlock()
      select {
      case done = <-doneChannel:
      default:
      }
      if !done {
        if !stopRequested && !stopped {
          stopChannel <- true
        }
      }
    }
    targetInvocations.lock.RUnlock()
  }
}

func processStopRequest(tracker *InvocationTracker) {
  for !tracker.Status.StopRequested && !tracker.Status.Stopped {
    tracker.Channels.Lock.RLock()
    stopChannel := tracker.Channels.StopChannel
    tracker.Channels.Lock.RUnlock()
    if stopChannel != nil {
      stopRequested := false
      select {
      case stopRequested = <-stopChannel:
      default:
      }
      if stopRequested {
        tracker.Status.StopRequested = true
        stopped := tracker.Status.Stopped
        if stopped {
          if global.EnableInvocationLogs {
            log.Printf("[%s]: Invocation[%d]: Received stop request for target [%s] that is already stopped\n", hostLabel, tracker.ID, tracker.Target.Name)
          }
        } else {
          remaining := (tracker.Target.RequestCount * tracker.Target.Replicas) - tracker.Status.CompletedRequests
          if global.EnableInvocationLogs {
            log.Printf("[%s]: Invocation[%d]: Received stop request for target [%s] with remaining requests [%d]\n", hostLabel, tracker.ID, tracker.Target.Name, remaining)
          }
        }
      } else {
        time.Sleep(2 * time.Second)
      }
    } else {
      break
    }
  }
}

func ResetActiveInvocations() {
  invocationsLock.Lock()
  invocationCounter = 0
  activeInvocations = map[uint32]*InvocationTracker{}
  activeTargets = map[string]*TargetInvocations{}
  invocationsLock.Unlock()
}

func RegisterInvocation(target *InvocationSpec, sinks ...ResultSinkFactory) (*InvocationTracker, error) {
  return newTracker(atomic.AddUint32(&invocationCounter, 1), target, sinks...)
}

func newTracker(id uint32, target *InvocationSpec, sinks ...ResultSinkFactory) (*InvocationTracker, error) {
  tracker := &InvocationTracker{
    ID:     id,
    Target: target,
    Channels: &InvocationChannels{
      StopChannel:   make(chan bool, 20),
      DoneChannel:   make(chan bool, 2),
      ResultChannel: make(chan *InvocationResult, 200),
    },
  }
  tracker.client = &InvocationClient{tracker: tracker}
  tracker.Status = &InvocationStatus{tracker: tracker, lastStatusCode: -1}
  for _, sinkFactory := range sinks {
    if sink := sinkFactory(tracker); sink != nil {
      tracker.Channels.Sinks = append(tracker.Channels.Sinks, sink)
    }
  }
  if target.http {
    tracker.client.transportClient = getHttpClientForTarget(tracker)
  } else if target.grpc {
    tracker.client.transportClient = getGrpcClientForTarget(tracker)
  }
  if len(target.StreamPayload) > 0 {
    for _, p := range target.StreamPayload {
      if target.Binary {
        if b, err := base64.RawStdEncoding.DecodeString(p); err == nil {
          tracker.Payloads = append(tracker.Payloads, b)
        } else {
          tracker.Payloads = append(tracker.Payloads, []byte(p))
        }
      } else {
        tracker.Payloads = append(tracker.Payloads, []byte(p))
      }
    }
    target.Body = ""
  } else if target.autoPayloadSize > 0 {
    tracker.Payloads = [][]byte{util.GenerateRandomPayload(target.autoPayloadSize)}
    target.Body = ""
  } else if target.Body != "" {
    tracker.Payloads = [][]byte{[]byte(target.Body)}
  } else if target.BodyReader != nil {
    tracker.Payloads = [][]byte{util.ReadBytes(target.BodyReader)}
    target.BodyReader = nil
  } else {
    tracker.Payloads = [][]byte{nil}
  }
  if tracker.client.transportClient == nil {
    return tracker, fmt.Errorf("Failed to create client for target [%s]", target.Name)
  }
  return tracker, nil
}

func tlsConfig(host string, verifyCert bool) *tls.Config {
  cfg := &tls.Config{
    ServerName:         host,
    InsecureSkipVerify: !verifyCert,
  }
  if util.RootCAs != nil {
    cfg.RootCAs = util.RootCAs
  }
  return cfg
}

func getHttpClientForTarget(tracker *InvocationTracker) transport.TransportClient {
  target := tracker.Target
  invocationsLock.RLock()
  client := targetClients[target.Name]
  invocationsLock.RUnlock()
  if client == nil || client.HTTP() == nil {
    client = transport.CreateHTTPClient(target.Name, target.h2, target.AutoUpgrade, target.TLS, target.authority, 0,
      target.requestTimeoutD, target.connTimeoutD, target.connIdleTimeoutD, metrics.ConnTracker)
    invocationsLock.Lock()
    targetClients[target.Name] = client
    invocationsLock.Unlock()
  }
  return client
}

func getGrpcClientForTarget(tracker *InvocationTracker) transport.TransportClient {
  target := tracker.Target
  invocationsLock.RLock()
  client := targetClients[target.Name]
  invocationsLock.RUnlock()
  if client == nil {
    if grpcClient, err := grpc.NewGRPCClient(target.Service, target.URL, target.authority, target.authority); err == nil {
      grpcClient.SetTLS(target.TLS, target.VerifyTLS)
      grpcClient.SetConnectionParams(target.connTimeoutD, target.connIdleTimeoutD, target.requestTimeoutD, target.keepOpenD)
      client = grpcClient
      invocationsLock.Lock()
      targetClients[target.Name] = client
      invocationsLock.Unlock()
    } else {
      log.Println(err.Error())
    }
  }
  return client
}

func (tracker *InvocationTracker) activate() {
  invocationsLock.Lock()
  activeInvocations[tracker.ID] = tracker
  targetInvocations := activeTargets[tracker.Target.Name]
  if targetInvocations == nil {
    targetInvocations = &TargetInvocations{trackers: map[uint32]*InvocationTracker{}}
    activeTargets[tracker.Target.Name] = targetInvocations
  }
  invocationsLock.Unlock()
  targetInvocations.lock.Lock()
  targetInvocations.trackers[tracker.ID] = tracker
  targetInvocations.lock.Unlock()
}

func (tracker *InvocationTracker) deactivate() {
  if tracker.Channels != nil {
    tracker.Channels.Done()
    tracker.Channels.ReadStopRequest()
  }
  tracker.reportRepeatedResponse()
  tracker.CloseChannels()
  tracker.client.close()
  tracker.client = nil
  tracker.Status.Closed = true
  invocationsLock.Lock()
  delete(activeInvocations, tracker.ID)
  targetInvocations := activeTargets[tracker.Target.Name]
  invocationsLock.Unlock()
  if targetInvocations != nil {
    targetInvocations.lock.Lock()
    delete(targetInvocations.trackers, tracker.ID)
    size := len(targetInvocations.trackers)
    targetInvocations.lock.Unlock()
    if size == 0 {
      invocationsLock.Lock()
      delete(activeTargets, tracker.Target.Name)
      invocationsLock.Unlock()
    }
  }
  tracker.logFinishedInvocation((tracker.Target.RequestCount * tracker.Target.Replicas) - tracker.Status.CompletedRequests)
}

func RemoveHttpClientForTarget(target string) {
  invocationsLock.Lock()
  if client := targetClients[target]; client != nil {
    client.Close()
    delete(targetClients, target)
  }
  invocationsLock.Unlock()
}

func monitorHttpClients() {
  watchListForRemoval := map[string]int{}
  for {
    select {
    case <-chanStopCleanup:
      return
    case <-time.Tick(clientConnReportDuration):
      invocationsLock.RLock()
      for target, client := range targetClients {
        if client != nil && client.Transport() != nil {
          metrics.UpdateActiveTargetConnCount(target, client.Transport().GetOpenConnectionCount())
        } else {
          metrics.UpdateActiveTargetConnCount(target, 1)
        }
      }
      invocationsLock.RUnlock()
    case <-time.Tick(maxIdleClientDuration):
      invocationsLock.Lock()
      for target, client := range targetClients {
        if activeTargets[target] == nil && (client.Transport() == nil || client.Transport().GetOpenConnectionCount() > 0) {
          if watchListForRemoval[target] < 3 {
            watchListForRemoval[target]++
          } else {
            client.Close()
            delete(targetClients, target)
            delete(watchListForRemoval, target)
          }
        } else if watchListForRemoval[target] > 0 {
          delete(watchListForRemoval, target)
        }
      }
      invocationsLock.Unlock()
    }
  }
}

func (t *InvocationTracker) CloseChannels() {
  if t.Channels != nil {
    t.Channels.Close()
  }
}

func (t *InvocationTracker) AddSink(sink ResultSink) {
  if t.Channels != nil {
    t.Channels.AddSink(sink)
  }
}

func (t *InvocationTracker) Stop() {
  if t.Channels != nil {
    t.Channels.Stop()
  }
}

func (t *InvocationTracker) IsStopped() {
  if t.Channels != nil {
    t.Channels.Stop()
  }
}

func (c *InvocationChannels) AddSink(sink ResultSink) {
  c.Lock.Lock()
  defer c.Lock.Unlock()
  c.Sinks = append(c.Sinks, sink)
}

func (c *InvocationChannels) Done() {
  c.Lock.Lock()
  defer c.Lock.Unlock()
  if c.DoneChannel != nil {
    c.DoneChannel <- true
  }
}

func (c *InvocationChannels) ReadStopRequest() {
  c.Lock.RLock()
  defer c.Lock.RUnlock()
  if c.StopChannel != nil {
    select {
    case <-c.StopChannel:
    default:
    }
  }
}

func (c *InvocationChannels) Stop() {
  c.Lock.Lock()
  defer c.Lock.Unlock()
  if c.StopChannel != nil {
    c.StopChannel <- true
  }
}

func (c *InvocationChannels) Close() {
  c.Lock.Lock()
  defer c.Lock.Unlock()
  if c.StopChannel != nil {
    close(c.StopChannel)
    c.StopChannel = nil
  }
  if c.DoneChannel != nil {
    close(c.DoneChannel)
    c.DoneChannel = nil
  }
  if c.ResultChannel != nil {
    close(c.ResultChannel)
    c.ResultChannel = nil
  }
}

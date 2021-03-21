package invocation

import (
  "bytes"
  "crypto/tls"
  "crypto/x509"
  "encoding/base64"
  "fmt"
  "goto/pkg/events"
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "io"
  "io/ioutil"
  "log"
  "net"
  "net/http"
  "path/filepath"
  "regexp"
  "strconv"
  "strings"
  "sync"
  "sync/atomic"
  "time"

  "github.com/google/uuid"
  "golang.org/x/net/http2"
)

type InvocationSpec struct {
  Name                 string       `json:"name"`
  Protocol             string       `json:"protocol"`
  Method               string       `json:"method"`
  URL                  string       `json:"url"`
  BURLS                []string     `json:"burls"`
  Headers              [][]string   `json:"headers"`
  Body                 string       `json:"body"`
  AutoPayload          string       `json:"autoPayload"`
  Replicas             int          `json:"replicas"`
  RequestCount         int          `json:"requestCount"`
  InitialDelay         string       `json:"initialDelay"`
  Delay                string       `json:"delay"`
  Retries              int          `json:"retries"`
  RetryDelay           string       `json:"retryDelay"`
  RetriableStatusCodes []int        `json:"retriableStatusCodes"`
  KeepOpen             string       `json:"keepOpen"`
  SendID               bool         `json:"sendID"`
  ConnTimeout          string       `json:"connTimeout"`
  ConnIdleTimeout      string       `json:"connIdleTimeout"`
  RequestTimeout       string       `json:"requestTimeout"`
  AutoInvoke           bool         `json:"autoInvoke"`
  Fallback             bool         `json:"fallback"`
  AB                   bool         `json:"ab"`
  Random               bool         `json:"random"`
  StreamPayload        []string     `json:"streamPayload"`
  StreamDelay          string       `json:"streamDelay"`
  Binary               bool         `json:"binary"`
  CollectResponse      bool         `json:"collectResponse"`
  Expectation          *Expectation `json:"expectation"`
  AutoUpgrade          bool         `json:"autoUpgrade"`
  VerifyTLS            bool         `json:"verifyTLS"`
  BodyReader           io.Reader    `json:"-"`
  httpVersionMajor     int
  httpVersionMinor     int
  tcp                  bool
  grpc                 bool
  http                 bool
  tls                  bool
  connTimeoutD         time.Duration
  connIdleTimeoutD     time.Duration
  requestTimeoutD      time.Duration
  initialDelayD        time.Duration
  delayD               time.Duration
  streamDelayD         time.Duration
  retryDelayD          time.Duration
  keepOpenD            time.Duration
  autoPayloadSize      int
  payloads             [][]byte
}

type Expectation struct {
  StatusCode    int               `json:"statusCode"`
  PayloadLength int               `json:"payloadLength"`
  Payload       string            `json:"payload"`
  Headers       map[string]string `json:"headers"`
  headersRegexp map[string]*regexp.Regexp
  payload       []byte
}

type InvocationStatus struct {
  CompletedReplicas int  `json:"completedReplicas"`
  SuccessCount      int  `json:"successCount"`
  FailureCount      int  `json:"failureCount"`
  RetriesCount      int  `json:"retriesCount"`
  ABCount           int  `json:"abCount"`
  TotalRequests     int  `json:"totalRequests"`
  StopRequested     bool `json:"stopRequested"`
  Stopped           bool `json:"stopped"`
  Closed            bool `json:"closed"`
  httpClient        *HTTPClientTracker
}

type InvocationResult struct {
  TargetName          string                 `json:"targetName"`
  TargetID            string                 `json:"targetID"`
  Status              string                 `json:"status"`
  StatusCode          int                    `json:"statusCode"`
  RequestPayloadSize  int                    `json:"requestPayloadSize"`
  ResponsePayloadSize int                    `json:"responsePayloadSize"`
  FirstByteInAt       string                 `json:"firstByteInAt"`
  LastByteInAt        string                 `json:"lastByteInAt"`
  FirstByteOutAt      string                 `json:"firstByteOutAt"`
  LastByteOutAt       string                 `json:"lastByteOutAt"`
  Retries             int                    `json:"retries"`
  URL                 string                 `json:"url"`
  URI                 string                 `json:"uri"`
  RequestID           string                 `json:"requestID"`
  Headers             map[string][]string    `json:"headers"`
  RetryURL            string                 `json:"retryURL"`
  LastRetryReason     string                 `json:"lastRetryReason"`
  TookNanos           int                    `json:"tookNanos"`
  Errors              map[string]interface{} `json:"errors"`
  Data                []byte                 `json:"-"`
}

type InvocationLog struct {
  Host       string
  Invocation uint32
  Target     string
  URL        string
  Result     *InvocationResult
}

type ResultSink func(*InvocationResult)
type ResultSinkFactory func(*InvocationTracker) ResultSink

type InvocationTracker struct {
  ID              uint32                 `json:"id"`
  Target          *InvocationSpec        `json:"target"`
  Status          *InvocationStatus      `json:"status"`
  StopChannel     chan bool              `json:"-"`
  DoneChannel     chan bool              `json:"-"`
  ResultChannel   chan *InvocationResult `json:"-"`
  sinks           []ResultSink
  lastStatusCode  int
  lastStatusCount int
  lastError       string
  lastErrorCount  int
  lock            sync.RWMutex
}

type TargetInvocations struct {
  trackers map[uint32]*InvocationTracker
  lock     sync.RWMutex
}

const (
  maxIdleClientDuration    = 60 * time.Second
  clientConnReportDuration = 5 * time.Second
)

var (
  hostLabel         string
  invocationCounter uint32
  activeInvocations = map[uint32]*InvocationTracker{}
  activeTargets     = map[string]*TargetInvocations{}
  targetClients     = map[string]*HTTPClientTracker{}
  chanStopCleanup   = make(chan bool, 2)
  rootCAs           *x509.CertPool
  caCert            []byte
  invocationsLock   sync.RWMutex
)

func Startup() {
  loadCerts()
  hostLabel = util.GetHostLabel()
  go monitorHttpClients()
}

func Shutdown() {
  chanStopCleanup <- true
}

func ValidateSpec(spec *InvocationSpec) error {
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
  if strings.Contains(strings.ToLower(spec.URL), "https") {
    spec.tls = true
  }
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
    } else if strings.EqualFold(strings.ToUpper(spec.Protocol), "HTTP/2") {
      spec.httpVersionMajor = 2
      spec.httpVersionMinor = 0
    }
  }
  if !spec.tcp && spec.httpVersionMajor == 0 {
    spec.httpVersionMajor = 1
    spec.httpVersionMinor = 1
    spec.Protocol = fmt.Sprintf("HTTP/%d.%d", spec.httpVersionMajor, spec.httpVersionMinor)
  }
  if !spec.tcp && !spec.grpc {
    spec.http = true
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
  var err error
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
  if spec.KeepOpen != "" {
    if spec.keepOpenD, err = time.ParseDuration(spec.KeepOpen); err != nil {
      return fmt.Errorf("Invalid keepOpen")
    }
  }
  if spec.AutoPayload != "" {
    spec.autoPayloadSize = util.ParseSize(spec.AutoPayload)
    if spec.autoPayloadSize <= 0 {
      return fmt.Errorf("Invalid AutoPayload, must be a valid size like 100, 10K, etc.")
    }
  }
  if spec.StreamDelay != "" {
    if spec.streamDelayD, err = time.ParseDuration(spec.StreamDelay); err != nil {
      return fmt.Errorf("Invalid delay")
    }
  } else {
    spec.streamDelayD = 10 * time.Millisecond
    spec.StreamDelay = "10ms"
  }
  if spec.Expectation != nil {
    if len(spec.Expectation.Payload) > 0 {
      if spec.Binary {
        if b, err := base64.RawStdEncoding.DecodeString(spec.Expectation.Payload); err == nil {
          spec.Expectation.payload = b
        } else {
          spec.Expectation.payload = []byte(spec.Expectation.Payload)
        }
      } else {
        spec.Expectation.payload = []byte(spec.Expectation.Payload)
      }
      spec.Expectation.PayloadLength = len(spec.Expectation.payload)
    }
    if len(spec.Expectation.Headers) > 0 {
      spec.Expectation.headersRegexp = map[string]*regexp.Regexp{}
      for h, hv := range spec.Expectation.Headers {
        if h != "" {
          h := strings.ToLower(h)
          spec.Expectation.headersRegexp[h] = nil
          if hv != "" {
            spec.Expectation.headersRegexp[h] = regexp.MustCompile("(?i)" + hv)
          }
        }
      }
    }
  }
  return nil
}

func PrepareAutoPayload(i *InvocationSpec) {
  if i.autoPayloadSize > 0 {
    i.payloads = [][]byte{util.GenerateRandomPayload(i.autoPayloadSize)}
    i.Body = ""
  }
}

func ResetActiveInvocations() {
  invocationsLock.Lock()
  invocationCounter = 0
  activeInvocations = map[uint32]*InvocationTracker{}
  activeTargets = map[string]*TargetInvocations{}
  invocationsLock.Unlock()
}

func StoreCACert(cert []byte) {
  invocationsLock.Lock()
  caCert = cert
  rootCAs.AppendCertsFromPEM(cert)
  invocationsLock.Unlock()
}

func RemoveCACert() {
  invocationsLock.Lock()
  caCert = nil
  loadCerts()
  invocationsLock.Unlock()
}

func loadCerts() {
  rootCAs = x509.NewCertPool()
  found := false
  if certs, err := filepath.Glob(global.CertPath + "/*.crt"); err == nil {
    for _, c := range certs {
      if cert, err := ioutil.ReadFile(c); err == nil {
        rootCAs.AppendCertsFromPEM(cert)
        found = true
      }
    }
  }
  if certs, err := filepath.Glob(global.CertPath + "/*.pem"); err == nil {
    for _, c := range certs {
      if cert, err := ioutil.ReadFile(c); err == nil {
        rootCAs.AppendCertsFromPEM(cert)
        found = true
      }
    }
  }
  if !found {
    rootCAs = nil
  }
}

func tlsConfig(host string, verifyCert bool) *tls.Config {
  cfg := &tls.Config{
    ServerName:         host,
    InsecureSkipVerify: !verifyCert,
  }
  if rootCAs != nil {
    cfg.RootCAs = rootCAs
  }
  return cfg
}

func httpTransport(target *InvocationSpec) (http.RoundTripper, *TransportTracker) {
  var transport http.RoundTripper
  var tracker *TransportTracker
  if target.httpVersionMajor == 1 {
    ht := NewHTTPTransportTracker(&http.Transport{
      MaxIdleConns:          200,
      MaxIdleConnsPerHost:   100,
      IdleConnTimeout:       target.connIdleTimeoutD,
      Proxy:                 http.ProxyFromEnvironment,
      DisableCompression:    true,
      ExpectContinueTimeout: target.requestTimeoutD,
      ResponseHeaderTimeout: target.requestTimeoutD,
      DialContext: (&net.Dialer{
        Timeout:   target.connTimeoutD,
        KeepAlive: target.connIdleTimeoutD,
      }).DialContext,
      TLSHandshakeTimeout: target.connTimeoutD,
      ForceAttemptHTTP2:   target.AutoUpgrade,
    })
    tracker = &ht.TransportTracker
    transport = ht.Transport
  } else {
    tr := &http2.Transport{
      ReadIdleTimeout: target.connIdleTimeoutD,
      PingTimeout:     target.connTimeoutD,
      AllowHTTP:       true,
    }
    tr.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
      if target.tls {
        return tls.Dial(network, addr, cfg)
      }
      return net.Dial(network, addr)
    }
    h2t := NewHTTP2TransportTracker(tr)
    tracker = &h2t.TransportTracker
    transport = h2t.Transport
  }
  return transport, tracker
}

func getHttpClientForTarget(target *InvocationSpec) *HTTPClientTracker {
  invocationsLock.RLock()
  client := targetClients[target.Name]
  invocationsLock.RUnlock()
  if client == nil {
    transport, tracker := httpTransport(target)
    client = NewHTTPClientTracker(&http.Client{Timeout: target.requestTimeoutD, Transport: transport}, tracker)
    invocationsLock.Lock()
    targetClients[target.Name] = client
    invocationsLock.Unlock()
  }
  return client
}

func RemoveHttpClientForTarget(target string) {
  invocationsLock.Lock()
  if client := targetClients[target]; client != nil {
    client.CloseIdleConnections()
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
        metrics.UpdateTargetConnCount(target, client.tracker.GetOpenConnectionCount())
      }
      invocationsLock.RUnlock()
    case <-time.Tick(maxIdleClientDuration):
      invocationsLock.Lock()
      for target, client := range targetClients {
        if activeTargets[target] == nil && client.tracker.GetOpenConnectionCount() > 0 {
          if watchListForRemoval[target] < 3 {
            watchListForRemoval[target]++
          } else {
            client.CloseIdleConnections()
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

func newTracker(id uint32, target *InvocationSpec, sinks ...ResultSinkFactory) *InvocationTracker {
  tracker := &InvocationTracker{}
  tracker.ID = id
  tracker.Status = &InvocationStatus{}
  tracker.Target = target
  tracker.StopChannel = make(chan bool, 20)
  tracker.DoneChannel = make(chan bool, 2)
  tracker.ResultChannel = make(chan *InvocationResult, 200)
  for _, sinkFactory := range sinks {
    if sink := sinkFactory(tracker); sink != nil {
      tracker.sinks = append(tracker.sinks, sink)
    }
  }
  if target.http {
    tracker.Status.httpClient = getHttpClientForTarget(target)
  }
  if len(target.StreamPayload) > 0 {
    for _, p := range target.StreamPayload {
      if target.Binary {
        if b, err := base64.RawStdEncoding.DecodeString(p); err == nil {
          target.payloads = append(target.payloads, b)
        } else {
          target.payloads = append(target.payloads, []byte(p))
        }
      } else {
        target.payloads = append(target.payloads, []byte(p))
      }
    }
  } else if target.Body != "" {
    target.payloads = [][]byte{[]byte(target.Body)}
  } else if target.BodyReader != nil {
    target.payloads = [][]byte{util.ReadBytes(target.BodyReader)}
    target.BodyReader = nil
  }
  tracker.lastStatusCode = -1
  return tracker
}

func RegisterInvocation(target *InvocationSpec, sinks ...ResultSinkFactory) *InvocationTracker {
  return newTracker(atomic.AddUint32(&invocationCounter, 1), target, sinks...)
}

func CloseInvocation(tracker *InvocationTracker) {
  tracker.lock.Lock()
  defer tracker.lock.Unlock()
  if tracker.StopChannel != nil {
    close(tracker.StopChannel)
    tracker.StopChannel = nil
  }
  if tracker.DoneChannel != nil {
    close(tracker.DoneChannel)
    tracker.DoneChannel = nil
  }
  if tracker.ResultChannel != nil {
    close(tracker.ResultChannel)
    tracker.ResultChannel = nil
  }
  tracker.Status.httpClient = nil
  tracker.Status.Closed = true
}

func DeregisterInvocation(tracker *InvocationTracker) {
  CloseInvocation(tracker)
  tracker.lock.RLock()
  trackerID := tracker.ID
  target := tracker.Target.Name
  tracker.lock.RUnlock()
  invocationsLock.RLock()
  activeTracker := activeInvocations[trackerID]
  invocationsLock.RUnlock()
  if activeTracker != nil {
    invocationsLock.Lock()
    delete(activeInvocations, trackerID)
    invocationsLock.Unlock()
    removeTargetTracker(trackerID, target)
  }
}

func removeTargetTracker(id uint32, target string) {
  invocationsLock.RLock()
  targetInvocations := activeTargets[target]
  invocationsLock.RUnlock()
  if targetInvocations != nil {
    targetInvocations.lock.Lock()
    delete(targetInvocations.trackers, id)
    size := len(targetInvocations.trackers)
    targetInvocations.lock.Unlock()
    if size == 0 {
      invocationsLock.Lock()
      delete(activeTargets, target)
      invocationsLock.Unlock()
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
    for _, tracker := range targetInvocations.trackers {
      tracker.lock.RLock()
      results[target][tracker.ID] = tracker.Status
      tracker.lock.RUnlock()
    }
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
    trackers := targetInvocations.trackers
    targetInvocations.lock.RUnlock()
    for _, tracker := range trackers {
      done := false
      tracker.lock.RLock()
      doneChannel := tracker.DoneChannel
      stopChannel := tracker.StopChannel
      stopRequested := tracker.Status.StopRequested
      stopped := tracker.Status.Stopped
      tracker.lock.RUnlock()
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
  }
}

func prepareTargetURL(url string, sendID bool, requestId string) (string, string) {
  if sendID && !strings.Contains(url, "x-request-id") {
    if !strings.Contains(url, "?") {
      url += "?"
    } else {
      pieces := strings.Split(url, "?")
      if len(pieces) > 1 && len(pieces[1]) > 0 && !strings.HasSuffix(pieces[1], "&") {
        url += "&"
      }
    }
    url += "x-request-id="
    if requestId == "" {
      requestId = uuid.New().String()
    }
    url += requestId
  }
  return url, requestId
}

func processStopRequest(tracker *InvocationTracker) {
  for !tracker.Status.StopRequested && !tracker.Status.Stopped {
    tracker.lock.RLock()
    stopChannel := tracker.StopChannel
    tracker.lock.RUnlock()
    if stopChannel != nil {
      stopRequested := false
      select {
      case stopRequested = <-stopChannel:
      default:
      }
      if stopRequested {
        tracker.lock.Lock()
        tracker.Status.StopRequested = true
        stopped := tracker.Status.Stopped
        tracker.lock.Unlock()
        if stopped {
          if global.EnableInvocationLogs {
            log.Printf("[%s]: Invocation[%d]: Received stop request for target [%s] that is already stopped\n", hostLabel, tracker.ID, tracker.Target.Name)
          }
        } else {
          tracker.lock.Lock()
          remaining := (tracker.Target.RequestCount * tracker.Target.Replicas) - tracker.Status.CompletedReplicas
          tracker.lock.Unlock()
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

func activateTracker(tracker *InvocationTracker) {
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

func StartInvocation(tracker *InvocationTracker, waitForResponse ...bool) []*InvocationResult {
  activateTracker(tracker)

  tracker.lock.RLock()
  target := tracker.Target
  trackerID := tracker.ID
  httpClient := tracker.Status.httpClient
  sinks := tracker.sinks
  resultChannel := tracker.ResultChannel
  doneChannel := tracker.DoneChannel
  stopChannel := tracker.StopChannel
  tracker.lock.RUnlock()

  completedCount := 0
  remaining := 0
  time.Sleep(target.initialDelayD)
  events.SendEventJSON(Client_InvocationStarted, fmt.Sprintf("%d-%s", trackerID, target.Name), target)
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Started target [%s] with total requests [%d]\n", hostLabel, trackerID, target.Name, (target.Replicas * target.RequestCount))
  }
  var results []*InvocationResult
  if len(waitForResponse) > 0 && waitForResponse[0] {
    sinks = append(sinks, func(result *InvocationResult) {
      results = append(results, result)
    })
  }
  go processStopRequest(tracker)
  for !tracker.Status.Stopped {
    if tracker.Status.StopRequested {
      tracker.Status.Stopped = true
      removeTargetTracker(tracker.ID, tracker.Target.Name)
      remaining = (tracker.Target.RequestCount * tracker.Target.Replicas) - tracker.Status.CompletedReplicas
      log.Printf("[%s]: Invocation[%d]: Stopping target [%s] with remaining requests [%d]\n", hostLabel, trackerID, target.Name, remaining)
      break
    }
    wg := &sync.WaitGroup{}
    for i := 0; i < target.Replicas; i++ {
      callCounter := completedCount + i + 1
      targetID := target.Name + "[" + strconv.Itoa(i+1) + "]" + "[" + strconv.Itoa(callCounter) + "]"
      wg.Add(1)
      go invokeTarget(tracker, targetID, target, httpClient, sinks, resultChannel, wg)
    }
    wg.Wait()
    delay := 10 * time.Millisecond
    if target.delayD > delay {
      delay = target.delayD
    }
    completedCount += target.Replicas
    tracker.lock.Lock()
    tracker.Status.CompletedReplicas = completedCount
    tracker.lock.Unlock()
    if completedCount < (target.RequestCount * target.Replicas) {
      time.Sleep(delay)
    } else {
      break
    }
  }
  tracker.lock.Lock()
  unsafeReportRepeatedResponse(tracker)
  tracker.lock.Unlock()

  if doneChannel != nil {
    doneChannel <- true
  }
  if stopChannel != nil {
    select {
    case <-stopChannel:
    default:
    }
  }
  DeregisterInvocation(tracker)
  events.SendEventJSON(Client_InvocationFinished, fmt.Sprintf("%d-%s", trackerID, target.Name),
    map[string]interface{}{"id": trackerID, "target": target.Name, "status": tracker.Status})
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: finished for  target [%s] with remaining requests [%d]\n", hostLabel, trackerID, target.Name, remaining)
  }
  return results
}

func invokeTarget(tracker *InvocationTracker, targetID string, target *InvocationSpec, client *HTTPClientTracker,
  sinks []ResultSink, resultChannel chan *InvocationResult, wg *sync.WaitGroup) {
  tracker.lock.RLock()
  trackerID := tracker.ID
  tracker.lock.RUnlock()
  result := &InvocationResult{
    Headers: map[string][]string{},
    Errors:  map[string]interface{}{},
  }
  result.TargetName = target.Name
  result.TargetID = targetID
  if target.Random {
    if r := util.Random(len(target.BURLS) + 1); r == 0 {
      result.URL = target.URL
    } else {
      result.URL = target.BURLS[r-1]
    }
  } else {
    result.URL = target.URL
  }
  result.Headers = map[string][]string{}
  if resp, err := doInvoke(trackerID, targetID, target, client, result, tracker); err == nil {
    if !tracker.Status.StopRequested || tracker.Status.Stopped {
      doProcessResponse(trackerID, targetID, resp, result, tracker)
      if target.AB {
        handleABCall(trackerID, targetID, target, result.RequestID, client, sinks, resultChannel, tracker)
      }
    }
  } else {
    processError(trackerID, targetID, result, err, tracker)
  }
  if !tracker.Status.StopRequested || tracker.Status.Stopped {
    publishResult(trackerID, targetID, result, sinks, resultChannel)
  }
  wg.Done()
}

func newClientRequest(method, targetURL string, headers [][]string, body io.Reader) (*http.Request, error) {
  if req, err := http.NewRequest(method, targetURL, body); err == nil {
    for _, h := range headers {
      if strings.EqualFold(h[0], "host") {
        req.Host = h[1]
      } else {
        req.Header.Add(h[0], h[1])
      }
    }
    if req.Host == "" {
      req.Host = req.URL.Host
    }
    return req, nil
  } else {
    return nil, err
  }
}

func doInvoke(index uint32, targetID string, target *InvocationSpec,
  client *HTTPClientTracker, result *InvocationResult, tracker *InvocationTracker) (*http.Response, error) {
  headers := target.Headers
  headers = append(headers, []string{"TargetID", targetID})
  if global.EnableInvocationLogs {
    var headersLog interface{} = ""
    if global.LogRequestHeaders {
      headersLog = target.Headers
    }
    log.Printf("[%s]: Invocation[%d]: Invoking targetID [%s], url [%s], method [%s], headers [%+v]\n",
      hostLabel, index, targetID, result.URL, target.Method, headersLog)
  }
  result.URL, result.RequestID = prepareTargetURL(result.URL, target.SendID, result.RequestID)
  originalRequestId := result.RequestID
  var requestReader io.ReadCloser
  var requestWriter io.WriteCloser
  if len(target.payloads) > 1 {
    requestReader, requestWriter = io.Pipe()
  } else if len(target.payloads) == 1 && len(target.payloads[0]) > 0 {
    requestReader = ioutil.NopCloser(bytes.NewReader(target.payloads[0]))
  }
  if req, err := newClientRequest(target.Method, result.URL, headers, requestReader); err == nil {
    result.URI = req.URL.Path
    client.tracker.tlsConfig = tlsConfig(req.Host, target.VerifyTLS)
    var resp *http.Response
    var reqError error
    for i := 0; i <= target.Retries; i++ {
      if tracker.Status.StopRequested || tracker.Status.Stopped {
        break
      }
      if i > 0 {
        result.Retries++
        time.Sleep(target.retryDelayD)
      }
      if tracker.Status.StopRequested || tracker.Status.Stopped {
        break
      }
      tracker.lock.Lock()
      tracker.Status.TotalRequests++
      metrics.UpdateTargetRequestCount(tracker.Target.Name)
      tracker.lock.Unlock()

      if requestWriter != nil {
        go writeRequestPayload(requestWriter, result, tracker)
      }
      startTime := time.Now()
      resp, reqError = client.Do(req)
      if reqError == nil && resp != nil {
        readResponsePayload(resp, result, tracker)
      }
      endTime := time.Now()
      result.TookNanos = int(endTime.Sub(startTime).Nanoseconds())

      retry := reqError != nil
      if !retry && target.RetriableStatusCodes != nil {
        for _, retriableCode := range target.RetriableStatusCodes {
          if retriableCode == resp.StatusCode {
            retry = true
          }
        }
      }
      if !retry {
        break
      } else if i < target.Retries {
        reason := ""
        if reqError != nil {
          reason = reqError.Error()
        }
        if reason == "" {
          reason = resp.Status
        }
        if target.Fallback && len(target.BURLS) > i {
          newURL, newRequestID := prepareTargetURL(target.BURLS[i], target.SendID, originalRequestId+"-"+strconv.Itoa(i+1))
          if req2, err := newClientRequest(target.Method, newURL, headers, bytes.NewReader(target.payloads[0])); err == nil {
            req = req2
            result.RetryURL = newURL
            result.RequestID = newRequestID
          } else {
            log.Printf("[%s]: Invocation[%d]: Target [%s] failed to create request for fallback url [%s]. Continuing with retry to previous url [%s] \n",
              hostLabel, index, targetID, target.BURLS[i], result.URL)
          }
        }
        result.LastRetryReason = reason
        log.Printf("[%s]: Invocation[%d]: Target [%s] url [%s] invocation requires retry due to [%s]. Retries left [%d].",
          hostLabel, index, targetID, result.URL, reason, target.Retries-i)
        tracker.lock.Lock()
        tracker.Status.RetriesCount++
        tracker.lock.Unlock()
      }
    }
    return resp, reqError
  } else {
    return nil, err
  }
}

func unsafeReportRepeatedResponse(tracker *InvocationTracker) {
  target := tracker.Target
  if tracker.lastStatusCount > 1 {
    msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Response Status [%d] Repeated x[%d]",
      hostLabel, tracker.ID, target.Name, target.URL, target.BURLS, tracker.lastStatusCode, tracker.lastStatusCount)
    events.SendEventJSON(Client_InvocationRepeatedResponse, fmt.Sprintf("%d-%s", tracker.ID, target.Name),
      map[string]interface{}{"id": tracker.ID, "details": msg})
    tracker.lastStatusCount = 0
    tracker.lastStatusCode = -1
  }
  if tracker.lastErrorCount > 1 {
    msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Failiure [%s] Repeated x[%d]",
      hostLabel, tracker.ID, target.Name, target.URL, target.BURLS, tracker.lastError, tracker.lastErrorCount)
    events.SendEventJSON(Client_InvocationRepeatedFailure, fmt.Sprintf("%d-%s", tracker.ID, target.Name),
      map[string]interface{}{"id": tracker.ID, "details": msg})
    tracker.lastErrorCount = 0
    tracker.lastError = ""
  }
}

func writeRequestPayload(w io.WriteCloser, result *InvocationResult, tracker *InvocationTracker) {
  if w != nil {
    size, first, last, err := util.WriteAndTrack(w, tracker.Target.payloads, tracker.Target.streamDelayD)
    if err == "" {
      result.RequestPayloadSize = size
      result.FirstByteOutAt = first.UTC().String()
      result.LastByteOutAt = last.UTC().String()
    } else {
      result.Errors["errorWrite"] = err
    }
  }
}

func readResponsePayload(resp *http.Response, result *InvocationResult, tracker *InvocationTracker) {
  if resp != nil && resp.Body != nil {
    defer resp.Body.Close()
    collect := tracker.Target.CollectResponse || tracker.Target.Expectation != nil && len(tracker.Target.Expectation.payload) > 0
    data, size, first, last, err := util.ReadAndTrack(resp.Body, collect)
    if err == "" {
      result.ResponsePayloadSize = size
      result.FirstByteInAt = first.UTC().String()
      result.LastByteInAt = last.UTC().String()
      if collect {
        result.Data = data
      }
    } else {
      result.Errors["errorRead"] = err
    }
  }
}

func validateResponse(result *InvocationResult, tracker *InvocationTracker) {
  expectation := tracker.Target.Expectation
  if expectation == nil {
    return
  }
  if result.StatusCode != expectation.StatusCode {
    result.Errors["statusCode"] = map[string]interface{}{"expected": expectation.StatusCode, "actual": result.StatusCode}
  }
  if expectation.PayloadLength > 0 {
    if result.ResponsePayloadSize != expectation.PayloadLength {
      result.Errors["payloadLength"] = map[string]interface{}{"expected": expectation.PayloadLength, "actual": result.ResponsePayloadSize}
    }
  }
  if len(expectation.Payload) > 0 {
    if bytes.Compare(expectation.payload, result.Data) != 0 {
      result.Errors["payload"] = map[string]interface{}{"expected": expectation.PayloadLength, "actual": result.ResponsePayloadSize}
    }
  }
  if len(expectation.headersRegexp) > 0 {
    if !util.ContainsAllHeaders(result.Headers, expectation.headersRegexp) {
      result.Errors["headers"] = map[string]interface{}{"expected": expectation.Headers, "actual": result.Headers}
    }
  }
}

func doProcessResponse(index uint32, targetID string, resp *http.Response, result *InvocationResult, tracker *InvocationTracker) {
  if resp == nil {
    return
  }
  for header, values := range resp.Header {
    result.Headers[strings.ToLower(header)] = values
  }
  result.Headers["status"] = []string{resp.Status}
  result.Status = resp.Status
  result.StatusCode = resp.StatusCode
  if tracker.Target.Expectation != nil {
    validateResponse(result, tracker)
  }
  tracker.lock.Lock()
  target := tracker.Target
  isRepeatStatus := tracker.lastStatusCode == result.StatusCode
  if !isRepeatStatus && tracker.lastStatusCount > 1 || tracker.lastErrorCount > 1 {
    unsafeReportRepeatedResponse(tracker)
    tracker.lastStatusCode = -1
  }
  if tracker.lastStatusCode >= 0 && isRepeatStatus {
    tracker.lastStatusCount++
  } else {
    tracker.lastStatusCount = 1
    tracker.lastStatusCode = result.StatusCode
  }
  tracker.lastError = ""
  tracker.lastErrorCount = 0
  tracker.Status.SuccessCount++
  tracker.lock.Unlock()

  url := result.URL
  if result.RetryURL != "" {
    url = result.RetryURL
  }
  if global.EnableInvocationLogs || !isRepeatStatus {
    data := InvocationLog{Host: hostLabel, Invocation: index, Target: targetID, URL: url, Result: result}
    if global.EnableInvocationLogs {
      log.Println(util.ToJSON(data))
    }
    tracker.lock.Lock()
    if !isRepeatStatus {
      events.SendEventJSON(Client_InvocationResponse, fmt.Sprintf("%d-%s", tracker.ID, target.Name), data)
    }
    tracker.lock.Unlock()
  }
}

func publishResult(index uint32, targetID string, result *InvocationResult, sinks []ResultSink, resultChannel chan *InvocationResult) {
  if len(sinks) > 0 {
    for _, sink := range sinks {
      sink(result)
    }
  } else if resultChannel != nil {
    if len(resultChannel) > 50 {
      log.Printf("[%s]: Invocation[%d]: Target %s ResultChannel length %d\n", hostLabel, index, targetID, len(resultChannel))
    }
    resultChannel <- result
  }
}

func processError(index uint32, targetID string, result *InvocationResult, err error, tracker *InvocationTracker) {
  tracker.lock.Lock()
  if tracker.lastStatusCount > 0 {
    unsafeReportRepeatedResponse(tracker)
  }
  msg := fmt.Sprintf("[%s]: Invocation[%d]: Target %s, url [%s] failed to invoke with error: %s, repeat count: [%d]",
    hostLabel, index, targetID, result.URL, err.Error(), tracker.lastErrorCount)
  if tracker.lastErrorCount == 0 {
    events.SendEventJSON(Client_InvocationFailure, fmt.Sprintf("%d-%s", tracker.ID, targetID),
      map[string]interface{}{"id": tracker.ID, "details": msg})
  }
  tracker.lastError = err.Error()
  tracker.lastErrorCount++
  tracker.lastStatusCode = 0
  tracker.lastStatusCount = 0
  tracker.Status.FailureCount++
  metrics.UpdateTargetFailureCount(tracker.Target.Name)
  tracker.lock.Unlock()
  result.Status = err.Error()
  log.Println(msg)
}

func handleABCall(index uint32, targetID string, target *InvocationSpec, aRequestId string, client *HTTPClientTracker,
  sinks []ResultSink, resultChannel chan *InvocationResult, tracker *InvocationTracker) {
  for i, burl := range target.BURLS {
    if tracker.Status.StopRequested || tracker.Status.Stopped {
      break
    }
    result := &InvocationResult{}
    result.TargetName = target.Name
    result.Headers = map[string][]string{}
    result.URL = burl
    result.RequestID = aRequestId + "-B-" + strconv.Itoa(i+1)
    if resp, err := doInvoke(index, targetID, target, client, result, tracker); err == nil {
      if !tracker.Status.StopRequested || tracker.Status.Stopped {
        doProcessResponse(index, targetID, resp, result, tracker)
      }
    } else {
      processError(index, targetID, result, err, tracker)
    }
    if !tracker.Status.StopRequested || tracker.Status.Stopped {
      publishResult(index, targetID, result, sinks, resultChannel)
      tracker.lock.Lock()
      tracker.Status.ABCount++
      tracker.lock.Unlock()
    }
  }
}

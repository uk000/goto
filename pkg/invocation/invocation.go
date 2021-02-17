package invocation

import (
  "crypto/tls"
  "crypto/x509"
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
  "net/url"
  "path/filepath"
  "strconv"
  "strings"
  "sync"
  "sync/atomic"
  "time"

  "github.com/google/uuid"
  "golang.org/x/net/http2"
)

type InvocationSpec struct {
  Name                 string     `json:"name"`
  Protocol             string     `json:"protocol"`
  Method               string     `json:"method"`
  URL                  string     `json:"url"`
  BURLS                []string   `json:"burls"`
  Headers              [][]string `json:"headers"`
  Body                 string     `json:"body"`
  BodyReader           io.Reader  `json:"-"`
  AutoUpgrade          bool       `json:"autoUpgrade"`
  Replicas             int        `json:"replicas"`
  RequestCount         int        `json:"requestCount"`
  InitialDelay         string     `json:"initialDelay"`
  Delay                string     `json:"delay"`
  Retries              int        `json:"retries"`
  RetryDelay           string     `json:"retryDelay"`
  RetriableStatusCodes []int      `json:"retriableStatusCodes"`
  KeepOpen             string     `json:"keepOpen"`
  SendID               bool       `json:"sendID"`
  ConnTimeout          string     `json:"connTimeout"`
  ConnIdleTimeout      string     `json:"connIdleTimeout"`
  RequestTimeout       string     `json:"requestTimeout"`
  VerifyTLS            bool       `json:"verifyTLS"`
  CollectResponse      bool       `json:"collectResponse"`
  AutoInvoke           bool       `json:"autoInvoke"`
  AutoPayload          string     `json:"autoPayload"`
  Fallback             bool       `json:"fallback"`
  ABMode               bool       `json:"abMode"`
  httpVersionMajor     int
  httpVersionMinor     int
  tcp                  bool
  grpc                 bool
  http                 bool
  tls                  bool
  host                 string
  connTimeoutD         time.Duration
  connIdleTimeoutD     time.Duration
  requestTimeoutD      time.Duration
  initialDelayD        time.Duration
  delayD               time.Duration
  retryDelayD          time.Duration
  keepOpenD            time.Duration
  autoPayloadSize      int
  payloadBody          string
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
  TargetName      string
  TargetID        string
  Status          string
  StatusCode      int
  Retries         int
  URL             string
  URI             string
  RequestID       string
  Headers         map[string][]string
  Body            string
  RetryURL        string
  LastRetryReason string
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
  if spec.ABMode && spec.Fallback {
    return fmt.Errorf("A target cannot have both ABMode and Fallback enabled simultaneously.")
  }
  if (spec.ABMode || spec.Fallback) && len(spec.BURLS) == 0 {
    return fmt.Errorf("At least one B-URL is required for Fallback or ABMode")
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
    spec.connTimeoutD = 10 * time.Second
    spec.ConnTimeout = "10s"
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
  if spec.BodyReader != nil && spec.Body == "" && spec.Replicas > 1 {
    return fmt.Errorf("Streaming request body can only be forwarded to one target whereas replicas is %d", spec.Replicas)
  }
  if spec.AutoPayload != "" {
    spec.autoPayloadSize = util.ParseSize(spec.AutoPayload)
    if spec.autoPayloadSize <= 0 {
      return fmt.Errorf("Invalid AutoPayload, must be a valid size like 100, 10K, etc.")
    }
  }
  return nil
}

func PrepareAutoPayload(i *InvocationSpec) {
  if i.autoPayloadSize > 0 {
    i.payloadBody = util.GenerateRandomString(i.autoPayloadSize)
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

func tlsConfig(target *InvocationSpec) *tls.Config {
  cfg := &tls.Config{
    ServerName:         target.host,
    InsecureSkipVerify: !target.VerifyTLS,
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
      TLSClientConfig:     tlsConfig(target),
      ForceAttemptHTTP2:   target.AutoUpgrade,
    })
    tracker = &ht.TransportTracker
    transport = ht.Transport
  } else {
    tr := &http2.Transport{
      ReadIdleTimeout: target.connIdleTimeoutD,
      PingTimeout:     target.connTimeoutD,
      TLSClientConfig: tlsConfig(target),
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
  if target.BodyReader != nil && (target.Replicas > 1 || target.RequestCount > 1) {
    body, _ := ioutil.ReadAll(target.BodyReader)
    target.payloadBody = string(body)
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

func extractTargetHost(target *InvocationSpec) {
  target.host = ""
  for _, kv := range target.Headers {
    if strings.EqualFold(kv[0], "Host") {
      target.host = kv[1]
    }
  }
  if target.host == "" {
    if url, err := url.Parse(target.URL); err == nil {
      target.host = url.Host
    } else {
      log.Printf("[%s]: Failed to parse target URL [%s]\n", hostLabel, target.URL)
    }
  }
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

  extractTargetHost(target)
  completedCount := 0
  remaining := 0
  time.Sleep(target.initialDelayD)
  events.SendEventJSON(Client_InvocationStarted, target)
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
      if target.Body != "" && target.autoPayloadSize <= 0 {
        target.payloadBody = target.Body
      } else if target.Body == "" && target.BodyReader != nil {
        target.payloadBody = util.Read(target.BodyReader)
        target.BodyReader = nil
      }
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
  events.SendEventJSON(Client_InvocationFinished, map[string]interface{}{"target": target.Name, "status": tracker.Status})
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: finished for  target [%s] with remaining requests [%d]\n", hostLabel, trackerID, target.Name, remaining)
  }
  return results
}

func newClientRequest(method string, url string, headers [][]string, body io.Reader) (*http.Request, error) {
  if req, err := http.NewRequest(method, url, body); err == nil {
    for _, h := range headers {
      if strings.EqualFold(h[0], "host") {
        req.Host = h[1]
      } else {
        req.Header.Add(h[0], h[1])
      }
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
    log.Printf("[%s]: Invocation[%d]: Invoking targetID [%s], url [%s], method [%s], headers [%+v]\n",
      hostLabel, index, targetID, result.URL, target.Method, target.Headers)
  }
  result.URL, result.RequestID = prepareTargetURL(result.URL, target.SendID, result.RequestID)
  originalRequestId := result.RequestID
  if req, err := newClientRequest(target.Method, result.URL, headers, strings.NewReader(target.payloadBody)); err == nil {
    req.Host = target.host
    result.URI = req.URL.Path
    var resp *http.Response
    var reqError error
    for i := 0; i <= target.Retries; i++ {
      if resp != nil {
        resp.Body.Close()
      }
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
      resp, reqError = client.Do(req)
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
          if req2, err := newClientRequest(target.Method, newURL, headers, strings.NewReader(target.payloadBody)); err == nil {
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
    events.SendEvent(Client_InvocationRepeatedResponse, msg)
    tracker.lastStatusCount = 0
    tracker.lastStatusCode = -1
  }
  if tracker.lastErrorCount > 1 {
    msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Failiure [%s] Repeated x[%d]",
      hostLabel, tracker.ID, target.Name, target.URL, target.BURLS, tracker.lastError, tracker.lastErrorCount)
    events.SendEvent(Client_InvocationRepeatedFailure, msg)
    tracker.lastErrorCount = 0
    tracker.lastError = ""
  }
}

func doProcessResponse(index uint32, targetID string, resp *http.Response, result *InvocationResult, tracker *InvocationTracker) {
  if resp == nil {
    return
  }
  if resp.Body != nil {
    defer resp.Body.Close()
  }
  for header, values := range resp.Header {
    result.Headers[header] = values
  }
  result.Headers["Status"] = []string{resp.Status}
  result.Status = resp.Status
  result.StatusCode = resp.StatusCode

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

  var responseLength int64
  if resp.Body != nil {
    if target.CollectResponse {
      result.Body = util.Read(resp.Body)
      responseLength = int64(len(result.Body))
    } else {
      responseLength, _ = io.Copy(ioutil.Discard, resp.Body)
    }
  }
  if global.EnableInvocationLogs {
    headerLogs := []string{}
    for header, values := range resp.Header {
      headerLogs = append(headerLogs, header+":["+strings.Join(values, ",")+"]")
    }
    headerLog := strings.Join(headerLogs, ",")
    url := result.URL
    if result.RetryURL != "" {
      url = result.RetryURL
    }
    msg := fmt.Sprintf("[%s]: Invocation[%d]: Target %s Response Status: %s, URL: [%s], Headers: [%s], Payload Length: [%d]",
      hostLabel, index, targetID, resp.Status, url, headerLog, responseLength)
    log.Println(msg)

    tracker.lock.Lock()
    if !isRepeatStatus {
      events.SendEvent(Client_InvocationResponse, fmt.Sprintf("[%s]: Invocation[%d]: Target %s Response Status: %s, URL: [%s], Payload Length: [%d]",
        hostLabel, index, targetID, resp.Status, url, responseLength))
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
    events.SendEvent(Client_InvocationFailure, msg)
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

func invokeTarget(tracker *InvocationTracker, targetID string, target *InvocationSpec, client *HTTPClientTracker,
  sinks []ResultSink, resultChannel chan *InvocationResult, wg *sync.WaitGroup) {
  tracker.lock.RLock()
  trackerID := tracker.ID
  tracker.lock.RUnlock()
  result := &InvocationResult{}
  result.TargetName = target.Name
  result.TargetID = targetID
  result.URL = target.URL
  result.Headers = map[string][]string{}
  if resp, err := doInvoke(trackerID, targetID, target, client, result, tracker); err == nil {
    if !tracker.Status.StopRequested || tracker.Status.Stopped {
      doProcessResponse(trackerID, targetID, resp, result, tracker)
      if target.ABMode {
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

package invocation

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"goto/pkg/global"
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
  Name             string     `json:"name"`
  Method           string     `json:"method"`
  URL              string     `json:"url"`
  Headers          [][]string `json:"headers"`
  Body             string     `json:"body"`
  BodyReader       io.Reader  `json:"-"`
  Protocol         string     `json:"protocol"`
  AutoUpgrade       bool      `json:"autoUpgrade"`
  Replicas         int        `json:"replicas"`
  RequestCount     int        `json:"requestCount"`
  InitialDelay     string     `json:"initialDelay"`
  Delay            string     `json:"delay"`
  KeepOpen         string     `json:"keepOpen"`
  SendID           bool       `json:"sendID"`
  ConnTimeout      string     `json:"connTimeout"`
  ConnIdleTimeout  string     `json:"connIdleTimeout"`
  RequestTimeout   string     `json:"requestTimeout"`
  VerifyTLS        bool       `json:"verifyTLS"`
  CollectResponse  bool       `json:"collectResponse"`
  AutoInvoke       bool       `json:"autoInvoke"`
  httpVersionMajor int 
  httpVersionMinor int 
  https             bool
  host             string
  connTimeoutD     time.Duration
  connIdleTimeoutD time.Duration
  requestTimeoutD  time.Duration
  initialDelayD    time.Duration
  delayD           time.Duration
  keepOpenD        time.Duration
}

type InvocationStatus struct {
  CompletedRequestCount int  `json:"completedRequestCount"`
  StopRequested         bool `json:"stopRequested"`
  Stopped               bool `json:"stopped"`
  Closed                bool `json:"closed"`
  client                *http.Client
}

type InvocationResult struct {
  TargetName string
  TargetID   string
  Status     string
  StatusCode int
  URI        string
  Headers    map[string][]string
  Body       string
}

type ResultSink func(*InvocationResult)
type ResultSinkFactory func(*InvocationTracker) ResultSink

type InvocationTracker struct {
  ID            uint32                 `json:"id"`
  Target        *InvocationSpec        `json:"target"`
  Status        *InvocationStatus      `json:"status"`
  StopChannel   chan bool              `json:"-"`
  DoneChannel   chan bool              `json:"-"`
  ResultChannel chan *InvocationResult `json:"-"`
  sinks         []ResultSink
  lock          sync.RWMutex
}

type TargetInvocations struct {
  trackers map[uint32]*InvocationTracker
  lock     sync.RWMutex
}

const (
  maxIdleClientDuration = 120 * time.Second
)

var (
  invocationCounter  uint32
  activeInvocations  map[uint32]*InvocationTracker = map[uint32]*InvocationTracker{}
  activeTargets      map[string]*TargetInvocations = map[string]*TargetInvocations{}
  targetClients      map[string]*http.Client       = map[string]*http.Client{}
  chanStopCleanup    chan bool                     = make(chan bool, 1)
  rootCAs            *x509.CertPool
  invocationsLock    sync.RWMutex
)

func Startup() {
  loadCerts()
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
  if strings.Contains(strings.ToLower(spec.URL), "https") {
    spec.https = true
  }
  if spec.Protocol != "" {
    if major, minor, ok := http.ParseHTTPVersion(spec.Protocol); ok {
      if major == 1 && (minor == 0 || minor == 1) {
        spec.httpVersionMajor = major
        spec.httpVersionMinor = minor
      } else if major == 2 {
        spec.httpVersionMajor = major
        spec.httpVersionMinor = 0
      }
    }
  }
  if spec.httpVersionMajor == 0 {
    spec.httpVersionMajor = 1
    spec.httpVersionMinor = 1
  }
  spec.Protocol = fmt.Sprintf("HTTP/%d.%d", spec.httpVersionMajor, spec.httpVersionMinor)
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
  }
  if spec.ConnTimeout != "" {
    if spec.connTimeoutD, err = time.ParseDuration(spec.ConnTimeout); err != nil {
      return fmt.Errorf("Invalid ConnectionTimeout")
    }
  } else {
    spec.connTimeoutD = 30 * time.Second
  }
  if spec.ConnIdleTimeout != "" {
    if spec.connIdleTimeoutD, err = time.ParseDuration(spec.ConnIdleTimeout); err != nil {
      return fmt.Errorf("Invalid ConnectionIdleTimeout")
    }
  } else {
    spec.connIdleTimeoutD = 5 * time.Minute
  }
  if spec.RequestTimeout != "" {
    if spec.requestTimeoutD, err = time.ParseDuration(spec.RequestTimeout); err != nil {
      return fmt.Errorf("Invalid RequestIdleTimeout")
    }
  } else {
    spec.requestTimeoutD = 30 * time.Second
  }
  if spec.KeepOpen != "" {
    if spec.keepOpenD, err = time.ParseDuration(spec.KeepOpen); err != nil {
      return fmt.Errorf("Invalid keepOpen")
    }
  }
  if spec.BodyReader != nil && spec.Body == "" && spec.Replicas > 1 {
    return fmt.Errorf("Streaming request body can only be forwarded to one target whereas replicas is %d", spec.Replicas)
  }
  return nil
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

func httpTransport(target *InvocationSpec) http.RoundTripper {
  var transport http.RoundTripper
  if target.httpVersionMajor == 1 {
    transport = &http.Transport{
      MaxIdleConns:       200,
      MaxIdleConnsPerHost: 100,
      IdleConnTimeout:    target.connIdleTimeoutD,
      Proxy:              http.ProxyFromEnvironment,
      DisableCompression:    true,
      ExpectContinueTimeout: 5 * time.Second,
      ResponseHeaderTimeout: 10 * time.Second,
      DialContext: (&net.Dialer{
        Timeout:   target.connTimeoutD,
        KeepAlive: time.Minute*10,
      }).DialContext,
      TLSHandshakeTimeout: 10 * time.Second,
      TLSClientConfig:     tlsConfig(target),
      ForceAttemptHTTP2: target.AutoUpgrade,
    }
  } else {
    tr := &http2.Transport{
      ReadIdleTimeout: target.connIdleTimeoutD,
      PingTimeout: target.connTimeoutD,
      TLSClientConfig: tlsConfig(target),
      AllowHTTP:           true,
    }
    tr.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
      if target.https {
        return tls.Dial(network, addr, cfg)
      }
      return net.Dial(network, addr)
    }
    transport = tr
  }
  return transport
}

func getHttpClientForTarget(target *InvocationSpec) *http.Client {
  invocationsLock.RLock()
  client := targetClients[target.Name]
  invocationsLock.RUnlock()
  if client == nil {
    client = &http.Client{Transport: httpTransport(target)}
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
    case <-time.Tick(maxIdleClientDuration):
      invocationsLock.Lock()
      for target, client := range targetClients {
        if activeTargets[target] == nil {
          if watchListForRemoval[target] < 3 {
            watchListForRemoval[target]++
          } else {
            client.CloseIdleConnections()
            delete(targetClients, target)
            delete(watchListForRemoval, target)
          }
        } else {
          if watchListForRemoval[target] > 0 {
            delete(watchListForRemoval, target)
          }
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
  tracker.Status.client = getHttpClientForTarget(target)
  if target.BodyReader != nil && (target.Replicas > 1 || target.RequestCount > 1) {
    body, _ := ioutil.ReadAll(target.BodyReader)
    target.Body = string(body)
    target.BodyReader = nil
  }
  return tracker
}

func RegisterInvocation(target *InvocationSpec, sinks ...ResultSinkFactory) *InvocationTracker {
  return newTracker(atomic.AddUint32(&invocationCounter, 1), target, sinks...)
}

func CloseInvocation(tracker *InvocationTracker) {
  tracker.lock.Lock()
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
  tracker.Status.client = nil
  tracker.Status.Closed = true
  tracker.lock.Unlock()
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
  invocationsLock.Lock()
  targetInvocations := activeTargets[target]
  invocationsLock.Unlock()
  if targetInvocations != nil {
    targetInvocations.lock.Lock()
    delete(targetInvocations.trackers, id)
    if len(targetInvocations.trackers) == 0 {
      invocationsLock.Lock()
      delete(activeTargets, target)
      invocationsLock.Unlock()
    }
    targetInvocations.lock.Unlock()
  }
}

func GetActiveInvocations() map[string]map[uint32]*InvocationStatus {
  results := map[string]map[uint32]*InvocationStatus{}
  invocationsLock.RLock()
  defer invocationsLock.RUnlock()
  for target, targetInvocations := range activeTargets {
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
    for _, tracker := range targetInvocations.trackers {
      tracker.lock.Lock()
      done := false
      select {
      case done = <-tracker.DoneChannel:
      default:
      }
      if !done {
        if !tracker.Status.StopRequested && !tracker.Status.Stopped {
          tracker.StopChannel <- true
        }
      }
      tracker.lock.Unlock()
    }
    targetInvocations.lock.RUnlock()
  }
}

func prepareTargetURL(target *InvocationSpec) string {
  url := target.URL
  if target.SendID && !strings.Contains(url, "x-request-id") {
    if !strings.Contains(url, "?") {
      url += "?"
    } else {
      pieces := strings.Split(url, "?")
      if len(pieces) > 1 && len(pieces[1]) > 0 && !strings.HasSuffix(pieces[1], "&") {
        url += "&"
      }
    }
    url += "x-request-id="
    url += uuid.New().String()
  }
  return url
}

func processStopRequest(tracker *InvocationTracker) bool {
  tracker.lock.Lock()
  defer tracker.lock.Unlock()
  if tracker.StopChannel != nil {
    select {
    case tracker.Status.StopRequested = <-tracker.StopChannel:
    default:
    }
    if tracker.Status.StopRequested {
      if tracker.Status.Stopped {
        if global.EnableInvocationLogs {
          log.Printf("Invocation[%d]: Received stop request for target = %s that is already stopped\n", tracker.ID, tracker.Target.Name)
        }
        return true
      } else {
        remaining := (tracker.Target.RequestCount * tracker.Target.Replicas) - (tracker.Status.CompletedRequestCount * tracker.Target.Replicas)
        if global.EnableInvocationLogs {
          log.Printf("Invocation[%d]: Received stop request for target = %s with remaining requests %d\n", tracker.ID, tracker.Target.Name, remaining)
        }
        tracker.Status.Stopped = true
        removeTargetTracker(tracker.ID, tracker.Target.Name)
        return true
      }
    }
  }
  return false
}

func activateTracker(tracker *InvocationTracker) {
  tracker.lock.RLock()
  defer tracker.lock.RUnlock()
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
      log.Printf("Failed to parse target URL: %s\n", target.URL)
    }
  }
}

func StartInvocation(tracker *InvocationTracker, waitForResponse ...bool) []*InvocationResult {
  activateTracker(tracker)

  tracker.lock.RLock()
  target := tracker.Target
  trackerID := tracker.ID
  httpClient := tracker.Status.client
  sinks := tracker.sinks
  resultChannel := tracker.ResultChannel
  doneChannel := tracker.DoneChannel
  stopChannel := tracker.StopChannel
  tracker.lock.RUnlock()

  extractTargetHost(target)
  completedCount := 0
  time.Sleep(target.initialDelayD)
  if global.EnableInvocationLogs {
    log.Printf("Invocation[%d]: Started with target %s and total requests %d\n", trackerID, target.Name, (target.Replicas * target.RequestCount))
  }
  activeTargets := []string{}
  invocationsLock.Lock()
  for k := range targetClients {
    activeTargets = append(activeTargets, k)
  }
  invocationsLock.Unlock()
  resultCount := 0
  var results []*InvocationResult

  if len(waitForResponse) > 0 && waitForResponse[0] {
    sinks = append(sinks, func(result *InvocationResult){
      results = append(results, result)
    })
  }

  for {
    if stopped := processStopRequest(tracker); stopped {
      break
    }
    wg := &sync.WaitGroup{}
    for i := 0; i < target.Replicas; i++ {
      callCounter := (completedCount * target.Replicas) + i + 1
      targetID := target.Name + "[" + strconv.Itoa(i+1) + "]" + "[" + strconv.Itoa(callCounter) + "]"
      url := prepareTargetURL(target)
      bodyReader := target.BodyReader
      target.BodyReader = nil
      if bodyReader == nil {
        bodyReader = strings.NewReader(target.Body)
      }
      wg.Add(1)
      go invokeTarget(trackerID, target.Name, targetID, url, target.Method, target.host, target.Headers,
        bodyReader, target.CollectResponse, httpClient, sinks, resultChannel, wg)
    }
    wg.Wait()
    delay := 10 * time.Millisecond
    if target.delayD > delay {
      delay = target.delayD
    }
    completedCount++
    tracker.Status.CompletedRequestCount = completedCount
    if completedCount < target.RequestCount {
      time.Sleep(delay)
    } else {
      break
    }
  }
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
  if global.EnableInvocationLogs {
    log.Printf("Invocation[%d]: Finished with responses %d\n", trackerID, resultCount)
  }

  activeTargets = []string{}
  invocationsLock.Lock()
  for k := range targetClients {
    activeTargets = append(activeTargets, k)
  }
  invocationsLock.Unlock()
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

func invokeTarget(index uint32, targetName string, targetID string, url string, method string, 
  host string, headers [][]string, body io.Reader, reportBody bool, client *http.Client, 
  sinks []ResultSink, resultChannel chan *InvocationResult, wg *sync.WaitGroup) {
  if global.EnableInvocationLogs {
    log.Printf("Invocation[%d]: Invoking targetID: %s, url: %s, method: %s, headers: %+v\n", index, targetID, url, method, headers)
  }
  result := &InvocationResult{}
  result.TargetName = targetName
  result.TargetID = targetID
  result.Headers = map[string][]string{}
  headers = append(headers, []string{"TargetID", targetID})
  if req, err := newClientRequest(method, url, headers, body); err == nil {
    req.Host = host
    result.URI = req.URL.Path
    if resp, err := client.Do(req); err == nil {
      defer resp.Body.Close()
      if global.EnableInvocationLogs {
        log.Printf("Invocation[%d]: Target %s Response Status: %s\n", index, targetID, resp.Status)
      }
      for header, values := range resp.Header {
        result.Headers[header] = values
      }
      result.Headers["Status"] = []string{resp.Status}
      result.Status = resp.Status
      result.StatusCode = resp.StatusCode
      if reportBody {
        result.Body = util.Read(resp.Body)
      } else {
        io.Copy(ioutil.Discard, resp.Body)
      }
    } else {
      log.Printf("Invocation[%d]: Target %s, url [%s] invocation failed with error: %s\n", index, targetID, url, err.Error())
      result.Status = err.Error()
    }
  } else {
    log.Printf("Invocation[%d]: Target %s, url [%s] failed to create request with error: %s\n", index, targetID, url, err.Error())
    result.Status = err.Error()
  }

  if len(sinks) > 0 {
    for _, sink := range sinks {
      sink(result)
    }
  } else if resultChannel != nil {
    if len(resultChannel) > 50 {
      log.Printf("Invocation[%d]: Target %s ResultChannel length %d\n", index, targetID, len(resultChannel))
    }
    resultChannel <- result
  }
  wg.Done()
}

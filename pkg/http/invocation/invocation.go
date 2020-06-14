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
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type InvocationSpec struct {
  Name             string     `json:"name"`
  Method           string     `json:"method"`
  URL              string     `json:"url"`
  Headers          [][]string `json:"headers"`
  Body             string     `json:"body"`
  BodyReader       io.Reader  `json:"bodyReader"`
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
  AutoInvoke       bool       `json:"autoInvoke"`
  host             string
  connTimeoutD     time.Duration
  connIdleTimeoutD time.Duration
  requestTimeoutD  time.Duration
  initialDelayD    time.Duration
  delayD           time.Duration
  keepOpenD        time.Duration
}

type InvocationChannels struct {
  ID            int
  StopChannel   chan string
  DoneChannel   chan bool
  ResultChannel chan *InvocationResult
}

type InvocationStatus struct {
  target                *InvocationSpec
  url                   string
  client                *http.Client
  completedRequestCount int
  stopRequested         bool
  stopped               bool
}

type InvocationResult struct {
  TargetName string
  TargetID   string
  Status     string
  StatusCode int
  Headers    map[string][]string
  Body       string
}

var (
  invocationChannels map[int]*InvocationChannels = map[int]*InvocationChannels{}
  rootCAs            *x509.CertPool
  channelsLock       sync.Mutex
)

func RegisterInvocation(index int) *InvocationChannels {
  channelsLock.Lock()
  defer channelsLock.Unlock()
  ic := &InvocationChannels{}
  ic.ID = index
  ic.StopChannel = make(chan string, 10)
  ic.DoneChannel = make(chan bool, 10)
  ic.ResultChannel = make(chan *InvocationResult, 10)
  invocationChannels[index] = ic
  return ic
}

func DeregisterInvocation(ic *InvocationChannels) {
  channelsLock.Lock()
  defer channelsLock.Unlock()
  if invocationChannels[ic.ID] != nil {
    close(ic.StopChannel)
    close(ic.DoneChannel)
    close(ic.ResultChannel)
    delete(invocationChannels, ic.ID)
  }
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
  } else {
    spec.initialDelayD = 1 * time.Second
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

func LoadCerts() {
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

func prepareInvocation(target *InvocationSpec) *InvocationStatus {
  if target.BodyReader != nil && (target.Replicas > 1 || target.RequestCount > 1) {
    body, _ := ioutil.ReadAll(target.BodyReader)
    target.Body = string(body)
    target.BodyReader = nil
  }
  invocationStatus := &InvocationStatus{}
  tr := &http.Transport{
    MaxIdleConns:       2,
    IdleConnTimeout:    target.connIdleTimeoutD,
    DisableCompression: true,
    Proxy:              http.ProxyFromEnvironment,
    DialContext: (&net.Dialer{
      Timeout:   target.connTimeoutD,
      KeepAlive: time.Minute,
    }).DialContext,
    TLSHandshakeTimeout: 10 * time.Second,
    TLSClientConfig:     tlsConfig(target),
  }
  invocationStatus.client = &http.Client{Transport: tr}
  return invocationStatus
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

func collectStopRequests(ic *InvocationChannels, invocationStatuses map[string]*InvocationStatus) {
  if ic.StopChannel != nil {
    for {
      stopTarget := ""
      select {
      case stopTarget = <-ic.StopChannel:
      default:
      }
      if stopTarget != "" {
        if invocationStatuses[stopTarget] != nil {
          invocationStatuses[stopTarget].stopRequested = true
        }
      } else {
        break
      }
    }
  }
}

func processTargetStopRequest(index int, target *InvocationSpec, invocationStatus *InvocationStatus) int {
  if invocationStatus.stopped {
    log.Printf("Invocation[%d]: Received stop request for target = %s that is already stopped\n", index, target.Name)
    invocationStatus.stopRequested = false
    return 0
  } else {
    remaining := (target.RequestCount * target.Replicas) - (invocationStatus.completedRequestCount * target.Replicas)
    log.Printf("Invocation[%d]: Received stop request for target = %s with remaining requests %d\n", index, target.Name, remaining)
    invocationStatus.stopped = true
    invocationStatus.stopRequested = false
    return remaining
  }
}

func InvokeTargets(targets []*InvocationSpec, invocationChannels *InvocationChannels, reportBody bool) []*InvocationResult {
  var responses []*InvocationResult
  invocationID := invocationChannels.ID
  invocationStatuses := map[string]*InvocationStatus{}
  if len(targets) > 0 {
    initialDelay := time.Millisecond
    totalRemainingRequestCount := 0
    for _, target := range targets {
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
      invocationStatuses[target.Name] = prepareInvocation(target)
      totalRemainingRequestCount += (target.Replicas * target.RequestCount)
      if target.initialDelayD > initialDelay {
        initialDelay = target.initialDelayD
      }
    }
    time.Sleep(initialDelay)
    log.Printf("Invocation[%d]: Started with target count: %d, total requests to make: %d\n", invocationID, len(targets), totalRemainingRequestCount)
    for {
      collectStopRequests(invocationChannels, invocationStatuses)
      batchSize := 0
      for _, target := range targets {
        if invocationStatuses[target.Name].stopRequested {
          remainingRequestCountForTarget := processTargetStopRequest(invocationID, target, invocationStatuses[target.Name])
          totalRemainingRequestCount -= remainingRequestCountForTarget
        } else if !invocationStatuses[target.Name].stopped && invocationStatuses[target.Name].completedRequestCount < target.RequestCount {
          batchSize += target.Replicas
        }
      }
      responseChannels := make([]chan *InvocationResult, batchSize)
      cases := make([]reflect.SelectCase, batchSize)
      index := 0
      delay := 10 * time.Millisecond
      for _, target := range targets {
        if !invocationStatuses[target.Name].stopped && invocationStatuses[target.Name].completedRequestCount < target.RequestCount {
          if target.delayD > delay {
            delay = target.delayD
          }
          for i := 0; i < target.Replicas; i++ {
            targetReplicaIndex := (invocationStatuses[target.Name].completedRequestCount * target.Replicas) + i + 1
            targetID := target.Name + "[" + strconv.Itoa(i+1) + "]" + "[" + strconv.Itoa(targetReplicaIndex) + "]"
            url := prepareTargetURL(target)
            totalRemainingRequestCount--
            responseChannels[index] = make(chan *InvocationResult)
            cases[index] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(responseChannels[index])}
            bodyReader := target.BodyReader
            target.BodyReader = nil
            if bodyReader == nil {
              bodyReader = strings.NewReader(target.Body)
            }
            go invokeTarget(invocationID, target.Name, targetID, url, target.Method, target.host, target.Headers,
              bodyReader, reportBody, invocationStatuses[target.Name].client, responseChannels[index])
            index++
          }
          invocationStatuses[target.Name].completedRequestCount++
        }
      }
      for len(cases) > 0 {
        i, v, _ := reflect.Select(cases)
        cases = append(cases[:i], cases[i+1:]...)
        result := v.Interface().(*InvocationResult)
        responses = append(responses, result)
        if invocationChannels.ResultChannel != nil {
          invocationChannels.ResultChannel <- result
        }
      }
      if totalRemainingRequestCount == 0 {
        break
      }
      time.Sleep(delay)
    }
    if invocationChannels.DoneChannel != nil {
      invocationChannels.DoneChannel <- true
    }
    if invocationChannels.StopChannel != nil {
      select {
      case <-invocationChannels.StopChannel:
      default:
      }
    }
  }
  log.Printf("Invocation[%d]: Finished with responses %d\n", invocationID, len(responses))
  return responses
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

func invokeTarget(index int, targetName string, targetID string, url string, method string, host string, headers [][]string, body io.Reader, reportBody bool, client *http.Client, c chan *InvocationResult) {
  defer close(c)
  log.Printf("Invocation[%d]: Invoking targetID: %s, url: %s, method: %s, headers: %+v\n", index, targetID, url, method, headers)
  var result InvocationResult
  result.TargetName = targetName
  result.TargetID = targetID
  result.Headers = map[string][]string{}
  headers = append(headers, []string{"TargetID", targetID})
  if req, err := newClientRequest(method, url, headers, body); err == nil {
    req.Host = host
    if resp, err := client.Do(req); err == nil {
      defer resp.Body.Close()
      for header, values := range resp.Header {
        result.Headers[header] = values
      }
      result.Headers["Status"] = []string{resp.Status}
      result.Status = resp.Status
      result.StatusCode = resp.StatusCode
      if reportBody {
        result.Body = util.Read(resp.Body)
      }
    } else {
      log.Printf("Invocation[%d]: Target %s, url [%s] invocation failed with error: %s\n", index, targetID, url, err.Error())
      result.Status = err.Error()
    }
  } else {
    log.Printf("Invocation[%d]: Target %s, url [%s] failed to create request with error: %s\n", index, targetID, url, err.Error())
    result.Status = err.Error()
  }
  c <- &result
}

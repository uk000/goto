package invocation

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type InvocationSpec struct {
  Name         string
  Method       string
  Url          string
  Headers      [][]string
  Body         string
  BodyReader   io.Reader
  Replicas     int
  RequestCount int
  Delay        string
  delayD       time.Duration
  KeepOpen     string
  keepOpenD    time.Duration
  SendId       bool
}

type InvocationChannels struct {
  ID          int
  StopChannel chan string
  DoneChannel chan bool
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

func ValidateSpec(spec *InvocationSpec) error {
  if spec.Name == "" {
    return fmt.Errorf("Name is required")
  }
  if spec.Method == "" {
    return fmt.Errorf("Method is required")
  }
  if spec.Url == "" {
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
  if spec.Delay != "" {
    if spec.delayD, err = time.ParseDuration(spec.Delay); err != nil {
      return fmt.Errorf("Invalid delay")
    }
  } else {
    spec.delayD = 10 * time.Millisecond
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

func prepareInvocation(target *InvocationSpec) *InvocationStatus {
  if target.BodyReader != nil && (target.Replicas > 1 || target.RequestCount > 1) {
    body, _ := ioutil.ReadAll(target.BodyReader)
    target.Body = string(body)
    target.BodyReader = nil
  }
  invocationStatus := &InvocationStatus{}
  tr := &http.Transport{
    MaxIdleConns:       1,
    IdleConnTimeout:    30 * time.Second,
    DisableCompression: true,
    Proxy:              http.ProxyFromEnvironment,
    Dial: (&net.Dialer{
      Timeout:   30 * time.Second,
      KeepAlive: time.Minute,
    }).Dial,
    TLSHandshakeTimeout: 10 * time.Second,
  }
  invocationStatus.client = &http.Client{Transport: tr}
  return invocationStatus
}

func prepareTargetURL(target *InvocationSpec) string {
  url := target.Url
  if target.SendId && !strings.Contains(url, "x-request-id") {
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

func collectStopRequests(invocationChannels *InvocationChannels, invocationStatuses map[string]*InvocationStatus) {
  if invocationChannels.StopChannel != nil {
    for {
      stopTarget := ""
      select {
      case stopTarget = <-invocationChannels.StopChannel:
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
    totalRemainingRequestCount := 0
    for _, target := range targets {
      invocationStatuses[target.Name] = prepareInvocation(target)
      totalRemainingRequestCount += (target.Replicas * target.RequestCount)
    }
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
            targetID := target.Name + "[" + strconv.Itoa(targetReplicaIndex) + "]"
            url := prepareTargetURL(target)
            totalRemainingRequestCount--
            responseChannels[index] = make(chan *InvocationResult)
            cases[index] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(responseChannels[index])}
            bodyReader := target.BodyReader
            target.BodyReader = nil
            if bodyReader == nil {
              bodyReader = strings.NewReader(target.Body)
            }
            go InvokeTarget(invocationID, target.Name, targetID, url, target.Method, target.Headers,
              bodyReader, reportBody, invocationStatuses[target.Name].client, responseChannels[index])
            index++
          }
          invocationStatuses[target.Name].completedRequestCount++
        }
      }
      for len(cases) > 0 {
        i, v, _ := reflect.Select(cases)
        cases = append(cases[:i], cases[i+1:]...)
        responses = append(responses, v.Interface().(*InvocationResult))
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

func InvokeTarget(index int, targetName string, targetID string, url string, method string, headers [][]string, body io.Reader, reportBody bool, client *http.Client, c chan *InvocationResult) {
  defer close(c)
  log.Printf("Invocation[%d]: Invoking targetID: %s, url: %s, method: %s, headers: %+v\n", index, targetID, url, method, headers)
  var result InvocationResult
  result.TargetName = targetName
  result.TargetID = targetID
  result.Headers = map[string][]string{}
  headers = append(headers, []string{"TargetID", targetID})
  if req, err := newClientRequest(method, url, headers, body); err == nil {
    if resp, err := client.Do(req); err == nil {
      defer resp.Body.Close()
      for header, values := range resp.Header {
        result.Headers[header] = values
      }
      result.Headers["Status"] = []string{resp.Status}
      result.Status = resp.Status
      result.StatusCode = resp.StatusCode
      if reportBody {
        if body, err := ioutil.ReadAll(resp.Body); err == nil {
          result.Body = string(body)
        }
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

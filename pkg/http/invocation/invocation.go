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
  Index       int
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
  TargetId   string
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

func InvokeTargets(targets []*InvocationSpec, invocationChannels *InvocationChannels, reportBody bool) []*InvocationResult {
  var responses []*InvocationResult
  invocationStatuses := map[string]*InvocationStatus{}
  if len(targets) > 0 {
    targetRequestCount := 0
    for _, target := range targets {
      invocationStatuses[target.Name] = prepareInvocation(target)
      targetRequestCount += (target.Replicas * target.RequestCount)
    }
    log.Printf("Invocation[%d]: Started with target count: %d, total requests to make: %d\n", invocationChannels.Index, len(targets), targetRequestCount)
    for {
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
      batchSize := 0
      for _, target := range targets {
        if invocationStatuses[target.Name].stopRequested {
          if invocationStatuses[target.Name].stopped {
            log.Printf("Invocation[%d]: Received stop request for target = %s that is already stopped\n", invocationChannels.Index, target.Name)
            invocationStatuses[target.Name].stopRequested = false
          } else {
            remaining := (target.RequestCount * target.Replicas) - (invocationStatuses[target.Name].completedRequestCount * target.Replicas)
            log.Printf("Invocation[%d]: Received stop request for target = %s with remaining requests %d\n", invocationChannels.Index, target.Name, remaining)
            targetRequestCount -= remaining
            invocationStatuses[target.Name].stopped = true
            invocationStatuses[target.Name].stopRequested = false
          }
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
            targetId := target.Name + "[" + strconv.Itoa(targetReplicaIndex) + "]"
            url := target.Url
            if target.SendId {
              if !strings.Contains(url, "?") {
                url += "?"
              } else if strings.Contains(url, "&") {
                url += "&"
              }
              url += "x-request-id="
              url += uuid.New().String()
            }
            targetRequestCount--
            responseChannels[index] = make(chan *InvocationResult)
            cases[index] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(responseChannels[index])}
            bodyReader := target.BodyReader
            if invocationStatuses[target.Name].completedRequestCount > 0 && i == 0 {
              bodyReader = strings.NewReader(target.Body)
            }
            go InvokeTarget(invocationChannels.Index, target.Name, targetId, url, target.Method, target.Headers,
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
      if targetRequestCount == 0 {
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
  log.Printf("Invocation[%d]: Finished with responses %d\n", invocationChannels.Index, len(responses))
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

func InvokeTarget(index int, targetName string, targetId string, url string, method string, headers [][]string, body io.Reader, reportBody bool, client *http.Client, c chan *InvocationResult) {
  defer close(c)
  var result InvocationResult
  result.TargetName = targetName
  result.TargetId = targetId
  result.Headers = map[string][]string{}
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
      log.Printf("Invocation[%d]: Target %s, url [%s] invocation failed with error: %s\n", index, targetId, url, err.Error())
      result.Status = err.Error()
    }
  } else {
    log.Printf("Invocation[%d]: Target %s, url [%s] failed to create request with error: %s\n", index, targetId, url, err.Error())
    result.Status = err.Error()
  }
  c <- &result
}

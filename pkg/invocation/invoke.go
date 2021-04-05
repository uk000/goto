package invocation

import (
  "bytes"
  "context"
  "crypto/tls"
  "fmt"
  . "goto/pkg/constants"
  "goto/pkg/grpc/pb"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "io"
  "io/ioutil"
  "net/http"
  "strconv"
  "sync"
  "time"

  "google.golang.org/grpc"
  "google.golang.org/grpc/metadata"
)

type InvocationClient struct {
  tracker       *InvocationTracker
  clientTracker *util.ClientTracker
  lock          sync.RWMutex
}

type InvocationRequest struct {
  requestID       string
  targetID        string
  url             string
  uri             string
  headers         map[string]string
  httpRequest     *http.Request
  grpcInput       *pb.Input
  grpcStreamInput *pb.StreamConfig
  requestReader   io.ReadCloser
  requestWriter   io.WriteCloser
  client          *util.ClientTracker
  tracker         *InvocationTracker
  result          *InvocationResult
}

func (tracker *InvocationTracker) invokeWithRetries(requestID string, targetID string, urls ...string) *InvocationResult {
  status := tracker.Status
  target := tracker.Target
  request := tracker.newClientRequest(requestID, targetID, urls...)
  if request == nil {
    return nil
  }
  result := request.result
  for i := 0; i <= target.Retries; i++ {
    if status.StopRequested || status.Stopped {
      break
    }
    if i > 0 {
      result.Retries++
      time.Sleep(target.retryDelayD)
    }
    if status.StopRequested || status.Stopped {
      break
    }
    request.invoke()
    metrics.UpdateTargetRequestCount(target.Name)
    retry := result.shouldRetry()
    if !retry {
      break
    } else if i < target.Retries {
      result.LastRetryReason = result.retryReason()
      tracker.logRetryRequired(result, target.Retries-i)
      result.FailedURLs[request.url]++
      request.requestID = fmt.Sprintf("%s-%d", requestID, i+2)
      request.addOrUpdateHeader(HeaderGotoRetryCount, strconv.Itoa(i+1))
      if target.Fallback && len(target.BURLS) > i {
        request.url = target.BURLS[i]
        if !tracker.client.prepareRequest(request) {
          tracker.logBRequestCreationFailed(result, target.BURLS[i])
        } else {
          result.URL = request.url
        }
      } else {
        request.addOrUpdateRequestId()
      }
      tracker.Status.incrementRetriesCount()
    }
  }
  if result != nil && !status.StopRequested && !status.Stopped {
    if result.err == nil {
      tracker.processResponse(result)
    } else {
      tracker.processError(result)
    }
  }
  return result
}

func (tracker *InvocationTracker) newClientRequest(requestID, targetID string, substituteURL ...string) *InvocationRequest {
  url := tracker.prepareRequestURL(requestID, targetID, substituteURL...)
  headers := tracker.prepareRequestHeaders(requestID, targetID, url)
  if tracker.client != nil {
    return tracker.newRequest(requestID, targetID, url, headers)
  }
  return nil
}

func (tracker *InvocationTracker) newRequest(requestID, targetID, url string, headers map[string]string) *InvocationRequest {
  ir := &InvocationRequest{
    requestID: requestID,
    targetID:  targetID,
    url:       url,
    headers:   headers,
    client:    tracker.client.clientTracker,
    tracker:   tracker,
  }
  ir.result = newInvocationResult(ir)
  tracker.client.prepareRequest(ir)
  return ir
}

func (tracker *InvocationTracker) prepareRequestURL(requestID, targetID string, substituteURL ...string) string {
  var url string
  target := tracker.Target
  if target.Random {
    if r := util.Random(len(target.BURLS) + 1); r == 0 {
      url = target.URL
    } else {
      url = target.BURLS[r-1]
    }
  } else if len(substituteURL) > 0 {
    url = substituteURL[0]
  } else {
    url = target.URL
  }
  return url
}

func (ir *InvocationRequest) addOrUpdateHeader(header, value string) {
  ir.headers[header] = value
  ir.httpRequest.Header.Del(header)
  ir.httpRequest.Header.Add(header, value)
}

func (ir *InvocationRequest) addOrUpdateRequestId() {
  if ir.httpRequest == nil {
    return
  }
  if ir.tracker.Target.SendID {
    q := ir.httpRequest.URL.Query()
    q.Del("x-request-id")
    q.Add("x-request-id", ir.requestID)
    ir.httpRequest.URL.RawQuery = q.Encode()
    ir.url = ir.httpRequest.URL.String()
    ir.addOrUpdateHeader(HeaderGotoTargetURL, ir.url)
  }
  ir.addOrUpdateHeader(HeaderGotoRequestID, ir.requestID)
}

func (client *InvocationClient) prepareRequest(ir *InvocationRequest) bool {
  client.lock.Lock()
  defer client.lock.Unlock()
  if client.clientTracker != nil {
    if client.clientTracker.IsGRPC {
      if len(client.tracker.payloads) > 1 {
        ir.uri = "/Goto/streamInOut"
        ir.grpcStreamInput = &pb.StreamConfig{ChunkSize: 1, ChunkCount: 1, Interval: "10ms"}
      } else {
        ir.uri = "/Goto/echo"
        ir.grpcInput = &pb.Input{}
      }
    } else if client.clientTracker.Client != nil {
      var requestReader io.ReadCloser
      var requestWriter io.WriteCloser
      if len(client.tracker.payloads) > 1 {
        requestReader, requestWriter = io.Pipe()
      } else if len(client.tracker.payloads) == 1 && len(client.tracker.payloads[0]) > 0 {
        requestReader = ioutil.NopCloser(bytes.NewReader(client.tracker.payloads[0]))
      }
      if req, err := http.NewRequest(client.tracker.Target.Method, ir.url, requestReader); err == nil {
        ir.httpRequest = req
        ir.addOrUpdateRequestId()
        for h, hv := range ir.headers {
          req.Header.Add(h, hv)
        }
        if req.Host == "" {
          req.Host = req.URL.Host
        }
        ir.uri = req.URL.Path
        ir.requestReader = requestReader
        ir.requestWriter = requestWriter
      } else {
        ir.result.err = err
        return false
      }
    }
  }
  ir.tracker.logRequestStart(ir.requestID, ir.targetID, ir.url)
  return true
}

func (tracker *InvocationTracker) prepareRequestHeaders(requestID, targetID, url string) map[string]string {
  headers := map[string]string{}
  for _, h := range tracker.Target.Headers {
    if len(h) >= 2 {
      headers[h[0]] = h[1]
    }
  }
  headers[HeaderGotoTargetID] = targetID
  return headers
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

func getHttpClientForTarget(tracker *InvocationTracker) *util.ClientTracker {
  target := tracker.Target
  invocationsLock.RLock()
  client := targetClients[target.Name]
  invocationsLock.RUnlock()
  if client == nil {
    client = util.CreateHTTPClient(target.Name, target.h2, target.AutoUpgrade, target.tls,
      target.requestTimeoutD, target.connTimeoutD, target.connIdleTimeoutD, metrics.ConnTracker)
    invocationsLock.Lock()
    targetClients[target.Name] = client
    invocationsLock.Unlock()
  }
  return client
}

func getGrpcClientForTarget(tracker *InvocationTracker) *util.ClientTracker {
  target := tracker.Target
  invocationsLock.RLock()
  client := targetClients[target.Name]
  invocationsLock.RUnlock()
  if client == nil {
    var opts []grpc.DialOption
    opts = append(opts, grpc.WithBlock())
    if target.authority != "" {
      opts = append(opts, grpc.WithAuthority(target.authority))
    }
    if !target.tls {
      opts = append(opts, grpc.WithInsecure())
    }
    if conn, err := grpc.Dial(target.URL, opts...); err == nil {
      client = util.NewHTTPClientTracker(nil, conn, nil)
      invocationsLock.Lock()
      targetClients[target.Name] = client
      invocationsLock.Unlock()
    } else {
      tracker.logConnectionFailed(err.Error())
    }
  }
  return client
}

func (ir *InvocationRequest) writeRequestPayload() {
  if ir.requestWriter != nil {
    go func() {
      size, first, last, err := util.WriteAndTrack(ir.requestWriter, ir.tracker.payloads, ir.tracker.Target.streamDelayD)
      if ir.tracker.Target.TrackPayload {
        if err == nil {
          ir.result.RequestPayloadSize = size
          ir.result.FirstByteOutAt = first.UTC().String()
          ir.result.LastByteOutAt = last.UTC().String()
        } else {
          ir.result.err = err
        }
      }
    }()
  }
}

func (ir *InvocationRequest) invoke() {
  start := time.Now()
  if ir.client.IsGRPC {
    ir.invokeGRPC()
  } else {
    ir.invokeHTTP()
  }
  end := time.Now()
  ir.result.trackRequest(start, end)
  ir.tracker.Status.trackRequest(end)
}

func (ir *InvocationRequest) invokeHTTP() {
  if ir.client == nil || ir.client.Tracker == nil || ir.client.Client == nil {
    fmt.Printf("Error: HTTP invocation attempted without a client")
    return
  }
  ir.client.Tracker.SetTLSConfig(tlsConfig(ir.httpRequest.Host, ir.tracker.Target.VerifyTLS))
  ir.writeRequestPayload()
  resp, err := ir.client.Do(ir.httpRequest)
  ir.result.processHTTPResponse(ir, resp, err)
}

func (ir *InvocationRequest) invokeGRPC() {
  ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(ir.headers))
  gotoClient := pb.NewGotoClient(ir.client.GrpcConn)
  if len(ir.tracker.payloads) > 1 {
    if streamClient, err := gotoClient.StreamInOut(ctx); err == nil {
      wg := sync.WaitGroup{}
      wg.Add(2)
      go func() {
        for _, payload := range ir.tracker.payloads {
          input := &pb.StreamConfig{ChunkSize: 1, ChunkCount: 1, Interval: "10ms", Payload: string(payload)}
          if err := streamClient.Send(input); err == nil {

          } else {
          }
        }
        wg.Done()
      }()
      go func() {
        for {
          if _, err := streamClient.Recv(); err != nil {
            fmt.Printf("Error: %s\n", err.Error())
            break
          }
        }
        wg.Done()
      }()
      wg.Wait()
    } else {
      ir.result.err = err
      fmt.Printf("Error while invoking GRPC stream request: %s\n", err.Error())
    }
  } else if len(ir.tracker.payloads) == 1 {
    _, err := gotoClient.Echo(ctx, &pb.Input{Payload: string(ir.tracker.payloads[0])})
    if err != nil {
      ir.result.err = err
      fmt.Printf("Error while invoking GRPC echo request: %s\n", err.Error())
    }
  }
}

func (c *InvocationClient) close() {
  c.lock.Lock()
  defer c.lock.Unlock()
  if c.clientTracker != nil {
    if c.clientTracker.GrpcConn != nil {
      c.clientTracker.GrpcConn.Close()
      c.clientTracker.GrpcConn = nil
    } else if c.clientTracker.Client != nil {
      c.clientTracker.Client.CloseIdleConnections()
      c.clientTracker.Client = nil
    }
  }
}

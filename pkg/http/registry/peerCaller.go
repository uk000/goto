package registry

import (
	"errors"
	"fmt"
	"goto/pkg/util"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func newPeerRequest(method string, url string, headers map[string][]string, payload string) (*http.Request, error) {
  var payloadReader *strings.Reader
  if len(payload) > 0 {
    payloadReader = strings.NewReader(payload)
  } else {
    payloadReader = strings.NewReader("")
  }
  if req, err := http.NewRequest(method, url, payloadReader); err == nil {
    for h, values := range headers {
      if strings.EqualFold(h, "host") {
        req.Host = values[0]
      } else {
        req.Header.Add(h, values[0])
      }
    }
    return req, nil
  } else {
    return nil, err
  }
}

func invokePeerAPI(pod *Pod, method, uri string, headers map[string][]string, payload string, expectedStatus int) (bool, string, error) {
  if req, err := newPeerRequest(method, pod.host+uri, headers, payload); err == nil {
    if resp, err := pod.client.Do(req); err == nil {
      data := util.Read(resp.Body)
      defer resp.Body.Close()
      if resp.StatusCode == expectedStatus {
        return true, data, nil
      } else {
        return false, data, fmt.Errorf("Expected status %d but received %d", expectedStatus, resp.StatusCode)
      }
    } else {
      return false, "", err
    }
  } else {
    return false, "", err
  }
}

func invokePod(peer string, pod *Pod, peerPodCount int, method string, uri string, headers map[string][]string, payload string,
  expectedStatus int, retryCount int, onPodDone func(string, *Pod, string, error)) bool {
  var success bool
  var err error
  var response string
  for i := 0; i <= retryCount; i++ {
    success, response, err = invokePeerAPI(pod, method, uri, headers, payload, expectedStatus)
    if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
      log.Printf("Peer %s Pod %s timed out for URI %s. Retrying... %d\n", peer, pod.Address, uri, i+1)
      time.Sleep(2 * time.Second)
      continue
    } else {
      break
    }
  }
  if success && err == nil {
    onPodDone(peer, pod, response, nil)
  } else if err != nil {
    if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
      log.Printf("Peer %s Pod %s has too many timouts. Marking pod as bad and removing from future operations\n", peer, pod.Address)
      pod.lock.Lock()
      pod.Healthy = false
      pod.lock.Unlock()
    }
    onPodDone(peer, pod, response, err)
  } else {
    onPodDone(peer, pod, response, errors.New(""))
  }
  return success
}

func invokeForPodsWithHeadersAndPayload(peerPods map[string][]*Pod, method string, uri string, headers map[string][]string, payload string, expectedStatus int, retryCount int, useUnhealthy bool,
  onPodDone func(string, *Pod, string, error), onPeerDone ...func(string)) map[string]map[string]bool {
  result := map[string]map[string]bool{}
  resultLock := sync.Mutex{}
  wg := &sync.WaitGroup{}
  for p := range peerPods {
    peer := p
    pods := peerPods[p]
    resultLock.Lock()
    if result[peer] == nil {
      result[peer] = map[string]bool{}
    }
    resultLock.Unlock()
    for i := range pods {
      pod := pods[i]
      pod.lock.RLock()
      healthy := pod.Healthy
      pod.lock.RUnlock()
      if !useUnhealthy && !healthy {
        log.Printf("Skipping bad pod %s for peer %s for URI %s.\n", pod.Address, peer, uri)
        resultLock.Lock()
        result[peer][pod.Address] = false
        resultLock.Unlock()
        continue
      }
      wg.Add(1)
      go func() {
        success := invokePod(peer, pod, len(pods), method, uri, headers, payload, expectedStatus, retryCount, onPodDone)
        resultLock.Lock()
        result[peer][pod.Address] = success
        resultLock.Unlock()
        wg.Done()
      }()
    }
  }
  wg.Wait()
  if len(onPeerDone) > 0 {
    for peer := range peerPods {
      onPeerDone[0](peer)
    }
  }
  return result
}

func invokeForPods(peerPods map[string][]*Pod, method string, uri string, expectedStatus int, retryCount int, useUnhealthy bool,
  onPodDone func(string, *Pod, string, error), onPeerDone ...func(string)) map[string]map[string]bool {
  return invokeForPodsWithHeadersAndPayload(peerPods, method, uri, nil, "", expectedStatus, retryCount, useUnhealthy,
    onPodDone, onPeerDone...)
}

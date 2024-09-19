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

package registry

import (
  "errors"
  "fmt"
  "goto/pkg/transport"
  "goto/pkg/util"
  "log"
  "net"
  "net/http"
  "sync"
  "time"
)

type OnPodDone func(string, *Pod, interface{}, error)
type OnPeerDone func(string)

func invokePeerAPI(pod *Pod, method, uri string, headers http.Header, payload []byte, expectedStatus int) (bool, interface{}, error) {
  if req, err := transport.CreateRequest(method, pod.URL+uri, headers, payload, nil); err == nil {
    if resp, err := pod.client.Do(req); err == nil {
      var data interface{}
      defer resp.Body.Close()
      if util.IsJSONContentType(resp.Header) {
        data = map[string]interface{}{}
        if err := util.ReadJsonPayloadFromBody(resp.Body, &data); err != nil {
          fmt.Println(err.Error())
        }
      } else {
        data = util.Read(resp.Body)
      }
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

func invokePod(peer string, pod *Pod, peerPodCount int, method string, uri string, headers http.Header,
  payload []byte, expectedStatus int, retryCount int, onPodDone OnPodDone) bool {
  if pod.client == nil || pod.Offline {
    log.Printf("Skipping offline/loaded/cloned Pod %s for Peer %s\n", pod.Address, peer)
    return true
  }
  var success bool
  var err error
  var response interface{}
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
      log.Printf("Peer %s Pod %s has too many timeouts. Marking pod as bad and removing from future operations\n", peer, pod.Address)
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

func invokeForPodsWithHeadersAndPayload(peerPods PeerPods, method string, uri string, headers http.Header,
  payload []byte, expectedStatus int, retryCount int, useUnhealthy bool, onPodDone OnPodDone, onPeerDone ...OnPeerDone) PeerResults {
  result := PeerResults{}
  resultLock := sync.Mutex{}
  wg := &sync.WaitGroup{}
  for p := range peerPods {
    peer := p
    pods := peerPods[p]
    resultLock.Lock()
    if result[peer] == nil {
      result[peer] = PodResults{}
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

func invokeForPodsWithPayload(peerPods PeerPods, method string, uri string, payload string, expectedStatus int,
  retryCount int, useUnhealthy bool, onPodDone OnPodDone, onPeerDone ...OnPeerDone) PeerResults {
  return invokeForPodsWithHeadersAndPayload(peerPods, method, uri, nil, []byte(payload), expectedStatus, retryCount, useUnhealthy,
    onPodDone, onPeerDone...)
}

func invokeForPods(peerPods PeerPods, method string, uri string, expectedStatus int, retryCount int, useUnhealthy bool,
  onPodDone OnPodDone, onPeerDone ...OnPeerDone) PeerResults {
  return invokeForPodsWithHeadersAndPayload(peerPods, method, uri, nil, nil, expectedStatus, retryCount, useUnhealthy,
    onPodDone, onPeerDone...)
}

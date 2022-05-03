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

package proxy

import (
  "goto/pkg/global"
  "goto/pkg/util"
  "io"
  "log"
  "net"
  "sync"
  "time"
)

func ProxyTCPConnection(port int, downConn net.Conn) {
  defer downConn.Close()
  p := getProxyForPort(port)
  if p.hasSNITargets() {
    p.proxyTCPWithSNI(downConn)
  } else {
    p.proxyTCPOpaque(downConn)
  }
}

func (p *Proxy) proxyTCPOpaque(downConn net.Conn) {
  p.proxyPipe(downConn, p.getMatchingTCPTarget("").target, nil)
}

func (p *Proxy) proxyTCPWithSNI(downConn net.Conn) {
  sni, buff, err := util.ReadTLSSNIFromConn(downConn)
  if err != nil {
    log.Printf("TCP Proxy[%d]: Error while reading downstream SNI from Connection [%s]: %s\n", p.Port, downConn.RemoteAddr().String(), err.Error())
  } else {
    log.Printf("TCP Proxy[%d]: Read downstream SNI = [%s]\n", p.Port, sni)
  }
  if m := p.getMatchingTCPTarget(sni); m != nil {
    p.proxyPipe(downConn, m.target, buff.Bytes())
  } else {
    log.Printf("TCP Proxy[%d]: No matching target found for SNI [%s] from downstream [%s]: %s\n", p.Port, sni, downConn.RemoteAddr().String(), err.Error())
  }
}

func (p *Proxy) proxyPipe(downConn net.Conn, target *ProxyTarget, pendingData []byte) {
  addr, err := net.ResolveTCPAddr("tcp", target.Endpoint)
  if err != nil {
    log.Printf("TCP Proxy[%d]: Error while resolving upstream address: %s\n", p.Port, err.Error())
    return
  }
  upConn, err := net.DialTCP("tcp", nil, addr)
  if err != nil {
    log.Printf("TCP Proxy[%d]: Error while dialing upstream address: %s\n", p.Port, err.Error())
    return
  }
  defer upConn.Close()
  log.Printf("TCP Proxy[%d]: Opening proxy pipe between [%s] and [%s]\n", p.Port, downConn.RemoteAddr().String(), upConn.RemoteAddr().String())
  err = util.Write(pendingData, upConn)
  if err != nil {
    if err == io.EOF {
      log.Printf("TCP Proxy[%d]: Connection [%s] closed by remote party\n", p.Port, upConn.RemoteAddr().String())
    } else {
      log.Printf("TCP Proxy[%d]: Error writing to Connection [%s]: %s\n", p.Port, upConn.RemoteAddr().String(), err.Error())
    }
    return
  }
  wg := &sync.WaitGroup{}
  wg.Add(2)
  done := make(chan bool, 2)
  defer close(done)
  go p.pipe(downConn, upConn, target, wg, done)
  go p.pipe(upConn, downConn, target, wg, done)
  wg.Wait()
  log.Printf("TCP Proxy[%d]: Closing proxy pipe between [%s] and [%s]\n", p.Port, downConn.RemoteAddr().String(), upConn.RemoteAddr().String())
}

func (p *Proxy) pipe(from, to net.Conn, target *ProxyTarget, wg *sync.WaitGroup, done chan bool) {
  defer wg.Done()
  fromAddr := from.RemoteAddr().String()
  toAddr := to.RemoteAddr().String()
  buff := make([]byte, global.MaxMTUSize)
  for {
    select {
    case <-done:
      return
    default:
      break
    }
    from.SetReadDeadline(time.Now().Add(5 * time.Second))
    n, err := from.Read(buff)
    if err != nil {
      if oe, ok := err.(*net.OpError); ok && oe != nil && oe.Timeout() {
        continue
      } else if err == io.EOF {
        log.Printf("TCP Proxy[%d]: Connection [%s] closed by remote party\n", p.Port, fromAddr)
      } else {
        log.Printf("TCP Proxy[%d]: Error reading from Connection [%s]: %s\n", p.Port, fromAddr, err.Error())
      }
      done <- true
      return
    }
    p.applyDelay(target, toAddr, nil)
    if p.shouldDropPacket(target) {
      log.Printf("TCP Proxy[%d]: Packet dropped for connection [%s]\n", p.Port, toAddr)
    } else {
      err = util.Write(buff[:n], to)
      if err != nil {
        if err == io.EOF {
          log.Printf("TCP Proxy[%d]: Connection [%s] closed by remote party\n", p.Port, toAddr)
        } else {
          log.Printf("TCP Proxy[%d]: Error writing to Connection [%s]: %s\n", p.Port, toAddr, err.Error())
        }
        done <- true
        return
      }
    }
  }
}

func (p *Proxy) shouldDropPacket(target *ProxyTarget) bool {
  target.lock.Lock()
  defer target.lock.Unlock()
  if target.DropPct <= 0 {
    return false
  }
  target.tcpWriteCount++
  target.tcpWriteSinceLastDrop++
  if target.tcpWriteSinceLastDrop >= (100 / target.DropPct) {
    target.tcpDropCount++
    target.tcpWriteSinceLastDrop = 0
    return true
  }
  return false
}

func (p *Proxy) hasSNITargets() bool {
  for _, target := range p.TCPTargets {
    if target.MatchAll != nil && target.MatchAll.SNI != "" ||
      target.MatchAny != nil && target.MatchAny.SNI != "" {
      return true
    }
  }
  return false
}

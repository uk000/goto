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

package proxy

import (
  "goto/pkg/global"
  "goto/pkg/util"
  "io"
  "log"
  "net"
  "os"
  "sync"
  "syscall"
  "time"
)

func ProxyTCPConnection(port int, downConn net.Conn) {
  defer downConn.Close()
  startTime := time.Now()
  p := getProxyForPort(port)
  if p.hasSNITargets() {
    p.proxyTCPWithSNI(downConn, startTime)
  } else {
    p.proxyTCPOpaque(downConn, startTime)
  }
}

func (p *Proxy) proxyTCPOpaque(downConn net.Conn, startTime time.Time) {
  if m := p.getMatchingTCPTarget(""); m != nil {
    p.TCPTracker.incrementMatchCounts(m.target.Name, "")
    st := p.TCPTracker.getOrAddTargetSessionTracker(m.target.Name, downConn.RemoteAddr().String())
    st.Downstream.StartTime = startTime
    p.proxyPipe(downConn, m.target, nil)
  } else {
    p.TCPTracker.incrementRejectCount("")
    log.Printf("TCP Proxy[%d]: No matching target found for downstream client [%s]\n", p.Port, downConn.RemoteAddr().String())
  }
}

func (p *Proxy) proxyTCPWithSNI(downConn net.Conn, startTime time.Time) {
  sni, buff, err := util.ReadTLSSNIFromConn(downConn)
  if err != nil {
    log.Printf("TCP Proxy[%d]: Error while reading downstream SNI from Connection [%s]: %s\n", p.Port, downConn.RemoteAddr().String(), err.Error())
  } else {
    log.Printf("TCP Proxy[%d]: Read downstream SNI = [%s]\n", p.Port, sni)
  }
  if m := p.getMatchingTCPTarget(sni); m != nil {
    st := p.TCPTracker.getOrAddTargetSessionTracker(m.target.Name, downConn.RemoteAddr().String())
    st.SNI = sni
    st.Downstream.StartTime = startTime
    st.Downstream.FirstByteInAt = startTime
    st.Downstream.TotalBytesRead = buff.Len()
    st.Downstream.TotalReads = 1
    p.TCPTracker.incrementMatchCounts(m.target.Name, sni)
    p.proxyPipe(downConn, m.target, buff.Bytes())
  } else {
    p.TCPTracker.incrementRejectCount(sni)
    log.Printf("TCP Proxy[%d]: No matching target found for SNI [%s] from downstream [%s]\n", p.Port, sni, downConn.RemoteAddr().String())
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
  done := make(chan bool, 2)
  downAddr := downConn.RemoteAddr().String()
  upAddr := upConn.RemoteAddr().String()
  st := p.TCPTracker.getOrAddTargetSessionTracker(target.Name, downAddr)
  st.Upstream.StartTime = time.Now()
  defer func() {
    log.Printf("TCP Proxy[%d]: Closing proxy pipe between [%s] and [%s]\n", p.Port, downAddr, upAddr)
    close(done)
    upConn.Close()
    st.Downstream.Closed = true
    st.Upstream.Closed = true
  }()
  log.Printf("TCP Proxy[%d]: Opening proxy pipe between [%s] and [%s]\n", p.Port, downAddr, upAddr)
  if len(pendingData) > 0 {
    err = util.Write(pendingData, upConn)
    if err != nil {
      if err == io.EOF {
        st.Upstream.RemoteClosed = true
        log.Printf("TCP Proxy[%d]: Connection [%s] closed by remote party\n", p.Port, upAddr)
      } else {
        st.Upstream.WriteError = true
        log.Printf("TCP Proxy[%d]: Error writing to Connection [%s]: %s\n", p.Port, upAddr, err.Error())
      }
      return
    } else {
      st.Upstream.TotalBytesWritten = len(pendingData)
      st.Upstream.TotalWrites = 1
    }
  }
  wg := &sync.WaitGroup{}
  wg.Add(2)
  go p.pipe(downConn, upConn, st.Downstream, st.Upstream, target, wg, done)
  go p.pipe(upConn, downConn, st.Upstream, st.Downstream, target, wg, done)
  wg.Wait()
}

func (p *Proxy) pipe(from, to net.Conn, fromTracker, toTracker *ConnTracker, target *ProxyTarget, wg *sync.WaitGroup, done chan bool) {
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
    readTime := time.Now()
    if err != nil {
      if oe, ok := err.(*net.OpError); ok && oe != nil {
        if oe.Timeout() {
          continue
        }
        if se, ok := oe.Err.(*os.SyscallError); ok && se != nil {
          if se.Err.Error() == syscall.ECONNRESET.Error() {
            fromTracker.RemoteClosed = true
            log.Printf("TCP Proxy[%d]: Connection [%s] reset by remote party\n", p.Port, fromAddr)
          }
        }
      } else if err == io.EOF {
        fromTracker.RemoteClosed = true
        log.Printf("TCP Proxy[%d]: Connection [%s] closed by remote party\n", p.Port, fromAddr)
      }
      if !fromTracker.RemoteClosed {
        fromTracker.ReadError = true
        log.Printf("TCP Proxy[%d]: Error reading from Connection [%s]: %s\n", p.Port, fromAddr, err.Error())
      }
      done <- true
      return
    } else {
      fromTracker.TotalReads++
      fromTracker.TotalBytesRead += n
      if fromTracker.FirstByteInAt.IsZero() {
        fromTracker.FirstByteInAt = readTime
      }
      fromTracker.LastByteInAt = readTime
    }
    if p.applyDelay(target, toAddr, nil) {
      toTracker.DelayCount++
    }
    if p.shouldDrop(target) {
      toTracker.DropCount++
      log.Printf("TCP Proxy[%d]: Packet dropped for connection [%s]\n", p.Port, toAddr)
    } else {
      err = util.Write(buff[:n], to)
      writeTime := time.Now()
      if err != nil {
        if err == io.EOF {
          toTracker.RemoteClosed = true
          log.Printf("TCP Proxy[%d]: Connection [%s] closed by remote party\n", p.Port, toAddr)
        } else {
          toTracker.WriteError = true
          log.Printf("TCP Proxy[%d]: Error writing to Connection [%s]: %s\n", p.Port, toAddr, err.Error())
        }
        done <- true
        return
      } else {
        toTracker.TotalWrites++
        toTracker.TotalBytesWritten += n
        if toTracker.FirstByteOutAt.IsZero() {
          toTracker.FirstByteOutAt = writeTime
        }
        toTracker.LastByteOutAt = writeTime
      }
    }
  }
}

func (p *Proxy) hasSNITargets() bool {
  for _, target := range p.TCPTargets {
    if target.MatchAll != nil && target.MatchAll.sniRegexp != nil ||
      target.MatchAny != nil && target.MatchAny.sniRegexp != nil {
      return true
    }
  }
  return false
}

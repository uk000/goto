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

package tcp

import (
  "fmt"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/util"
  "math"
  "net/http"
  "strconv"
  "strings"
  "time"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "tcp", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  tcpRouter := util.PathPrefix(r, "/server?/tcp")
  util.AddRoute(tcpRouter, "/{port}/configure", configureTCP, "POST")
  util.AddRoute(tcpRouter, "/{port}/timeout/set/read={duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/timeout/set/write={duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/timeout/set/idle={duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/connection/set/life={duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/echo/response/set/delay={duration}", setConnectionDurationConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/stream/payload={payloadSize}/duration={duration}/delay={delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/stream/chunksize={chunkSize}/duration={duration}/delay={delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/stream/chunksize={chunkSize}/count={chunkCount}/delay={delay}", setStreamConfig, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/expect/payload/length={length}", setExpectedPayloadLength, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/expect/payload", setExpectedPayload, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/mode/validate={enable}", configurePayloadValidation, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/mode/stream={enable}", setModes, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/mode/echo={enable}", setModes, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/mode/conversation={enable}", setModes, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/mode/silentlife={enable}", setModes, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/mode/closeatfirst={enable}", setModes, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/set/payload={enable}", setModes, "PUT", "POST")
  util.AddRoute(tcpRouter, "/{port}/active", getActiveConnections, "GET")
  util.AddRoute(tcpRouter, "/active", getActiveConnections, "GET")
  util.AddRoute(tcpRouter, "/{port}/history/{mode}", getConnectionHistory, "GET")
  util.AddRoute(tcpRouter, "/{port}/history", getConnectionHistory, "GET")
  util.AddRoute(tcpRouter, "/history/{mode}", getConnectionHistory, "GET")
  util.AddRoute(tcpRouter, "/history", getConnectionHistory, "GET")
  util.AddRoute(tcpRouter, "/{port}/history/clear", clearConnectionHistory, "POST")
  util.AddRoute(tcpRouter, "/history/clear", clearConnectionHistory, "POST")
}

func validateListener(w http.ResponseWriter, r *http.Request) bool {
  port := util.GetIntParamValue(r, "port")
  if !global.IsListenerPresent(port) {
    w.WriteHeader(http.StatusBadRequest)
    msg := fmt.Sprintf("No listener for port %d", port)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Configuration Rejected", msg, r)
    return false
  }
  return true
}

func validateTCPListener(w http.ResponseWriter, r *http.Request) bool {
  if !validateListener(w, r) {
    return false
  }
  port := util.GetIntParamValue(r, "port")
  if tcpListeners[port] == nil {
    w.WriteHeader(http.StatusBadRequest)
    msg := fmt.Sprintf("Port %d is not a TCP listener", port)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Configuration Rejected", msg, r)
    return false
  }
  return true
}

func configureTCP(w http.ResponseWriter, r *http.Request) {
  if validateListener(w, r) {
    msg := ""
    port := util.GetIntParamValue(r, "port")
    tcpConfig := &TCPConfig{Port: port}
    if err := util.ReadJsonPayload(r, tcpConfig); err == nil {
      if _, msg = InitTCPConfig(port, tcpConfig); msg == "" {
        msg = fmt.Sprintf("TCP configuration applied to port %d", port)
        events.SendRequestEventJSON("TCP Configured", tcpConfig.ListenerID, tcpConfig, r)
      } else {
        w.WriteHeader(http.StatusBadRequest)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
      events.SendRequestEvent("TCP Configuration Rejected", msg, r)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func (tcp *TCPConfig) configure() string {
  msg := ""
  if tcp.ReadTimeout != "" {
    if tcp.ReadTimeoutD = util.ParseDuration(tcp.ReadTimeout); tcp.ReadTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid read timeout: %s]", tcp.ReadTimeout)
    }
  } else {
    tcp.ReadTimeoutD = 30 * time.Second
  }
  if tcp.WriteTimeout != "" {
    if tcp.WriteTimeoutD = util.ParseDuration(tcp.WriteTimeout); tcp.WriteTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", tcp.WriteTimeout)
    }
  } else {
    tcp.WriteTimeoutD = 30 * time.Second
  }
  if tcp.ConnectTimeout != "" {
    if tcp.ConnectTimeoutD = util.ParseDuration(tcp.ConnectTimeout); tcp.ConnectTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", tcp.ConnectTimeout)
    }
  } else {
    tcp.ConnectTimeoutD = 30 * time.Second
  }
  if tcp.ConnIdleTimeout != "" {
    if tcp.ConnIdleTimeoutD = util.ParseDuration(tcp.ConnIdleTimeout); tcp.ConnIdleTimeoutD < 0 {
      msg += fmt.Sprintf("[Invalid write timeout: %s]", tcp.ConnIdleTimeout)
    }
  } else {
    tcp.ConnIdleTimeoutD = 30 * time.Second
  }
  if tcp.ConnectionLife != "" {
    if tcp.ConnectionLifeD = util.ParseDuration(tcp.ConnectionLife); tcp.ConnectionLifeD < 0 {
      msg += fmt.Sprintf("[Invalid connection life: %s]", tcp.ConnectionLife)
    }
  } else {
    tcp.ConnectionLifeD = 0
  }
  if tcp.ResponseDelay != "" {
    if tcp.ResponseDelayD = util.ParseDuration(tcp.ResponseDelay); tcp.ResponseDelayD < 0 {
      msg += fmt.Sprintf("[Invalid response delay: %s]", tcp.ResponseDelay)
    }
  } else {
    tcp.ResponseDelayD = 0
  }
  if tcp.EchoResponseDelay != "" {
    if tcp.EchoResponseDelayD = util.ParseDuration(tcp.EchoResponseDelay); tcp.EchoResponseDelayD < 0 {
      msg += fmt.Sprintf("[Invalid echo response delay: %s]", tcp.EchoResponseDelay)
    }
  } else {
    tcp.EchoResponseDelayD = 0
  }
  if tcp.EchoResponseSize <= 0 {
    tcp.EchoResponseSize = 10
  }
  tcp.configureStream()
  return msg
}

func (tcp *TCPConfig) configureStream() {
  tcp.configureStreamParams(tcp.StreamPayloadSize, tcp.StreamChunkSize, tcp.StreamDuration, tcp.StreamChunkDelay, tcp.StreamChunkCount)
}

func (tcp *TCPConfig) configureStreamParams(sPayloadSize, sChunkSize, sDuration, sChunkDelay string, chunkCount int) {
  requestedPayloadSize := util.ParseSize(sPayloadSize)
  payloadSize := requestedPayloadSize
  chunkSize := util.ParseSize(sChunkSize)
  streamDuration := util.ParseDuration(sDuration)
  chunkDelay := util.ParseDuration(sChunkDelay)

  if payloadSize > 0 && chunkSize > 0 && chunkCount > 0 {
    chunkCount = 0
  } else if streamDuration > 0 && chunkDelay > 0 && chunkCount > 0 {
    chunkCount = 0
  }
  if streamDuration > 0 && chunkDelay > 0 && payloadSize > 0 && chunkSize > 0 {
    chunkSize = 0
  }

  for i := 0; i < 1; i++ {
    if payloadSize == 0 {
      payloadSize = computePayloadSize(chunkSize, chunkCount, chunkDelay, streamDuration)
    }
    if streamDuration == 0 {
      streamDuration = computeStreamDuration(payloadSize, chunkSize, chunkCount, chunkDelay)
    }
    if chunkCount == 0 {
      chunkCount = computeChunkCount(payloadSize, chunkSize, chunkDelay, streamDuration)
    }
    if chunkSize == 0 {
      chunkSize = computeChunkSize(payloadSize, chunkCount, chunkDelay, streamDuration)
    }
    if chunkDelay == 0 {
      chunkDelay = computeChunkDelay(payloadSize, chunkSize, chunkCount, streamDuration)
    }
  }
  if tcp.Stream {
    if chunkDelay == 0 {
      chunkDelay = 100 * time.Millisecond
    }
    if chunkSize == 0 {
      chunkSize = computeChunkSize(payloadSize, chunkCount, chunkDelay, streamDuration)
      if chunkSize == 0 {
        chunkSize = 100
      }
    }
    if chunkCount == 0 {
      chunkCount = computeChunkCount(payloadSize, chunkSize, chunkDelay, streamDuration)
      if chunkCount == 0 {
        chunkCount = 10
      }
    }
    if payloadSize == 0 {
      payloadSize = computePayloadSize(chunkSize, chunkCount, chunkDelay, streamDuration)
      if payloadSize == 0 {
        payloadSize = 1000
      }
    }
    if streamDuration == 0 {
      streamDuration = computeStreamDuration(payloadSize, chunkSize, chunkCount, chunkDelay)
      if streamDuration == 0 {
        streamDuration = 1 * time.Second
      }
    }
  }
  tcp.StreamChunkSizeV = chunkSize
  tcp.StreamChunkSize = strconv.Itoa(chunkSize)
  tcp.StreamChunkCount = chunkCount
  tcp.StreamChunkDelayD = chunkDelay
  tcp.StreamChunkDelay = chunkDelay.String()
  tcp.StreamDurationD = streamDuration
  tcp.StreamDuration = streamDuration.String()
  tcp.StreamPayloadSizeV = payloadSize
  if math.Abs(float64(requestedPayloadSize-(chunkCount*chunkSize))) > 10 {
    tcp.StreamPayloadSize = strconv.Itoa(payloadSize)
  }
}

func computeChunkCount(payloadSize, chunkSize int, chunkDelay, streamDuration time.Duration) int {
  if payloadSize > 0 && chunkSize > 0 {
    return payloadSize / chunkSize
  } else if streamDuration > 0 && chunkDelay > 0 {
    return int((streamDuration.Milliseconds() / chunkDelay.Milliseconds()))
  }
  return 0
}

func computeChunkDelay(payloadSize, chunkSize, chunkCount int, streamDuration time.Duration) time.Duration {
  if streamDuration > 0 {
    if chunkCount > 0 {
      return streamDuration / time.Duration(chunkCount)
    } else if payloadSize > 0 && chunkSize > 0 {
      return streamDuration / time.Duration(payloadSize/chunkSize)
    }
  }
  return 0
}

func computeChunkSize(payloadSize, chunkCount int, chunkDelay, streamDuration time.Duration) int {
  if payloadSize > 0 {
    if chunkCount > 0 {
      return payloadSize / chunkCount
    } else if streamDuration > 0 && chunkDelay > 0 {
      return payloadSize / int((streamDuration.Milliseconds() / chunkDelay.Milliseconds()))
    }
  }
  return 0
}

func computePayloadSize(chunkSize, chunkCount int, chunkDelay, streamDuration time.Duration) int {
  if chunkSize > 0 {
    if chunkCount > 0 {
      return chunkSize * chunkCount
    } else if streamDuration > 0 && chunkDelay > 0 {
      return chunkSize * int((streamDuration.Milliseconds() / chunkDelay.Milliseconds()))
    }
  }
  return 0
}

func computeStreamDuration(payloadSize, chunkSize, chunkCount int, chunkDelay time.Duration) time.Duration {
  if chunkDelay > 0 {
    if chunkCount > 0 {
      return chunkDelay * time.Duration(chunkCount)
    } else if payloadSize > 0 && chunkSize > 0 {
      return chunkDelay * time.Duration(payloadSize/chunkSize)
    }
  }
  return 0
}

func setConnectionDurationConfig(w http.ResponseWriter, r *http.Request) {
  if validateTCPListener(w, r) {
    msg := ""
    port := util.GetIntParamValue(r, "port")
    dur := util.GetStringParamValue(r, "duration")
    setLife := strings.Contains(r.RequestURI, "connection/set/life")
    setReadTimeout := strings.Contains(r.RequestURI, "timeout/set/read")
    setWriteTimeout := strings.Contains(r.RequestURI, "timeout/set/write")
    setIdleTimeout := strings.Contains(r.RequestURI, "timeout/set/idle")
    setEchoResponseDelay := strings.Contains(r.RequestURI, "echo/response/set/delay")
    if d := util.ParseDuration(dur); d >= 0 {
      tcpConfig := getTCPConfig(port)
      if setLife {
        tcpConfig.ConnectionLifeD = d
        msg = fmt.Sprintf("Connection will close %s after creation for listener %d", dur, port)
      } else if setReadTimeout {
        tcpConfig.ReadTimeoutD = d
        msg = fmt.Sprintf("Read timeout set to %s for listener %d", dur, port)
      } else if setWriteTimeout {
        tcpConfig.WriteTimeoutD = d
        msg = fmt.Sprintf("Write timeout set to %s for listener %d", dur, port)
      } else if setIdleTimeout {
        tcpConfig.ConnIdleTimeoutD = d
        msg = fmt.Sprintf("Connection idle timeout set to %s for listener %d", dur, port)
      } else if setEchoResponseDelay {
        tcpConfig.EchoResponseDelayD = d
        msg = fmt.Sprintf("Response will be sent %s after connection for listener %d", dur, port)
      }
    } else {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Port [%d]: Invalid duration: %s", port, dur)
    }
    events.SendRequestEvent("TCP Connection Duration Configured", msg, r)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setStreamConfig(w http.ResponseWriter, r *http.Request) {
  if validateTCPListener(w, r) {
    msg := ""
    port := util.GetIntParamValue(r, "port")
    payloadSize := util.GetStringParamValue(r, "payloadSize")
    chunkSize := util.GetStringParamValue(r, "chunkSize")
    chunkCount := util.GetIntParamValue(r, "chunkCount")
    duration := util.GetStringParamValue(r, "duration")
    delay := util.GetStringParamValue(r, "delay")
    if (chunkSize == "" && payloadSize == "") || (duration == "" && chunkCount == 0) {
      w.WriteHeader(http.StatusBadRequest)
      msg = "Invalid parameters for streaming"
    } else {
      tcpConfig := getTCPConfig(port)
      tcpConfig.configureStreamParams(payloadSize, chunkSize, duration, delay, chunkCount)
      tcpConfig.turnOffAllModes()
      tcpConfig.Stream = true
      msg = fmt.Sprintf("Connection will stream [%d] chunks of size [%d] with delay [%s] for a duration of [%s] for listener %d",
        tcpConfig.StreamChunkCount, tcpConfig.StreamChunkSizeV, tcpConfig.StreamChunkDelayD.String(), tcpConfig.StreamDurationD.String(), port)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Streaming Configured", msg, r)
  }
}

func setExpectedPayloadLength(w http.ResponseWriter, r *http.Request) {
  if validateTCPListener(w, r) {
    port := util.GetIntParamValue(r, "port")
    tcpConfig := getTCPConfig(port)
    tcpConfig.ExpectedPayloadLength = util.GetIntParamValue(r, "length")
    tcpConfig.turnOffAllModes()
    tcpConfig.ValidatePayloadLength = true
    tcpConfig.ExpectedPayload = nil
    msg := fmt.Sprintf("Stored expected payload length [%d]", tcpConfig.ExpectedPayloadLength)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Expected Payload Configured", msg, r)
  }
}

func setExpectedPayload(w http.ResponseWriter, r *http.Request) {
  if validateTCPListener(w, r) {
    port := util.GetIntParamValue(r, "port")
    tcpConfig := getTCPConfig(port)
    tcpConfig.ExpectedPayload = util.ReadBytes(r.Body)
    tcpConfig.ExpectedPayloadLength = len(tcpConfig.ExpectedPayload)
    tcpConfig.turnOffAllModes()
    tcpConfig.ValidatePayloadLength = true
    tcpConfig.ValidatePayloadContent = true
    msg := fmt.Sprintf("Stored expected payload content of length [%d]", tcpConfig.ExpectedPayloadLength)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Expected Payload Configured", msg, r)
  }
}

func configurePayloadValidation(w http.ResponseWriter, r *http.Request) {
  if validateTCPListener(w, r) {
    msg := ""
    port := util.GetIntParamValue(r, "port")
    tcpConfig := getTCPConfig(port)
    enable := util.GetBoolParamValue(r, "enable")
    if enable {
      tcpConfig.turnOffAllModes()
      tcpConfig.ValidatePayloadLength = true
      if len(tcpConfig.ExpectedPayload) > 0 {
        tcpConfig.ValidatePayloadContent = true
        msg = fmt.Sprintf("Will validate payload content of size [%d]", tcpConfig.ExpectedPayloadLength)
      } else {
        msg = fmt.Sprintf("Will validate payload length [%d]", tcpConfig.ExpectedPayloadLength)
      }
    } else {
      tcpConfig.ValidatePayloadLength = false
      tcpConfig.ValidatePayloadContent = false
      msg = "Payload validation turned off"
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Payload Validation Configured", msg, r)
  }
}

func setModes(w http.ResponseWriter, r *http.Request) {
  if validateTCPListener(w, r) {
    msg := ""
    port := util.GetIntParamValue(r, "port")
    tcpConfig := getTCPConfig(port)
    enable := util.GetBoolParamValue(r, "enable")
    echo := strings.Contains(r.RequestURI, "echo")
    conversation := strings.Contains(r.RequestURI, "conversation")
    stream := strings.Contains(r.RequestURI, "stream")
    silentlife := strings.Contains(r.RequestURI, "silentlife")
    closeatfirst := strings.Contains(r.RequestURI, "closeatfirst")
    responsePayload := strings.Contains(r.RequestURI, "payload")
    if enable {
      tcpConfig.turnOffAllModes()
    }
    if responsePayload {
      tcpConfig.Payload = enable
      if enable {
        tcpConfig.configure()
      }
      msg = fmt.Sprintf("Response Payload mode set to [%t] for listener %d", enable, port)
    } else if stream {
      tcpConfig.Stream = enable
      if enable {
        tcpConfig.configureStream()
      }
      msg = fmt.Sprintf("Stream mode set to [%t] for listener %d", enable, port)
    } else if echo {
      tcpConfig.Echo = enable
      if enable {
        tcpConfig.configure()
      }
      msg = fmt.Sprintf("Echo mode set to [%t] for listener %d", enable, port)
    } else if conversation {
      tcpConfig.Conversation = enable
      msg = fmt.Sprintf("Conversation mode set to [%t] for listener %d", enable, port)
    } else if silentlife {
      tcpConfig.SilentLife = enable
      msg = fmt.Sprintf("SilentLife mode set to [%t] for listener %d", enable, port)
    } else if closeatfirst {
      tcpConfig.CloseAtFirstByte = enable
      msg = fmt.Sprintf("CloseAtFirstByte mode set to [%t] for listener %d", enable, port)
    }
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("TCP Mode Configured", msg, r)
  }
}

func getActiveConnections(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  activeConns := map[int]map[int]map[string]interface{}{}
  lock.RLock()
  if port > 0 {
    if activeConns[port] == nil {
      activeConns[port] = map[int]map[string]interface{}{}
    }
    listenerID := global.GetListenerID(port)
    for requestID, tcpHandler := range activeConnections[listenerID] {
      activeConns[port][requestID] = map[string]interface{}{"status": tcpHandler.status, "config": &tcpHandler.TCPConfig}
    }
  } else {
    for _, conns := range activeConnections {
      for requestID, tcpHandler := range conns {
        if activeConns[tcpHandler.Port] == nil {
          activeConns[tcpHandler.Port] = map[int]map[string]interface{}{}
        }
        activeConns[tcpHandler.Port][requestID] = map[string]interface{}{"status": tcpHandler.status, "config": &tcpHandler.TCPConfig}
      }
    }
  }
  lock.RUnlock()
  msg := ""
  if len(activeConns) > 0 {
    msg = util.ToJSONText(activeConns)
  } else {
    msg = "{}"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage("Active connections reported", r)
}

func getConnectionHistory(w http.ResponseWriter, r *http.Request) {
  port := util.GetIntParamValue(r, "port")
  mode := util.GetStringParamValue(r, "mode")
  connHistory := map[int]map[int]*ConnectionHistory{}
  lock.RLock()
  if port > 0 {
    if connectionHistory[port] != nil {
      if connHistory[port] == nil {
        connHistory[port] = map[int]*ConnectionHistory{}
      }
      for _, ch := range connectionHistory[port] {
        if mode == "" || isMode(ch.Config, mode) {
          connHistory[port][ch.Status.RequestID] = ch
        }
      }
    }
  } else {
    for port, portHistory := range connectionHistory {
      if connHistory[port] == nil {
        connHistory[port] = map[int]*ConnectionHistory{}
      }
      for _, ch := range portHistory {
        if mode == "" || isMode(ch.Config, mode) {
          connHistory[port][ch.Status.RequestID] = ch
        }
      }
    }
  }
  lock.RUnlock()
  msg := ""
  if len(connHistory) > 0 {
    msg = util.ToJSONText(connHistory)
  } else {
    msg = "{}"
  }
  fmt.Fprintln(w, msg)
  util.AddLogMessage("Connection history reported", r)
}

func clearConnectionHistory(w http.ResponseWriter, r *http.Request) {
  msg := "TCP Connection History Cleared"
  port := util.GetIntParamValue(r, "port")
  lock.Lock()
  if port > 0 {
    connectionHistory[port] = []*ConnectionHistory{}
    msg += " for port " + strconv.Itoa(port)
  } else {
    connectionHistory = map[int][]*ConnectionHistory{}
  }
  lock.Unlock()
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
  events.SendRequestEvent(msg, "", r)
}

func (tcpConfig *TCPConfig) turnOffAllModes() {
  tcpConfig.Payload = false
  tcpConfig.Conversation = false
  tcpConfig.Echo = false
  tcpConfig.Stream = false
  tcpConfig.ValidatePayloadLength = false
  tcpConfig.ValidatePayloadContent = false
  tcpConfig.SilentLife = false
  tcpConfig.CloseAtFirstByte = false
}

func isMode(tcpConfig *TCPConfig, mode string) bool {
  requestedMode := strings.ToLower(mode)
  actualMode := ""
  if tcpConfig.Payload {
    actualMode = PayloadValidation
  } else if tcpConfig.Conversation {
    actualMode = Conversation
  } else if tcpConfig.Echo {
    actualMode = Echo
  } else if tcpConfig.Stream {
    actualMode = Stream
  } else if tcpConfig.ValidatePayloadLength || tcpConfig.ValidatePayloadContent {
    actualMode = PayloadValidation
  } else if tcpConfig.SilentLife {
    actualMode = SilentLife
  } else if tcpConfig.CloseAtFirstByte {
    actualMode = CloseAtFirstByte
  }
  actualMode = strings.ToLower(actualMode)
  return strings.Contains(actualMode, requestedMode)
}

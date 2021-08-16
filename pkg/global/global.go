/**
 * Copyright 2021 uk
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

package global

import (
  "net/http"
  "time"

  "github.com/gorilla/mux"
)

var (
  Version                    string
  Commit                     string
  ServerPort                 int
  PeerName                   string
  PeerAddress                string
  PodName                    string
  Namespace                  string
  NodeName                   string
  Cluster                    string
  HostIP                     string
  HostLabel                  string
  RegistryURL                string
  CertPath                   string
  WorkDir                    string
  KubeConfig                 string
  UseLocker                  bool
  EnableEvents               bool
  PublishEvents              bool
  StartupDelay               time.Duration
  ShutdownDelay              time.Duration
  StartupScript              []string
  
  Stopping                   bool = false
  EnableServerLogs           bool = true
  EnableAdminLogs            bool = true
  EnableClientLogs           bool = true
  EnableInvocationLogs       bool = true
  EnableRegistryLogs         bool = true
  EnableRegistryLockerLogs   bool = false
  EnableRegistryEventsLogs   bool = false
  EnableRegistryReminderLogs bool = false
  EnablePeerHealthLogs       bool = true
  EnableProbeLogs            bool = false
  EnableMetricsLogs          bool = true
  LogRequestHeaders          bool = true
  LogRequestMiniBody         bool = false
  LogRequestBody             bool = false
  LogResponseHeaders         bool = false
  LogResponseMiniBody        bool = false
  LogResponseBody            bool = false
  GetPeers                   func(string, *http.Request) map[string]string
  IsReadinessProbe           func(*http.Request) bool
  IsLivenessProbe            func(*http.Request) bool
  IsListenerPresent          func(int) bool
  IsListenerOpen             func(int) bool
  GetListenerID              func(int) string
  GetListenerLabel           func(*http.Request) string
  GetListenerLabelForPort    func(int) string
  GetHostLabelForPort        func(int) string
  StoreEventInCurrentLocker  func(interface{})
  HasTunnel                  func(*http.Request, *mux.RouteMatch) bool
  Debug                      bool = false
)

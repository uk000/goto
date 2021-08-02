package global

import (
  "net/http"
  "time"
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
  Debug                      bool = false
)

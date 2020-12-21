package global

import (
  "net/http"
  "time"
)

var (
  ServerPort                 int
  PeerName                   string
  PeerAddress                string
  PodName                    string
  Namespace                  string
  NodeName                   string
  Cluster                    string
  HostLabel                  string
  RegistryURL                string
  CertPath                   string
  UseLocker                  bool
  StartupDelay               time.Duration
  ShutdownDelay              time.Duration
  Stopping                   bool = false
  EnableTrackingLogs         bool = true
  EnableAdminLogs            bool = true
  EnableInvocationLogs       bool = true
  EnableRegistryLogs         bool = true
  EnableRegistryLockerLogs   bool = true
  EnableRegistryReminderLogs bool = false
  EnablePeerHealthLogs       bool = false
  EnableClientLogs           bool = true
  EnableServerLogs           bool = true
  EnableProbeLogs            bool = true
  GetPeers                   func(string, *http.Request) map[string]string
  IsReadinessProbe           func(*http.Request) bool
  IsLivenessProbe            func(*http.Request) bool
  IsListenerPresent          func(int) bool
  IsListenerOpen             func(int) bool
  GetListenerID              func(int) string
  IsIgnoredURI               func(*http.Request) bool
)

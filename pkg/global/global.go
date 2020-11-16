package global

import (
	"net/http"
	"time"
)

var (
  ServerPort                 int
  PeerName                   string
  PeerAddress                string
  RegistryURL                string
  CertPath                   string
  UseLocker                  bool
  StartupDelay               time.Duration
  ShutdownDelay              time.Duration
  ReadinessProbe             string = "/ready"
  LivenessProbe              string = "/live"
  Stopping                   bool   = false
  EnableTrackingLogs         bool   = true
  EnableAdminLogs            bool   = true
  EnableInvocationLogs       bool   = true
  EnableRegistryLogs         bool   = true
  EnableRegistryReminderLogs bool   = true
  EnableClientLogs           bool   = true
  EnableServerLogs           bool   = true
  EnableProbeLogs            bool   = true
  GetPeers                   func(string, *http.Request) map[string]string
)

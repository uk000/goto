package global

import "net/http"

var (
  ServerPort           int
  PeerName             string
  PeerAddress          string
  RegistryURL          string
  CertPath             string
  UseLocker            bool
  EnableTrackingLogs   bool = true
  EnableAdminLogs      bool = true
  EnableInvocationLogs bool = true
  EnableRegistryLogs   bool = true
  EnableClientLogs     bool = true
  EnableServerLogs     bool = true
  GetPeers             func(string, *http.Request) map[string]string
)

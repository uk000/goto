package global

import "net/http"

var (
  ServerPort         int
  PeerName           string
  PeerAddress        string
  RegistryURL        string
  CertPath           string
  UseLocker          bool
  EnableAdminLogging bool = true
  GetPeers           func(string, *http.Request) map[string]string
)

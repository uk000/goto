package peer

import (
	"goto/pkg/http/invocation"
	"goto/pkg/job/jobtypes"
)

type Peer struct {
  Name    string `json:"name"`
  Address string `json:"address"`
}

type Peers struct {
  Name      string         `json:"name"`
  Addresses map[string]int `json:"addresses"`
}

type PeerTarget struct {
  invocation.InvocationSpec
}

type PeerTargets map[string]*PeerTarget

type PeerJob struct {
  jobtypes.Job
}

type PeerJobs map[string]*PeerJob

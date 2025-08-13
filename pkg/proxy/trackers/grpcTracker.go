package trackers

import "sync"

type GRPCProxyTracker struct {
	ConnCount                int                       `json:"connCount"`
	ConnCountByUpstream      map[string]int            `json:"connCountByUpstream"`
	RequestCountByUpstream   map[string]int            `json:"requestCountByUpstream"`
	RequestCountByService    map[string]int            `json:"requestCountByService"`
	RequestCountBySvcMethod  map[string]map[string]int `json:"requestCountByServiceMethod"`
	ResponseCountByUpstream  map[string]int            `json:"responseCountByUpstream"`
	ResponseCountByService   map[string]int            `json:"responseCountByService"`
	ResponseCountBySvcMethod map[string]map[string]int `json:"responseCountByServiceMethod"`
	MessageCountByType       map[string]int            `json:"messageCountByType"`
	lock                     sync.RWMutex
}

func NewGRPCProxyTracker() *GRPCProxyTracker {
	return &GRPCProxyTracker{
		ConnCount:                0,
		ConnCountByUpstream:      map[string]int{},
		RequestCountByUpstream:   map[string]int{},
		RequestCountByService:    map[string]int{},
		RequestCountBySvcMethod:  map[string]map[string]int{},
		ResponseCountByUpstream:  map[string]int{},
		ResponseCountByService:   map[string]int{},
		ResponseCountBySvcMethod: map[string]map[string]int{},
		MessageCountByType:       map[string]int{},
	}
}

func (pt *GRPCProxyTracker) IncrementConnCounts(upstream string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	if upstream != "" {
		pt.ConnCountByUpstream[upstream]++
	}
}

func (pt *GRPCProxyTracker) AddMatchCounts(upstream, service, method, requestMessageType string, requestCount int, responseMessageType string, responseCount int) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if upstream != "" {
		pt.RequestCountByUpstream[upstream] += requestCount
		pt.ResponseCountByUpstream[upstream] += responseCount
	}
	if service != "" {
		pt.RequestCountByService[service] += requestCount
		pt.ResponseCountByService[service] += responseCount
		if method != "" {
			if pt.RequestCountBySvcMethod[service] == nil {
				pt.RequestCountBySvcMethod[service] = map[string]int{}
			}
			pt.RequestCountBySvcMethod[service][method] += requestCount
			if pt.ResponseCountBySvcMethod[service] == nil {
				pt.ResponseCountBySvcMethod[service] = map[string]int{}
			}
			pt.ResponseCountBySvcMethod[service][method] += responseCount
		}
	}
	if requestMessageType != "" {
		pt.MessageCountByType[requestMessageType] += requestCount
	}
	if responseMessageType != "" {
		pt.MessageCountByType[responseMessageType] += responseCount
	}
}

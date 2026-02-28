package tracking

import "sync"

type HeaderValueCounts struct {
	CountsByHeader      int            `json:"countsByHeader"`
	CountsByHeaderValue map[string]int `json:"countsByHeaderValue"`
	lock                sync.RWMutex
}

type HeaderTrackingData map[string]*HeaderValueCounts

type StatusCounts map[int]int

type HeaderValueStatusCounts map[string]map[int]int

type TrackingData struct {
	ByURI                   map[string]int                     `json:"byURI"`
	ByReqHeader             HeaderTrackingData                 `json:"byRequestHeader"`
	ByURIAndReqHeader       map[string]HeaderTrackingData      `json:"byURIAndRequestHeader"`
	ByRespHeader            HeaderTrackingData                 `json:"byResponseHeader"`
	ByRespStatus            StatusCounts                       `json:"byResponseStatus"`
	ByURIAndRespHeader      map[string]HeaderTrackingData      `json:"byURIAndResponseHeader"`
	ByURIAndRespStatus      map[string]StatusCounts            `json:"byURIAndResponseStatus"`
	ByRespHeaderAndStatus   map[string]HeaderValueStatusCounts `json:"byResponseHeaderAndStatus"`
	ByUpURI                 map[string]int                     `json:"byUpstreamURI"`
	ByUpReqHeader           HeaderTrackingData                 `json:"byUpstreamRequestHeader"`
	ByUpRespHeader          HeaderTrackingData                 `json:"byUpstreamResponseHeader"`
	ByUpRespStatus          StatusCounts                       `json:"byUpstreamResponseStatus"`
	ByUpURIAndReqHeader     map[string]HeaderTrackingData      `json:"byUpstreamURIAndRequestHeader"`
	ByUpURIAndRespHeader    map[string]HeaderTrackingData      `json:"byUpstreamURIAndResponseHeader"`
	ByUpURIAndRespStatus    map[string]StatusCounts            `json:"byUpstreamURIAndResponseStatus"`
	ByUpRespHeaderAndStatus map[string]HeaderValueStatusCounts `json:"byUpstreamResponseHeaderAndStatus"`
	lock                    sync.RWMutex
}

func (td *TrackingData) init() {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByURI = map[string]int{}
	td.ByReqHeader = newHeaderTrackingData()
	td.ByURIAndReqHeader = map[string]HeaderTrackingData{}
	td.ByRespHeader = newHeaderTrackingData()
	td.ByRespStatus = newStatusCounts()
	td.ByURIAndRespHeader = map[string]HeaderTrackingData{}
	td.ByURIAndRespStatus = map[string]StatusCounts{}
	td.ByRespHeaderAndStatus = map[string]HeaderValueStatusCounts{}
	td.ByUpURI = map[string]int{}
	td.ByUpReqHeader = newHeaderTrackingData()
	td.ByUpRespHeader = newHeaderTrackingData()
	td.ByUpRespStatus = newStatusCounts()
	td.ByUpURIAndReqHeader = map[string]HeaderTrackingData{}
	td.ByUpURIAndRespHeader = map[string]HeaderTrackingData{}
	td.ByUpURIAndRespStatus = map[string]StatusCounts{}
	td.ByUpRespHeaderAndStatus = map[string]HeaderValueStatusCounts{}
}

func (td *TrackingData) clearCounts() {
	td.lock.Lock()
	defer td.lock.Unlock()
	for uri := range td.ByURI {
		td.ByURI[uri] = 0
	}
	td.ByReqHeader.clearCounts()
	for _, htd := range td.ByURIAndReqHeader {
		htd.clearCounts()
	}
	td.ByRespHeader.clearCounts()
	td.ByRespStatus = newStatusCounts()
	for _, htd := range td.ByURIAndRespHeader {
		htd.clearCounts()
	}
	for uri := range td.ByURIAndRespStatus {
		td.ByURIAndRespStatus[uri] = newStatusCounts()
	}
	for h := range td.ByRespHeaderAndStatus {
		td.ByRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
	}
	for uri := range td.ByUpURI {
		td.ByUpURI[uri] = 0
	}
	td.ByUpReqHeader.clearCounts()
	td.ByUpRespHeader.clearCounts()
	td.ByUpRespStatus = newStatusCounts()
	for _, htd := range td.ByUpURIAndReqHeader {
		htd.clearCounts()
	}
	for _, htd := range td.ByUpURIAndRespHeader {
		htd.clearCounts()
	}
	for uri := range td.ByUpURIAndRespStatus {
		td.ByUpURIAndRespStatus[uri] = newStatusCounts()
	}
	for h := range td.ByUpRespHeaderAndStatus {
		td.ByUpRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
	}
}

func (td *TrackingData) addRequestTracking(uri string, headers []string) {
	if uri != "" {
		td.ByURI[uri] = 0
		if len(headers) > 0 {
			if td.ByURIAndReqHeader[uri] == nil {
				td.ByURIAndReqHeader[uri] = newHeaderTrackingData()
			}
			td.ByURIAndReqHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByReqHeader.add(headers)
	}
}

func (td *TrackingData) addResponseTracking(uri string, headers []string) {
	if uri != "" {
		if td.ByURIAndRespStatus[uri] == nil {
			td.ByURIAndRespStatus[uri] = newStatusCounts()
		}
		if len(headers) > 0 {
			if td.ByURIAndRespHeader[uri] == nil {
				td.ByURIAndRespHeader[uri] = newHeaderTrackingData()
			}
			td.ByURIAndRespHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByRespHeader.add(headers)
		for _, h := range headers {
			if td.ByRespHeaderAndStatus[h] == nil {
				td.ByRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
			}
		}
	}
}

func (td *TrackingData) addUpstreamRequestTracking(uri string, headers []string) {
	if uri != "" {
		td.ByUpURI[uri] = 0
		if len(headers) > 0 {
			if td.ByUpURIAndReqHeader[uri] == nil {
				td.ByUpURIAndReqHeader[uri] = newHeaderTrackingData()
			}
			td.ByUpURIAndReqHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByUpReqHeader.add(headers)
	}
}

func (td *TrackingData) addUpstreamResponseTracking(uri string, headers []string) {
	if uri != "" {
		if td.ByUpURIAndRespStatus[uri] == nil {
			td.ByUpURIAndRespStatus[uri] = newStatusCounts()
		}
		if len(headers) > 0 {
			if td.ByUpURIAndRespHeader[uri] == nil {
				td.ByUpURIAndRespHeader[uri] = newHeaderTrackingData()
			}
			td.ByUpURIAndRespHeader[uri].add(headers)
		}
	}
	if len(headers) > 0 {
		td.ByUpRespHeader.add(headers)
		for _, h := range headers {
			if td.ByUpRespHeaderAndStatus[h] == nil {
				td.ByUpRespHeaderAndStatus[h] = newHeaderValueStatusCounts()
			}
		}
	}
}

func (td *TrackingData) trackRequest(uri string, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByURI[uri]++
	if htd := td.ByURIAndReqHeader[uri]; htd != nil {
		htd.track(headers)
	}
	td.ByReqHeader.track(headers)
}

func (td *TrackingData) trackResponse(uri string, statusCode int, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByRespStatus.track(statusCode)
	td.ByRespHeader.track(headers)
	if td.ByURIAndRespStatus[uri] != nil {
		td.ByURIAndRespStatus[uri].track(statusCode)
	}
	for h, hv := range headers {
		if td.ByRespHeaderAndStatus[h] != nil {
			td.ByRespHeaderAndStatus[h].track(hv[0], statusCode)
		}
		if td.ByURIAndRespHeader[uri] != nil {
			td.ByURIAndRespHeader[uri].track(headers)
		}
	}
}

func (td *TrackingData) trackUpstreamRequest(uri string, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByUpURI[uri]++
	if htd := td.ByUpURIAndReqHeader[uri]; htd != nil {
		htd.track(headers)
	}
	td.ByUpReqHeader.track(headers)
}

func (td *TrackingData) trackUpstreamResponse(uri string, statusCode int, headers map[string][]string) {
	td.lock.Lock()
	defer td.lock.Unlock()
	td.ByUpRespStatus.track(statusCode)
	td.ByUpRespHeader.track(headers)
	if td.ByUpURIAndRespStatus[uri] != nil {
		td.ByUpURIAndRespStatus[uri].track(statusCode)
	}
	for h, hv := range headers {
		if td.ByUpRespHeaderAndStatus[h] != nil {
			td.ByUpRespHeaderAndStatus[h].track(hv[0], statusCode)
		}
		if td.ByUpURIAndRespHeader[uri] != nil {
			td.ByUpURIAndRespHeader[uri].track(headers)
		}
	}
}

func newHeaderTrackingData() HeaderTrackingData {
	return HeaderTrackingData{}
}

func newStatusCounts() StatusCounts {
	return map[int]int{}
}

func newHeaderValueStatusCounts() HeaderValueStatusCounts {
	return map[string]map[int]int{}
}

func (sc StatusCounts) track(status int) {
	sc[status]++
}

func (sc HeaderValueStatusCounts) track(hv string, status int) {
	if hv != "" {
		if sc[hv] == nil {
			sc[hv] = map[int]int{}
		}
		sc[hv][status]++
	}
}

func (htd HeaderTrackingData) add(headers []string) {
	for _, h := range headers {
		htd[h] = newHeaderValueCounts()
	}
}

func (htd HeaderTrackingData) track(headers map[string][]string) {
	for h, hvc := range htd {
		hValues := headers[h]
		for _, hv := range hValues {
			hvc.track(hv)
		}
	}
}

func (htd HeaderTrackingData) clearCounts() {
	for h := range htd {
		htd[h].clearCounts()
	}
}

func newHeaderValueCounts() *HeaderValueCounts {
	hvc := &HeaderValueCounts{}
	hvc.init()
	return hvc
}

func (hvc *HeaderValueCounts) init() {
	hvc.lock.Lock()
	defer hvc.lock.Unlock()
	hvc.CountsByHeader = 0
	hvc.CountsByHeaderValue = map[string]int{}
}

func (hvc *HeaderValueCounts) track(headerValue string) {
	hvc.lock.Lock()
	defer hvc.lock.Unlock()
	hvc.CountsByHeader++
	if headerValue != "" {
		hvc.CountsByHeaderValue[headerValue]++
	}
}

func (hvc *HeaderValueCounts) clearCounts() {
	hvc.CountsByHeader = 0
	hvc.CountsByHeaderValue = map[string]int{}
}

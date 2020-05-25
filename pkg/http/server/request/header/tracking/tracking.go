package tracking

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"goto/pkg/http/server/intercept"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type HeaderData struct {
	RequestCountsByHeaderValue                   map[string]int
	RequestCountsByHeaderValueAndRequestedStatus map[string]map[string]int
	RequestCountsByHeaderValueAndResponseStatus  map[string]map[string]int
	lock                                         sync.RWMutex
}

type RequestTrackingData struct {
	headerMap map[string]*HeaderData
	lock      sync.RWMutex
}

type RequestTracking struct {
	requestTrackingByPort map[string]*RequestTrackingData
	lock                  sync.RWMutex
}

var (
	requestTracking RequestTracking = RequestTracking{requestTrackingByPort: map[string]*RequestTrackingData{}}
)

func SetRoutes(r *mux.Router, parent *mux.Router) {
	headerTrackingRouter := r.PathPrefix("/track").Subrouter()
	util.AddRoute(headerTrackingRouter, "/clear", clearHeaders, "POST")
	util.AddRoute(headerTrackingRouter, "/add/{headers}", addHeaders, "PUT", "POST")
	util.AddRoute(headerTrackingRouter, "/{header}/remove", removeHeader, "PUT", "POST")
	util.AddRoute(headerTrackingRouter, "/{header}/counts", getHeaderCount, "GET")
	util.AddRoute(headerTrackingRouter, "/counts/clear/{headers}", clearHeaderCounts, "PUT", "POST")
	util.AddRoute(headerTrackingRouter, "/counts/clear", clearHeaderCounts, "POST")
	util.AddRoute(headerTrackingRouter, "/counts", getHeaderCount, "GET")
}

func (hd *HeaderData) init() {
	hd.lock.Lock()
	defer hd.lock.Unlock()
	hd.RequestCountsByHeaderValue = map[string]int{}
	hd.RequestCountsByHeaderValueAndRequestedStatus = map[string]map[string]int{}
	hd.RequestCountsByHeaderValueAndResponseStatus = map[string]map[string]int{}
}

func (hd *HeaderData) trackRequest(headerValue string, requestedStatus string) {
	if headerValue != "" {
		hd.lock.Lock()
		defer hd.lock.Unlock()
		hd.RequestCountsByHeaderValue[headerValue]++
		if requestedStatus != "" {
			requestedStatusMap, present := hd.RequestCountsByHeaderValueAndRequestedStatus[headerValue]
			if !present {
				requestedStatusMap = map[string]int{}
				hd.RequestCountsByHeaderValueAndRequestedStatus[headerValue] = requestedStatusMap
			}
			requestedStatusMap[requestedStatus]++
		}
	}
}

func (hd *HeaderData) trackResponse(headerValue string, responseStatus string) {
	if headerValue != "" && responseStatus != "" {
		hd.lock.Lock()
		defer hd.lock.Unlock()
		responseStatusMap, present := hd.RequestCountsByHeaderValueAndResponseStatus[headerValue]
		if !present {
			responseStatusMap = map[string]int{}
			hd.RequestCountsByHeaderValueAndResponseStatus[headerValue] = responseStatusMap
		}
		responseStatusMap[responseStatus]++
	}
}

func (rtd *RequestTrackingData) init() {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	rtd.headerMap = map[string]*HeaderData{}
}

func (rtd *RequestTrackingData) initHeaders(headers []string) {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	for _, h := range headers {
		_, present := rtd.headerMap[h]
		if !present {
			rtd.headerMap[h] = &HeaderData{}
			rtd.headerMap[h].init()
		}
	}
}

func (rtd *RequestTrackingData) removeHeader(header string) bool {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	_, present := rtd.headerMap[header]
	if present {
		delete(rtd.headerMap, header)
	}
	return present
}

func (rtd *RequestTrackingData) clearCounts() {
	rtd.lock.Lock()
	defer rtd.lock.Unlock()
	for _, hd := range rtd.headerMap {
		hd.init()
	}
}

func (rtd *RequestTrackingData) getHeaderData(header string) *HeaderData {
	rtd.lock.RLock()
	defer rtd.lock.RUnlock()
	return rtd.headerMap[header]
}

func (rt *RequestTracking) getPortRequestTrackingData(r *http.Request) *RequestTrackingData {
	rt.lock.Lock()
	defer rt.lock.Unlock()
	listenerPort := util.GetListenerPort(r)
	rtd, present := rt.requestTrackingByPort[listenerPort]
	if !present {
		rtd = &RequestTrackingData{}
		rtd.init()
		rt.requestTrackingByPort[listenerPort] = rtd
	}
	return rtd
}

func (rt *RequestTracking) initHeaders(r *http.Request) []string {
	if headersP, hp := util.GetStringParam(r, "headers"); hp {
		headers := strings.Split(headersP, ",")
		rtd := rt.getPortRequestTrackingData(r)
		rtd.initHeaders(headers)
		return headers
	}
	return nil
}

func (rt *RequestTracking) removeHeader(r *http.Request) (header string, present bool) {
	if header, hp := util.GetStringParam(r, "header"); hp {
		rtd := rt.getPortRequestTrackingData(r)
		present := rtd.removeHeader(header)
		return header, present
	} else {
		return "", false
	}
}

func (rt *RequestTracking) clear(r *http.Request) {
	rt.getPortRequestTrackingData(r).init()
}

func (rt *RequestTracking) clearHeaderCounts(r *http.Request) []string {
	rtd := rt.getPortRequestTrackingData(r)
	if headersP, hp := util.GetStringParam(r, "headers"); hp {
		headers := strings.Split(headersP, ",")
		rtd.initHeaders(headers)
		return headers
	}
	rtd.clearCounts()
	return nil
}

func (rt *RequestTracking) getHeaderCounts(r *http.Request) (header string, headerData *HeaderData) {
	rtd := rt.getPortRequestTrackingData(r)
	if header, hp := util.GetStringParam(r, "header"); hp {
		return header, rtd.getHeaderData(header)
	}
	return "", nil
}

func addHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""

	if headers := requestTracking.initHeaders(r); headers != nil {
		msg = fmt.Sprintf("Header %s will be tracked", headers)
		w.WriteHeader(http.StatusAccepted)
	} else {
		msg = "Cannot add. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func removeHeader(w http.ResponseWriter, r *http.Request) {
	msg := ""
	header, present := requestTracking.removeHeader(r)
	if present {
		msg = fmt.Sprintf("Header %s removed", header)
		w.WriteHeader(http.StatusAccepted)
	} else {
		msg = "Cannot remove. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func clearHeaders(w http.ResponseWriter, r *http.Request) {
	requestTracking.clear(r)
	util.AddLogMessage("Headers cleared", r)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "Headers cleared\n")
}

func clearHeaderCounts(w http.ResponseWriter, r *http.Request) {
	msg := ""

	if headers := requestTracking.clearHeaderCounts(r); headers != nil {
		msg = fmt.Sprintf("Header %s count reset", headers)
	} else {
		msg = "Clearing counts for all headers"
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getHeaderCount(w http.ResponseWriter, r *http.Request) {
	header, headerData := requestTracking.getHeaderCounts(r)
	if header != "" {
		util.AddLogMessage(fmt.Sprintf("Reporting counts for header %s", header), r)
		if headerData != nil {
			result := util.ToJSON(headerData)
			util.AddLogMessage(result, r)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, result)
		} else {
			util.AddLogMessage("No data to report", r)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "{}")
		}
	} else {
		util.AddLogMessage(fmt.Sprintf("Reporting counts for all headers"), r)
		result := util.ToJSON(requestTracking.getPortRequestTrackingData(r).headerMap)
		util.AddLogMessage(result, r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, result)
	}
}

func trackRequestHeaders(r *http.Request) {
	rtd := requestTracking.getPortRequestTrackingData(r)
	for header, headerData := range rtd.headerMap {
		headerData.trackRequest(r.Header.Get(header), util.GetStringParamValue(r, "status"))
	}
}

func trackResponseForRequestHeaders(r *http.Request, statusCode int) {
	rtd := requestTracking.getPortRequestTrackingData(r)
	for header, headerData := range rtd.headerMap {
		headerData.trackResponse(r.Header.Get(header), strconv.Itoa(statusCode))
	}
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trackRequestHeaders(r)
		crw := intercept.NewInterceptResponseWriter(w, false)
		next.ServeHTTP(crw, r)
		trackResponseForRequestHeaders(r, crw.StatusCode)
	})
}

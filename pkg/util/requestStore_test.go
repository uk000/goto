package util

import (
	"net/http/httptest"
	"testing"
	"time"

	"goto/pkg/constants"
)

// The time of a request is reported under names that tell who reports it: the server, the proxy that took the request
// (for the head of the response, as Goto-Proxy-Took is the time of all of it), or a tunnel.
func TestReportTimeNamesTheReporter(t *testing.T) {
	tests := []struct {
		name  string
		setup func(rs *RequestStore)
		names [3]string
	}{
		{"server", func(rs *RequestStore) {}, [3]string{constants.HeaderGotoInAt, constants.HeaderGotoOutAt, constants.HeaderGotoTook}},
		{"proxy", func(rs *RequestStore) { rs.ProxiedRequest = true },
			[3]string{constants.HeaderGotoProxyInAt, constants.HeaderGotoProxyOutAt, constants.HeaderGotoProxyHeadTook}},
		{"tunnel", func(rs *RequestStore) { rs.IsTunnelRequest, rs.TunnelCount = true, 2 },
			[3]string{constants.HeaderGotoInAt + "-2", constants.HeaderGotoOutAt + "-2", constants.HeaderGotoTook + "-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := &RequestStore{}
			tt.setup(rs)
			rs.Start()
			time.Sleep(5 * time.Millisecond)
			rr := httptest.NewRecorder()
			rs.ReportTime(rr)
			for _, name := range tt.names {
				if rr.Header().Get(name) == "" {
					t.Errorf("no %s in %v", name, rr.Header())
				}
			}
			if len(rr.Header()) != 3 {
				t.Errorf("headers = %v, want the three of the time only", rr.Header())
			}
			if took, err := time.ParseDuration(rr.Header().Get(tt.names[2])); err != nil || took < 5*time.Millisecond {
				t.Errorf("%s = %q, want the 5ms that the request took (err %v)", tt.names[2], rr.Header().Get(tt.names[2]), err)
			}
		})
	}
}

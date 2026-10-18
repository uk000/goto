package util

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestGetAltPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{"no optional segments", "/a/{b}", []string{"/a/{b}"}},
		{"single optional segment", "/{peer}?/targets",
			[]string{"/{peer}/targets", "/targets", "/{peer}/targets/", "/targets/"}},
		{"optional segments separated by static text", "/{peer}?/health/{address}?",
			[]string{"/{peer}/health/{address}", "/health/{address}", "/{peer}/health", "/health",
				"/{peer}/health/{address}/", "/health/{address}/", "/{peer}/health/", "/health/"}},
		// Adjacent optional segments are filled left to right: "/{label}/lockers" must not be an alternative,
		// or a request for "/p1/lockers" would bind p1 to label instead of peer.
		{"adjacent optional segments", "/{peer}?/{label}?/lockers",
			[]string{"/{peer}/{label}/lockers", "/lockers", "/{peer}/lockers",
				"/{peer}/{label}/lockers/", "/lockers/", "/{peer}/lockers/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetAltPaths(tt.path, true); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetAltPaths(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestRoutePaths_AddsTrailingSlashVariants(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"", []string{"", "/"}},
		{"/a", []string{"/a", "/a/"}},
		{"/a/", []string{"/a/"}},
		{"/{peer}?/b", []string{"/{peer}/b", "/b", "/{peer}/b/", "/b/"}},
	}
	for _, tt := range tests {
		if got := routePaths(tt.path); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("routePaths(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func queriesOf(r *mux.Router) []string {
	var queries []string
	r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		if path, _ := route.GetPathTemplate(); strings.HasSuffix(path, "/") {
			return nil
		}
		q, _ := route.GetQueriesTemplates()
		queries = append(queries, strings.Join(q, "&"))
		return nil
	})
	return queries
}

// Params accumulate from one set to the next and are registered together, most specific route first.
func TestAddRouteWithMultiQ_AccumulatesParamsAcrossSets(t *testing.T) {
	noop := func(http.ResponseWriter, *http.Request) {}
	tests := []struct {
		name string
		sets [][]string
		want []string
	}{
		{"three sets without a plain route", [][]string{{"a", "b"}, {"c"}, {"d", "e"}},
			[]string{"a={a}&b={b}&c={c}&d={d}&e={e}", "a={a}&b={b}&c={c}", "a={a}&b={b}"}},
		{"empty first set adds a plain route", [][]string{{}, {"data", "reverse"}},
			[]string{"data={data}&reverse={reverse}", ""}},
		{"plain route and accumulated sets", [][]string{{}, {"data", "level"}, {"md"}},
			[]string{"data={data}&level={level}&md={md}", "data={data}&level={level}", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mux.NewRouter()
			AddRouteWithMultiQ(r, "/x", noop, tt.sets, "GET")
			if got := queriesOf(r); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("routes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAddRouteWithMultiQ_ParamsAreCapturedWhenGivenTogether(t *testing.T) {
	var vars map[string]string
	r := mux.NewRouter()
	AddRouteWithMultiQ(r, "/x", func(w http.ResponseWriter, req *http.Request) { vars = mux.Vars(req) },
		[][]string{{}, {"data", "level"}, {"md"}}, "GET")
	serve := func(target string) {
		vars = nil
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", target, nil))
	}
	serve("/x?data=y&level=3")
	if vars["data"] != "y" || vars["level"] != "3" || vars["md"] != "" {
		t.Errorf("data+level: vars = %v", vars)
	}
	serve("/x?data=y&level=3&md=y")
	if vars["data"] != "y" || vars["level"] != "3" || vars["md"] != "y" {
		t.Errorf("data+level+md: vars = %v", vars)
	}
	// The params of a set are meant to be given together: a lone param is not captured.
	serve("/x?data=y")
	if vars == nil || vars["data"] != "" {
		t.Errorf("lone data param: vars = %v", vars)
	}
}

func TestAddRoute_AcceptsTrailingSlashAndAdjacentOptionalSegments(t *testing.T) {
	var vars map[string]string
	r := mux.NewRouter().SkipClean(true)
	AddRoute(r, "/{peer}?/{label}?/lockers", func(w http.ResponseWriter, req *http.Request) { vars = mux.Vars(req) }, "GET")
	AddRoute(r, "/plain", func(w http.ResponseWriter, req *http.Request) { vars = mux.Vars(req) }, "GET")
	for target, want := range map[string]map[string]string{
		"/p1/lockers":    {"peer": "p1"},
		"/p1/a1/lockers": {"peer": "p1", "label": "a1"},
		"/lockers":       {},
		"/p1/lockers/":   {"peer": "p1"},
		"/plain":         {},
		"/plain/":        {},
	} {
		vars = nil
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d", target, rec.Code)
			continue
		}
		if len(vars) != len(want) {
			t.Errorf("GET %s: vars = %v, want %v", target, vars, want)
			continue
		}
		for k, v := range want {
			if vars[k] != v {
				t.Errorf("GET %s: vars = %v, want %v", target, vars, want)
			}
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/plain/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /plain/: status %d, want 405", rec.Code)
	}
}

func newListParamRequest(target string, vars map[string]string) *http.Request {
	r := httptest.NewRequest("GET", target, nil)
	return mux.SetURLVars(r, vars)
}

func TestGetListParam(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		vars        map[string]string
		want        []string
		wantPresent bool
	}{
		{"absent", "/x", nil, []string{}, false},
		{"absent with empty var", "/x", map[string]string{"p": ""}, []string{}, false},
		{"path var list", "/x", map[string]string{"p": "a,b"}, []string{"a", "b"}, true},
		{"query list", "/x?p=a,b", nil, []string{"a", "b"}, true},
		{"repeated query param", "/x?p=a&p=b", nil, []string{"a", "b"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present := GetListParam(newListParamRequest(tt.target, tt.vars), "p")
			if len(got) == 0 && len(tt.want) == 0 {
				got = []string{}
			}
			if !reflect.DeepEqual(got, tt.want) || present != tt.wantPresent {
				t.Errorf("GetListParam = %q, %v; want %q, %v", got, present, tt.want, tt.wantPresent)
			}
		})
	}
}

func TestToJSONText_HandlesAnyKindOfValue(t *testing.T) {
	var nilMap map[string]string
	var nilPtr *struct{}
	type s struct{ A int }
	tests := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil", nil, ""},
		{"nil map", nilMap, ""},
		{"nil pointer", nilPtr, ""},
		{"string", "zz", `"zz"`},
		{"empty string", "", `""`},
		{"int", 5, "5"},
		{"bool", true, "true"},
		{"struct", s{A: 1}, `{"A":1}`},
		{"map", map[string]int{"a": 1}, `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToJSONText(tt.in); got != tt.want {
				t.Errorf("ToJSONText(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWriteJsonPayload_HandlesAnyKindOfValue(t *testing.T) {
	var nilMap map[string]string
	tests := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil", nil, "\n"},
		{"nil map", nilMap, "\n"},
		{"string", "zz", "zz\n"},
		{"empty map", map[string]string{}, "{}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteJsonPayload(rec, tt.in)
			if rec.Body.String() != tt.want {
				t.Errorf("WriteJsonPayload(%v) = %q, want %q", tt.in, rec.Body.String(), tt.want)
			}
		})
	}
}

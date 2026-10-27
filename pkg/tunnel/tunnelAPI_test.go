/**
 * Copyright 2026 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package tunnel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goto/pkg/server/middleware"
)

// admin sends a request to the admin API of tunnels, which lives on the router of /tunnels.
func admin(t *testing.T, port int, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	router() // registers the routes
	rr := httptest.NewRecorder()
	middleware.RootRouters["/tunnels"].ServeHTTP(rr, newRequest(port, method, target, nil))
	return rr
}

func adminOK(t *testing.T, port int, method, target string) string {
	t.Helper()
	rr := admin(t, port, method, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s answered %d %q", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func TestAPIAddTunnels(t *testing.T) {
	port := newPort(t)
	out := adminOK(t, port, "POST", "/tunnels/add/svc:8080")
	if !strings.Contains(out, "Tunnel added on port [") || !strings.Contains(out, "to endpoint [svc:8080]") {
		t.Errorf("answered %q", out)
	}
	adminOK(t, port, "PUT", "/tunnels/add/uri-svc:8080?uri=/api")
	adminOK(t, port, "POST", "/tunnels/add/header-svc:8080/header/X-Route=blue")
	adminOK(t, port, "POST", "/tunnels/add/any-svc:8080/header/X-Any")
	adminOK(t, port, "POST", "/tunnels/add/both-svc:8080/header/X-Route=red?uri=/api")
	adminOK(t, port, "POST", "/tunnels/add/transparent-svc:8080/transparent")
	adminOK(t, port, "POST", "/tunnels/add/h2:h2-svc:8443")

	pt := GetOrCreatePortTunnel(port)
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	if got := strings.Join(pt.BroadTunnels, ","); got != "http:svc:8080,http:transparent-svc:8080,h2:h2-svc:8443" {
		t.Errorf("broad = %q", got)
	}
	if got := pt.URITunnels["/api"]; len(got) != 1 || got[0] != "http:uri-svc:8080" {
		t.Errorf("by URI = %v", pt.URITunnels)
	}
	if got := pt.HeaderTunnels["X-Route"]["blue"]; len(got) != 1 || got[0] != "http:header-svc:8080" {
		t.Errorf("by header = %v", pt.HeaderTunnels)
	}
	if got := pt.HeaderTunnels["X-Any"][""]; len(got) != 1 || got[0] != "http:any-svc:8080" {
		t.Errorf("by header without a value = %v", pt.HeaderTunnels)
	}
	if got := pt.URIHeaderTunnels["/api"]["X-Route"]["red"]; len(got) != 1 || got[0] != "http:both-svc:8080" {
		t.Errorf("by URI and header = %v", pt.URIHeaderTunnels)
	}
	if ep := pt.Tunnels["http:transparent-svc:8080"]; ep == nil || !ep.Transparent {
		t.Errorf("transparent tunnel = %+v", ep)
	}
	if ep := pt.Tunnels["http:svc:8080"]; ep == nil || ep.Transparent {
		t.Errorf("a tunnel is not transparent unless it's asked to be: %+v", ep)
	}
	if ep := pt.Tunnels["h2:h2-svc:8443"]; ep == nil || !ep.IsH2 || !ep.IsTLS {
		t.Errorf("tunnel with a scheme = %+v", ep)
	}
}

func TestAPIRemoveTunnels(t *testing.T) {
	port := newPort(t)
	adminOK(t, port, "POST", "/tunnels/add/a:80")
	adminOK(t, port, "POST", "/tunnels/add/b:80")
	adminOK(t, port, "POST", "/tunnels/add/a:80?uri=/u")
	adminOK(t, port, "POST", "/tunnels/add/a:80/header/X-H=v")
	adminOK(t, port, "POST", "/tunnels/add/a:80/header/X-H=v?uri=/u")
	pt := GetOrCreatePortTunnel(port)

	if out := adminOK(t, port, "POST", "/tunnels/remove/http:a:80"); !strings.Contains(out, "Tunnel removed on port") {
		t.Errorf("answered %q", out)
	}
	if out := adminOK(t, port, "POST", "/tunnels/remove/http:a:80"); !strings.Contains(out, "No tunnel to remove on port") {
		t.Errorf("removing it again answered %q", out)
	}
	adminOK(t, port, "POST", "/tunnels/remove/http:a:80?uri=/u")
	adminOK(t, port, "PUT", "/tunnels/remove/http:a:80/header/X-H=v")
	adminOK(t, port, "POST", "/tunnels/remove/http:a:80/header/X-H=v?uri=/u")
	pt.lock.RLock()
	if got := strings.Join(pt.BroadTunnels, ","); got != "http:b:80" {
		t.Errorf("broad after removals = %q", got)
	}
	if len(pt.URITunnels) != 0 || len(pt.HeaderTunnels) != 0 || len(pt.URIHeaderTunnels) != 0 {
		t.Errorf("tunnels left after removals: %v %v %v", pt.URITunnels, pt.HeaderTunnels, pt.URIHeaderTunnels)
	}
	pt.lock.RUnlock()

	adminOK(t, port, "POST", "/tunnels/clear")
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	if len(pt.BroadTunnels) != 0 {
		t.Errorf("broad after clear = %v", pt.BroadTunnels)
	}
}

func TestAPITrackingAndTraffic(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusOK, "ok")
	adminOK(t, port, "POST", "/tunnels/add/"+up.addr())
	if out := adminOK(t, port, "POST", "/tunnels/track/header/X-A,X-B"); !strings.Contains(out, "Tracking headers [X-A X-B]") {
		t.Errorf("answered %q", out)
	}
	adminOK(t, port, "PUT", "/tunnels/track/query/p")
	adminOK(t, port, "POST", "/tunnels/traffic/capture=y")
	serve(chain(), newRequest(port, "GET", "/t?p=1", nil, "X-A", "a"))

	var tracking map[int]*TunnelTraffic
	if err := json.Unmarshal([]byte(adminOK(t, port, "GET", "/tunnels/track")), &tracking); err != nil {
		t.Fatal(err)
	}
	c := tracking[port]
	if c == nil || c.ByEndpoints["http:"+up.addr()] != 1 || c.ByRequestHeaders["X-A"] != 1 || c.ByRequestQueries["p"] != 1 {
		t.Errorf("tracking = %+v", c)
	}
	var traffic TunnelTraffic
	if err := json.Unmarshal([]byte(adminOK(t, port, "GET", "/tunnels/traffic")), &traffic); err != nil {
		t.Fatal(err)
	}
	if len(traffic.Logs) != 1 || traffic.Logs[0].RequestURI != "/t?p=1" {
		t.Errorf("traffic = %+v", traffic.Logs)
	}

	if out := adminOK(t, port, "POST", "/tunnels/traffic/capture=n"); !strings.Contains(out, "disabled") {
		t.Errorf("answered %q", out)
	}
	serve(chain(), newRequest(port, "GET", "/t", nil))
	pt := GetOrCreatePortTunnel(port)
	pt.lock.RLock()
	logs := len(pt.Traffic.Logs)
	pt.lock.RUnlock()
	if logs != 1 {
		t.Errorf("traffic is not logged once capturing is disabled, got %d logs", logs)
	}

	adminOK(t, port, "POST", "/tunnels/track/clear")
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	if len(pt.TunnelTrackingHeaders) != 0 || len(pt.TunnelTrackingQueries) != 0 || len(pt.Traffic.ByEndpoints) != 0 || len(pt.Traffic.Logs) != 0 {
		t.Errorf("tracking after clear = %v %v %+v", pt.TunnelTrackingHeaders, pt.TunnelTrackingQueries, pt.Traffic)
	}
}

func TestAPIListTunnelsAndActiveTunnels(t *testing.T) {
	port := newPort(t)
	adminOK(t, port, "POST", "/tunnels/add/a:80")
	adminOK(t, port, "POST", "/tunnels/add/b:80?uri=/u")
	pt := GetOrCreatePortTunnel(port)
	pt.lock.Lock()
	pt.ProxyTunnels["10.0.0.1:1000"] = &ProxyTunnel{FromAddress: "10.0.0.2:2000", ToAddress: "dest:443", IsTLS: true}
	pt.lock.Unlock()

	var all map[int]*PortTunnel
	if err := json.Unmarshal([]byte(adminOK(t, port, "GET", "/tunnels")), &all); err != nil {
		t.Fatal(err)
	}
	got := all[port]
	if got == nil || got.Port != port || len(got.Tunnels) != 2 || len(got.BroadTunnels) != 1 || len(got.URITunnels["/u"]) != 1 || got.ProxyTunnels["10.0.0.1:1000"] == nil {
		t.Errorf("tunnels = %+v", got)
	}

	var active map[string]*ProxyTunnel
	if err := json.Unmarshal([]byte(adminOK(t, port, "GET", "/tunnels/active")), &active); err != nil {
		t.Fatal(err)
	}
	if p := active["10.0.0.1:1000"]; len(active) != 1 || p == nil || p.ToAddress != "dest:443" || !p.IsTLS {
		t.Errorf("active tunnels = %+v", active)
	}
}

func TestAPIMethods(t *testing.T) {
	port := newPort(t)
	for _, c := range []struct{ method, target string }{
		{"GET", "/tunnels/add/a:80"},
		{"GET", "/tunnels/clear"},
		{"POST", "/tunnels"},
		{"POST", "/tunnels/active"},
		{"GET", "/tunnels/traffic/capture=y"},
	} {
		if rr := admin(t, port, c.method, c.target); rr.Code == http.StatusOK {
			t.Errorf("%s %s answered %d, want it refused", c.method, c.target, rr.Code)
		}
	}
}

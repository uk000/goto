package registry

// Test glue for registryAPI_test.go: sets up the registry router and the request context the way the server does.
// (The tests describe main. A snapshot of the v0.8.x flavor of the tests is kept outside the repo.)
//
// On main the registry routes are mounted on the global root router returned by middleware.RootPath("/registry").
// That router (and therefore its core middleware) is a process wide singleton, so routes are registered once.

import (
	"net/http"
	"sync"
	"testing"

	"goto/pkg/client/results"
	"goto/pkg/global"
	"goto/pkg/registry/locker"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var routesOnce sync.Once

// newRouter returns the router that serves the registry routes. The given middlewares are not needed on main:
// util.GetRequestStore creates a RequestStore on demand.
func newRouter(_ ...mux.MiddlewareFunc) *mux.Router {
	routesOnce.Do(func() {
		stubGlobalFuncs()
		setRoutes(nil)
	})
	return middleware.RootRouters["/registry"]
}

// stubGlobalFuncs fills the global.Funcs hooks that the server package normally wires up at startup.
// util.GetRequestStore (called by the intercept middleware on every request) dereferences some of them.
func stubGlobalFuncs() {
	no := func(*http.Request) bool { return false }
	if global.Funcs.IsReadinessProbe == nil {
		global.Funcs.IsReadinessProbe = no
	}
	if global.Funcs.IsLivenessProbe == nil {
		global.Funcs.IsLivenessProbe = no
	}
	if global.Funcs.IsListenerPresent == nil {
		global.Funcs.IsListenerPresent = func(int) bool { return false }
	}
	if global.Funcs.IsListenerOpen == nil {
		global.Funcs.IsListenerOpen = func(int) bool { return false }
	}
	if global.Funcs.GetListenerID == nil {
		global.Funcs.GetListenerID = func(int) string { return "" }
	}
	if global.Funcs.GetListenerLabel == nil {
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	}
	if global.Funcs.GetListenerLabelForPort == nil {
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	}
}

// requestStoreMiddleware is only needed on v0.8.x. It's a no-op passthrough on main.
var requestStoreMiddleware mux.MiddlewareFunc

// prepareRequest attaches a RequestStore to the request context, as server.HTTPHandler does before routing.
// The intercept middleware on the registry router requires one to be present.
func prepareRequest(r *http.Request) *http.Request {
	_, r, _ = util.WithRequestStore(r, nil)
	return r
}

// setSelfAddress sets the address the registry reports for itself (used in the API URLs it lists)
// and returns a func that restores the previous value.
func setSelfAddress(address string) (restore func()) {
	prev := global.Self.Address
	global.Self.Address = address
	return func() { global.Self.Address = prev }
}

// resetRegistryState puts the registry singleton, and the globals its APIs touch, back to a pristine state.
func resetRegistryState() {
	registry.reset()
	registry.peersLock.Lock()
	registry.peerProbes = nil
	registry.peerTrackingHeaders = ""
	registry.trackingHeaders = nil
	registry.crossTrackingHeaders = nil
	registry.peerTrackingTimeBuckets = ""
	registry.trackingTimeBuckets = nil
	registry.peersLock.Unlock()
	registry.lockersLock.Lock()
	registry.contextLockers = locker.NewContextLockers()
	registry.lockersLock.Unlock()
	results.EnableAllTargetResults(false)
	results.EnableInvocationResults(false)
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "registry" {
		t.Errorf("Middleware.Name = %q, want registry", Middleware.Name)
	}
	if Middleware.SetRoutes == nil {
		t.Fatal("Middleware.SetRoutes is nil")
	}
}

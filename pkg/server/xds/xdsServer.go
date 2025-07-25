package xds

import (
	"context"
	"goto/pkg/server/xds/store"
	"sync"

	"google.golang.org/grpc"

	clusterservice "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointservice "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	listenerservice "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routeservice "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	runtimeservice "github.com/envoyproxy/go-control-plane/envoy/service/runtime/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	k8scache "k8s.io/client-go/tools/cache"
)

var (
	PortXDSServer     = map[int]*XDSServer{}
	endpointInformers []k8scache.SharedIndexInformer
	Callbacks         = CallbackHandler{}
)

type XDSServer struct {
	server.Server
	Store *store.Store
	lock  sync.RWMutex
}

func GetXDSServer(port int) *XDSServer {
	if PortXDSServer[port] == nil {
		PortXDSServer[port] = &XDSServer{}
		PortXDSServer[port].init()
	}
	return PortXDSServer[port]
}

func (x *XDSServer) init() {
	x.Store = store.NewStore()
	x.Server = server.NewServer(context.Background(), x.Store.Cache, Callbacks)
}

func (x *XDSServer) registerServer(grpcServer *grpc.Server) {
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(grpcServer, x)
	endpointservice.RegisterEndpointDiscoveryServiceServer(grpcServer, x)
	clusterservice.RegisterClusterDiscoveryServiceServer(grpcServer, x)
	routeservice.RegisterRouteDiscoveryServiceServer(grpcServer, x)
	listenerservice.RegisterListenerDiscoveryServiceServer(grpcServer, x)
	secretservice.RegisterSecretDiscoveryServiceServer(grpcServer, x)
	runtimeservice.RegisterRuntimeDiscoveryServiceServer(grpcServer, x)
}

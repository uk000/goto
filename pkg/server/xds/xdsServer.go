/**
 * Copyright 2025 uk
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

package xds

import (
	"context"
	"goto/pkg/global"
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

type XDSService struct {
	discoverygrpc.UnimplementedAggregatedDiscoveryServiceServer
	endpointservice.UnimplementedEndpointDiscoveryServiceServer
	clusterservice.UnimplementedClusterDiscoveryServiceServer
	routeservice.UnimplementedRouteDiscoveryServiceServer
	listenerservice.UnimplementedListenerDiscoveryServiceServer
	secretservice.UnimplementedSecretDiscoveryServiceServer
	runtimeservice.UnimplementedRuntimeDiscoveryServiceServer
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
	global.AddGRPCIntercept(x.RegisterServices)
}

func (x *XDSServer) RegisterServices(server global.IGRPCManager) {
	service := &XDSService{}
	server.InterceptAndServe(&discoverygrpc.AggregatedDiscoveryService_ServiceDesc, service)
	server.InterceptAndServe(&endpointservice.EndpointDiscoveryService_ServiceDesc, service)
	server.InterceptAndServe(&clusterservice.ClusterDiscoveryService_ServiceDesc, service)
	server.InterceptAndServe(&routeservice.RouteDiscoveryService_ServiceDesc, service)
	server.InterceptAndServe(&listenerservice.ListenerDiscoveryService_ServiceDesc, service)
	server.InterceptAndServe(&secretservice.SecretDiscoveryService_ServiceDesc, service)
	server.InterceptAndServe(&runtimeservice.RuntimeDiscoveryService_ServiceDesc, service)
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

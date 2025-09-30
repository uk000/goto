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

package store

import (
	"context"
	"errors"
	"goto/pkg/util"
	"io"
	"log"
	"strconv"
	"sync"
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
)

type XDSResource interface {
	types.Resource
	Name() string
}

type XDSCluster struct {
	cluster.Cluster
}

type XDSListener struct {
	listener.Listener
}

type XDSRoute struct {
	route.Route
}

type XDSSecret struct {
	tls.Secret
}

type Store struct {
	Resources    map[resource.Type][]types.Resource
	ResourceMaps map[resource.Type]map[string]types.Resource
	Endpoints    map[string]*endpoint.ClusterLoadAssignment
	Cache        cache.SnapshotCache
	lock         sync.RWMutex
}

func (x *XDSCluster) Name() string {
	return x.Cluster.Name
}

func (x *XDSListener) Name() string {
	return x.Listener.Name
}

func (x *XDSRoute) Name() string {
	return x.Route.Name
}

func (x *XDSSecret) Name() string {
	return x.Secret.Name
}

func NewStore() *Store {
	return &Store{
		Resources:    map[resource.Type][]types.Resource{},
		ResourceMaps: map[resource.Type]map[string]types.Resource{},
		Endpoints:    map[string]*endpoint.ClusterLoadAssignment{},
		Cache:        cache.NewSnapshotCache(false, cache.IDHash{}, nil),
	}
}

func (s *Store) GenerateSnapshot() {
	s.lock.RLock()
	defer s.lock.RUnlock()

	version := strconv.FormatInt(time.Now().UnixNano(), 10)

	snap, err := cache.NewSnapshot(
		version,
		map[resource.Type][]types.Resource{
			resource.ClusterType:  s.Resources[resource.ClusterType],
			resource.ListenerType: s.Resources[resource.ListenerType],
			resource.RouteType:    s.Resources[resource.RouteType],
			resource.SecretType:   s.Resources[resource.SecretType],
			resource.EndpointType: epToResources(s.Endpoints),
		},
	)
	if err != nil {
		log.Printf("failed to create snapshot: %v", err)
		return
	}

	if err := snap.Consistent(); err != nil {
		log.Printf("inconsistent snapshot: %v", err)
		return
	}

	if err := s.Cache.SetSnapshot(context.Background(), "node0", snap); err != nil {
		log.Printf("failed to set snapshot: %v", err)
	} else {
		log.Println("snapshot updated")
	}
}

func (s *Store) StoreEndpoints(cluster string, cla *endpoint.ClusterLoadAssignment) {
	s.lock.Lock()
	s.Endpoints[cluster] = cla
	s.lock.Unlock()
	s.GenerateSnapshot()
}

func (s *Store) RemoveEndpoints(cluster string) {
	s.lock.Lock()
	delete(s.Endpoints, cluster)
	s.lock.Unlock()
	s.GenerateSnapshot()

}

func epToResources(eps map[string]*endpoint.ClusterLoadAssignment) []types.Resource {
	out := make([]types.Resource, 0, len(eps))
	for _, e := range eps {
		out = append(out, e)
	}
	return out
}

func AddResources[T XDSResource](s *Store, resourceType resource.Type, resources []T) {
	s.lock.Lock()
	if s.ResourceMaps[resourceType] == nil {
		s.Resources[resourceType] = []types.Resource{}
		s.ResourceMaps[resourceType] = map[string]types.Resource{}
	}
	for _, r := range resources {
		s.Resources[resourceType] = append(s.Resources[resourceType], r)
		s.ResourceMaps[resourceType][r.Name()] = r
	}
	s.lock.Unlock()
	s.GenerateSnapshot()
}

func (s *Store) RemoveResources(resourceType, name string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.ResourceMaps[resourceType] != nil && s.ResourceMaps[resourceType][name] != nil {
		delete(s.ResourceMaps[resourceType], name)
		resources := []types.Resource{}
		for _, r := range s.ResourceMaps[resourceType] {
			resources = append(resources, r)
		}
		s.Resources[resourceType] = resources
		s.GenerateSnapshot()
		return true
	}
	return false
}

func (s *Store) LoadResourcesFromBody(name string, body io.ReadCloser) (int, error) {
	count := 0
	switch name {
	case "cluster", "clusters":
		var c []*XDSCluster
		if err := util.ReadJsonPayloadFromBody(body, &c); err != nil {
			return 0, err
		}
		count = len(c)
		AddResources(s, resource.ClusterType, c)
	case "route", "routes":
		var r []*XDSRoute
		if err := util.ReadJsonPayloadFromBody(body, &r); err != nil {
			return 0, err
		}
		count = len(r)
		AddResources(s, resource.RouteType, r)
	case "listener", "listeners":
		var l []*XDSListener
		if err := util.ReadJsonPayloadFromBody(body, &l); err != nil {
			return 0, err
		}
		count = len(l)
		AddResources(s, resource.ListenerType, l)
	case "secret", "secrets":
		var sec []*XDSSecret
		if err := util.ReadJsonPayloadFromBody(body, &sec); err != nil {
			return 0, err
		}
		count = len(sec)
		AddResources(s, resource.SecretType, sec)
	default:
		return 0, errors.New("unsupported type")
	}
	return count, nil
}

func (s *Store) GetResources(resourceType string) interface{} {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var rtype string
	switch resourceType {
	case "cluster", "clusters":
		rtype = resource.ClusterType
	case "route", "routes":
		rtype = resource.RouteType
	case "listener", "listeners":
		rtype = resource.ListenerType
	case "secret", "secrets":
		rtype = resource.SecretType
	default:
		rtype = ""
	}
	if rtype == "" {
		return s.ResourceMaps
	} else {
		return s.ResourceMaps[rtype]
	}
}

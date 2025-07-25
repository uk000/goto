package k8s

import (
	"context"
	"goto/pkg/server/xds/store"
	"log"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
)

func StartWatcher(ctx context.Context, store *store.Store) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Printf("k8s config error: %v", err)
		return
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Printf("k8s client error: %v", err)
		return
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	epsInformer := factory.Discovery().V1().EndpointSlices().Informer()

	epsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			handleEndpointSlice(obj, store)
		},
		UpdateFunc: func(_, newObj interface{}) {
			handleEndpointSlice(newObj, store)
		},
		DeleteFunc: func(obj interface{}) {
			slice, ok := obj.(*discoveryv1.EndpointSlice)
			if !ok {
				return
			}
			store.RemoveEndpoints(slice.Labels["kubernetes.io/service-name"])
		},
	})

	go epsInformer.Run(ctx.Done())
	<-ctx.Done()
}

func handleEndpointSlice(obj interface{}, store *store.Store) {
	slice, ok := obj.(*discoveryv1.EndpointSlice)
	if !ok {
		return
	}

	cla := toClusterLoadAssignment(slice)
	store.StoreEndpoints(cla.ClusterName, cla)
}

func toClusterLoadAssignment(slice *discoveryv1.EndpointSlice) *endpointv3.ClusterLoadAssignment {
	cla := &endpointv3.ClusterLoadAssignment{
		ClusterName: slice.Labels["kubernetes.io/service-name"],
		Endpoints:   []*endpointv3.LocalityLbEndpoints{},
	}

	lbEndpoints := []*endpointv3.LbEndpoint{}
	for _, ep := range slice.Endpoints {
		for _, addr := range ep.Addresses {
			for _, port := range slice.Ports {
				if port.Port != nil {
					lbEndpoints = append(lbEndpoints, &endpointv3.LbEndpoint{
						HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
							Endpoint: &endpointv3.Endpoint{
								Address: &corev3.Address{
									Address: &corev3.Address_SocketAddress{
										SocketAddress: &corev3.SocketAddress{
											Address: addr,
											PortSpecifier: &corev3.SocketAddress_PortValue{
												PortValue: uint32(*port.Port),
											},
										},
									},
								},
							},
						},
					})
				}
			}
		}
	}

	if len(lbEndpoints) > 0 {
		cla.Endpoints = append(cla.Endpoints, &endpointv3.LocalityLbEndpoints{
			LbEndpoints: lbEndpoints,
		})
	}

	return cla
}

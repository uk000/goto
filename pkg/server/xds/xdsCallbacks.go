package xds

import (
	"context"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
)

type CallbackHandler struct {
	server.CallbackFuncs
}

func (c CallbackHandler) OnStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	return nil
}

func (c CallbackHandler) OnStreamClosed(streamID int64, node *core.Node) {
}

func (c CallbackHandler) OnDeltaStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	return nil
}

func (c CallbackHandler) OnDeltaStreamClosed(streamID int64, node *core.Node) {
}

func (c CallbackHandler) OnStreamRequest(streamID int64, req *discovery.DiscoveryRequest) error {
	return nil
}

func (c CallbackHandler) OnStreamResponse(ctx context.Context, streamID int64, req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
}

func (c CallbackHandler) OnStreamDeltaRequest(streamID int64, req *discovery.DeltaDiscoveryRequest) error {
	return nil
}

func (c CallbackHandler) OnStreamDeltaResponse(streamID int64, req *discovery.DeltaDiscoveryRequest, resp *discovery.DeltaDiscoveryResponse) {
}

func (c CallbackHandler) OnFetchRequest(ctx context.Context, req *discovery.DiscoveryRequest) error {
	return nil
}

func (c CallbackHandler) OnFetchResponse(req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
}

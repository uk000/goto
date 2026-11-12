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

package grpcserver

import (
	"context"
	"goto/pkg/util"

	"google.golang.org/grpc/stats"
)

// udsConnTagger tags the connections accepted on unix domain socket listeners with the port of the listener, since the
// local address of such connections has no port.
type udsConnTagger struct{}

func (t *udsConnTagger) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	if port := util.GetUDSPort(info.LocalAddr); port > 0 {
		return util.WithPort(ctx, port)
	}
	return ctx
}

func (t *udsConnTagger) HandleConn(context.Context, stats.ConnStats) {}

func (t *udsConnTagger) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

func (t *udsConnTagger) HandleRPC(context.Context, stats.RPCStats) {}

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

package grpcclient

import (
	"goto/pkg/types"
	"sync"

	"google.golang.org/grpc/metadata"
)

type GRPCPayload struct {
	Payload string         `json:"payload"`
	Stream  []string       `json:"stream"`
	Linear  []*GRPCPayload `json:"linear"`
	Headers *types.Headers `json:"headers"`
}

type GRPCPayloads struct {
	Concurrent []*GRPCPayload `json:"concurrent"`
	Linear     []*GRPCPayload `json:"linear"`
}

type UnaryCallHandler func(md metadata.MD, payload []byte, wg *sync.WaitGroup)
type StreamCallHandler func(md metadata.MD, payload [][]byte, wg *sync.WaitGroup)

// Count returns the number of messages or calls that the payload stands for.
func (gp *GRPCPayload) Count() int {
	if len(gp.Linear) > 0 {
		return len(gp.Linear)
	} else if len(gp.Stream) > 0 {
		return 1
	} else if gp.Payload != "" {
		return 1
	}
	return 0
}

// Process sends the payload (recursing into linear payloads, applying its headers) through the unary or stream handler.
func (gp *GRPCPayload) Process(call *GRPCCall, md metadata.MD, uCall UnaryCallHandler, sCall StreamCallHandler, wg *sync.WaitGroup) {
	finalMD := md
	if gp.Headers != nil {
		// a copy, so that the headers of a payload apply to it only, whether or not there are headers to start with
		finalMD = md.Copy()
		call.UpdateStreamHeaders(types.MD(finalMD), gp)
	}
	if len(gp.Linear) > 0 {
		for _, gp2 := range gp.Linear {
			gp2.Process(call, finalMD, uCall, sCall, wg)
		}
	} else if len(gp.Stream) > 0 {
		streamPayload := [][]byte{}
		for _, payload := range gp.Stream {
			streamPayload = append(streamPayload, []byte(payload))
		}
		if sCall != nil {
			sCall(finalMD, streamPayload, wg)
		} else if uCall != nil {
			// a unary call has no stream to send the messages to, so each of them is a call
			for i, payload := range streamPayload {
				if i == len(streamPayload)-1 {
					uCall(finalMD, payload, wg)
				} else {
					uCall(finalMD, payload, nil)
				}
			}
		} else if wg != nil {
			wg.Done()
		}
	} else if gp.Payload != "" {
		if uCall != nil {
			uCall(finalMD, []byte(gp.Payload), wg)
		} else if sCall != nil {
			sCall(finalMD, [][]byte{[]byte(gp.Payload)}, wg)
		}
	}
}

// Process runs the linear payloads in order, or the concurrent payloads in parallel and waits for them.
func (gp *GRPCPayloads) Process(call *GRPCCall, md metadata.MD, uCall UnaryCallHandler, sCall StreamCallHandler) {
	if len(gp.Linear) > 0 {
		for _, gp2 := range gp.Linear {
			gp2.Process(call, md, uCall, sCall, nil)
		}
	} else if len(gp.Concurrent) > 0 {
		wg := &sync.WaitGroup{}
		for _, gp2 := range gp.Concurrent {
			wg.Add(1)
			go func() {
				// the payloads run in order within a payload, and the wait is for the payloads, not the calls that
				// each of them makes, so the handlers are given no wait group
				defer wg.Done()
				gp2.Process(call, md, uCall, sCall, nil)
			}()
		}
		wg.Wait()
	}
}

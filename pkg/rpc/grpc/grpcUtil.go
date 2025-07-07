package grpc

import (
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/util"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/dynamicpb"
)

func ParseRequest(req interface{}) ([]byte, error) {
	msg, ok := req.(*dynamicpb.Message)
	if !ok {
		return nil, fmt.Errorf("unexpected message type")
	}
	if b, err := protojson.Marshal(msg); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	} else {
		return b, nil
	}
}

// func ParseRequestJSON(req interface{}) (util.JSON, error) {
// 	if b, err := parseRequest(req); err != nil {
// 		return nil, err
// 	} else {
// 		return util.FromBytes(b), nil
// 	}
// }

func GetRequestHeaders(ctx context.Context) (map[string][]string, error) {
	headers := make(map[string][]string)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for k, v := range md {
			headers[k] = v
		}
	} else {
		return nil, fmt.Errorf("failed to get headers from context")
	}

	return headers, nil
}

func GetRequestHost(md map[string][]string) string {
	host := ""
	if v, ok := md[":authority"]; ok {
		host = v[0]
		md["host"] = v
	} else if v, ok := md["host"]; ok {
		host = v[0]
	} else if v, ok := md["hostName"]; ok {
		host = v[0]
	}
	return host
}

func GetRequestRemoteAddr(md map[string][]string) string {
	remoteAddr := ""
	if v, ok := md["remoteAddr"]; ok {
		remoteAddr = v[0]
	}
	return remoteAddr
}

func CreateDummyRequest(method *GRPCServiceMethod, dec func(interface{}) error) *dynamicpb.Message {
	req := dynamicpb.NewMessage(method.InputType())
	if err := dec(req); err != nil {
		return nil
	}
	return req
}

func BuildResponse(method *GRPCServiceMethod, resp [][]byte) (*dynamicpb.Message, error) {
	dmsg := dynamicpb.NewMessage(method.OutputType())
	if err := protojson.Unmarshal(resp[0], dmsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response to dynamic message: %w", err)
	}
	return dmsg, nil
}

func SendStreamResponse(method *GRPCServiceMethod, stream grpc.ServerStream, resp [][]byte, from, to int) error {
	rem := to - from
	if from > len(resp) {
		from = 0
		to = from + rem
	}
	for i := from; i < to; i++ {
		if i >= len(resp) {
			i = 0
		}
		rem--
		dmsg := dynamicpb.NewMessage(method.OutputType())
		if err := protojson.Unmarshal(resp[i], dmsg); err != nil {
			return fmt.Errorf("failed to unmarshal response to dynamic message: %w", err)
		}
		if err := stream.SendMsg(dmsg); err != nil {
			return err
		}
		if rem == 0 {
			break
		}
	}
	return nil
}

func CommonHandler(ctx context.Context, stream grpc.ServerStream) (*GRPCServiceMethod, int, string, map[string][]string, error) {
	if ctx == nil {
		ctx = stream.Context()
	}
	port := util.GetContextPort(ctx)
	method := ServiceRegistry.ParseGRPCServiceMethod(ctx)
	if method == nil {
		return nil, port, "", nil, fmt.Errorf("method not found in context")
	}
	md, err := GetRequestHeaders(ctx)
	if err != nil || md == nil {
		return method, port, "", nil, fmt.Errorf("Metadata not found in context")
	}
	authority := ""
	if a := md[constants.HeaderAuthority]; len(a) > 0 {
		authority = a[0]
	}
	return method, port, authority, md, nil
}

func AddRequestLogMessage(ctx context.Context, port int, service, method, authority string, requestHeaders any, requestCount, requestBodyLength int, requestMiniBody string) {
	if !global.Flags.EnableServerLogs {
		return
	}
	hostLabel := listeners.GetHostLabelForPort(port)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	remoteAddr := ""
	if p, ok := peer.FromContext(ctx); ok {
		remoteAddr = p.Addr.String()
	}
	msg := fmt.Sprintf("[%s]@[%s]: Protocol: [GRPC] Service [%s] Method [%s] RemoteAddr [%s], Request{ Host: [%s], Count [%d], Content Length [%d]",
		listenerLabel, hostLabel, service, method, remoteAddr, authority, requestCount, requestBodyLength)
	if global.Flags.LogRequestHeaders {
		msg += fmt.Sprintf(", Request Headers: [%+v]", util.ToJSONText(requestHeaders))
	}
	if global.Flags.LogRequestMiniBody {
		msg += fmt.Sprintf(", Request Mini Body [%s]", requestMiniBody)
	}
	msg += " }"
	util.AddLogMessageForContext(ctx, msg)
}

func AddResponseLogMessage(ctx context.Context, responseHeaders any, responseStatus, responseCount, responseLength int, action string) {
	if !global.Flags.EnableServerLogs {
		return
	}
	msg := fmt.Sprintf("%s --> Response Status [%d], Response Body Length [%d]", action, responseStatus, responseLength)
	if global.Flags.LogResponseHeaders {
		msg += fmt.Sprintf(", Response Headers: [%s]", util.ToJSONText(responseHeaders))
	}
	util.AddLogMessageForContext(ctx, msg)
}

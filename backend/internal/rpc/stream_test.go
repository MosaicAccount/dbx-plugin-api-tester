package rpc

import (
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/jlian/dbx-plugin-api-tester/internal/grpctool"
	"google.golang.org/grpc"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
)

// 复用 grpc_testing 桩：FullDuplexCall 严格一问一答，StreamingInputCall 收齐再回。
const testStreamService = "grpc.testing.TestService"

type testStreamServer struct {
	testpb.UnimplementedTestServiceServer
}

func (s *testStreamServer) StreamingInputCall(
	stream grpc.ClientStreamingServer[testpb.StreamingInputCallRequest, testpb.StreamingInputCallResponse],
) error {
	total := int32(0)
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&testpb.StreamingInputCallResponse{AggregatedPayloadSize: total})
		}
		if err != nil {
			return err
		}
		total += int32(len(request.GetPayload().GetBody()))
	}
}

func (s *testStreamServer) FullDuplexCall(
	stream grpc.BidiStreamingServer[testpb.StreamingOutputCallRequest, testpb.StreamingOutputCallResponse],
) error {
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&testpb.StreamingOutputCallResponse{Payload: request.GetPayload()}); err != nil {
			return err
		}
	}
}

func startTestStreamServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	server := grpc.NewServer()
	testpb.RegisterTestServiceServer(server, &testStreamServer{})
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func dispatch(t *testing.T, handler *Handler, method string, payload map[string]any) (any, *grpctool.WaitStreamResult) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("cannot marshal params: %v", err)
	}
	result, pluginError := handler.Dispatch(method, raw)
	if pluginError != nil {
		t.Fatalf("%s: %v", method, pluginError)
	}
	if wait, ok := result.(*grpctool.WaitStreamResult); ok {
		return result, wait
	}
	return result, nil
}

// 双向流：每条消息单独发送、单独取回复，最后半关闭收 status。
// 这条用例走的是 rpc.Dispatch，覆盖参数解码、会话表与真实 gRPC 服务端。
func TestStreamBidiThroughDispatch(t *testing.T) {
	target := startTestStreamServer(t)
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	opened, _ := dispatch(t, handler, MethodGRPCStreamOpen, map[string]any{
		"target": target, "timeoutMs": 5000,
		"service": testStreamService, "method": "FullDuplexCall",
	})
	session, ok := opened.(*grpctool.OpenStreamResult)
	if !ok || session.Kind != "bidi" {
		t.Fatalf("unexpected open result %#v", opened)
	}

	for index, size := range []int{3, 5} {
		sent, _ := dispatch(t, handler, MethodGRPCStreamSend, map[string]any{
			"streamId": session.StreamID,
			"message":  map[string]any{"payload": map[string]any{"body": make([]byte, size)}},
		})
		if out := sent.(*grpctool.SendStreamResult); out.OutSeq != index+1 {
			t.Fatalf("send #%d: expected outSeq %d, got %d", index+1, index+1, out.OutSeq)
		}

		_, waited := dispatch(t, handler, MethodGRPCStreamWait, map[string]any{
			"streamId": session.StreamID, "waitMs": 5000,
		})
		if len(waited.Messages) != 1 {
			t.Fatalf("wait #%d: expected one echo, got %d", index+1, len(waited.Messages))
		}
		if waited.Messages[0].Seq != index+1 {
			t.Fatalf("wait #%d: expected inbound seq %d, got %d", index+1, index+1, waited.Messages[0].Seq)
		}
	}

	_, half := dispatch(t, handler, MethodGRPCStreamHalfClose, map[string]any{
		"streamId": session.StreamID, "waitMs": 5000,
	})
	if !half.Ended || half.Status == nil || half.Status.Code != 0 {
		t.Fatalf("expected OK after half-close, got ended=%v status=%+v", half.Ended, half.Status)
	}

	dispatch(t, handler, MethodGRPCStreamClose, map[string]any{"streamId": session.StreamID})
	// close 必须幂等。
	dispatch(t, handler, MethodGRPCStreamClose, map[string]any{"streamId": session.StreamID})
}

// 客户端流：逐条发送时服务端不回，半关闭之后才拿到那一条总响应。
func TestStreamClientThroughDispatch(t *testing.T) {
	target := startTestStreamServer(t)
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	opened, _ := dispatch(t, handler, MethodGRPCStreamOpen, map[string]any{
		"target": target, "timeoutMs": 5000,
		"service": testStreamService, "method": "StreamingInputCall",
	})
	session := opened.(*grpctool.OpenStreamResult)
	if session.Kind != "client" {
		t.Fatalf("expected client stream, got %q", session.Kind)
	}

	for index := 0; index < 3; index++ {
		dispatch(t, handler, MethodGRPCStreamSend, map[string]any{
			"streamId": session.StreamID,
			"message":  map[string]any{"payload": map[string]any{"body": make([]byte, 3)}},
		})
	}

	_, half := dispatch(t, handler, MethodGRPCStreamHalfClose, map[string]any{
		"streamId": session.StreamID, "waitMs": 5000,
	})
	if !half.Ended {
		t.Fatalf("client stream must end after half-close, got %+v", half)
	}
	if len(half.Messages) != 1 {
		t.Fatalf("expected exactly one response, got %d", len(half.Messages))
	}

	var payload struct {
		AggregatedPayloadSize int `json:"aggregatedPayloadSize"`
	}
	if err := json.Unmarshal([]byte(half.Messages[0].JSON), &payload); err != nil {
		t.Fatalf("invalid response JSON %q: %v", half.Messages[0].JSON, err)
	}
	if payload.AggregatedPayloadSize != 9 {
		t.Fatalf("expected 9 aggregated bytes, got %d", payload.AggregatedPayloadSize)
	}

	dispatch(t, handler, MethodGRPCStreamClose, map[string]any{"streamId": session.StreamID})
}

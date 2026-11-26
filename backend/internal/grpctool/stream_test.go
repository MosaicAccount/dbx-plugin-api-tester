package grpctool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
)

// grpc_testing.TestService 同时提供客户端流、双向流和一元的现成桩，
// 省掉为测试自己生成 pb 代码的成本。
const streamingService = "grpc.testing.TestService"

type streamingServer struct {
	testpb.UnimplementedTestServiceServer
}

func (s *streamingServer) EmptyCall(context.Context, *testpb.Empty) (*testpb.Empty, error) {
	return &testpb.Empty{}, nil
}

func (s *streamingServer) StreamingInputCall(
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

// FullDuplexCall 严格一问一答：收到一条立刻回一条，模拟真实的交互式服务。
func (s *streamingServer) FullDuplexCall(
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
		response := &testpb.StreamingOutputCallResponse{Payload: request.GetPayload()}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

func startStreamingServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	server := grpc.NewServer()
	testpb.RegisterTestServiceServer(server, &streamingServer{})
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// payloadMessage 生成一条 protojson 消息，body 是 size 个字节（bytes 字段用 base64）。
func payloadMessage(size int) json.RawMessage {
	body := strings.Repeat("a", size)
	return json.RawMessage(fmt.Sprintf(`{"payload":{"body":"%s"}}`, base64.StdEncoding.EncodeToString([]byte(body))))
}

func TestInvokeGRPCClientStreaming(t *testing.T) {
	target := startStreamingServer(t)
	result, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 10000},
		Service:      streamingService,
		Method:       "StreamingInputCall",
		Messages:     []json.RawMessage{payloadMessage(3), payloadMessage(3), payloadMessage(3)},
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation := result.(*InvokeResult)
	if !invocation.OK {
		t.Fatalf("expected OK, got %+v", invocation.Status)
	}
	if !invocation.Streaming || invocation.StreamKind != "client" {
		t.Fatalf("expected a client stream, got streaming=%v kind=%q", invocation.Streaming, invocation.StreamKind)
	}
	if len(invocation.Messages) != 1 {
		t.Fatalf("client streaming must return exactly one message, got %d", len(invocation.Messages))
	}

	var payload struct {
		AggregatedPayloadSize int `json:"aggregatedPayloadSize"`
	}
	if err := json.Unmarshal([]byte(invocation.Messages[0]), &payload); err != nil {
		t.Fatalf("invalid response JSON %q: %v", invocation.Messages[0], err)
	}
	if payload.AggregatedPayloadSize != 9 {
		t.Fatalf("expected 9 aggregated bytes, got %d", payload.AggregatedPayloadSize)
	}
}

func TestInvokeGRPCBidiStreaming(t *testing.T) {
	target := startStreamingServer(t)
	result, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 10000},
		Service:      streamingService,
		Method:       "FullDuplexCall",
		Messages:     []json.RawMessage{payloadMessage(3), payloadMessage(5), payloadMessage(7)},
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation := result.(*InvokeResult)
	if !invocation.OK {
		t.Fatalf("expected OK, got %+v", invocation.Status)
	}
	if invocation.StreamKind != "bidi" {
		t.Fatalf("expected a bidi stream, got %q", invocation.StreamKind)
	}
	if len(invocation.Messages) != 3 {
		t.Fatalf("expected 3 echoes, got %d: %v", len(invocation.Messages), invocation.Status)
	}
	for _, message := range invocation.Messages {
		if !strings.Contains(message, "body") {
			t.Fatalf("echoed message lost its payload: %q", message)
		}
	}
}

// 双向流大载荷回归测试。
//
// 服务端严格一问一答时，如果接收不并发，两边缓冲会互相等死：
// 这里 8 条 256KB 的消息会一直卡到超时。这条用例就是防止把并发接收改回串行。
func TestInvokeGRPCBidiLargePayloadDoesNotStall(t *testing.T) {
	target := startStreamingServer(t)
	messages := make([]json.RawMessage, 0, 8)
	for index := 0; index < 8; index++ {
		messages = append(messages, payloadMessage(256*1024))
	}

	result, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 10000},
		Service:      streamingService,
		Method:       "FullDuplexCall",
		Messages:     messages,
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation := result.(*InvokeResult)
	if !invocation.OK {
		t.Fatalf("large bidi payload stalled or failed: %+v", invocation.Status)
	}
	if len(invocation.Messages) != 8 {
		t.Fatalf("expected 8 echoes, got %d", len(invocation.Messages))
	}
}

func TestInvokeGRPCStreamingValidation(t *testing.T) {
	target := startStreamingServer(t)

	// 一元方法带多条消息属于调用方错误，应在发到网络层之前拒绝。
	if _, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "EmptyCall",
		Messages:     []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{}`)},
	}); pluginError == nil {
		t.Fatal("expected multiple messages on a unary method to be rejected")
	}

	// 单条 message 的旧参数形式必须继续可用。
	if _, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "EmptyCall",
		Message:      json.RawMessage(`{}`),
	}); pluginError != nil {
		t.Fatalf("single-message form should still work: %v", pluginError)
	}

	// 消息条数上限。
	many := make([]json.RawMessage, maxOutboundMessages+1)
	for index := range many {
		many[index] = json.RawMessage(`{}`)
	}
	if _, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "FullDuplexCall",
		Messages:     many,
	}); pluginError == nil {
		t.Fatal("expected too many outbound messages to be rejected")
	}
}

package grpctool

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/values"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const healthService = "grpc.health.v1.Health"

func startHealthServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// slowWatchServer 每 40ms 推一条健康状态，用来验证取消时能保住已收到的消息。
type slowWatchServer struct {
	healthpb.UnimplementedHealthServer
}

func (s *slowWatchServer) Watch(
	_ *healthpb.HealthCheckRequest,
	stream grpc.ServerStreamingServer[healthpb.HealthCheckResponse],
) error {
	for index := 0; index < 1000; index++ {
		if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
	return nil
}

func startSlowWatchServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, &slowWatchServer{})
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func TestDiscoverGRPCServices(t *testing.T) {
	target := startHealthServer(t)
	result, pluginError := Discover(context.Background(), DiscoveryParams{TargetParams{Target: target, TimeoutMs: 5000}})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	services := result.(*ServicesResult).Services
	if !contains(services, healthService) {
		t.Fatalf("expected %s in %v", healthService, services)
	}
	for _, service := range services {
		if strings.HasPrefix(service, "grpc.reflection.") {
			t.Fatalf("reflection services should be hidden: %v", services)
		}
	}
}

func TestDescribeGRPCMethods(t *testing.T) {
	target := startHealthServer(t)
	result, pluginError := DescribeMethods(context.Background(), MethodsParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      healthService,
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	methods := result.(*MethodsResult).Methods
	check := findMethodInfo(methods, "Check")
	if check == nil || check.ServerStreaming || check.ClientStreaming {
		t.Fatalf("unexpected Check method: %+v", check)
	}
	watch := findMethodInfo(methods, "Watch")
	if watch == nil || !watch.ServerStreaming {
		t.Fatalf("unexpected Watch method: %+v", watch)
	}
	if !strings.Contains(watch.InputType, "HealthCheckRequest") {
		t.Fatalf("unexpected input type: %q", watch.InputType)
	}
}

func TestInvokeGRPCUnary(t *testing.T) {
	target := startHealthServer(t)
	result, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      healthService,
		Method:       "Check",
		Message:      json.RawMessage(`{"service":""}`),
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation := result.(*InvokeResult)
	if !invocation.OK {
		t.Fatalf("expected OK, got %+v", invocation.Status)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(invocation.MessageJSON), &payload); err != nil {
		t.Fatalf("invalid response JSON %q: %v", invocation.MessageJSON, err)
	}
	if payload["status"] != "SERVING" {
		t.Fatalf("unexpected response payload: %v", payload)
	}
}

func TestInvokeGRPCServerStream(t *testing.T) {
	target := startHealthServer(t)
	result, pluginError := Invoke(context.Background(), InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 1200},
		Service:      healthService,
		Method:       "Watch",
		Message:      json.RawMessage(`{"service":""}`),
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation := result.(*InvokeResult)
	if !invocation.Streaming {
		t.Fatalf("expected a streaming invocation: %+v", invocation)
	}
	if invocation.StreamKind != "server" {
		t.Fatalf("expected a server stream, got %q", invocation.StreamKind)
	}
	if len(invocation.Messages) == 0 {
		t.Fatalf("expected at least one streamed message: %+v", invocation)
	}
	if !strings.Contains(invocation.Messages[0], "SERVING") {
		t.Fatalf("unexpected streamed message: %q", invocation.Messages[0])
	}
	if invocation.Status.Name != "DeadlineExceeded" {
		t.Fatalf("expected the stream to end with the deadline, got %+v", invocation.Status)
	}
}

// 长连接流被用户终止时，结果要标记 canceled，而不是丢掉已经收到的消息：
// 前端要能显示"已终止"，也要能继续展示终止前收到的响应。
func TestInvokeGRPCCanceledByUser(t *testing.T) {
	target := startHealthServer(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(apperr.ErrCanceled) })

	value, pluginError := Invoke(ctx, InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      healthService,
		Method:       "Watch",
		Message:      json.RawMessage(`{"service":""}`),
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation, ok := value.(*InvokeResult)
	if !ok {
		t.Fatalf("unexpected result type %T", value)
	}
	if !invocation.Canceled {
		t.Fatalf("expected canceled result, got %+v", invocation)
	}
	if invocation.OK {
		t.Fatalf("a canceled invocation must not be OK: %+v", invocation)
	}
}

// 服务端每 40ms 推一条；取消后必须把终止前收到的消息带回来。
func TestInvokeGRPCCanceledKeepsPartialMessages(t *testing.T) {
	target := startSlowWatchServer(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(250*time.Millisecond, func() { cancel(apperr.ErrCanceled) })

	value, pluginError := Invoke(ctx, InvokeParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      healthService,
		Method:       "Watch",
		Message:      json.RawMessage(`{"service":""}`),
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	invocation := value.(*InvokeResult)
	if !invocation.Canceled {
		t.Fatalf("expected canceled result, got %+v", invocation)
	}
	if len(invocation.Messages) == 0 {
		t.Fatal("expected the messages received before cancellation to be kept")
	}
}

func TestInvokeGRPCValidation(t *testing.T) {
	target := startHealthServer(t)
	cases := []InvokeParams{
		{TargetParams: TargetParams{Target: target, TimeoutMs: 3000}, Service: healthService, Method: "Missing"},
		{TargetParams: TargetParams{Target: target, TimeoutMs: 3000}, Service: "example.Unknown", Method: "Check"},
		{TargetParams: TargetParams{Target: target, TimeoutMs: 3000}, Service: healthService, Method: "Check", Message: json.RawMessage(`{"service":123}`)},
		{TargetParams: TargetParams{Target: "", TimeoutMs: 3000}, Service: healthService, Method: "Check"},
		{TargetParams: TargetParams{Target: "localhost", TimeoutMs: 3000}, Service: healthService, Method: "Check"},
	}
	for index, params := range cases {
		if _, pluginError := Invoke(context.Background(), params); pluginError == nil {
			t.Fatalf("case %d should be rejected", index)
		}
	}
}

func TestGRPCMetadataValidation(t *testing.T) {
	if _, err := metadataFromEntries([]values.KeyValue{{Name: "Bad Key", Value: "x"}}); err == nil {
		t.Fatal("expected an invalid metadata key error")
	}
	if _, err := metadataFromEntries([]values.KeyValue{{Name: "grpc-internal", Value: "x"}}); err == nil {
		t.Fatal("expected a reserved metadata key error")
	}
	if _, err := metadataFromEntries([]values.KeyValue{{Name: "authorization", Value: "a\r\nb"}}); err == nil {
		t.Fatal("expected a metadata line break error")
	}
	values, err := metadataFromEntries([]values.KeyValue{{Name: "Authorization", Value: "Bearer token"}})
	if err != nil || len(values["authorization"]) != 1 {
		t.Fatalf("unexpected metadata: %v %v", values, err)
	}
}

func TestNormalizeGRPCTarget(t *testing.T) {
	valid := map[string]string{
		"localhost:50051":             "localhost:50051",
		"https://api.example.com:443": "api.example.com:443",
		"grpc://127.0.0.1:9000":       "127.0.0.1:9000",
	}
	for input, expected := range valid {
		actual, err := normalizeGRPCTarget(input)
		if err != nil || actual != expected {
			t.Fatalf("%q: got %q, %v", input, actual, err)
		}
	}
	for _, input := range []string{"", "localhost", "://bad"} {
		if _, err := normalizeGRPCTarget(input); err == nil {
			t.Fatalf("%q should be rejected", input)
		}
	}
}

func TestGRPCStatusInfo(t *testing.T) {
	info := StatusInfoFromError(nil)
	if info.Code != 0 || info.Name != "OK" {
		t.Fatalf("unexpected OK status: %+v", info)
	}
	info = StatusInfoFromError(nil)
	if info.Message != "" {
		t.Fatalf("OK status should not carry a message: %+v", info)
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func findMethodInfo(methods []MethodInfo, name string) *MethodInfo {
	for index := range methods {
		if methods[index].Name == name {
			return &methods[index]
		}
	}
	return nil
}

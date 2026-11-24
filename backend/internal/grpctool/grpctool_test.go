package grpctool

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

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

func TestDiscoverGRPCServices(t *testing.T) {
	target := startHealthServer(t)
	result, pluginError := Discover(DiscoveryParams{TargetParams{Target: target, TimeoutMs: 5000}})
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
	result, pluginError := DescribeMethods(MethodsParams{
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
	result, pluginError := Invoke(InvokeParams{
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
	result, pluginError := Invoke(InvokeParams{
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
		if _, pluginError := Invoke(params); pluginError == nil {
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

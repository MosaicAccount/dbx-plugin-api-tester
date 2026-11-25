package grpctool

import (
	"encoding/json"
	"strings"
	"testing"
)

const demoProto = `syntax = "proto3";

package local.demo;

import "common/base.proto";
import "google/protobuf/timestamp.proto";

service Greeter {
  rpc Hello(HelloRequest) returns (HelloReply);
  rpc Upload(stream HelloRequest) returns (HelloReply);
  rpc Watch(HelloRequest) returns (stream HelloReply);
}

message HelloRequest {
  string name = 1;
  local.common.Page page = 2;
  google.protobuf.Timestamp at = 3;
}

message HelloReply {
  string message = 1;
}
`

const baseProto = `syntax = "proto3";

package local.common;

message Page {
  int32 size = 1;
  string cursor = 2;
}
`

const healthProto = `syntax = "proto3";

package grpc.health.v1;

message HealthCheckRequest {
  string service = 1;
}

message HealthCheckResponse {
  enum ServingStatus {
    UNKNOWN = 0;
    SERVING = 1;
    NOT_SERVING = 2;
    SERVICE_UNKNOWN = 3;
  }
  ServingStatus status = 1;
}

service Health {
  rpc Check(HealthCheckRequest) returns (HealthCheckResponse);
  rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
}
`

func protoFiles(entries ...string) []ProtoFile {
	files := make([]ProtoFile, 0, len(entries)/2)
	for index := 0; index+1 < len(entries); index += 2 {
		files = append(files, ProtoFile{Name: entries[index], Content: entries[index+1]})
	}
	return files
}

func TestDiscoverFromProtoFilesDoesNotDial(t *testing.T) {
	// target 根本不可用；文件模式不应该尝试连接或反射。
	result, pluginError := Discover(DiscoveryParams{TargetParams: TargetParams{
		Target:        "no-such-host:1",
		UseProtoFiles: true,
		ProtoFiles:    protoFiles("demo.proto", demoProto, "common/base.proto", baseProto),
	}})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	services := result.(*ServicesResult).Services
	if len(services) != 1 || services[0] != "local.demo.Greeter" {
		t.Fatalf("unexpected services: %v", services)
	}
}

func TestDescribeMethodsFromProtoFiles(t *testing.T) {
	result, pluginError := DescribeMethods(MethodsParams{
		TargetParams: TargetParams{
			UseProtoFiles: true,
			ProtoFiles:    protoFiles("demo.proto", demoProto, "common/base.proto", baseProto),
		},
		Service: "local.demo.Greeter",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	methods := result.(*MethodsResult).Methods
	if len(methods) != 3 {
		t.Fatalf("unexpected method count: %+v", methods)
	}
	hello := findMethodInfo(methods, "Hello")
	if hello == nil || hello.ClientStreaming || hello.ServerStreaming {
		t.Fatalf("unexpected Hello method: %+v", hello)
	}
	if hello.InputType != "local.demo.HelloRequest" || hello.OutputType != "local.demo.HelloReply" {
		t.Fatalf("unexpected types: %+v", hello)
	}
	upload := findMethodInfo(methods, "Upload")
	if upload == nil || !upload.ClientStreaming || upload.ServerStreaming {
		t.Fatalf("unexpected Upload method: %+v", upload)
	}
	watch := findMethodInfo(methods, "Watch")
	if watch == nil || watch.ClientStreaming || !watch.ServerStreaming {
		t.Fatalf("unexpected Watch method: %+v", watch)
	}
}

func TestProtoFilesResolveBasenameImport(t *testing.T) {
	// 用户选中的文件带目录，但 proto 里 import 只写了 basename。
	const mainProto = `syntax = "proto3";
package local.flat;

import "base.proto";

service Flat {
  rpc Get(local.common.Page) returns (local.common.Page);
}
`
	result, pluginError := DescribeMethods(MethodsParams{
		TargetParams: TargetParams{
			UseProtoFiles: true,
			ProtoFiles:    protoFiles("proto/flat.proto", mainProto, "proto/common/base.proto", baseProto),
		},
		Service: "local.flat.Flat",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	methods := result.(*MethodsResult).Methods
	if len(methods) != 1 || methods[0].InputType != "local.common.Page" {
		t.Fatalf("unexpected methods: %+v", methods)
	}
}

func TestInvokeWithProtoFiles(t *testing.T) {
	target := startHealthServer(t)
	result, pluginError := Invoke(InvokeParams{
		TargetParams: TargetParams{
			Target:        target,
			TimeoutMs:     5000,
			UseProtoFiles: true,
			ProtoFiles:    protoFiles("health.proto", healthProto),
		},
		Service: "grpc.health.v1.Health",
		Method:  "Check",
		Message: json.RawMessage(`{"service":""}`),
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

func TestProtoFileValidation(t *testing.T) {
	cases := []struct {
		name  string
		files []ProtoFile
		match string
	}{
		{name: "empty list", files: nil, match: "at least one"},
		{name: "empty name", files: []ProtoFile{{Name: " ", Content: demoProto}}, match: "file name"},
		{name: "empty content", files: []ProtoFile{{Name: "demo.proto", Content: "  "}}, match: "empty"},
		{
			name:  "duplicate",
			files: []ProtoFile{{Name: "demo.proto", Content: demoProto}, {Name: "./demo.proto", Content: demoProto}},
			match: "share the file name",
		},
		{
			name:  "syntax error",
			files: []ProtoFile{{Name: "broken.proto", Content: "syntax = \"proto3\"; message {"}},
			match: "compile",
		},
		{
			name:  "missing import",
			files: []ProtoFile{{Name: "demo.proto", Content: demoProto}},
			match: "base.proto",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, pluginError := loadProtoSchema(testCase.files)
			if pluginError == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(strings.ToLower(pluginError.Message), strings.ToLower(testCase.match)) {
				t.Fatalf("error %q does not mention %q", pluginError.Message, testCase.match)
			}
		})
	}
}

func TestProtoFileLimits(t *testing.T) {
	big := strings.Repeat(" ", maxProtoFileBytes)
	_, pluginError := loadProtoSchema([]ProtoFile{{Name: "big.proto", Content: "syntax = \"proto3\";" + big}})
	if pluginError == nil || !strings.Contains(pluginError.Message, "larger than") {
		t.Fatalf("expected a size error, got %v", pluginError)
	}
}

func TestDescribeMethodsFromProtoFilesDoesNotDial(t *testing.T) {
	// 同样是不可达的 target：proto 文件模式的方法解析不应依赖连接。
	result, pluginError := DescribeMethods(MethodsParams{
		TargetParams: TargetParams{
			Target:        "no-such-host:1",
			UseProtoFiles: true,
			ProtoFiles:    protoFiles("demo.proto", demoProto, "common/base.proto", baseProto),
		},
		Service: "local.demo.Greeter",
	})
	if pluginError != nil {
		t.Fatalf("unexpected error: %v", pluginError)
	}
	if methods := result.(*MethodsResult).Methods; len(methods) != 3 {
		t.Fatalf("unexpected methods: %+v", methods)
	}

	_, pluginError = DescribeMethods(MethodsParams{
		TargetParams: TargetParams{UseProtoFiles: true, ProtoFiles: protoFiles("base.proto", baseProto)},
		Service:      "local.demo.Greeter",
	})
	if pluginError == nil {
		t.Fatal("expected a missing service error")
	}
}

func TestProtoFileTotalSizeLimit(t *testing.T) {
	// 每份都在单文件上限内，但总和超过总量上限。
	padding := strings.Repeat("/", 100*1024)
	makeProto := func(pkg string) string {
		return "syntax = \"proto3\";\npackage " + pkg + ";\nmessage M {}\n" + padding
	}
	_, pluginError := loadProtoSchema([]ProtoFile{
		{Name: "a.proto", Content: makeProto("a")},
		{Name: "b.proto", Content: makeProto("b")},
		{Name: "c.proto", Content: makeProto("c")},
	})
	if pluginError == nil || !strings.Contains(pluginError.Message, "in total") {
		t.Fatalf("expected a total size error, got %v", pluginError)
	}
}

func TestNormalizeProtoImportsRewritesPaths(t *testing.T) {
	source := `syntax = "proto3";
import "common/base.proto";
import public "google/protobuf/empty.proto";
import weak "weak/other.proto";
`
	rewritten := normalizeProtoImports(source)
	if !strings.Contains(rewritten, `import "base.proto";`) {
		t.Fatalf("relative import was not rewritten: %q", rewritten)
	}
	if !strings.Contains(rewritten, `import public "google/protobuf/empty.proto";`) {
		t.Fatalf("standard import should be kept: %q", rewritten)
	}
	if !strings.Contains(rewritten, `import weak "other.proto";`) {
		t.Fatalf("weak import was not rewritten: %q", rewritten)
	}
}

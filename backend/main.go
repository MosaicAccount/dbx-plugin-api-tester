// Command dbx-plugin-apitester 是 API Tester 插件的原生 sidecar。
//
// 结构约定（详见 AGENTS.md）：
//   - 本文件只做装配与进程生命周期，不含业务逻辑。
//   - 方法分发在 internal/rpc，HTTP 在 internal/httptool，gRPC 在 internal/grpctool。
//   - 错误码集中在 internal/apperr，不要在别处手写 JSON-RPC 数字字面量。
//
// 协议约束：stdout 只输出 JSON-RPC 消息，任何日志都必须走 stderr。
package main

import (
	"encoding/json"
	"log"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/httptool"
	"github.com/jlian/dbx-plugin-api-tester/internal/rpc"
	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const (
	// pluginID 与 pluginVersion 必须与 manifest.json 完全一致。
	pluginID      = "com.jlian.apitester"
	pluginVersion = "0.1.2-SNAPSHOT"
)

// server 把分层后的分发器适配成 SDK 的 Handler 接口。
type server struct {
	handler *rpc.Handler
}

// Handle 实现 SDK 的 Handler。
//
// 这里做两件边界工作：panic 兜底、错误类型转换。业务代码因此可以放心返回
// *apperr.Error，不必关心 SDK 的具体结构。
func (s *server) Handle(
	_ dbxpluginsdk.RequestContext,
	method string,
	params json.RawMessage,
	_ *dbxpluginsdk.Emitter,
) (result any, pluginError *dbxpluginsdk.PluginError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// panic 细节只写 stderr，不回传给前端，避免泄露内部状态。
			log.Printf("panic while handling %s: %v", method, recovered)
			result = nil
			pluginError = toPluginError(apperr.Internal("Internal error while handling the request"))
		}
	}()

	value, failure := s.handler.Dispatch(method, params)
	if failure != nil {
		return nil, toPluginError(failure)
	}
	return value, nil
}

// toPluginError 把分层错误转换成 SDK 错误。
func toPluginError(err *apperr.Error) *dbxpluginsdk.PluginError {
	if err == nil {
		return nil
	}
	return &dbxpluginsdk.PluginError{
		Code:    int(err.Code),
		Message: err.Message,
		Data:    err.Data(),
	}
}

func main() {
	httptool.SetVersion(pluginVersion)

	metadata := dbxpluginsdk.Metadata{
		ID:           pluginID,
		Version:      pluginVersion,
		Capabilities: []string{"http", "grpc"},
	}

	plugin := &server{handler: rpc.New(pluginID, pluginVersion)}
	if err := dbxpluginsdk.NewServer(metadata, plugin).Serve(); err != nil {
		log.Fatal(err)
	}
}

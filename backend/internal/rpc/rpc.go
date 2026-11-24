// Package rpc 是 JSON-RPC 分发层。
//
// 它只负责三件事：路由到对应处理器、把 params 解码成强类型结构、把业务错误
// 统一成 apperr.Error。业务实现放在 httptool / grpctool 包里，这一层不含网络逻辑。
//
// 方法名集中声明在 Methods 里，前端调用与这里的字符串必须一致；新增方法时先加进
// Methods，再补 handler，避免出现"实现了但没登记"或"登记了但没实现"的漂移。
package rpc

import (
	"encoding/json"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/grpctool"
	"github.com/jlian/dbx-plugin-api-tester/internal/httptool"
)

// 全部对外方法名。前端 src/lib/store.js 里 invoke 的第一个参数与之对应。
const (
	MethodPing         = "api/ping"
	MethodHTTPSend     = "http/send"
	MethodGRPCServices = "grpc/services"
	MethodGRPCMethods  = "grpc/methods"
	MethodGRPCInvoke   = "grpc/invoke"
)

// Methods 返回已实现的方法列表，供 ping 自检与文档使用。
func Methods() []string {
	return []string{
		MethodHTTPSend,
		MethodGRPCServices,
		MethodGRPCMethods,
		MethodGRPCInvoke,
	}
}

// PingResult 是 api/ping 的返回结构。
type PingResult struct {
	OK      bool     `json:"ok"`
	ID      string   `json:"id"`
	Version string   `json:"version"`
	Methods []string `json:"methods"`
}

// 编译期确认各处理器的错误类型与分发层一致。
// 若某个包改了返回类型，这里会直接编译失败，而不是等到运行时才暴露。
var (
	_ func(httptool.SendParams) (*httptool.SendResult, *apperr.Error) = httptool.Send
	_ func(grpctool.DiscoveryParams) (any, *apperr.Error)             = grpctool.Discover
	_ func(grpctool.MethodsParams) (any, *apperr.Error)               = grpctool.DescribeMethods
	_ func(grpctool.InvokeParams) (any, *apperr.Error)                = grpctool.Invoke
)

// Handler 持有分发所需的依赖。
type Handler struct {
	id      string
	version string
}

// New 构造分发器。id 与 version 必须与 manifest.json 一致，
// 否则宿主会以 "Sidecar identity does not match manifest" 拒绝启动。
func New(id, version string) *Handler {
	return &Handler{id: id, version: version}
}

// Dispatch 把一次调用路由到具体实现。
func (h *Handler) Dispatch(method string, params json.RawMessage) (any, *apperr.Error) {
	switch method {
	case MethodPing:
		return &PingResult{OK: true, ID: h.id, Version: h.version, Methods: Methods()}, nil

	case MethodHTTPSend:
		var request httptool.SendParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return httptool.Send(request)

	case MethodGRPCServices:
		var request grpctool.DiscoveryParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return grpctool.Discover(request)

	case MethodGRPCMethods:
		var request grpctool.MethodsParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return grpctool.DescribeMethods(request)

	case MethodGRPCInvoke:
		var request grpctool.InvokeParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return grpctool.Invoke(request)

	default:
		return nil, apperr.MethodNotFound(method)
	}
}

// decode 把 params 解成强类型结构。
//
// 空 params 合法（调用方可以不传），此时保持零值；解析失败一律视为参数错误，
// 而不是让业务函数拿到半截数据。
func decode(raw json.RawMessage, target any) *apperr.Error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return apperr.Invalid("Invalid request parameters: %s", err.Error())
	}
	return nil
}

// Package apperr 定义插件统一错误码与错误构造。
//
// 分成两层概念：
//   - Code：JSON-RPC 错误码，决定宿主与前端如何对待这次失败（参数问题还是执行问题）。
//   - Kind：业务分类，用于前端挑选排错提示。前端通过 error.data.kind 读取。
//
// 过去这两个信息散落在各个文件里手写数字字面量和字符串，新增方法时容易写错，
// 所以集中到这一处定义。
package apperr

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Code 是 JSON-RPC 错误码。
type Code int

const (
	// CodeInvalidParams 表示调用方传参非法，例如缺少 URL、method 名不合规。
	// 对应 JSON-RPC 规范的 -32602。
	CodeInvalidParams Code = -32602

	// CodeInternal 表示插件内部异常（panic、编码失败等）。对应 -32603。
	CodeInternal Code = -32603

	// CodeUpstream 表示网络、上游服务、TLS 等执行期失败。对应 -32000。
	CodeUpstream Code = -32000

	// CodeMethodNotFound 表示方法未实现。对应 -32601。
	CodeMethodNotFound Code = -32601
)

// Kind 是给前端用的业务分类，前端据此展示对应的排错提示文案。
type Kind string

const (
	KindInvalid    Kind = "invalid"
	KindNetwork    Kind = "network"
	KindConnection Kind = "connection"
	KindTimeout    Kind = "timeout"
	KindDNS        Kind = "dns"
	KindTLS        Kind = "tls"
	KindAuth       Kind = "auth"
	KindReflection Kind = "reflection"
	KindProto      Kind = "proto"
	KindCanceled   Kind = "canceled"
	KindInternal   Kind = "internal"
)

// ErrCanceled 是用户主动终止请求时写入 context 的取消原因。
//
// 只靠 context.Canceled 无法区分"用户点了终止"和"内部取消/超时"，
// 因此用一个独立哨兵值作为 cancel cause，让网络层能给出准确错误分类。
var ErrCanceled = errors.New("request canceled by user")

// Error 是插件唯一的错误类型。
//
// 额外实现了 error 接口，便于在内部包之间用普通 error 传递，再由边界转换。
type Error struct {
	Code    Code
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return e.Message }

// MarshalJSON 输出符合 JSON-RPC 2.0 的 error 结构。
func (e *Error) MarshalJSON() ([]byte, error) {
	type payload struct {
		Code    Code           `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data,omitempty"`
	}
	data := map[string]any{}
	if e.Kind != "" {
		data["kind"] = string(e.Kind)
	}
	return json.Marshal(payload{Code: e.Code, Message: e.Message, Data: data})
}

// Data 返回 error.data 载荷，便于 Sidecar SDK 序列化。
func (e *Error) Data() map[string]any {
	if e.Kind == "" {
		return nil
	}
	return map[string]any{"kind": string(e.Kind)}
}

// Invalid 构造参数错误。
func Invalid(format string, args ...any) *Error {
	return &Error{Code: CodeInvalidParams, Kind: KindInvalid, Message: fmt.Sprintf(format, args...)}
}

// Proto 构造导入的 .proto 文件编译失败错误。
//
// 它不是网络问题，但仍属于调用方输入（文件内容）的问题，所以沿用 -32602；
// 单独的 Kind 让前端能给出"检查 import 路径/语法"的排错提示。
func Proto(format string, args ...any) *Error {
	return &Error{Code: CodeInvalidParams, Kind: KindProto, Message: fmt.Sprintf(format, args...)}
}

// Upstream 构造上游执行错误。
func Upstream(kind Kind, format string, args ...any) *Error {
	return &Error{Code: CodeUpstream, Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Canceled 构造用户主动终止请求的错误。
//
// 它与超时同属 -32000，但 Kind 不同：前端据此显示"已终止"而不是"请求失败"，
// 也不该提示用户去检查网络或服务器。
func Canceled() *Error {
	return &Error{Code: CodeUpstream, Kind: KindCanceled, Message: "Request canceled"}
}

// Internal 构造内部错误。
func Internal(format string, args ...any) *Error {
	return &Error{Code: CodeInternal, Kind: KindInternal, Message: fmt.Sprintf(format, args...)}
}

// MethodNotFound 构造方法未实现错误。
func MethodNotFound(method string) *Error {
	return &Error{
		Code:    CodeMethodNotFound,
		Kind:    KindInvalid,
		Message: "Method not found: " + method,
	}
}

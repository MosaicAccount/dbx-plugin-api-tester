// Package rpc 是 JSON-RPC 分发层。
//
// 它只负责三件事：路由到对应处理器、把 params 解码成强类型结构、把业务错误
// 统一成 apperr.Error。业务实现放在 httptool / grpctool 包里，这一层不含网络逻辑。
//
// 方法名集中声明在 Methods 里，前端调用与这里的字符串必须一致；新增方法时先加进
// Methods，再补 handler，避免出现"实现了但没登记"或"登记了但没实现"的漂移。
package rpc

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/grpctool"
	"github.com/jlian/dbx-plugin-api-tester/internal/httptool"
	"github.com/jlian/dbx-plugin-api-tester/internal/inflight"
	"github.com/jlian/dbx-plugin-api-tester/internal/uploads"
)

// 全部对外方法名。前端 src/lib/store.js 里 invoke 的第一个参数与之对应。
const (
	MethodPing          = "api/ping"
	MethodHTTPSend      = "http/send"
	MethodUploadBegin   = "http/upload/begin"
	MethodUploadChunk   = "http/upload/chunk"
	MethodUploadAbort   = "http/upload/abort"
	MethodRequestCancel = "request/cancel"

	// 流式下载：响应体大小未知，Start 只等响应头，正文由 Read 逐块取。
	// 前端拿到句柄后边收边写本地文件，因此这三条 RPC 都不受单次响应体上限约束。
	MethodHTTPDownloadStart  = "http/download/start"
	MethodHTTPDownloadRead   = "http/download/read"
	MethodHTTPDownloadCancel = "http/download/cancel"
	MethodGRPCServices       = "grpc/services"
	MethodGRPCMethods        = "grpc/methods"
	MethodGRPCInvoke         = "grpc/invoke"

	MethodGRPCStreamOpen      = "grpc/stream/open"
	MethodGRPCStreamSend      = "grpc/stream/send"
	MethodGRPCStreamWait      = "grpc/stream/wait"
	MethodGRPCStreamHalfClose = "grpc/stream/halfClose"
	MethodGRPCStreamClose     = "grpc/stream/close"
)

// Methods 返回已实现的方法列表，供 ping 自检与文档使用。
func Methods() []string {
	return []string{
		MethodHTTPSend,
		MethodUploadBegin,
		MethodUploadChunk,
		MethodUploadAbort,
		MethodRequestCancel,
		MethodHTTPDownloadStart,
		MethodHTTPDownloadRead,
		MethodHTTPDownloadCancel,
		MethodGRPCServices,
		MethodGRPCMethods,
		MethodGRPCInvoke,
		MethodGRPCStreamOpen,
		MethodGRPCStreamSend,
		MethodGRPCStreamWait,
		MethodGRPCStreamHalfClose,
		MethodGRPCStreamClose,
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
	_ func(context.Context, httptool.SendParams, httptool.UploadResolver) (*httptool.SendResult, *apperr.Error) = httptool.SendWithUploads
	_ func(context.Context, grpctool.DiscoveryParams) (any, *apperr.Error)                                      = grpctool.Discover
	_ func(context.Context, grpctool.MethodsParams) (any, *apperr.Error)                                        = grpctool.DescribeMethods
	_ func(context.Context, grpctool.InvokeParams) (any, *apperr.Error)                                         = grpctool.Invoke
)

// CancelResult 是 request/cancel 的返回结构。
type CancelResult struct {
	Canceled bool `json:"canceled"`
}

// UploadBeginResult 返回后续分块使用的 uploadId。
type UploadBeginResult struct {
	UploadID string `json:"uploadId"`
}

// UploadChunkResult 返回文件当前累计字节数，前端据此显示进度。
type UploadChunkResult struct {
	Size int64 `json:"size"`
}

// UploadAbortResult 表示暂存文件是否被删除。
type UploadAbortResult struct {
	Aborted bool `json:"aborted"`
}

// Handler 持有分发所需的依赖。
type Handler struct {
	id        string
	version   string
	inflight  *inflight.Registry
	streams   *grpctool.StreamManager
	uploads   *uploads.Store
	downloads *httptool.DownloadManager
}

// New 构造分发器。id 与 version 必须与 manifest.json 一致，
// 否则宿主会以 "Sidecar identity does not match manifest" 拒绝启动。
func New(id, version string) *Handler {
	uploadStore := uploads.NewStore()
	return &Handler{
		id:        id,
		version:   version,
		inflight:  inflight.New(),
		streams:   grpctool.NewStreamManager(),
		uploads:   uploadStore,
		downloads: httptool.NewDownloadManager(uploadStore),
	}
}

// Close 释放会话与回收协程。进程退出时由内核回收；测试和热重载应显式调用。
func (h *Handler) Close() {
	if h.streams != nil {
		h.streams.CloseAll()
	}
	if h.uploads != nil {
		h.uploads.Close()
	}
	if h.downloads != nil {
		h.downloads.CloseAll()
	}
}

// Dispatch 把一次调用路由到具体实现。
func (h *Handler) Dispatch(method string, params json.RawMessage) (any, *apperr.Error) {
	switch method {
	case MethodPing:
		return &PingResult{OK: true, ID: h.id, Version: h.version, Methods: Methods()}, nil

	case MethodHTTPSend:
		var request struct {
			httptool.SendParams
			RequestID string `json:"requestId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		ctx, release, failure := h.begin(request.RequestID)
		if failure != nil {
			return nil, failure
		}
		defer release()
		return httptool.SendWithUploads(ctx, request.SendParams, h.uploads)

	case MethodUploadBegin:
		var request struct {
			FileName string `json:"fileName"`
			Size     int64  `json:"size"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		uploadID, failure := h.uploads.Begin(request.FileName, request.Size)
		if failure != nil {
			return nil, failure
		}
		return &UploadBeginResult{UploadID: uploadID}, nil

	case MethodUploadChunk:
		var request struct {
			UploadID   string `json:"uploadId"`
			DataBase64 string `json:"dataBase64"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		size, failure := h.uploads.Append(request.UploadID, request.DataBase64)
		if failure != nil {
			return nil, failure
		}
		return &UploadChunkResult{Size: size}, nil

	case MethodUploadAbort:
		var request struct {
			UploadID string `json:"uploadId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return &UploadAbortResult{Aborted: h.uploads.Abort(request.UploadID)}, nil

	case MethodHTTPDownloadStart:
		var request struct {
			httptool.SendParams
			DownloadID string `json:"downloadId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return h.downloads.Start(request.SendParams, request.DownloadID)

	case MethodHTTPDownloadRead:
		var request httptool.DownloadReadParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return h.downloads.Read(request)

	case MethodHTTPDownloadCancel:
		var request struct {
			DownloadID string `json:"downloadId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(request.DownloadID)
		if id == "" {
			return nil, apperr.Invalid("downloadId is required")
		}
		return &CancelResult{Canceled: h.downloads.Cancel(id)}, nil

	case MethodRequestCancel:
		var request struct {
			RequestID string `json:"requestId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(request.RequestID)
		if id == "" {
			return nil, apperr.Invalid("requestId is required")
		}
		return &CancelResult{Canceled: h.inflight.Cancel(id)}, nil

	case MethodGRPCServices:
		var request struct {
			grpctool.DiscoveryParams
			RequestID string `json:"requestId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		ctx, release, failure := h.begin(request.RequestID)
		if failure != nil {
			return nil, failure
		}
		defer release()
		return grpctool.Discover(ctx, request.DiscoveryParams)

	case MethodGRPCMethods:
		var request struct {
			grpctool.MethodsParams
			RequestID string `json:"requestId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		ctx, release, failure := h.begin(request.RequestID)
		if failure != nil {
			return nil, failure
		}
		defer release()
		return grpctool.DescribeMethods(ctx, request.MethodsParams)

	case MethodGRPCInvoke:
		var request struct {
			grpctool.InvokeParams
			RequestID string `json:"requestId"`
		}
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		ctx, release, failure := h.begin(request.RequestID)
		if failure != nil {
			return nil, failure
		}
		defer release()
		return grpctool.Invoke(ctx, request.InvokeParams)

	case MethodGRPCStreamOpen:
		var request grpctool.OpenStreamParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return h.streams.Open(context.Background(), request)

	case MethodGRPCStreamSend:
		var request grpctool.SendStreamParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return h.streams.Send(request)

	case MethodGRPCStreamWait:
		var request grpctool.WaitStreamParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return h.streams.Wait(request)

	case MethodGRPCStreamHalfClose:
		var request grpctool.HalfCloseStreamParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		return h.streams.HalfClose(request)

	case MethodGRPCStreamClose:
		var request grpctool.CloseStreamParams
		if err := decode(params, &request); err != nil {
			return nil, err
		}
		if err := h.streams.Close(request); err != nil {
			return nil, err
		}
		return struct{}{}, nil

	default:
		return nil, apperr.MethodNotFound(method)
	}
}

// begin 为一次可取消调用建立上下文。requestId 为空时退化为不可取消，
// 旧版前端不传这个字段也能继续工作。
func (h *Handler) begin(requestID string) (context.Context, func(), *apperr.Error) {
	return h.inflight.Begin(strings.TrimSpace(requestID))
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

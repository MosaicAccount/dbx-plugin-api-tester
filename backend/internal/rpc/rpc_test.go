package rpc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"github.com/jlian/dbx-plugin-api-tester/internal/httptool"
)

func TestPingReportsIdentityAndMethods(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")

	result, err := handler.Dispatch(MethodPing, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ping, ok := result.(*PingResult)
	if !ok {
		t.Fatalf("unexpected result type %T", result)
	}
	if !ping.OK || ping.ID != "com.jlian.apitester" || ping.Version != "0.0.1" {
		t.Fatalf("unexpected ping payload: %+v", ping)
	}
	if len(ping.Methods) != 13 {
		t.Fatalf("expected 13 methods, got %v", ping.Methods)
	}
}

// 未知方法必须返回 method-not-found，而不是内部错误：
// 前端与宿主靠这个码区分"调用写错了"和"插件崩了"。
func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")

	_, err := handler.Dispatch("not/a/method", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if err.Code != apperr.CodeMethodNotFound {
		t.Fatalf("expected method-not-found code, got %d", err.Code)
	}
}

// 参数解不出来时必须是 invalid-params，且不能把坏数据交给业务层。
func TestMalformedParamsReturnsInvalidParams(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")

	for _, method := range []string{
		MethodHTTPSend,
		MethodGRPCServices,
		MethodGRPCMethods,
		MethodGRPCInvoke,
		MethodRequestCancel,
		MethodGRPCStreamOpen,
		MethodGRPCStreamSend,
		MethodGRPCStreamWait,
		MethodGRPCStreamHalfClose,
		MethodGRPCStreamClose,
	} {
		_, err := handler.Dispatch(method, json.RawMessage(`{"timeoutMs":"not-a-number"}`))
		if err == nil {
			t.Fatalf("%s: expected an error", method)
		}
		if err.Code != apperr.CodeInvalidParams {
			t.Fatalf("%s: expected invalid-params code, got %d", method, err.Code)
		}
	}
}

// 空 params 与 null 都应当被接受（调用方可以不传），由业务层决定缺什么算错。
func TestEmptyParamsIsAccepted(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")

	for _, raw := range []json.RawMessage{nil, json.RawMessage(``), json.RawMessage(`null`)} {
		_, err := handler.Dispatch(MethodHTTPSend, raw)
		if err == nil {
			t.Fatal("expected the handler to report a missing URL")
		}
		if err.Code != apperr.CodeInvalidParams {
			t.Fatalf("expected invalid-params for missing URL, got %d (%s)", err.Code, err.Message)
		}
	}
}

// Methods() 声明的每个方法都必须真的能路由到实现。
// 这条测试防止"登记了但没实现"的漂移。
func TestDeclaredMethodsAreImplemented(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	for _, method := range Methods() {
		_, err := handler.Dispatch(method, json.RawMessage(`{}`))
		if err != nil && err.Code == apperr.CodeMethodNotFound {
			t.Fatalf("%s is declared but not implemented", method)
		}
	}
}

// 完整的分块暂存链路：begin → chunk → http/send 引用 uploadId → 发送后仍可重发。
func TestStagedUploadFlow(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	begin, beginError := handler.Dispatch(MethodUploadBegin, json.RawMessage(`{"fileName":"note.txt","size":5}`))
	if beginError != nil {
		t.Fatalf("begin failed: %v", beginError)
	}
	uploadID := begin.(*UploadBeginResult).UploadID
	if uploadID == "" {
		t.Fatal("expected an upload id")
	}

	chunk, chunkError := handler.Dispatch(MethodUploadChunk, json.RawMessage(
		`{"uploadId":"`+uploadID+`","dataBase64":"aGVsbG8="}`,
	))
	if chunkError != nil {
		t.Fatalf("chunk failed: %v", chunkError)
	}
	if chunk.(*UploadChunkResult).Size != 5 {
		t.Fatalf("unexpected size: %+v", chunk)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("cannot read multipart body: %v", err)
			return
		}
		part, partError := reader.NextPart()
		if partError != nil {
			t.Errorf("cannot read multipart part: %v", partError)
			return
		}
		data, _ := io.ReadAll(part)
		_, _ = writer.Write([]byte(part.FormName() + ":" + part.FileName() + "=" + string(data)))
	}))
	defer server.Close()

	params, marshalError := json.Marshal(map[string]any{
		"method":       "POST",
		"url":          server.URL,
		"bodyEncoding": "multipart",
		"multipart": map[string]any{
			"files": []map[string]any{{
				"name":     "upload",
				"fileName": "note.txt",
				"uploadId": uploadID,
			}},
		},
	})
	if marshalError != nil {
		t.Fatalf("cannot encode params: %v", marshalError)
	}
	result, sendError := handler.Dispatch(MethodHTTPSend, params)
	if sendError != nil {
		t.Fatalf("send failed: %v", sendError)
	}
	if result.(*httptool.SendResult).BodyText != "upload:note.txt=hello" {
		t.Fatalf("unexpected echo: %q", result.(*httptool.SendResult).BodyText)
	}
	// 发送只读取暂存文件；同一个 uploadId 必须能继续复用（用户改完 URL/header 再发）。
	if _, statError := handler.uploads.Stat(uploadID); statError != nil {
		t.Fatalf("expected the staged upload to stay available after sending: %v", statError)
	}
	again, againError := handler.Dispatch(MethodHTTPSend, params)
	if againError != nil {
		t.Fatalf("second send failed: %v", againError)
	}
	if again.(*httptool.SendResult).BodyText != "upload:note.txt=hello" {
		t.Fatalf("unexpected second echo: %q", again.(*httptool.SendResult).BodyText)
	}
}

// 中止暂存文件应当幂等：重复 abort 返回 false，而不是报错。
func TestUploadAbortIsIdempotent(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	begin, beginError := handler.Dispatch(MethodUploadBegin, json.RawMessage(`{"fileName":"note.txt","size":0}`))
	if beginError != nil {
		t.Fatalf("begin failed: %v", beginError)
	}
	uploadID := begin.(*UploadBeginResult).UploadID
	for index, expected := range []bool{true, false} {
		raw, _ := json.Marshal(map[string]string{"uploadId": uploadID})
		result, err := handler.Dispatch(MethodUploadAbort, raw)
		if err != nil {
			t.Fatalf("abort %d failed: %v", index, err)
		}
		if result.(*UploadAbortResult).Aborted != expected {
			t.Fatalf("abort %d: unexpected result %+v", index, result)
		}
	}
}

func TestCancelUnknownRequestIsNotAnError(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")

	result, err := handler.Dispatch(MethodRequestCancel, json.RawMessage(`{"requestId":"missing"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	canceled, ok := result.(*CancelResult)
	if !ok || canceled.Canceled {
		t.Fatalf("expected canceled=false for an unknown request, got %#v", result)
	}
}

func TestCancelRequiresRequestID(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")

	_, err := handler.Dispatch(MethodRequestCancel, json.RawMessage(`{"requestId":"  "}`))
	if err == nil || err.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected invalid-params, got %#v", err)
	}
}

// 请求正在阻塞时，第二条 RPC（request/cancel）必须能立刻打断它：
// 这正是"发送后可以点终止"的完整链路，不能只在 httptool 层验证。
func TestCancelStopsInFlightRequest(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-release
		_, _ = writer.Write([]byte("late"))
	}))
	defer server.Close()
	defer close(release)

	handler := New("com.jlian.apitester", "0.0.1")
	params, marshalError := json.Marshal(map[string]any{
		"method":    "GET",
		"url":       server.URL,
		"timeoutMs": 5000,
		"requestId": "req-1",
	})
	if marshalError != nil {
		t.Fatalf("cannot marshal params: %v", marshalError)
	}

	type outcome struct {
		result any
		err    *apperr.Error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := handler.Dispatch(MethodHTTPSend, params)
		done <- outcome{result: result, err: err}
	}()

	// 取消可能比 send 的登记更早到达（会被登记表记为预取消），所以这里
	// 一直重试到真正命中，或者 send 已经返回。
	deadline := time.Now().Add(2 * time.Second)
	for {
		result, err := handler.Dispatch(MethodRequestCancel, json.RawMessage(`{"requestId":"req-1"}`))
		if err != nil {
			t.Fatalf("unexpected cancel error: %v", err)
		}
		if result.(*CancelResult).Canceled {
			break
		}
		select {
		case out := <-done:
			assertCanceledOutcome(t, out.result, out.err)
			return
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the request was never registered for cancellation")
		}
	}

	select {
	case out := <-done:
		assertCanceledOutcome(t, out.result, out.err)
	case <-time.After(3 * time.Second):
		t.Fatal("http/send did not return after request/cancel")
	}
}

func assertCanceledOutcome(t *testing.T, result any, err *apperr.Error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a canceled error, got result %#v", result)
	}
	if err.Kind != apperr.KindCanceled {
		t.Fatalf("expected canceled kind, got %q (%s)", err.Kind, err.Message)
	}
}

// 会话方法在没有 streamId 时必须给出参数错误；close 是例外——幂等，未知 id 也成功。
func TestStreamMethodsRequireStreamID(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	for _, method := range []string{MethodGRPCStreamSend, MethodGRPCStreamWait, MethodGRPCStreamHalfClose} {
		_, err := handler.Dispatch(method, json.RawMessage(`{}`))
		if err == nil || err.Code != apperr.CodeInvalidParams {
			t.Fatalf("%s: expected invalid-params for a missing streamId, got %#v", method, err)
		}
	}

	result, err := handler.Dispatch(MethodGRPCStreamClose, json.RawMessage(`{"streamId":"missing"}`))
	if err != nil {
		t.Fatalf("close of an unknown stream must be idempotent: %v", err)
	}
	if result == nil {
		t.Fatal("close should return an empty result")
	}
}

// open 缺服务/方法时要在建连接之前拒绝。
func TestStreamOpenRequiresMethod(t *testing.T) {
	handler := New("com.jlian.apitester", "0.0.1")
	defer handler.Close()

	_, err := handler.Dispatch(MethodGRPCStreamOpen, json.RawMessage(`{}`))
	if err == nil || err.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected invalid-params for a missing method, got %#v", err)
	}
}

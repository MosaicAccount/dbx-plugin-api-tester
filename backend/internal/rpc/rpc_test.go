package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
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
	if len(ping.Methods) != 5 {
		t.Fatalf("expected 5 methods, got %v", ping.Methods)
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

	for _, method := range []string{MethodHTTPSend, MethodGRPCServices, MethodGRPCMethods, MethodGRPCInvoke, MethodRequestCancel} {
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

	for _, method := range Methods() {
		_, err := handler.Dispatch(method, json.RawMessage(`{}`))
		if err != nil && err.Code == apperr.CodeMethodNotFound {
			t.Fatalf("%s is declared but not implemented", method)
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

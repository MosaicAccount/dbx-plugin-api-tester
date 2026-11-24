package rpc

import (
	"encoding/json"
	"testing"

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
	if len(ping.Methods) != 4 {
		t.Fatalf("expected 4 methods, got %v", ping.Methods)
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

	for _, method := range []string{MethodHTTPSend, MethodGRPCServices, MethodGRPCMethods, MethodGRPCInvoke} {
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

package apperr

import (
	"encoding/json"
	"strings"
	"testing"
)

// 错误码是宿主与前端的契约，改动会被下游依赖，这里固定住。
func TestCodesMatchJSONRPC(t *testing.T) {
	cases := map[Code]int{
		CodeInvalidParams:  -32602,
		CodeInternal:       -32603,
		CodeUpstream:       -32000,
		CodeMethodNotFound: -32601,
	}
	for code, expected := range cases {
		if int(code) != expected {
			t.Fatalf("code %d should be %d", code, expected)
		}
	}
}

func TestInvalidCarriesKind(t *testing.T) {
	err := Invalid("bad %s", "input")
	if err.Code != CodeInvalidParams {
		t.Fatalf("unexpected code %d", err.Code)
	}
	if err.Kind != KindInvalid {
		t.Fatalf("unexpected kind %q", err.Kind)
	}
	if err.Message != "bad input" {
		t.Fatalf("unexpected message %q", err.Message)
	}
}

// 前端靠 error.data.kind 选择排错提示，序列化形状必须稳定。
func TestMarshalProducesKindInData(t *testing.T) {
	err := Upstream(KindTimeout, "Request timed out")

	encoded, marshalError := json.Marshal(err)
	if marshalError != nil {
		t.Fatalf("unexpected marshal error: %v", marshalError)
	}

	var payload struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if unmarshalError := json.Unmarshal(encoded, &payload); unmarshalError != nil {
		t.Fatalf("unexpected unmarshal error: %v", unmarshalError)
	}

	if payload.Code != int(CodeUpstream) {
		t.Fatalf("unexpected code %d", payload.Code)
	}
	if payload.Data["kind"] != string(KindTimeout) {
		t.Fatalf("unexpected data kind %v", payload.Data["kind"])
	}
}

// 没有 Kind 时不应输出空的 data 字段，避免前端读到空分类。
func TestMarshalOmitsEmptyData(t *testing.T) {
	encoded, err := json.Marshal(&Error{Code: CodeInternal, Message: "boom"})
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	if strings.Contains(string(encoded), `"data"`) {
		t.Fatalf("expected data to be omitted, got %s", encoded)
	}
}

func TestMethodNotFoundMessageIncludesMethod(t *testing.T) {
	err := MethodNotFound("foo/bar")
	if err.Code != CodeMethodNotFound {
		t.Fatalf("unexpected code %d", err.Code)
	}
	if !strings.Contains(err.Message, "foo/bar") {
		t.Fatalf("message should name the method, got %q", err.Message)
	}
}

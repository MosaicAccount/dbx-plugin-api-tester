package grpctool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func openTestSession(t *testing.T, manager *StreamManager, method string) *OpenStreamResult {
	t.Helper()
	target := startStreamingServer(t)
	result, pluginError := manager.Open(context.Background(), OpenStreamParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       method,
	})
	if pluginError != nil {
		t.Fatalf("open %s: %v", method, pluginError)
	}
	t.Cleanup(func() {
		_ = manager.Close(CloseStreamParams{StreamID: result.StreamID})
	})
	return result
}

func sessionOf(t *testing.T, manager *StreamManager, id string) *streamSession {
	t.Helper()
	session, pluginError := manager.get(id)
	if pluginError != nil {
		t.Fatalf("session %s: %v", id, pluginError)
	}
	return session
}

// collectUntilEnded 把剩余消息收齐，直到 ended 或超时。测试里用短等待，避免
// 每条用例都等 5 秒。
func collectUntilEnded(t *testing.T, session *streamSession, initial *WaitStreamResult) (*WaitStreamResult, []StreamMessage) {
	t.Helper()
	messages := append([]StreamMessage(nil), initial.Messages...)
	result := initial
	deadline := time.Now().Add(3 * time.Second)
	for !result.Ended && time.Now().Before(deadline) {
		result = session.wait(100 * time.Millisecond)
		messages = append(messages, result.Messages...)
	}
	if !result.Ended {
		t.Fatalf("stream did not end, status=%+v timedOut=%v", result.Status, result.TimedOut)
	}
	return result, messages
}

func TestStreamSessionClientStreaming(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "StreamingInputCall")
	if opened.Kind != "client" {
		t.Fatalf("expected client stream, got %q", opened.Kind)
	}

	for _, size := range []int{3, 3, 3} {
		result, pluginError := manager.Send(SendStreamParams{StreamID: opened.StreamID, Message: payloadMessage(size)})
		if pluginError != nil {
			t.Fatalf("send: %v", pluginError)
		}
		if result.OutSeq == 0 {
			t.Fatal("send must return a positive outSeq")
		}
	}

	half, pluginError := manager.HalfClose(HalfCloseStreamParams{StreamID: opened.StreamID, WaitMs: 5000})
	if pluginError != nil {
		t.Fatalf("halfClose: %v", pluginError)
	}
	final, messages := collectUntilEnded(t, sessionOf(t, manager, opened.StreamID), half)
	if len(messages) != 1 {
		t.Fatalf("client streaming must return exactly one response, got %d: %v", len(messages), messages)
	}
	if final.Status == nil || final.Status.Code != 0 {
		t.Fatalf("expected OK status, got %+v", final.Status)
	}

	var payload struct {
		AggregatedPayloadSize int `json:"aggregatedPayloadSize"`
	}
	if err := json.Unmarshal([]byte(messages[0].JSON), &payload); err != nil {
		t.Fatalf("invalid response JSON %q: %v", messages[0].JSON, err)
	}
	if payload.AggregatedPayloadSize != 9 {
		t.Fatalf("expected 9 aggregated bytes, got %d", payload.AggregatedPayloadSize)
	}
	if messages[0].Seq != 1 {
		t.Fatalf("first inbound message must have seq 1, got %d", messages[0].Seq)
	}
}

// 服务端流：请求随建流发出，之后只收不发；长推送不再受单次 invoke 的超时约束。
func TestStreamSessionServerStreaming(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	target := startStreamingServer(t)
	result, pluginError := manager.Open(context.Background(), OpenStreamParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "StreamingOutputCall",
		Message:      json.RawMessage(`{"response_parameters":[{"size":3},{"size":3},{"size":3}]}`),
	})
	if pluginError != nil {
		t.Fatalf("open: %v", pluginError)
	}
	if result.Kind != "server" {
		t.Fatalf("expected server stream, got %q", result.Kind)
	}

	final, messages := collectUntilEnded(t, sessionOf(t, manager, result.StreamID), &WaitStreamResult{})
	if len(messages) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(messages))
	}
	if final.Status == nil || final.Status.Code != 0 {
		t.Fatalf("expected OK status, got %+v", final.Status)
	}

	// 服务端流不接受额外消息，也不允许再次半关闭。
	if _, sendErr := manager.Send(SendStreamParams{StreamID: result.StreamID, Message: json.RawMessage(`{}`)}); sendErr == nil {
		t.Fatal("server-streaming methods must reject extra messages")
	}
	if _, halfErr := manager.HalfClose(HalfCloseStreamParams{StreamID: result.StreamID, WaitMs: 5000}); halfErr == nil {
		t.Fatal("server-streaming methods are half-closed when the stream opens")
	}
}

func TestStreamSessionBidiEcho(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "FullDuplexCall")
	if opened.Kind != "bidi" {
		t.Fatalf("expected bidi stream, got %q", opened.Kind)
	}

	for index, size := range []int{3, 5} {
		sendResult, pluginError := manager.Send(SendStreamParams{StreamID: opened.StreamID, Message: payloadMessage(size)})
		if pluginError != nil {
			t.Fatalf("send #%d: %v", index+1, pluginError)
		}
		if sendResult.OutSeq != index+1 {
			t.Fatalf("expected outSeq %d, got %d", index+1, sendResult.OutSeq)
		}

		waitResult := sessionOf(t, manager, opened.StreamID).wait(2 * time.Second)
		if len(waitResult.Messages) != 1 {
			t.Fatalf("send #%d: expected one echo, got %d", index+1, len(waitResult.Messages))
		}
		if !strings.Contains(waitResult.Messages[0].JSON, "body") {
			t.Fatalf("echo lost its payload: %q", waitResult.Messages[0].JSON)
		}
		if waitResult.Messages[0].Seq != index+1 {
			t.Fatalf("expected inbound seq %d, got %d", index+1, waitResult.Messages[0].Seq)
		}
	}

	half, pluginError := manager.HalfClose(HalfCloseStreamParams{StreamID: opened.StreamID, WaitMs: 5000})
	if pluginError != nil {
		t.Fatalf("halfClose: %v", pluginError)
	}
	final, _ := collectUntilEnded(t, sessionOf(t, manager, opened.StreamID), half)
	if final.Status == nil || final.Status.Code != 0 {
		t.Fatalf("expected OK status, got %+v", final.Status)
	}
}

func TestStreamSessionWaitTimesOutWithoutClosing(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "FullDuplexCall")
	session := sessionOf(t, manager, opened.StreamID)

	result := session.wait(80 * time.Millisecond)
	if !result.TimedOut || result.Ended {
		t.Fatalf("expected a plain timeout, got %+v", result)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(result.Messages))
	}

	// 超时不能把会话弄死：后续仍然可以发送。
	if _, pluginError := manager.Send(SendStreamParams{StreamID: opened.StreamID, Message: payloadMessage(1)}); pluginError != nil {
		t.Fatalf("send after wait timeout: %v", pluginError)
	}
}

func TestStreamSessionCloseInterruptsWait(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "FullDuplexCall")
	session := sessionOf(t, manager, opened.StreamID)

	done := make(chan *WaitStreamResult, 1)
	go func() {
		done <- session.wait(30 * time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	if pluginError := manager.Close(CloseStreamParams{StreamID: opened.StreamID}); pluginError != nil {
		t.Fatalf("close: %v", pluginError)
	}

	select {
	case result := <-done:
		if !result.Ended {
			t.Fatalf("expected ended after close, got %+v", result)
		}
		if result.Status == nil || result.Status.Code != 1 {
			t.Fatalf("expected canceled status, got %+v", result.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not interrupt the pending wait")
	}
}

func TestStreamSessionCloseIsIdempotent(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "FullDuplexCall")
	for index := 0; index < 2; index++ {
		if pluginError := manager.Close(CloseStreamParams{StreamID: opened.StreamID}); pluginError != nil {
			t.Fatalf("close #%d: %v", index+1, pluginError)
		}
	}
	if pluginError := manager.Close(CloseStreamParams{StreamID: "missing"}); pluginError != nil {
		t.Fatalf("close of an unknown stream must be idempotent: %v", pluginError)
	}
	if _, pluginError := manager.Send(SendStreamParams{StreamID: opened.StreamID, Message: payloadMessage(1)}); pluginError == nil {
		t.Fatal("send after close must fail")
	}
}

func TestStreamSessionRejectsUnary(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	target := startStreamingServer(t)
	_, pluginError := manager.Open(context.Background(), OpenStreamParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "EmptyCall",
	})
	if pluginError == nil {
		t.Fatal("unary methods must be rejected by the stream session")
	}
	if manager.count() != 0 {
		t.Fatalf("a rejected open must not leave a session behind, got %d", manager.count())
	}
}

func TestStreamSessionSendAfterHalfClose(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "StreamingInputCall")
	if _, pluginError := manager.HalfClose(HalfCloseStreamParams{StreamID: opened.StreamID, WaitMs: 5000}); pluginError != nil {
		t.Fatalf("halfClose: %v", pluginError)
	}
	if _, pluginError := manager.Send(SendStreamParams{StreamID: opened.StreamID, Message: payloadMessage(1)}); pluginError == nil {
		t.Fatal("send after half-close must fail")
	}
}

func TestStreamSessionSendRejectsUnknownFields(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "StreamingInputCall")
	if _, pluginError := manager.Send(SendStreamParams{
		StreamID: opened.StreamID,
		Message:  json.RawMessage(`{"notAField":1}`),
	}); pluginError == nil {
		t.Fatal("unknown fields must be rejected instead of silently ignored")
	}
}

func TestStreamSessionValidationRanges(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	target := startStreamingServer(t)
	base := OpenStreamParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "FullDuplexCall",
	}

	for _, mutate := range []func(*OpenStreamParams){
		func(params *OpenStreamParams) { params.IdleTimeoutMs = 1000 },
		func(params *OpenStreamParams) { params.MaxDurationMs = 1000 },
	} {
		params := base
		mutate(&params)
		if _, pluginError := manager.Open(context.Background(), params); pluginError == nil {
			t.Fatalf("out-of-range durations must be rejected: %+v", params)
		}
	}

	if _, pluginError := manager.Wait(WaitStreamParams{StreamID: "missing", WaitMs: 100}); pluginError == nil {
		t.Fatal("waitMs below the minimum must be rejected")
	}
}

func TestStreamSessionLimitsOpenCount(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	for index := 1; index <= maxStreamSessions; index++ {
		openTestSession(t, manager, "FullDuplexCall")
	}
	target := startStreamingServer(t)
	_, pluginError := manager.Open(context.Background(), OpenStreamParams{
		TargetParams: TargetParams{Target: target, TimeoutMs: 5000},
		Service:      streamingService,
		Method:       "FullDuplexCall",
	})
	if pluginError == nil {
		t.Fatal("opening more than the session limit must fail")
	}
}

func TestStreamQueueDropsOldest(t *testing.T) {
	queue := newStreamQueue()
	for index := 0; index < streamQueueMaxMessages+3; index++ {
		queue.push(StreamMessage{Seq: index + 1, JSON: "{}"})
	}
	if dropped := queue.droppedCount(); dropped != 3 {
		t.Fatalf("expected 3 dropped messages, got %d", dropped)
	}
	batch := queue.drain(50, 1<<20)
	if len(batch) != 50 {
		t.Fatalf("expected a bounded batch of 50, got %d", len(batch))
	}
	if batch[0].Seq != 4 {
		t.Fatalf("expected the oldest surviving seq to be 4, got %d", batch[0].Seq)
	}
}

func TestStreamManagerReapsIdleSession(t *testing.T) {
	manager := NewStreamManager()
	defer manager.CloseAll()

	opened := openTestSession(t, manager, "FullDuplexCall")
	manager.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	manager.reap()

	if manager.count() != 0 {
		t.Fatalf("expected the idle session to be reaped, got %d", manager.count())
	}
	if _, pluginError := manager.get(opened.StreamID); pluginError == nil {
		t.Fatal("reaped session must no longer be addressable")
	}
}

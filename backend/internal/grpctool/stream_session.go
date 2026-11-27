// 会话式 gRPC 流（阶段一：拉模式）。
//
// 和 A+ 的一次性调用不同，这里把连接、流、接收队列保存到会话表里，让同一条流
// 活过多次 RPC：界面先 open，之后每次 send 只写一条，wait 从队列取回复，
// halfClose 做半关闭，close 统一回收。
//
// 三条硬约束（改代码前先读一遍）：
//  1. 会话必须持有 ClientConn；流挂在连接上，连接一关流就死。
//  2. open 之后必须一直读；停了读，服务端会卡在发送上，最后双向互锁。
//  3. close 是唯一回收出口，必须幂等；TTL 和进程退出都走它。
package grpctool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	defaultStreamWaitMs = 20000
	minStreamWaitMs     = 5000
	maxStreamWaitMs     = 60000

	streamConnectTimeout = 10 * time.Second

	defaultStreamIdle        = 3 * time.Minute
	minStreamIdle            = 30 * time.Second
	maxStreamIdle            = 15 * time.Minute
	defaultStreamMaxDuration = 30 * time.Minute
	minStreamMaxDuration     = time.Minute
	maxStreamMaxDuration     = 120 * time.Minute

	maxStreamSessions      = 4
	streamQueueMaxMessages = 1024
	streamQueueMaxBytes    = 8 * 1024 * 1024
	streamWaitMaxMessages  = 200
	streamWaitMaxBytes     = 4 * 1024 * 1024

	streamJanitorInterval = 15 * time.Second
	streamEndedGrace      = time.Minute
)

const (
	streamStateOpen       = "open"
	streamStateHalfClosed = "halfClosed"
	streamStateEnded      = "ended"
)

// OpenStreamParams 是 grpc/stream/open 的参数。
//
// 不接收首条消息：首条消息也走 send，避免 sent 下标和 open 的初始消息对不上。
type OpenStreamParams struct {
	TargetParams
	Service string `json:"service"`
	Method  string `json:"method"`
	// Message 只用于服务端流：这类方法一次调用只发一条请求，建流时就随流发出去。
	// 客户端流 / 双向流的首条消息仍然走 send，避免 sent 下标和 open 的初始消息对不上。
	Message json.RawMessage `json:"message"`
	// 可选：用户在「选项」里调整的会话时限，毫秒。0 表示用默认值。
	IdleTimeoutMs int `json:"idleTimeoutMs"`
	MaxDurationMs int `json:"maxDurationMs"`
}

type SendStreamParams struct {
	StreamID string          `json:"streamId"`
	Message  json.RawMessage `json:"message"`
}

type WaitStreamParams struct {
	StreamID string `json:"streamId"`
	WaitMs   int    `json:"waitMs"`
}

type HalfCloseStreamParams struct {
	StreamID string `json:"streamId"`
	WaitMs   int    `json:"waitMs"`
}

type CloseStreamParams struct {
	StreamID string `json:"streamId"`
}

type OpenStreamResult struct {
	StreamID string `json:"streamId"`
	Kind     string `json:"kind"`
}

type SendStreamResult struct {
	OutSeq int `json:"outSeq"`
}

type StreamMessage struct {
	Seq  int    `json:"seq"`
	JSON string `json:"json"`
}

type WaitStreamResult struct {
	Messages []StreamMessage `json:"messages"`
	InSeq    int             `json:"inSeq"`
	Ended    bool            `json:"ended"`
	TimedOut bool            `json:"timedOut"`
	Dropped  int             `json:"dropped"`
	Status   *StatusInfo     `json:"status,omitempty"`
}

// StreamManager 保存所有活跃的会话。
//
// 它由 rpc.Handler 持有，随进程存活；CloseAll 只给测试和进程收尾用。
type StreamManager struct {
	mu       sync.Mutex
	sessions map[string]*streamSession
	now      func() time.Time
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewStreamManager 构造会话表，并启动空闲/超时回收。
func NewStreamManager() *StreamManager {
	manager := &StreamManager{
		sessions: make(map[string]*streamSession),
		now:      time.Now,
		stop:     make(chan struct{}),
	}
	manager.wg.Add(1)
	go manager.janitor()
	return manager
}

// CloseAll 关闭所有会话并停止回收协程。进程退出时不必调用，测试必须调用。
func (m *StreamManager) CloseAll() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.wg.Wait()

	m.mu.Lock()
	sessions := make([]*streamSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*streamSession)
	m.mu.Unlock()

	for _, session := range sessions {
		session.shutdown(StatusInfo{Code: int(codes.Canceled), Name: codes.Canceled.String(), Message: "Plugin stopped"})
	}
}

// Open 建立连接与流，返回 streamId。
//
// 只接受客户端流和双向流；一元与服务端流在阶段一继续走 grpc/invoke。
func (m *StreamManager) Open(ctx context.Context, params OpenStreamParams) (*OpenStreamResult, *apperr.Error) {
	service, methodName, failure := splitServiceMethod(params.Service, params.Method)
	if failure != nil {
		return nil, failure
	}

	idleTimeout, failure := streamDuration(params.IdleTimeoutMs, defaultStreamIdle, minStreamIdle, maxStreamIdle, "idleTimeoutMs")
	if failure != nil {
		return nil, failure
	}
	maxDuration, failure := streamDuration(params.MaxDurationMs, defaultStreamMaxDuration, minStreamMaxDuration, maxStreamMaxDuration, "maxDurationMs")
	if failure != nil {
		return nil, failure
	}

	// 先做本地校验，别为了一个非法参数去建连接。
	if m.count() >= maxStreamSessions {
		return nil, apperr.Invalid("Too many open streams (limit %d); close one first", maxStreamSessions)
	}

	var schema *protoSchema
	var method protoreflect.MethodDescriptor
	if params.UseProtoFiles {
		loaded, pluginError := loadProtoSchema(params.ProtoFiles)
		if pluginError != nil {
			return nil, pluginError
		}
		schema = loaded
		found, err := schema.findMethod(service, methodName)
		if err != nil {
			return nil, apperr.Invalid(err.Error())
		}
		method = found
	}

	connection, pluginError := dialGRPC(params.TargetParams)
	if pluginError != nil {
		return nil, pluginError
	}

	readyCtx, cancelReady := context.WithTimeout(ctx, streamConnectTimeout)
	readyError := waitForReady(readyCtx, connection)
	cancelReady()
	if readyError != nil {
		_ = connection.Close()
		return nil, streamConnectError(readyError)
	}

	if method == nil {
		reflectCtx, cancelReflect, contextError := grpcContext(ctx, params.TimeoutMs, params.Metadata)
		if contextError != nil {
			_ = connection.Close()
			return nil, contextError
		}
		defer cancelReflect()

		registry, err := loadFileRegistry(reflectCtx, connection, service)
		if err != nil {
			_ = connection.Close()
			if pluginError := canceledByUser(reflectCtx); pluginError != nil {
				return nil, pluginError
			}
			return nil, grpcReflectionError(err)
		}
		found, err := findMethod(registry, service, methodName)
		if err != nil {
			_ = connection.Close()
			return nil, apperr.Invalid(err.Error())
		}
		method = found
	}

	if !method.IsStreamingClient() && !method.IsStreamingServer() {
		_ = connection.Close()
		return nil, apperr.Invalid("This method is not a streaming method; use grpc/invoke for unary methods")
	}

	values, err := metadataFromEntries(params.Metadata)
	if err != nil {
		_ = connection.Close()
		return nil, apperr.Invalid(err.Error())
	}

	streamCtx, streamCancel := context.WithCancel(context.Background())
	if len(values) > 0 {
		streamCtx = metadata.NewOutgoingContext(streamCtx, values)
	}

	fullMethod := fmt.Sprintf("/%s/%s", method.Parent().FullName(), method.Name())
	stream, streamError := connection.NewStream(
		streamCtx,
		&grpc.StreamDesc{
			StreamName:    string(method.Name()),
			ClientStreams: method.IsStreamingClient(),
			ServerStreams: method.IsStreamingServer(),
		},
		fullMethod,
		grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
		grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
	)
	if streamError != nil {
		streamCancel()
		_ = connection.Close()
		return nil, grpcSessionError(streamError)
	}

	// 服务端流：请求随建流一次发出并立即半关闭，之后只收不发。
	// 这样一条会持续推送的流就不再受单次 invoke 的超时与条数上限约束。
	if !method.IsStreamingClient() {
		message := params.Message
		if len(bytes.TrimSpace(message)) == 0 {
			message = json.RawMessage("{}")
		}
		request := dynamicpb.NewMessage(method.Input())
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(message, request); err != nil {
			streamCancel()
			_ = connection.Close()
			return nil, apperr.Invalid("Request message does not match %s: %v", method.Input().FullName(), err)
		}
		if err := stream.SendMsg(request); err != nil {
			streamCancel()
			_ = connection.Close()
			return nil, grpcSessionError(err)
		}
		if err := stream.CloseSend(); err != nil {
			streamCancel()
			_ = connection.Close()
			return nil, grpcSessionError(err)
		}
	}

	now := m.now()
	session := &streamSession{
		id:          newStreamID(),
		kind:        streamKind(method),
		conn:        connection,
		stream:      stream,
		method:      method,
		cancel:      streamCancel,
		queue:       newStreamQueue(),
		state:       streamStateOpen,
		started:     now,
		lastActive:  now,
		idleTimeout: idleTimeout,
		maxDuration: maxDuration,
		done:        make(chan struct{}),
	}

	m.mu.Lock()
	if len(m.sessions) >= maxStreamSessions {
		m.mu.Unlock()
		session.shutdown(StatusInfo{Code: int(codes.Canceled), Name: codes.Canceled.String(), Message: "Too many open streams"})
		return nil, apperr.Invalid("Too many open streams (limit %d); close one first", maxStreamSessions)
	}
	m.sessions[session.id] = session
	m.mu.Unlock()

	go session.receive()
	return &OpenStreamResult{StreamID: session.id, Kind: session.kind}, nil
}

// Send 往会话里写一条消息，立即返回出站序号。
func (m *StreamManager) Send(params SendStreamParams) (*SendStreamResult, *apperr.Error) {
	session, failure := m.get(params.StreamID)
	if failure != nil {
		return nil, failure
	}
	return session.send(params.Message)
}

// Wait 从会话队列取消息；没有就挂起等待，超时返回 timedOut。
func (m *StreamManager) Wait(params WaitStreamParams) (*WaitStreamResult, *apperr.Error) {
	session, failure := m.get(params.StreamID)
	if failure != nil {
		return nil, failure
	}
	wait, failure := streamWaitDuration(params.WaitMs)
	if failure != nil {
		return nil, failure
	}
	return session.wait(wait), nil
}

// HalfClose 做半关闭，然后等响应/状态。
func (m *StreamManager) HalfClose(params HalfCloseStreamParams) (*WaitStreamResult, *apperr.Error) {
	session, failure := m.get(params.StreamID)
	if failure != nil {
		return nil, failure
	}
	wait, failure := streamWaitDuration(params.WaitMs)
	if failure != nil {
		return nil, failure
	}
	return session.halfClose(wait)
}

// Close 取消并回收会话。未知 id 返回成功，重复调用也成功。
func (m *StreamManager) Close(params CloseStreamParams) *apperr.Error {
	id := strings.TrimSpace(params.StreamID)
	if id == "" {
		return apperr.Invalid("streamId is required")
	}

	m.mu.Lock()
	session := m.sessions[id]
	if session != nil {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if session == nil {
		return nil
	}
	session.shutdown(StatusInfo{Code: int(codes.Canceled), Name: codes.Canceled.String(), Message: "Stream closed"})
	return nil
}

func (m *StreamManager) get(id string) (*streamSession, *apperr.Error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, apperr.Invalid("streamId is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[id]
	if session == nil {
		return nil, apperr.Invalid("Stream session not found; it may have been closed or expired")
	}
	return session, nil
}

func (m *StreamManager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *StreamManager) janitor() {
	defer m.wg.Done()
	ticker := time.NewTicker(streamJanitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reap()
		case <-m.stop:
			return
		}
	}
}

// reap 回收空闲超时、总时长超时，以及已经结束一段时间的会话。
func (m *StreamManager) reap() {
	now := m.now()

	m.mu.Lock()
	sessions := make([]*streamSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	for _, session := range sessions {
		state, idle, expired, endedAt := session.timing(now)
		switch {
		case state == streamStateEnded && now.Sub(endedAt) > streamEndedGrace:
			m.closeExpired(session.id, "Stream ended")
		case state != streamStateEnded && (idle || expired):
			m.closeExpired(session.id, "Stream expired")
		}
	}
}

func (m *StreamManager) closeExpired(id, reason string) {
	m.mu.Lock()
	session := m.sessions[id]
	if session != nil {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if session != nil {
		session.shutdown(StatusInfo{Code: int(codes.Canceled), Name: codes.Canceled.String(), Message: reason})
	}
}

// ---------------------------------------------------------------------------
// session
// ---------------------------------------------------------------------------

type streamSession struct {
	id     string
	kind   string
	conn   *grpc.ClientConn
	stream grpc.ClientStream
	method protoreflect.MethodDescriptor
	cancel context.CancelFunc
	queue  *streamQueue

	mu      sync.Mutex
	sendMu  sync.Mutex
	waitMu  sync.Mutex
	state   string
	status  StatusInfo
	outSeq  int
	inSeq   int
	started time.Time
	endedAt time.Time

	lastActive  time.Time
	idleTimeout time.Duration
	maxDuration time.Duration

	done         chan struct{}
	shutdownOnce sync.Once
}

func (s *streamSession) receive() {
	for {
		message := dynamicpb.NewMessage(s.method.Output())
		if err := s.stream.RecvMsg(message); err != nil {
			s.finish(err)
			return
		}
		encoded, err := marshalProtoJSON(message)
		if err != nil {
			s.finish(apperr.Internal("Cannot encode a response message: %s", err.Error()))
			return
		}

		s.mu.Lock()
		s.inSeq++
		seq := s.inSeq
		s.mu.Unlock()

		s.queue.push(StreamMessage{Seq: seq, JSON: string(encoded)})
	}
}

func (s *streamSession) send(message json.RawMessage) (*SendStreamResult, *apperr.Error) {
	if s.kind == "server" {
		return nil, apperr.Invalid("This method takes a single request, sent when the stream opens")
	}
	if len(bytes.TrimSpace(message)) == 0 {
		return nil, apperr.Invalid("Message is required")
	}

	request := dynamicpb.NewMessage(s.method.Input())
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(message, request); err != nil {
		return nil, apperr.Invalid("Request message does not match %s: %v", s.method.Input().FullName(), err)
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	switch state {
	case streamStateHalfClosed:
		return nil, apperr.Invalid("Stream is half-closed; no more messages can be sent")
	case streamStateEnded:
		return nil, apperr.Invalid("Stream is already closed")
	}

	if err := s.stream.SendMsg(request); err != nil {
		s.finish(err)
		return nil, grpcSessionError(err)
	}

	s.mu.Lock()
	s.outSeq++
	seq := s.outSeq
	s.lastActive = time.Now()
	s.mu.Unlock()

	return &SendStreamResult{OutSeq: seq}, nil
}

func (s *streamSession) wait(wait time.Duration) *WaitStreamResult {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()

	deadline := time.Now().Add(wait)
	s.touch()

	for {
		result := s.collect()
		if len(result.Messages) > 0 {
			return result
		}
		if result.Ended {
			return result
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			result.TimedOut = true
			return result
		}

		select {
		case <-s.queue.notify:
		case <-s.done:
		case <-time.After(remaining):
		}
	}
}

func (s *streamSession) collect() *WaitStreamResult {
	messages := s.queue.drain(streamWaitMaxMessages, streamWaitMaxBytes)

	s.mu.Lock()
	ended := s.state == streamStateEnded
	inSeq := s.inSeq
	status := s.status
	s.mu.Unlock()

	if ended && !s.queue.isEmpty() {
		ended = false
	}

	result := &WaitStreamResult{
		Messages: messages,
		InSeq:    inSeq,
		Ended:    ended,
		Dropped:  s.queue.droppedCount(),
	}
	if ended {
		result.Status = &status
	}
	return result
}

func (s *streamSession) halfClose(wait time.Duration) (*WaitStreamResult, *apperr.Error) {
	if s.kind == "server" {
		return nil, apperr.Invalid("This method sends its request when the stream opens")
	}

	s.sendMu.Lock()

	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	switch state {
	case streamStateHalfClosed:
		s.sendMu.Unlock()
		return nil, apperr.Invalid("Stream is already half-closed")
	case streamStateEnded:
		s.sendMu.Unlock()
		return nil, apperr.Invalid("Stream is already closed")
	}

	if err := s.stream.CloseSend(); err != nil {
		s.sendMu.Unlock()
		s.finish(err)
		return nil, grpcSessionError(err)
	}

	s.mu.Lock()
	s.state = streamStateHalfClosed
	s.lastActive = time.Now()
	s.mu.Unlock()
	s.sendMu.Unlock()

	return s.wait(wait), nil
}

func (s *streamSession) finish(err error) {
	statusInfo := StatusInfo{Code: 0, Name: "OK"}
	if err != nil && !errors.Is(err, io.EOF) {
		statusInfo = StatusInfoFromError(err)
	}
	s.shutdown(statusInfo)
}

func (s *streamSession) shutdown(statusInfo StatusInfo) {
	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.state = streamStateEnded
		s.status = statusInfo
		s.endedAt = time.Now()
		s.mu.Unlock()

		if s.cancel != nil {
			s.cancel()
		}
		s.queue.close()
		if s.conn != nil {
			_ = s.conn.Close()
		}
		close(s.done)
	})
}

func (s *streamSession) touch() {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

func (s *streamSession) timing(now time.Time) (state string, idle bool, expired bool, endedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, now.Sub(s.lastActive) > s.idleTimeout, now.Sub(s.started) > s.maxDuration, s.endedAt
}

// ---------------------------------------------------------------------------
// queue
// ---------------------------------------------------------------------------

type streamQueue struct {
	mu      sync.Mutex
	items   []StreamMessage
	bytes   int
	dropped int
	closed  bool
	notify  chan struct{}
}

func newStreamQueue() *streamQueue {
	return &streamQueue{notify: make(chan struct{}, 1)}
}

func (q *streamQueue) push(message StreamMessage) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, message)
	q.bytes += len(message.JSON)
	for (len(q.items) > streamQueueMaxMessages || q.bytes > streamQueueMaxBytes) && len(q.items) > 0 {
		q.bytes -= len(q.items[0].JSON)
		q.items = q.items[1:]
		q.dropped++
	}
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *streamQueue) drain(maxMessages, maxBytes int) []StreamMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}

	count := 0
	bytes := 0
	for count < len(q.items) && count < maxMessages {
		size := len(q.items[count].JSON)
		if count > 0 && bytes+size > maxBytes {
			break
		}
		bytes += size
		count++
	}

	out := make([]StreamMessage, count)
	copy(out, q.items[:count])
	q.items = q.items[count:]
	q.bytes -= bytes
	return out
}

func (q *streamQueue) isEmpty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) == 0
}

func (q *streamQueue) droppedCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

func (q *streamQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func splitServiceMethod(service, method string) (string, string, *apperr.Error) {
	serviceName := strings.TrimSpace(service)
	methodName := strings.TrimSpace(method)
	if methodName == "" {
		return "", "", apperr.Invalid("Method name is required")
	}
	if serviceName == "" {
		full := strings.TrimPrefix(methodName, "/")
		index := strings.LastIndex(full, "/")
		if index < 0 {
			return "", "", apperr.Invalid("Service name is required")
		}
		serviceName = strings.ReplaceAll(full[:index], "/", ".")
		methodName = full[index+1:]
	}
	return serviceName, methodName, nil
}

func streamWaitDuration(value int) (time.Duration, *apperr.Error) {
	if value == 0 {
		return time.Duration(defaultStreamWaitMs) * time.Millisecond, nil
	}
	wait := time.Duration(value) * time.Millisecond
	min := time.Duration(minStreamWaitMs) * time.Millisecond
	max := time.Duration(maxStreamWaitMs) * time.Millisecond
	if wait < min || wait > max {
		return 0, apperr.Invalid("waitMs must be between %d and %d milliseconds", minStreamWaitMs, maxStreamWaitMs)
	}
	return wait, nil
}

func streamDuration(value int, fallback, min, max time.Duration, field string) (time.Duration, *apperr.Error) {
	if value == 0 {
		return fallback, nil
	}
	duration := time.Duration(value) * time.Millisecond
	if duration < min || duration > max {
		return 0, apperr.Invalid(
			"%s must be between %d and %d milliseconds",
			field, min.Milliseconds(), max.Milliseconds())
	}
	return duration, nil
}

func waitForReady(ctx context.Context, connection *grpc.ClientConn) error {
	connection.Connect()
	for {
		state := connection.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.Shutdown {
			return errors.New("connection closed")
		}
		if !connection.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

func streamConnectError(err error) *apperr.Error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return apperr.Upstream(apperr.KindTimeout, "Connecting to the gRPC server timed out")
	}
	return apperr.Upstream(apperr.KindConnection, "Cannot connect to the gRPC server: %s", err)
}

func grpcSessionError(err error) *apperr.Error {
	if err == nil {
		return nil
	}

	kind := apperr.KindConnection
	message := err.Error()
	if parsed, ok := status.FromError(err); ok {
		message = parsed.Code().String()
		if parsed.Message() != "" {
			message += ": " + parsed.Message()
		}
		switch parsed.Code() {
		case codes.DeadlineExceeded:
			kind = apperr.KindTimeout
		case codes.Unauthenticated, codes.PermissionDenied:
			kind = apperr.KindAuth
		case codes.Canceled:
			kind = apperr.KindCanceled
		}
	} else if errors.Is(err, context.DeadlineExceeded) {
		kind = apperr.KindTimeout
	}
	return apperr.Upstream(kind, "gRPC stream failed: %s", message)
}

func newStreamID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("s-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

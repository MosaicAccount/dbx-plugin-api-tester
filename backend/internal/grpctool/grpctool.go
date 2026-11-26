package grpctool

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	common "github.com/jlian/dbx-plugin-api-tester/internal/values"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	defaultGRPCTimeoutMs = 20000
	maxGRPCTimeoutMs     = 120000
	maxGRPCMessageBytes  = 8 * 1024 * 1024
	maxReflectionFiles   = 256
	maxStreamMessages    = 100
	maxStreamBytes       = 4 * 1024 * 1024
	maxOutboundMessages  = 100

	// 长连接保活。
	//
	// 一条会话可能长时间没有业务消息（例如只订阅、服务端偶尔才推）。没有流量时，
	// 链路上的 NAT、防火墙、云负载均衡会按空闲超时（常见 60s–350s）把 TCP 悄悄回收，
	// 两端都不知道，用户看到的是"流自己结束了"。周期性 PING 让连接一直有流量，
	// 同时能更快发现对端已经死掉。
	//
	// 参数保持保守：PING 太频繁会被部分服务端以 GOAWAY(too_many_pings) 拒绝。
	// PermitWithoutStream 保持默认的 false——只在有活跃流时探活，避免空闲连接上的
	// PING 触发服务端的最小间隔限制。
	keepalivePingInterval = 30 * time.Second
	keepalivePingTimeout  = 10 * time.Second
)

var metadataKeyPattern = regexp.MustCompile(`^[0-9a-z_.-]+$`)

type TargetParams struct {
	Target        string            `json:"target"`
	UseTLS        bool              `json:"tls"`
	SkipTLSVerify bool              `json:"skipTlsVerify"`
	TimeoutMs     int               `json:"timeoutMs"`
	Metadata      []common.KeyValue `json:"metadata"`
	// UseProtoFiles 为 true 时用导入的 .proto 文件代替 Server Reflection 解析方法。
	UseProtoFiles bool        `json:"useProtoFiles"`
	ProtoFiles    []ProtoFile `json:"protoFiles"`
}

type DiscoveryParams struct {
	TargetParams
}

type MethodsParams struct {
	TargetParams
	Service string `json:"service"`
}

type InvokeParams struct {
	TargetParams
	Service string `json:"service"`
	Method  string `json:"method"`
	// Message 是一元与服务端流唯一的请求消息。
	Message json.RawMessage `json:"message"`
	// Messages 是客户端流与双向流的出站消息序列；非空时优先于 Message。
	Messages []json.RawMessage `json:"messages"`
}

type ServicesResult struct {
	Target    string   `json:"target"`
	Services  []string `json:"services"`
	ElapsedMs int64    `json:"elapsedMs"`
}

type MethodInfo struct {
	Name            string `json:"name"`
	FullName        string `json:"fullName"`
	InputType       string `json:"inputType"`
	OutputType      string `json:"outputType"`
	ClientStreaming bool   `json:"clientStreaming"`
	ServerStreaming bool   `json:"serverStreaming"`
}

type MethodsResult struct {
	Service   string       `json:"service"`
	Methods   []MethodInfo `json:"methods"`
	ElapsedMs int64        `json:"elapsedMs"`
}

type StatusInfo struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type InvokeResult struct {
	OK          bool       `json:"ok"`
	Method      string     `json:"method"`
	Status      StatusInfo `json:"status"`
	MessageJSON string     `json:"messageJson,omitempty"`
	Messages    []string   `json:"messages,omitempty"`
	Streaming   bool       `json:"streaming,omitempty"`
	// StreamKind 取 client / server / bidi，让界面能显示准确的流类型。
	StreamKind string `json:"streamKind,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	// Canceled 表示这次调用是用户主动终止的。为 true 时 Messages 里保留
	// 终止前已经收到的响应，界面可以展示"终止前收到了什么"。
	Canceled  bool              `json:"canceled,omitempty"`
	Metadata  []common.KeyValue `json:"metadata"`
	Trailers  []common.KeyValue `json:"trailers"`
	ElapsedMs int64             `json:"elapsedMs"`
	SizeBytes int               `json:"sizeBytes"`
}

// Discover 列出目标服务端可用的 gRPC 服务。
//
// 导入了 proto 文件时直接从本地描述符里列服务，不再要求服务端开启 Reflection。
func Discover(ctx context.Context, params DiscoveryParams) (any, *apperr.Error) {
	started := time.Now()
	if pluginError := canceledByUser(ctx); pluginError != nil {
		return nil, pluginError
	}
	if params.UseProtoFiles {
		schema, pluginError := loadProtoSchema(params.ProtoFiles)
		if pluginError != nil {
			return nil, pluginError
		}
		return &ServicesResult{
			Target:    params.Target,
			Services:  schema.serviceNames(),
			ElapsedMs: time.Since(started).Milliseconds(),
		}, nil
	}

	connection, pluginError := dialGRPC(params.TargetParams)
	if pluginError != nil {
		return nil, pluginError
	}
	defer connection.Close()

	ctx, cancel, pluginError := grpcContext(ctx, params.TimeoutMs, params.Metadata)
	if pluginError != nil {
		return nil, pluginError
	}
	defer cancel()

	services, err := listServices(ctx, connection)
	if err != nil {
		if pluginError := canceledByUser(ctx); pluginError != nil {
			return nil, pluginError
		}
		return nil, grpcReflectionError(err)
	}
	visible := make([]string, 0, len(services))
	for _, service := range services {
		if strings.HasPrefix(service, "grpc.reflection.") {
			continue
		}
		visible = append(visible, service)
	}
	return &ServicesResult{
		Target:    params.Target,
		Services:  visible,
		ElapsedMs: time.Since(started).Milliseconds(),
	}, nil
}

// DescribeMethods 列出某个服务的全部方法签名。
func DescribeMethods(ctx context.Context, params MethodsParams) (any, *apperr.Error) {
	serviceName := strings.TrimSpace(params.Service)
	if serviceName == "" {
		return nil, apperr.Invalid("Service name is required")
	}

	started := time.Now()
	if pluginError := canceledByUser(ctx); pluginError != nil {
		return nil, pluginError
	}
	if params.UseProtoFiles {
		schema, pluginError := loadProtoSchema(params.ProtoFiles)
		if pluginError != nil {
			return nil, pluginError
		}
		methods, err := schema.methodInfos(serviceName)
		if err != nil {
			return nil, apperr.Invalid(err.Error())
		}
		return &MethodsResult{
			Service:   serviceName,
			Methods:   methods,
			ElapsedMs: time.Since(started).Milliseconds(),
		}, nil
	}

	connection, pluginError := dialGRPC(params.TargetParams)
	if pluginError != nil {
		return nil, pluginError
	}
	defer connection.Close()

	ctx, cancel, pluginError := grpcContext(ctx, params.TimeoutMs, params.Metadata)
	if pluginError != nil {
		return nil, pluginError
	}
	defer cancel()

	registry, err := loadFileRegistry(ctx, connection, serviceName)
	if err != nil {
		if pluginError := canceledByUser(ctx); pluginError != nil {
			return nil, pluginError
		}
		return nil, grpcReflectionError(err)
	}
	descriptor, err := registry.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil, apperr.Invalid("Service not found: " + serviceName)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, apperr.Invalid(serviceName + " is not a service")
	}
	methods := make([]MethodInfo, 0, service.Methods().Len())
	for index := 0; index < service.Methods().Len(); index++ {
		methods = append(methods, methodInfoOf(service, service.Methods().Get(index)))
	}
	return &MethodsResult{
		Service:   serviceName,
		Methods:   methods,
		ElapsedMs: time.Since(started).Milliseconds(),
	}, nil
}

// Invoke 调用一个 gRPC 方法（一元、服务端流、客户端流与双向流）。
func Invoke(ctx context.Context, params InvokeParams) (any, *apperr.Error) {
	if pluginError := canceledByUser(ctx); pluginError != nil {
		return nil, pluginError
	}
	serviceName := strings.TrimSpace(params.Service)
	methodName := strings.TrimSpace(params.Method)
	if methodName == "" {
		return nil, apperr.Invalid("Method name is required")
	}
	if serviceName == "" {
		full := strings.TrimPrefix(methodName, "/")
		index := strings.LastIndex(full, "/")
		if index < 0 {
			return nil, apperr.Invalid("Service name is required")
		}
		serviceName = strings.ReplaceAll(full[:index], "/", ".")
		methodName = full[index+1:]
	}

	// 先编译本地 proto：语法问题应该在发起连接之前就返回。
	var schema *protoSchema
	if params.UseProtoFiles {
		loaded, pluginError := loadProtoSchema(params.ProtoFiles)
		if pluginError != nil {
			return nil, pluginError
		}
		schema = loaded
	}

	connection, pluginError := dialGRPC(params.TargetParams)
	if pluginError != nil {
		return nil, pluginError
	}
	defer connection.Close()

	ctx, cancel, pluginError := grpcContext(ctx, params.TimeoutMs, params.Metadata)
	if pluginError != nil {
		return nil, pluginError
	}
	defer cancel()

	var method protoreflect.MethodDescriptor
	if schema != nil {
		found, err := schema.findMethod(serviceName, methodName)
		if err != nil {
			return nil, apperr.Invalid(err.Error())
		}
		method = found
	} else {
		registry, err := loadFileRegistry(ctx, connection, serviceName)
		if err != nil {
			if pluginError := canceledByUser(ctx); pluginError != nil {
				return nil, pluginError
			}
			return nil, grpcReflectionError(err)
		}
		found, err := findMethod(registry, serviceName, methodName)
		if err != nil {
			return nil, apperr.Invalid(err.Error())
		}
		method = found
	}

	payloads := outboundPayloads(params)
	if len(payloads) > maxOutboundMessages {
		return nil, apperr.Invalid(fmt.Sprintf("Too many request messages (limit %d)", maxOutboundMessages))
	}
	// 一元与服务端流在协议上只能带一条请求消息，多给出来的是调用方的错误，
	// 与其静默丢弃，不如在边界上直接拒绝。
	if !method.IsStreamingClient() && len(payloads) != 1 {
		return nil, apperr.Invalid("This method accepts exactly one request message")
	}

	outbound := make([]proto.Message, 0, len(payloads))
	for index, payload := range payloads {
		requestMessage := dynamicpb.NewMessage(method.Input())
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, requestMessage); err != nil {
			if len(payloads) == 1 {
				return nil, apperr.Invalid(fmt.Sprintf("Request message does not match %s: %v", method.Input().FullName(), err))
			}
			return nil, apperr.Invalid(fmt.Sprintf("Request message #%d does not match %s: %v", index+1, method.Input().FullName(), err))
		}
		outbound = append(outbound, requestMessage)
	}

	fullMethod := fmt.Sprintf("/%s/%s", method.Parent().FullName(), method.Name())
	started := time.Now()
	if method.IsStreamingServer() || method.IsStreamingClient() {
		result, pluginError := invokeStream(ctx, connection, method, fullMethod, outbound, started)
		return markCanceled(ctx, result, pluginError)
	}
	result, pluginError := invokeUnary(ctx, connection, method, fullMethod, outbound[0], started)
	return markCanceled(ctx, result, pluginError)
}

// markCanceled 把"用户点了终止"标到结果上，而不是丢掉已经收到的消息。
//
// 远端对我们取消的反应通常只是 Canceled 状态；这里以调用方写入的 cause 为准，
// 这样前端既能显示"已终止"，也能继续看到终止前收到的响应。
// 已经成功结束的调用即使恰好在此时被取消，也当作成功返回。
func markCanceled(ctx context.Context, value any, pluginError *apperr.Error) (any, *apperr.Error) {
	if pluginError != nil {
		return value, pluginError
	}
	invocation, ok := value.(*InvokeResult)
	if !ok {
		return value, nil
	}
	if invocation.OK || canceledByUser(ctx) == nil {
		return invocation, nil
	}
	invocation.Canceled = true
	return invocation, nil
}

// outboundPayloads 决定这次调用要发哪些消息。
//
// 客户端流与双向流用 messages 数组；一元与服务端流沿用单条 message。
// 两者都没给时退回空对象，保证永远至少有一条可发的消息。
func outboundPayloads(params InvokeParams) []json.RawMessage {
	if len(params.Messages) > 0 {
		return params.Messages
	}
	if len(strings.TrimSpace(string(params.Message))) > 0 {
		return []json.RawMessage{params.Message}
	}
	return []json.RawMessage{json.RawMessage("{}")}
}

func invokeUnary(
	ctx context.Context,
	connection *grpc.ClientConn,
	method protoreflect.MethodDescriptor,
	fullMethod string,
	requestMessage proto.Message,
	started time.Time,
) (any, *apperr.Error) {
	responseMessage := dynamicpb.NewMessage(method.Output())
	var header, trailer metadata.MD
	callOptions := []grpc.CallOption{
		grpc.Header(&header),
		grpc.Trailer(&trailer),
		grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
		grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
	}
	err := connection.Invoke(ctx, fullMethod, requestMessage, responseMessage, callOptions...)
	elapsed := time.Since(started)
	result := &InvokeResult{
		OK:        err == nil,
		Method:    fullMethod,
		Status:    StatusInfoFromError(err),
		Metadata:  metadataEntries(header),
		Trailers:  metadataEntries(trailer),
		ElapsedMs: elapsed.Milliseconds(),
	}
	if err != nil {
		return result, nil
	}
	encoded, marshalError := marshalProtoJSON(responseMessage)
	if marshalError != nil {
		return nil, apperr.Invalid("Cannot encode the response message: " + marshalError.Error())
	}
	result.MessageJSON = string(encoded)
	result.SizeBytes = len(encoded)
	return result, nil
}

// invokeStream 执行客户端流、服务端流与双向流。
//
// 收与发跑在两条执行线上：发送可能因为对端不读而阻塞，如果接收排在它后面，
// 两边的缓冲会互相等死——对端等我们读，我们等对端读。并发接收保证我们一直在
// "掏自己的口袋"，对端永远塞得进来，这也是双向流大载荷不会卡住的原因。
func invokeStream(
	ctx context.Context,
	connection *grpc.ClientConn,
	method protoreflect.MethodDescriptor,
	fullMethod string,
	outbound []proto.Message,
	started time.Time,
) (any, *apperr.Error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := connection.NewStream(streamCtx, &grpc.StreamDesc{
		StreamName:    string(method.Name()),
		ClientStreams: method.IsStreamingClient(),
		ServerStreams: method.IsStreamingServer(),
	}, fullMethod,
		grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
		grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
	)
	if err != nil {
		return failedStreamResult(fullMethod, err, started), nil
	}

	received := make(chan streamOutcome, 1)
	go func() { received <- receiveStream(stream, method, cancel) }()

	var sendError error
	for _, message := range outbound {
		if err := stream.SendMsg(message); err != nil {
			sendError = err
			cancel()
			break
		}
	}
	if sendError == nil {
		if err := stream.CloseSend(); err != nil {
			sendError = err
			cancel()
		}
	}

	outcome := <-received
	callError := outcome.err
	if sendError != nil {
		// 服务端明确给了状态时以它为准（Canceled/Unavailable 通常是我们取消的副产物），
		// 否则发送侧的失败更能说明问题。
		code := status.Code(callError)
		if callError == nil || code == codes.Canceled || code == codes.Unavailable {
			callError = sendError
		}
	}

	header, _ := stream.Header()
	return &InvokeResult{
		OK:         callError == nil,
		Method:     fullMethod,
		Status:     StatusInfoFromError(callError),
		Messages:   outcome.messages,
		Streaming:  true,
		StreamKind: streamKind(method),
		Truncated:  outcome.truncated,
		Metadata:   metadataEntries(header),
		Trailers:   metadataEntries(stream.Trailer()),
		ElapsedMs:  time.Since(started).Milliseconds(),
		SizeBytes:  outcome.sizeBytes,
	}, nil
}

type streamOutcome struct {
	messages  []string
	sizeBytes int
	truncated bool
	err       error
}

// receiveStream 一直读到流结束或达到展示上限。
//
// 达到上限时必须取消整个流：我们不再读了，对端就会卡在发送上，
// 取消是唯一能让它退出的方式。
func receiveStream(stream grpc.ClientStream, method protoreflect.MethodDescriptor, cancel context.CancelFunc) streamOutcome {
	outcome := streamOutcome{messages: make([]string, 0, 8)}
	for {
		responseMessage := dynamicpb.NewMessage(method.Output())
		if receiveError := stream.RecvMsg(responseMessage); receiveError != nil {
			if !errors.Is(receiveError, io.EOF) {
				outcome.err = receiveError
			}
			return outcome
		}
		encoded, marshalError := marshalProtoJSON(responseMessage)
		if marshalError != nil {
			outcome.err = marshalError
			return outcome
		}
		outcome.messages = append(outcome.messages, string(encoded))
		outcome.sizeBytes += len(encoded)
		if len(outcome.messages) >= maxStreamMessages || outcome.sizeBytes >= maxStreamBytes {
			outcome.truncated = true
			cancel()
			return outcome
		}
	}
}

func failedStreamResult(fullMethod string, err error, started time.Time) *InvokeResult {
	return &InvokeResult{
		OK:        false,
		Method:    fullMethod,
		Status:    StatusInfoFromError(err),
		Metadata:  []common.KeyValue{},
		Trailers:  []common.KeyValue{},
		ElapsedMs: time.Since(started).Milliseconds(),
	}
}

// streamKind 把描述符上的两个布尔值翻成界面用的流类型。
func streamKind(method protoreflect.MethodDescriptor) string {
	if method.IsStreamingClient() && method.IsStreamingServer() {
		return "bidi"
	}
	if method.IsStreamingClient() {
		return "client"
	}
	return "server"
}

func dialGRPC(params TargetParams) (*grpc.ClientConn, *apperr.Error) {
	target, err := normalizeGRPCTarget(params.Target)
	if err != nil {
		return nil, apperr.Invalid(err.Error())
	}
	options := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                keepalivePingInterval,
			Timeout:             keepalivePingTimeout,
			PermitWithoutStream: false,
		}),
	}
	if params.UseTLS {
		options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: params.SkipTLSVerify,
		})))
	} else {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	connection, err := grpc.NewClient(target, options...)
	if err != nil {
		return nil, grpcConnectionError(err)
	}
	return connection, nil
}

func normalizeGRPCTarget(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", errors.New("gRPC target is required (for example localhost:50051)")
	}
	if strings.Contains(target, "://") {
		parsed, err := url.Parse(target)
		if err != nil {
			return "", errors.New("gRPC target is not a valid URL")
		}
		if parsed.Host == "" {
			return "", errors.New("gRPC target must include a host and port")
		}
		return parsed.Host, nil
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", errors.New("gRPC target must be in host:port form (for example localhost:50051)")
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return "", errors.New("gRPC target must include a host and port")
	}
	return net.JoinHostPort(host, port), nil
}

func grpcContext(parent context.Context, timeoutMs int, entries []common.KeyValue) (context.Context, context.CancelFunc, *apperr.Error) {
	values, err := metadataFromEntries(entries)
	if err != nil {
		return nil, nil, apperr.Invalid(err.Error())
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(common.ClampInt(timeoutMs, defaultGRPCTimeoutMs, 1, maxGRPCTimeoutMs))*time.Millisecond)
	if len(values) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, values)
	}
	return ctx, cancel, nil
}

// canceledByUser 判断这次失败是否由"用户点了终止"引起。
//
// context.Cause 会沿父子链取出我们写入的 apperr.ErrCanceled；派生出的
// WithTimeout 上下文超时则得到 DeadlineExceeded，不会被误判为终止。
func canceledByUser(ctx context.Context) *apperr.Error {
	if errors.Is(context.Cause(ctx), apperr.ErrCanceled) {
		return apperr.Canceled()
	}
	return nil
}

func metadataFromEntries(entries []common.KeyValue) (metadata.MD, error) {
	values := metadata.MD{}
	for _, entry := range entries {
		name := strings.ToLower(strings.TrimSpace(entry.Name))
		if name == "" {
			continue
		}
		if !metadataKeyPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid metadata key %q", entry.Name)
		}
		if strings.HasPrefix(name, "grpc-") {
			return nil, fmt.Errorf("metadata key %q is reserved by gRPC", entry.Name)
		}
		if strings.ContainsAny(entry.Value, "\r\n") {
			return nil, fmt.Errorf("metadata %q contains a line break", entry.Name)
		}
		values.Append(name, entry.Value)
	}
	return values, nil
}

func metadataEntries(values metadata.MD) []common.KeyValue {
	entries := make([]common.KeyValue, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range values[key] {
			entries = append(entries, common.KeyValue{Name: key, Value: value})
		}
	}
	return entries
}

func findMethod(registry *protoregistry.Files, serviceName, methodName string) (protoreflect.MethodDescriptor, error) {
	descriptor, err := registry.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil, fmt.Errorf("service %q was not found on the server", serviceName)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is not a service", serviceName)
	}
	method := service.Methods().ByName(protoreflect.Name(methodName))
	if method == nil {
		return nil, fmt.Errorf("method %q was not found on %q", methodName, serviceName)
	}
	return method, nil
}

func marshalProtoJSON(message proto.Message) ([]byte, error) {
	return (protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		EmitUnpopulated: false,
	}).Marshal(message)
}

func StatusInfoFromError(err error) StatusInfo {
	if err == nil {
		return StatusInfo{Code: 0, Name: "OK"}
	}
	parsed, ok := status.FromError(err)
	if !ok {
		return StatusInfo{Code: -1, Name: "ERROR", Message: err.Error()}
	}
	return StatusInfo{
		Code:    int(parsed.Code()),
		Name:    parsed.Code().String(),
		Message: parsed.Message(),
	}
}

func grpcReflectionError(err error) *apperr.Error {
	kind := "reflection"
	message := err.Error()
	if parsed, ok := status.FromError(err); ok {
		message = parsed.Code().String()
		if parsed.Message() != "" {
			message += ": " + parsed.Message()
		}
		switch parsed.Code() {
		case codes.Unavailable:
			kind = "connection"
		case codes.DeadlineExceeded:
			kind = "timeout"
		case codes.Unauthenticated, codes.PermissionDenied:
			kind = "auth"
		case codes.Unimplemented, codes.NotFound:
			kind = "reflection"
		}
	} else if errors.Is(err, context.DeadlineExceeded) {
		kind = "timeout"
	}
	return apperr.Upstream(apperr.Kind(kind), "gRPC request failed: %s", message)
}

func grpcConnectionError(err error) *apperr.Error {
	return apperr.Upstream(apperr.KindConnection, "Cannot create the gRPC client: %s", err.Error())
}

func withReflectionFallback[T any](
	callV1 func() (T, error),
	callAlpha func() (T, error),
) (T, error) {
	value, err := callV1()
	if err == nil {
		return value, nil
	}
	if status.Code(err) == codes.Unimplemented {
		return callAlpha()
	}
	var zero T
	return zero, err
}

func listServices(ctx context.Context, connection *grpc.ClientConn) ([]string, error) {
	return withReflectionFallback(
		func() ([]string, error) { return listServicesV1(ctx, connection) },
		func() ([]string, error) { return listServicesAlpha(ctx, connection) },
	)
}

func listServicesV1(ctx context.Context, connection *grpc.ClientConn) ([]string, error) {
	stream, err := reflectionv1.NewServerReflectionClient(connection).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if failure := response.GetErrorResponse(); failure != nil {
		return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
	}
	list := response.GetListServicesResponse()
	if list == nil {
		return nil, errors.New("unexpected reflection response")
	}
	services := make([]string, 0, len(list.GetService()))
	for _, service := range list.GetService() {
		if name := service.GetName(); name != "" {
			services = append(services, name)
		}
	}
	sort.Strings(services)
	return services, nil
}

func listServicesAlpha(ctx context.Context, connection *grpc.ClientConn) ([]string, error) {
	stream, err := reflectionv1alpha.NewServerReflectionClient(connection).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if failure := response.GetErrorResponse(); failure != nil {
		return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
	}
	list := response.GetListServicesResponse()
	if list == nil {
		return nil, errors.New("unexpected reflection response")
	}
	services := make([]string, 0, len(list.GetService()))
	for _, service := range list.GetService() {
		if name := service.GetName(); name != "" {
			services = append(services, name)
		}
	}
	sort.Strings(services)
	return services, nil
}

func fileDescriptorsForSymbol(ctx context.Context, connection *grpc.ClientConn, symbol string) ([][]byte, error) {
	return withReflectionFallback(
		func() ([][]byte, error) { return fileDescriptorsForSymbolV1(ctx, connection, symbol) },
		func() ([][]byte, error) { return fileDescriptorsForSymbolAlpha(ctx, connection, symbol) },
	)
}

func fileDescriptorsForSymbolV1(ctx context.Context, connection *grpc.ClientConn, symbol string) ([][]byte, error) {
	stream, err := reflectionv1.NewServerReflectionClient(connection).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if failure := response.GetErrorResponse(); failure != nil {
		return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
	}
	if descriptors := response.GetFileDescriptorResponse(); descriptors != nil {
		return descriptors.GetFileDescriptorProto(), nil
	}
	return nil, errors.New("unexpected reflection response")
}

func fileDescriptorsForSymbolAlpha(ctx context.Context, connection *grpc.ClientConn, symbol string) ([][]byte, error) {
	stream, err := reflectionv1alpha.NewServerReflectionClient(connection).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if failure := response.GetErrorResponse(); failure != nil {
		return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
	}
	if descriptors := response.GetFileDescriptorResponse(); descriptors != nil {
		return descriptors.GetFileDescriptorProto(), nil
	}
	return nil, errors.New("unexpected reflection response")
}

func fileDescriptorsByFilename(ctx context.Context, connection *grpc.ClientConn, name string) ([][]byte, error) {
	return withReflectionFallback(
		func() ([][]byte, error) { return fileDescriptorsByFilenameV1(ctx, connection, name) },
		func() ([][]byte, error) { return fileDescriptorsByFilenameAlpha(ctx, connection, name) },
	)
}

func fileDescriptorsByFilenameV1(ctx context.Context, connection *grpc.ClientConn, name string) ([][]byte, error) {
	stream, err := reflectionv1.NewServerReflectionClient(connection).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_FileByFilename{FileByFilename: name},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if failure := response.GetErrorResponse(); failure != nil {
		return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
	}
	if descriptors := response.GetFileDescriptorResponse(); descriptors != nil {
		return descriptors.GetFileDescriptorProto(), nil
	}
	return nil, errors.New("unexpected reflection response")
}

func fileDescriptorsByFilenameAlpha(ctx context.Context, connection *grpc.ClientConn, name string) ([][]byte, error) {
	stream, err := reflectionv1alpha.NewServerReflectionClient(connection).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileByFilename{FileByFilename: name},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if failure := response.GetErrorResponse(); failure != nil {
		return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
	}
	if descriptors := response.GetFileDescriptorResponse(); descriptors != nil {
		return descriptors.GetFileDescriptorProto(), nil
	}
	return nil, errors.New("unexpected reflection response")
}

func loadFileRegistry(ctx context.Context, connection *grpc.ClientConn, symbol string) (*protoregistry.Files, error) {
	files := make(map[string]*descriptorpb.FileDescriptorProto)
	queue := make([]string, 0, 8)

	consume := func(rawDescriptors [][]byte) error {
		for _, raw := range rawDescriptors {
			descriptor := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(raw, descriptor); err != nil {
				return errors.New("server returned an invalid protobuf file descriptor")
			}
			name := descriptor.GetName()
			if name == "" {
				continue
			}
			if _, exists := files[name]; exists {
				continue
			}
			files[name] = descriptor
			queue = append(queue, descriptor.GetDependency()...)
		}
		return nil
	}

	descriptors, err := fileDescriptorsForSymbol(ctx, connection, symbol)
	if err != nil {
		return nil, err
	}
	if err := consume(descriptors); err != nil {
		return nil, err
	}

	for len(queue) > 0 {
		if len(files) > maxReflectionFiles {
			return nil, errors.New("server returned too many protobuf files")
		}
		name := queue[0]
		queue = queue[1:]
		if _, exists := files[name]; exists {
			continue
		}
		if _, err := protoregistry.GlobalFiles.FindFileByPath(name); err == nil {
			continue
		}
		descriptors, err := fileDescriptorsByFilename(ctx, connection, name)
		if err != nil {
			return nil, fmt.Errorf("reflection did not return dependency %q: %w", name, err)
		}
		if err := consume(descriptors); err != nil {
			return nil, err
		}
	}

	return buildFileRegistry(files)
}

type descriptorResolver struct {
	local *protoregistry.Files
}

func (resolver descriptorResolver) FindFileByPath(path string) (protoreflect.FileDescriptor, error) {
	if file, err := resolver.local.FindFileByPath(path); err == nil {
		return file, nil
	}
	return protoregistry.GlobalFiles.FindFileByPath(path)
}

func (resolver descriptorResolver) FindDescriptorByName(name protoreflect.FullName) (protoreflect.Descriptor, error) {
	if descriptor, err := resolver.local.FindDescriptorByName(name); err == nil {
		return descriptor, nil
	}
	return protoregistry.GlobalFiles.FindDescriptorByName(name)
}

func buildFileRegistry(files map[string]*descriptorpb.FileDescriptorProto) (*protoregistry.Files, error) {
	registry := &protoregistry.Files{}
	resolver := descriptorResolver{local: registry}
	visiting := make(map[string]bool, len(files))

	var add func(name string) error
	add = func(name string) error {
		if _, err := registry.FindFileByPath(name); err == nil {
			return nil
		}
		descriptor, exists := files[name]
		if !exists {
			if _, err := protoregistry.GlobalFiles.FindFileByPath(name); err == nil {
				return nil
			}
			return fmt.Errorf("missing protobuf file %q", name)
		}
		if visiting[name] {
			return fmt.Errorf("circular protobuf dependency at %q", name)
		}
		visiting[name] = true
		defer delete(visiting, name)
		for _, dependency := range descriptor.GetDependency() {
			if err := add(dependency); err != nil {
				return err
			}
		}
		file, err := protodesc.NewFile(descriptor, resolver)
		if err != nil {
			return fmt.Errorf("cannot parse protobuf file %q: %w", name, err)
		}
		return registry.RegisterFile(file)
	}

	for name := range files {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

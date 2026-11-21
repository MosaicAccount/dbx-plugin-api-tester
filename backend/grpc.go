package main

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

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
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
)

var metadataKeyPattern = regexp.MustCompile(`^[0-9a-z_.-]+$`)

type GRPCTargetParams struct {
	Target        string     `json:"target"`
	UseTLS        bool       `json:"tls"`
	SkipTLSVerify bool       `json:"skipTlsVerify"`
	TimeoutMs     int        `json:"timeoutMs"`
	Metadata      []keyValue `json:"metadata"`
}

type grpcDiscoveryParams struct {
	GRPCTargetParams
}

type grpcMethodsParams struct {
	GRPCTargetParams
	Service string `json:"service"`
}

type grpcInvokeParams struct {
	GRPCTargetParams
	Service string          `json:"service"`
	Method  string          `json:"method"`
	Message json.RawMessage `json:"message"`
}

type grpcServicesResult struct {
	Target    string   `json:"target"`
	Services  []string `json:"services"`
	ElapsedMs int64    `json:"elapsedMs"`
}

type grpcMethodInfo struct {
	Name            string `json:"name"`
	FullName        string `json:"fullName"`
	InputType       string `json:"inputType"`
	OutputType      string `json:"outputType"`
	ClientStreaming bool   `json:"clientStreaming"`
	ServerStreaming bool   `json:"serverStreaming"`
}

type grpcMethodsResult struct {
	Service   string           `json:"service"`
	Methods   []grpcMethodInfo `json:"methods"`
	ElapsedMs int64            `json:"elapsedMs"`
}

type grpcStatusInfo struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type grpcInvokeResult struct {
	OK          bool           `json:"ok"`
	Method      string         `json:"method"`
	Status      grpcStatusInfo `json:"status"`
	MessageJSON string         `json:"messageJson,omitempty"`
	Messages    []string       `json:"messages,omitempty"`
	Streaming   bool           `json:"streaming,omitempty"`
	Truncated   bool           `json:"truncated,omitempty"`
	Metadata    []keyValue     `json:"metadata"`
	Trailers    []keyValue     `json:"trailers"`
	ElapsedMs   int64          `json:"elapsedMs"`
	SizeBytes   int            `json:"sizeBytes"`
}

func discoverGRPCServices(params grpcDiscoveryParams) (any, *dbxpluginsdk.PluginError) {
	connection, pluginError := dialGRPC(params.GRPCTargetParams)
	if pluginError != nil {
		return nil, pluginError
	}
	defer connection.Close()

	ctx, cancel, pluginError := grpcContext(params.TimeoutMs, params.Metadata)
	if pluginError != nil {
		return nil, pluginError
	}
	defer cancel()

	started := time.Now()
	services, err := listServices(ctx, connection)
	if err != nil {
		return nil, grpcReflectionError(err)
	}
	visible := make([]string, 0, len(services))
	for _, service := range services {
		if strings.HasPrefix(service, "grpc.reflection.") {
			continue
		}
		visible = append(visible, service)
	}
	return &grpcServicesResult{
		Target:    params.Target,
		Services:  visible,
		ElapsedMs: time.Since(started).Milliseconds(),
	}, nil
}

func describeGRPCMethods(params grpcMethodsParams) (any, *dbxpluginsdk.PluginError) {
	serviceName := strings.TrimSpace(params.Service)
	if serviceName == "" {
		return nil, invalid("Service name is required")
	}

	connection, pluginError := dialGRPC(params.GRPCTargetParams)
	if pluginError != nil {
		return nil, pluginError
	}
	defer connection.Close()

	ctx, cancel, pluginError := grpcContext(params.TimeoutMs, params.Metadata)
	if pluginError != nil {
		return nil, pluginError
	}
	defer cancel()

	started := time.Now()
	registry, err := loadFileRegistry(ctx, connection, serviceName)
	if err != nil {
		return nil, grpcReflectionError(err)
	}
	descriptor, err := registry.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil, invalid("Service not found: " + serviceName)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, invalid(serviceName + " is not a service")
	}
	methods := make([]grpcMethodInfo, 0, service.Methods().Len())
	for index := 0; index < service.Methods().Len(); index++ {
		method := service.Methods().Get(index)
		methods = append(methods, grpcMethodInfo{
			Name:            string(method.Name()),
			FullName:        fmt.Sprintf("/%s/%s", service.FullName(), method.Name()),
			InputType:       string(method.Input().FullName()),
			OutputType:      string(method.Output().FullName()),
			ClientStreaming: method.IsStreamingClient(),
			ServerStreaming: method.IsStreamingServer(),
		})
	}
	return &grpcMethodsResult{
		Service:   serviceName,
		Methods:   methods,
		ElapsedMs: time.Since(started).Milliseconds(),
	}, nil
}

func invokeGRPC(params grpcInvokeParams) (any, *dbxpluginsdk.PluginError) {
	serviceName := strings.TrimSpace(params.Service)
	methodName := strings.TrimSpace(params.Method)
	if methodName == "" {
		return nil, invalid("Method name is required")
	}
	if serviceName == "" {
		full := strings.TrimPrefix(methodName, "/")
		index := strings.LastIndex(full, "/")
		if index < 0 {
			return nil, invalid("Service name is required")
		}
		serviceName = strings.ReplaceAll(full[:index], "/", ".")
		methodName = full[index+1:]
	}

	connection, pluginError := dialGRPC(params.GRPCTargetParams)
	if pluginError != nil {
		return nil, pluginError
	}
	defer connection.Close()

	ctx, cancel, pluginError := grpcContext(params.TimeoutMs, params.Metadata)
	if pluginError != nil {
		return nil, pluginError
	}
	defer cancel()

	registry, err := loadFileRegistry(ctx, connection, serviceName)
	if err != nil {
		return nil, grpcReflectionError(err)
	}
	method, err := findMethod(registry, serviceName, methodName)
	if err != nil {
		return nil, invalid(err.Error())
	}
	if method.IsStreamingClient() {
		return nil, invalid("Client-streaming and bidirectional gRPC methods are not supported yet")
	}

	requestMessage := dynamicpb.NewMessage(method.Input())
	messageBytes := params.Message
	if len(strings.TrimSpace(string(messageBytes))) == 0 {
		messageBytes = []byte("{}")
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(messageBytes, requestMessage); err != nil {
		return nil, invalid(fmt.Sprintf("Request message does not match %s: %v", method.Input().FullName(), err))
	}

	fullMethod := fmt.Sprintf("/%s/%s", method.Parent().FullName(), method.Name())
	started := time.Now()
	if method.IsStreamingServer() {
		return invokeServerStream(ctx, connection, method, fullMethod, requestMessage, started)
	}
	return invokeUnary(ctx, connection, method, fullMethod, requestMessage, started)
}

func invokeUnary(
	ctx context.Context,
	connection *grpc.ClientConn,
	method protoreflect.MethodDescriptor,
	fullMethod string,
	requestMessage proto.Message,
	started time.Time,
) (any, *dbxpluginsdk.PluginError) {
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
	result := &grpcInvokeResult{
		OK:        err == nil,
		Method:    fullMethod,
		Status:    grpcStatusInfoFromError(err),
		Metadata:  metadataEntries(header),
		Trailers:  metadataEntries(trailer),
		ElapsedMs: elapsed.Milliseconds(),
	}
	if err != nil {
		return result, nil
	}
	encoded, marshalError := marshalProtoJSON(responseMessage)
	if marshalError != nil {
		return nil, invalid("Cannot encode the response message: " + marshalError.Error())
	}
	result.MessageJSON = string(encoded)
	result.SizeBytes = len(encoded)
	return result, nil
}

func invokeServerStream(
	ctx context.Context,
	connection *grpc.ClientConn,
	method protoreflect.MethodDescriptor,
	fullMethod string,
	requestMessage proto.Message,
	started time.Time,
) (any, *dbxpluginsdk.PluginError) {
	stream, err := connection.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    string(method.Name()),
		ServerStreams: true,
	}, fullMethod,
		grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
		grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
	)
	if err != nil {
		return &grpcInvokeResult{
			OK:        false,
			Method:    fullMethod,
			Status:    grpcStatusInfoFromError(err),
			Metadata:  []keyValue{},
			Trailers:  []keyValue{},
			ElapsedMs: time.Since(started).Milliseconds(),
		}, nil
	}
	if err := stream.SendMsg(requestMessage); err != nil {
		return &grpcInvokeResult{OK: false, Method: fullMethod, Status: grpcStatusInfoFromError(err), Metadata: []keyValue{}, Trailers: []keyValue{}, ElapsedMs: time.Since(started).Milliseconds()}, nil
	}
	if err := stream.CloseSend(); err != nil {
		return &grpcInvokeResult{OK: false, Method: fullMethod, Status: grpcStatusInfoFromError(err), Metadata: []keyValue{}, Trailers: []keyValue{}, ElapsedMs: time.Since(started).Milliseconds()}, nil
	}

	messages := make([]string, 0, 8)
	totalBytes := 0
	var callError error
	for len(messages) < maxStreamMessages && totalBytes < maxStreamBytes {
		responseMessage := dynamicpb.NewMessage(method.Output())
		if receiveError := stream.RecvMsg(responseMessage); receiveError != nil {
			if !errors.Is(receiveError, io.EOF) {
				callError = receiveError
			}
			break
		}
		encoded, marshalError := marshalProtoJSON(responseMessage)
		if marshalError != nil {
			callError = marshalError
			break
		}
		messages = append(messages, string(encoded))
		totalBytes += len(encoded)
	}

	header, _ := stream.Header()
	result := &grpcInvokeResult{
		OK:        callError == nil,
		Method:    fullMethod,
		Status:    grpcStatusInfoFromError(callError),
		Messages:  messages,
		Streaming: true,
		Truncated: callError == nil && (len(messages) >= maxStreamMessages || totalBytes >= maxStreamBytes),
		Metadata:  metadataEntries(header),
		Trailers:  metadataEntries(stream.Trailer()),
		ElapsedMs: time.Since(started).Milliseconds(),
		SizeBytes: totalBytes,
	}
	return result, nil
}

func dialGRPC(params GRPCTargetParams) (*grpc.ClientConn, *dbxpluginsdk.PluginError) {
	target, err := normalizeGRPCTarget(params.Target)
	if err != nil {
		return nil, invalid(err.Error())
	}
	options := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
		),
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

func grpcContext(timeoutMs int, entries []keyValue) (context.Context, context.CancelFunc, *dbxpluginsdk.PluginError) {
	values, err := metadataFromEntries(entries)
	if err != nil {
		return nil, nil, invalid(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(clampInt(timeoutMs, defaultGRPCTimeoutMs, 1, maxGRPCTimeoutMs))*time.Millisecond)
	if len(values) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, values)
	}
	return ctx, cancel, nil
}

func metadataFromEntries(entries []keyValue) (metadata.MD, error) {
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

func metadataEntries(values metadata.MD) []keyValue {
	entries := make([]keyValue, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range values[key] {
			entries = append(entries, keyValue{Name: key, Value: value})
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

func grpcStatusInfoFromError(err error) grpcStatusInfo {
	if err == nil {
		return grpcStatusInfo{Code: 0, Name: "OK"}
	}
	parsed, ok := status.FromError(err)
	if !ok {
		return grpcStatusInfo{Code: -1, Name: "ERROR", Message: err.Error()}
	}
	return grpcStatusInfo{
		Code:    int(parsed.Code()),
		Name:    parsed.Code().String(),
		Message: parsed.Message(),
	}
}

func grpcReflectionError(err error) *dbxpluginsdk.PluginError {
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
	return &dbxpluginsdk.PluginError{
		Code:    -32000,
		Message: "gRPC request failed: " + message,
		Data:    map[string]any{"kind": kind},
	}
}

func grpcConnectionError(err error) *dbxpluginsdk.PluginError {
	return &dbxpluginsdk.PluginError{
		Code:    -32000,
		Message: "Cannot create the gRPC client: " + err.Error(),
		Data:    map[string]any{"kind": "connection"},
	}
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

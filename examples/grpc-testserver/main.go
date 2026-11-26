// Command grpc-testserver 是一个带 Server Reflection 的本地 gRPC 测试服务，
// 四个方法分别覆盖一元、服务端流、客户端流、双向流四种调用方式。
//
//	go run . -addr :50051
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	demov1 "github.com/jlian/dbx-plugin-api-tester/examples/grpc-testserver/gen/demo/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	defaultAddr        = "127.0.0.1:50051"
	defaultStreamCount = 5
	maxStreamCount     = 50
	maxDelay           = 30 * time.Second
)

func main() {
	addr := flag.String("addr", defaultAddr, "监听地址，例如 :50051 或 127.0.0.1:50051")
	flag.Parse()

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("监听 %s 失败: %v", *addr, err)
	}
	defer listener.Close()

	server := newGRPCServer()
	log.Printf("gRPC 测试服务已启动: %s（明文 h2c，已开启 Server Reflection）", listener.Addr())
	log.Printf("服务: demo.v1.Greeter（一元/服务端流/客户端流/双向流）、grpc.health.v1.Health")

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		log.Print("收到退出信号，正在关闭服务")
		server.GracefulStop()
	}()

	if serveErr := server.Serve(listener); serveErr != nil {
		log.Fatalf("服务异常退出: %v", serveErr)
	}
}

// newGRPCServer 装配测试服务；测试里也直接用它启动一个随机端口的实例。
func newGRPCServer() *grpc.Server {
	server := grpc.NewServer()
	demov1.RegisterGreeterServer(server, &greeterServer{})
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	return server
}

type greeterServer struct {
	demov1.UnimplementedGreeterServer
}

// SayHello 是一元调用：一条请求换一条响应。
func (s *greeterServer) SayHello(ctx context.Context, request *demov1.HelloRequest) (*demov1.HelloReply, error) {
	if request.GetFail() {
		return nil, intentionalError()
	}
	if err := wait(ctx, request.GetDelayMs()); err != nil {
		return nil, err
	}
	if err := grpc.SetHeader(ctx, metadata.Pairs("x-demo-server", "grpc-testserver")); err != nil {
		return nil, status.Errorf(codes.Internal, "set header: %v", err)
	}
	grpc.SetTrailer(ctx, metadata.Pairs("x-demo-method", "unary"))

	return &demov1.HelloReply{
		Message:      fmt.Sprintf("Hello, %s! (#1)", displayName(request.GetName())),
		Index:        1,
		ServerTimeMs: time.Now().UnixMilli(),
		Metadata:     echoMetadata(ctx),
	}, nil
}

// SayHelloStream 是服务端流：一次请求，连续推送 count 条响应。
func (s *greeterServer) SayHelloStream(request *demov1.HelloRequest, stream grpc.ServerStreamingServer[demov1.HelloReply]) error {
	if request.GetFail() {
		return intentionalError()
	}
	count := int(request.GetCount())
	switch {
	case count <= 0:
		count = defaultStreamCount
	case count > maxStreamCount:
		count = maxStreamCount
	}
	if err := stream.SetHeader(metadata.Pairs("x-demo-server", "grpc-testserver")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-demo-count", strconv.Itoa(count)))

	for index := 1; index <= count; index++ {
		if err := wait(stream.Context(), request.GetDelayMs()); err != nil {
			return err
		}
		reply := &demov1.HelloReply{
			Message:      fmt.Sprintf("Hello, %s! (#%d)", displayName(request.GetName()), index),
			Index:        int32(index),
			ServerTimeMs: time.Now().UnixMilli(),
			Metadata:     echoMetadata(stream.Context()),
		}
		if err := stream.Send(reply); err != nil {
			return err
		}
	}
	return nil
}

// CollectHellos 是客户端流：收完客户端的所有消息后返回一条汇总。
func (s *greeterServer) CollectHellos(stream grpc.ClientStreamingServer[demov1.HelloRequest, demov1.HelloSummary]) error {
	names := make([]string, 0, 8)
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if request.GetFail() {
			return intentionalError()
		}
		if err := wait(stream.Context(), request.GetDelayMs()); err != nil {
			return err
		}
		names = append(names, displayName(request.GetName()))
	}

	if err := stream.SetHeader(metadata.Pairs("x-demo-server", "grpc-testserver")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-demo-count", strconv.Itoa(len(names))))
	return stream.SendAndClose(&demov1.HelloSummary{
		Count:        int32(len(names)),
		Names:        names,
		ServerTimeMs: time.Now().UnixMilli(),
	})
}

// Chat 是双向流：每收到一条就回一条，客户端关闭发送方向后结束。
func (s *greeterServer) Chat(stream grpc.BidiStreamingServer[demov1.ChatMessage, demov1.ChatMessage]) error {
	if err := stream.SetHeader(metadata.Pairs("x-demo-server", "grpc-testserver")); err != nil {
		return err
	}
	index := 0
	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			stream.SetTrailer(metadata.Pairs("x-demo-count", strconv.Itoa(index)))
			return nil
		}
		if err != nil {
			return err
		}
		index++
		reply := &demov1.ChatMessage{
			Text:         "echo: " + message.GetText(),
			Index:        int32(index),
			ServerTimeMs: time.Now().UnixMilli(),
		}
		if err := stream.Send(reply); err != nil {
			return err
		}
	}
}

// wait 按 delay_ms 暂停，同时尊重客户端取消和 deadline。
func wait(ctx context.Context, delayMs int64) error {
	if delayMs <= 0 {
		return nil
	}
	if delayMs > int64(maxDelay/time.Millisecond) {
		delayMs = int64(maxDelay / time.Millisecond)
	}
	timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case <-timer.C:
		return nil
	}
}

func displayName(name string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	return "world"
}

// intentionalError 是 fail=true 时固定返回的错误，方便客户端验证错误码展示。
func intentionalError() error {
	return status.Error(codes.FailedPrecondition, "intentional failure because fail=true")
}

// echoMetadata 把调用方带来的 metadata 回显到响应里（小写 key）。
func echoMetadata(ctx context.Context) map[string]string {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return map[string]string{}
	}
	echoed := make(map[string]string, len(incoming))
	for key, values := range incoming {
		if len(values) == 0 || strings.HasPrefix(key, ":") || strings.HasPrefix(key, "grpc-") {
			continue
		}
		if key == "content-type" || key == "user-agent" {
			continue
		}
		echoed[key] = values[0]
	}
	return echoed
}

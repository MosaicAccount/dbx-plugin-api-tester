package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	demov1 "github.com/jlian/dbx-plugin-api-tester/examples/grpc-testserver/gen/demo/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

func startTestServer(t *testing.T) *grpc.ClientConn {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := newGRPCServer()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestSayHelloUnary(t *testing.T) {
	client := demov1.NewGreeterClient(startTestServer(t))
	ctx := metadata.NewOutgoingContext(testContext(t), metadata.Pairs("x-trace-id", "abc-123"))

	var header, trailer metadata.MD
	reply, err := client.SayHello(ctx, &demov1.HelloRequest{Name: "Tester"}, grpc.Header(&header), grpc.Trailer(&trailer))
	if err != nil {
		t.Fatalf("SayHello: %v", err)
	}
	if want := "Hello, Tester! (#1)"; reply.GetMessage() != want {
		t.Fatalf("message = %q, want %q", reply.GetMessage(), want)
	}
	if reply.GetIndex() != 1 {
		t.Fatalf("index = %d, want 1", reply.GetIndex())
	}
	if got := reply.GetMetadata()["x-trace-id"]; got != "abc-123" {
		t.Fatalf("echoed metadata x-trace-id = %q, want abc-123", got)
	}
	if got := header.Get("x-demo-server"); len(got) == 0 || got[0] != "grpc-testserver" {
		t.Fatalf("header x-demo-server = %v", got)
	}
	if got := trailer.Get("x-demo-method"); len(got) == 0 || got[0] != "unary" {
		t.Fatalf("trailer x-demo-method = %v", got)
	}
}

func TestSayHelloIntentionalFailure(t *testing.T) {
	client := demov1.NewGreeterClient(startTestServer(t))

	_, err := client.SayHello(testContext(t), &demov1.HelloRequest{Name: "Tester", Fail: true})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err: %v)", status.Code(err), err)
	}
	if message := status.Convert(err).Message(); message == "" {
		t.Fatal("expected a non-empty error message")
	}
}

func TestSayHelloStream(t *testing.T) {
	client := demov1.NewGreeterClient(startTestServer(t))
	stream, err := client.SayHelloStream(testContext(t), &demov1.HelloRequest{Name: "stream", Count: 3, DelayMs: 5})
	if err != nil {
		t.Fatalf("SayHelloStream: %v", err)
	}

	messages := make([]*demov1.HelloReply, 0, 3)
	for {
		reply, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("recv: %v", recvErr)
		}
		messages = append(messages, reply)
	}
	if len(messages) != 3 {
		t.Fatalf("received %d messages, want 3", len(messages))
	}
	for index, reply := range messages {
		if want := int32(index + 1); reply.GetIndex() != want {
			t.Fatalf("message %d index = %d, want %d", index, reply.GetIndex(), want)
		}
	}
	if got := stream.Trailer().Get("x-demo-count"); len(got) == 0 || got[0] != "3" {
		t.Fatalf("trailer x-demo-count = %v, want 3", got)
	}
}

func TestCollectHellosClientStream(t *testing.T) {
	client := demov1.NewGreeterClient(startTestServer(t))
	stream, err := client.CollectHellos(testContext(t))
	if err != nil {
		t.Fatalf("CollectHellos: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if sendErr := stream.Send(&demov1.HelloRequest{Name: name}); sendErr != nil {
			t.Fatalf("send %q: %v", name, sendErr)
		}
	}

	summary, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	if summary.GetCount() != 3 {
		t.Fatalf("count = %d, want 3", summary.GetCount())
	}
	if got := summary.GetNames(); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("names = %v, want [a b c]", got)
	}
	if got := stream.Trailer().Get("x-demo-count"); len(got) == 0 || got[0] != "3" {
		t.Fatalf("trailer x-demo-count = %v, want 3", got)
	}
}

func TestChatBidiStream(t *testing.T) {
	client := demov1.NewGreeterClient(startTestServer(t))
	stream, err := client.Chat(testContext(t))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	for _, text := range []string{"hi", "there"} {
		if sendErr := stream.Send(&demov1.ChatMessage{Text: text}); sendErr != nil {
			t.Fatalf("send %q: %v", text, sendErr)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	replies := make([]*demov1.ChatMessage, 0, 2)
	for {
		reply, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("recv: %v", recvErr)
		}
		replies = append(replies, reply)
	}
	if len(replies) != 2 {
		t.Fatalf("received %d messages, want 2", len(replies))
	}
	if want := "echo: hi"; replies[0].GetText() != want {
		t.Fatalf("first reply = %q, want %q", replies[0].GetText(), want)
	}
	if replies[0].GetIndex() != 1 || replies[1].GetIndex() != 2 {
		t.Fatalf("indices = %d, %d, want 1, 2", replies[0].GetIndex(), replies[1].GetIndex())
	}
	if got := stream.Trailer().Get("x-demo-count"); len(got) == 0 || got[0] != "2" {
		t.Fatalf("trailer x-demo-count = %v, want 2", got)
	}
}

func TestServerReflectionListsGreeter(t *testing.T) {
	connection := startTestServer(t)
	client := reflectionv1.NewServerReflectionClient(connection)
	stream, err := client.ServerReflectionInfo(testContext(t))
	if err != nil {
		t.Fatalf("ServerReflectionInfo: %v", err)
	}
	request := &reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{ListServices: ""},
	}
	if err := stream.Send(request); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	response, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}

	for _, service := range response.GetListServicesResponse().GetService() {
		if service.GetName() == "demo.v1.Greeter" {
			return
		}
	}
	t.Fatalf("demo.v1.Greeter not found in reflection response: %v", response)
}

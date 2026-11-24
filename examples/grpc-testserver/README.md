# gRPC 测试服务（demo.v1.Greeter）

本地自测用的最小 gRPC 服务：一个 proto + 一个 `main.go`，开启 Server Reflection，四个方法分别覆盖
一元、服务端流、客户端流、双向流。明文 h2c、无鉴权，只在本机开发时使用。

## 启动

```bash
cd examples/grpc-testserver
go run .                  # 默认监听 127.0.0.1:50051
go run . -addr :50052     # 换端口
```

启动后会打印：

```
gRPC 测试服务已启动: 127.0.0.1:50051（明文 h2c，已开启 Server Reflection）
服务: demo.v1.Greeter（一元/服务端流/客户端流/双向流）、grpc.health.v1.Health
```

## 方法一览

| 方法 | 调用方式 | 请求 → 响应 | 行为 |
| --- | --- | --- | --- |
| `SayHello` | 一元 | `HelloRequest` → `HelloReply` | 返回 `Hello, <name>! (#1)`，并把 metadata 回显到 `metadata` 字段 |
| `SayHelloStream` | 服务端流 | `HelloRequest` → `stream HelloReply` | 推送 `count` 条（默认 5，上限 50），`index` 从 1 递增 |
| `CollectHellos` | 客户端流 | `stream HelloRequest` → `HelloSummary` | 收完所有消息后汇总条数与 name 列表 |
| `Chat` | 双向流 | `stream ChatMessage` → `stream ChatMessage` | 每收一条回一条 `echo: <text>`，`index` 从 1 递增；客户端半关闭后结束 |

### 请求字段（`HelloRequest`）

| 字段 | 说明 |
| --- | --- |
| `name` | 名字，留空时按 `world` 处理 |
| `count` | 服务端流条数，默认 5，上限 50 |
| `delayMs` | 每条消息之间的等待毫秒数（一元是响应前等待），上限 30s，用来测超时/取消 |
| `fail` | 置为 `true` 时返回 `FailedPrecondition`，用来测错误展示 |

### 可以断言的内容

- 响应 Header：`x-demo-server: grpc-testserver`
- Trailers：一元方法为 `x-demo-method: unary`，流式方法为 `x-demo-count: <条数>`
- `HelloReply.metadata`：调用方发来的 metadata 回显（已去掉 `grpc-*`、`content-type`、`user-agent`）

## 在 API Tester 里测

1. 切到 gRPC 标签，Target 填 `localhost:50051`，不要勾 TLS
2. 点「发现服务」，选择 `demo.v1.Greeter`，再选方法
3. 按方法填请求 JSON

一元：

```json
{"name": "dbx"}
```

服务端流（3 条、每条间隔 200ms，方便观察流式进度）：

```json
{"name": "dbx", "count": 3, "delayMs": 200}
```

客户端流 / 双向流：点「添加消息」逐条填写，发送时先发完所有消息、半关闭流，再接收响应。

`CollectHellos` 填三条：

- 消息 1：`{"name": "alice"}`
- 消息 2：`{"name": "bob"}`
- 消息 3：`{"name": "carol"}`

`Chat` 填两条：

- 消息 1：`{"text": "hi"}`
- 消息 2：`{"text": "bye"}`

错误与超时：

```json
{"fail": true}
```

`{"delayMs": 5000}` 搭配 2s 超时可以看到超时状态；客户端的取消会传到这里，服务端立刻退出等待。

## 命令行验证（可选）

```bash
brew install grpcurl
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 describe demo.v1.Greeter

grpcurl -plaintext -H 'x-trace-id: abc-123' \
  -d '{"name":"cli"}' localhost:50051 demo.v1.Greeter/SayHello

grpcurl -plaintext -d '{"name":"cli","count":3,"delayMs":200}' \
  localhost:50051 demo.v1.Greeter/SayHelloStream
```

grpcurl 只支持一元和服务端流；客户端流与双向流用测试覆盖：

```bash
go test -v ./...
```

测试会在随机端口拉起同一个服务，覆盖四种调用方式、错误码、metadata/header/trailer 回显和 Reflection。

## 目录结构

```
proto/greeter.proto       # 服务与消息定义
gen/demo/v1/              # protoc 生成的 Go 代码（已提交，直接跑服务不需要装 protoc）
main.go                   # 服务实现与启动
server_test.go            # 四种调用方式的端到端测试
```

改了 proto 后重新生成：

```bash
protoc -I proto \
  --go_out=. --go_opt=module=github.com/jlian/dbx-plugin-api-tester/examples/grpc-testserver \
  --go-grpc_out=. --go-grpc_opt=module=github.com/jlian/dbx-plugin-api-tester/examples/grpc-testserver \
  proto/greeter.proto
```

需要 `protoc`、`protoc-gen-go`、`protoc-gen-go-grpc` 在 PATH 里。

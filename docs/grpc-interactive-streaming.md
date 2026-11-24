# gRPC 交互式双向流（方案 B）设计稿

> **状态：未实现。** 这是留给以后的设计记录，不是使用文档。
>
> 已经实现的是「方案 A+」：一次调用把多条消息全部发出去、半关闭、把服务端的响应全部收回来，
> 收与发并发执行以免大载荷互锁。A+ 覆盖了四种调用方式，但**没有**"看到服务端回复再决定下一条
> 发什么"的能力。本文件描述补上这项能力需要做什么、以及为什么现在没做。

---

## 1. 现在的边界

| 调用方式 | A+ 能做到 | A+ 做不到 |
| --- | --- | --- |
| 一元 | 1 条进、1 条出 | — |
| 服务端流 | 1 条进、N 条出 | 中途插话 |
| 客户端流 | N 条一次发完、1 条出 | 边发边等反馈 |
| 双向流 | N 条一次发完、N 条收齐 | **交替收发；按回复决定下一帧；实时看到进度** |

用 `examples/grpc-testserver` 的 `Chat` 说明差距：现在必须在点「调用」之前把
`{"text":"hi"}` 和 `{"text":"bye"}` 两条都填好。想改成"先发 hi，看到 `echo: hi` 之后
再输入下一句"，A+ 做不到——两条消息在发出时就已经定死了。

**A+ 的实现位置**（后续改动应该扩展它，而不是另起一套）：

- `backend/internal/grpctool/grpctool.go`
  - `invokeStream`：建流、并发收发、结果汇总
  - `receiveStream`：接收循环、达到上限时 `cancel()`
  - `outboundPayloads`：`messages` 数组与单条 `message` 的兼容
  - `streamKind`：`client` / `server` / `bidi`
- 前端 `src/lib/store.js` 的 `invokeGrpc`、`src/components/GrpcPanel.svelte` 的多消息编辑器

---

## 2. 前置验证（必须先做，约 30 分钟）

B 依赖一条**后端主动推前端**的通道。现在的 JSON-RPC 是"前端问、后端答"，没有这条路。

### 2.1 已经验证的部分

sidecar 能写出合法的 JSON-RPC 通知。实测 `Emitter.Event` 的输出：

```
{"jsonrpc":"2.0","method":"grpc/stream/message","params":{...}}   ← 没有 id，是通知
{"id":1,"jsonrpc":"2.0","result":{"ok":true}}                      ← 正常应答
```

两行可以共存于同一个 stdout；通知不带 `id`，不会被误当成应答。

### 2.2 尚未验证的部分

**宿主是否把 sidecar 的这条通知转发到前端的 `onEvent`。** 已知证据：DBX 安装包的二进制里
有 `host.events` 权限、Tauri 事件名 `dbx-plugin-event`、桥接对象上的 `onEvent`；官方文档
只给了 Rust SDK 的 `PluginEmitter` 例子。**没有端到端跑通过。**

### 2.3 验证步骤

1. `manifest.json` 的 `permissions` 加 `"host.events"`（现在是 `["host.storage"]`）。
2. `backend/main.go` 的 `Handle` 把 emitter 透出来（现在参数名是 `_`），加一个临时方法：

   ```go
   if method == "probe/emit" {
       _ = emitter.Event("plugin/probe", map[string]any{"seq": 1})
       return map[string]any{"ok": true}, nil
   }
   ```

3. 前端临时加：

   ```js
   const off = window.dbxPlugin.onEvent?.((event) => console.log("EVENT", event));
   await window.dbxPlugin.invoke("probe/emit", {});
   ```

4. `dbx-plugin dev --path . --port 5190`，在 DBX 里打开工作台，看控制台有没有 `EVENT`。

### 2.4 不通过怎么办

**不要用轮询硬撑。** 轮询（前端每 500ms 调 `grpc/stream/poll`）会丢帧、增加状态机复杂度、
体验比 A+ 更差。验证失败就停在 A+，把 B 继续搁置。

---

## 3. 设计

### 3.1 通道模型

两件事同时成立：

1. **调用要能跨多次 RPC 操作同一个流** → 后端用会话表保存 `grpc.ClientStream`。
2. **服务端的消息要能主动到前端** → 后端用 `Emitter.Event` 推，前端用 `onEvent` 收。

```
invoke("grpc/stream/open",  {target, service, method})  → {streamId}
invoke("grpc/stream/send",   {streamId, message})        → {seq}
invoke("grpc/stream/send",   {streamId, message})        → {seq}
invoke("grpc/stream/halfClose", {streamId})              → {}
invoke("grpc/stream/close",  {streamId})                 → {}

事件 grpc/stream/message {streamId, seq, message}   ← 后端主动推
事件 grpc/stream/status  {streamId, status, trailers, elapsedMs}
```

`streamId` 由后端生成（`crypto/rand` 十六进制），不复用请求 uid——我们的工作台是单编辑器，
没有集合里那种稳定 uid。

### 3.2 方法清单

| 方法 | 参数 | 返回 | 备注 |
| --- | --- | --- | --- |
| `grpc/stream/open` | target 参数 + `service` + `method` | `streamId` | 只建流，不发消息 |
| `grpc/stream/send` | `streamId` + `message` | `seq` | 同一流内串行执行 |
| `grpc/stream/halfClose` | `streamId` | — | 对应 `CloseSend`，**不关连接** |
| `grpc/stream/close` | `streamId` | — | 取消 + 回收，必须幂等 |
| `grpc/stream/list` | — | `streamId[]` | 可选，UI 重载后重新接管 |

新增方法要同步三处（`internal/rpc` 的方法名常量、`Methods()`、`Dispatch` 分支），
否则 `rpc_test.go` 的漂移检查会失败。

### 3.3 事件

- `grpc/stream/message`：每收到一条就推一次，带 `seq`（该流内从 1 递增），前端按 `seq` 追加/去重。
- `grpc/stream/status`：流终结时推一次，带最终 `status`、`trailers`、`elapsedMs`、`truncated`。
- 事件里**不得**出现 metadata 值、请求体等敏感内容（沿用现有日志边界）。

### 3.4 会话生命周期

```
open → active ──send──> active ──halfClose──> draining ──status──> closed
  │                      │                        │
  └──── close/cancel ────┴────────────────────────┘
```

- 每个会话持有自己的 `context.WithCancel`，**不能**沿用单次 `invoke` 的 deadline（见 3.5）。
- 空闲回收：`send`/`halfClose` 都没有活动超过 **60 秒**就自动 `close`（服务端一直不发消息时尤其重要）。
- 硬上限：单流最长 **10 分钟**、最多 **4 个并发流**，超出拒绝并给出可读错误。
- `close` 必须幂等：重复调用返回成功，不报错。
- 进程退出、插件被卸载/重载时清空所有会话——参考 Bruno 在 `window-all-closed` 时
  `clearAllConnections()` 的做法。**这是最容易漏掉、也最容易泄漏服务端连接的一步。**

### 3.5 超时模型

现在一次 `invoke` 是短调用：前端把超时夹在 `TIMEOUT_MAX = 110000`，后端 `maxGRPCTimeoutMs = 120000`。

交互式会话必然超过这个上限，所以：

- **不能**用一个长 `invoke` 挂着流；
- 会话的 ctx 不设总超时，由 `close`／空闲回收／硬上限来结束；
- 每次 `send` 这类短调用仍然带自己的超时（防止会话表被锁住）。

### 3.6 顺序与并发

SDK 对每个请求起一个 goroutine 分发，所以两次 `send` 可能并发到达。

- 每个会话加互斥锁，保证 `SendMsg` 串行执行，返回的 `seq` 就是实际发送顺序；
- 前端拿到 `seq` 之后再发下一条（或者本地排队），不要依赖调用先后。

### 3.7 背压：必须继续读

这一条是 A+ 已经踩过的坑，B 里更容易踩：

> 服务端发消息靠"我们读"来归还额度。我们一停读，服务端就卡在发送上；
> 它卡住就不再读我们发的东西，最后双向互锁。

所以：**即使前端来不及消费，后端也必须继续 `RecvMsg`。** 正确做法是有界队列：

- 队列满了就丢弃消息体，只累加 `dropped` 计数，继续接收；
- `status` 事件里带上 `dropped`，前端提示"有 N 条消息未展示"；
- 绝对不能"暂停读取"来实现背压。

（A+ 的做法是达到上限直接 `cancel()`，对一次性调用可以接受；交互式会话不该这么做，
因为用户还想继续聊。）

### 3.8 前端状态与路由

- `state.grpc.stream = { id, status, messages: [], dropped: 0 }`；
- `onEvent` 按 `streamId` 过滤，只处理当前会话；其他会话的事件直接丢弃；
- 页面卸载 / 切换请求 / 关闭工作台时调用 `grpc/stream/close`，并在失败时不阻塞 UI；
- 重载后如果需要接管已有会话，先调 `grpc/stream/list`（可选功能，可以第一版不做）。

### 3.9 持久化与历史

- 出站消息（`grpc.messages`）照旧存进草稿和历史，可以直接复用 A+ 的数据形状；
- **响应不入历史**：交互式会话无法重放，历史里只保留"请求是什么"；
- 界面上要明确区分"可以重放的一次性请求"和"已结束的交互会话"。

---

## 4. 代码落点

| 文件 | 改什么 |
| --- | --- |
| `backend/internal/grpctool/stream_session.go`（新） | 会话表、`open/send/halfClose/close`、事件推送、TTL 回收 |
| `backend/internal/grpctool/grpctool.go` | 复用 `receiveStream` 的接收逻辑；抽出事件回调接口 |
| `backend/internal/rpc/rpc.go` | `Dispatch` 增加 emitter 参数；登记 4 个新方法 |
| `backend/main.go` | 把现在被丢掉的 emitter 传下去 |
| `backend/internal/rpc/rpc_test.go` | 适配 `Dispatch` 签名；断言方法数量 |
| `manifest.json` | `permissions` 加 `host.events` |
| `src/lib/host.js` | 加 `subscribe(handler)`，包一层 `onEvent`（目前只有 `invoke`/`copy`/`createStorage`） |
| `src/lib/store.js` | 会话状态、事件路由、`openStream/sendMessage/halfClose/closeStream` |
| `src/components/GrpcPanel.svelte` | 每条消息独立的「发送」按钮（连接未建立时禁用）、断开/停止 |
| `src/components/ResponsePanel.svelte` | 增量追加而不是一次性渲染；显示 `dropped` |
| `src/lib/i18n.js` | 新文案，中英都要补 |

**不要改 `backend/sdk/`**：它是 CLI 分发的固定副本，升级才动。`Emitter` 已经够用。

`grpctool` 不直接依赖 SDK 类型：定义一个最小的 `EventSink interface { Event(method string, params any) error }`，
由 `main.go` 注入，这样会话层可以单测（用假的 sink 捕获事件）。

---

## 5. 实施顺序与验收

1. **通道验证**（第 2 节）。不通就停。
2. 会话层 + 单元测试（假 sink）：开流、发 3 条、断言收到 3 个事件、半关闭、收到 `status`。
3. `rpc` 接线 + `manifest` 权限。
4. 前端最小交互：消息列表、每条的发送按钮、增量追加。
5. 生命周期收尾：空闲回收、幂等 close、卸载清理。

**定义完成的验收用例**（这条用例 A+ 永远过不了，是 B 的判据）：

```
open Chat
send {"text":"hi"}      → 必须在 assistant 尚未发送下一条之前，收到 1 条 echo
send {"text":"bye"}     → 再收到 1 条 echo，seq 递增
halfClose               → 收到 status，流结束
```

手工验证靶子：`examples/grpc-testserver` 的 `Chat`（每收一条回一条，`index` 递增），
以及 `SayHelloStream` + `delayMs` 用来看"服务端还在推、我还能插话"的交叉场景。

---

## 6. 参考实现：Bruno

Bruno 4.3.0 的 gRPC 实现（Electron 主进程，`app.asar` 内
`node_modules/@usebruno/requests/src/grpc/grpc-client.js`）就是完整形态，可对照：

| Bruno 的做法 | 对我们的意义 |
| --- | --- |
| `activeConnections: Map<requestId, {rpc, client}>` | 会话表模型可行，代码量很小（收发/结束/取消合计不到 150 行） |
| `makeClientStreamRequest` / `makeBidiStreamRequest` | 与我们的 `NewStream` + `StreamDesc` 等价 |
| `end()` 只调 `rpc.end()`，注释明确"不关 channel，响应流可能还在跑" | 半关闭 ≠ 关闭，我们已有同样语义 |
| 终结事件（status/error/end/cancel）统一走 `#removeConnection` | 回收只留一个出口 |
| `#addConnection` 先 cancel 同 id 的旧连接 | 防止重复连接泄漏 |
| 窗口关闭时 `clearAllConnections()` | 全局兜底，必抄 |
| 每条消息在 UI 上有独立发送按钮 | 交互式的最小交互形状 |
| 把 `methodType` 持久化在请求上 | **不要学**：应从描述符实时算，避免 proto 变了以后漂移 |

Bruno 没有做"一次发完"的批量模式（它只有交互式）。我们反过来：先有 A+，B 是在它之外新增一条路径。

---

## 7. 风险

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| `host.events` 到不了前端 | B 不可做 | 先验证；不通就不做，别轮询 |
| 用户关页面/刷新，会话残留 | 服务端连接泄漏 | 空闲 TTL + 硬上限 + 卸载时 close |
| 前端消费慢 | 后端内存涨、甚至阻塞 | 有界队列 + 继续读 + `dropped` 计数 |
| 两次 `send` 并发到达 | 出站乱序 | 会话内互斥 + `seq` |
| 沿用单次 `invoke` 的超时 | 会话 2 分钟就断 | 会话独立 ctx（见 3.5） |
| 旧宿主不认识 `host.events` | 装不上/权限被拒 | 按 `capabilities` 探测，降级为只保留 A+ |

---

## 8. 明确不做的事

- 不做轮询兜底的"伪交互"。
- 不为 B 改 `backend/sdk/`。
- 不把交互式响应写进历史记录（无法重放，徒增困惑）。
- 不在 B 里重写 A+：一次性调用继续走 `invokeStream`，两条路径互不影响。

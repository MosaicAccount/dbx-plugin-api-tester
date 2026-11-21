# API Tester（API 调试台）— DBX 插件

在 DBX 工作台里调试 HTTP 接口、调用 gRPC 服务的插件。所有真实网络请求由插件内置的 Go sidecar 发起，沙箱 UI 只通过 `window.dbxPlugin` 与宿主通信。

## 能做什么

**HTTP**
- 常用方法、完整 URL、查询参数表格（自动编码并合并进 URL）
- 请求头编辑，JSON / 文本 / XML / 表单请求体，自动补充 `Content-Type`
- 超时、跟随/禁用重定向、跳过 TLS 校验（仅建议用于开发服务器）
- 响应状态、耗时、大小、响应头、重定向链
- JSON 响应自动格式化并语法高亮；二进制响应以 base64 展示；大响应按上限截断并提示

**gRPC**
- 基于 Server Reflection 自动发现 service / method
- 动态 protobuf 编解码，请求消息直接用 JSON 编辑
- 支持一元调用与服务端流式调用（客户端流、双向流暂不支持）
- Metadata 编辑、TLS、跳过 TLS 校验、超时；展示响应 Metadata 与 Trailers

**工作台**
- 请求保存/另存为、历史记录、草稿自动恢复
- 中英文、浅色/深色主题（跟随 DBX 注入的 `--color-*` / `--radius-*` / `--font-*` token）
- `Cmd/Ctrl + Enter` 发送请求

## 安装与使用

### 本地安装（.dbxp 文件）

1. 打开 DBX → **插件中心** → **设置**
2. 展开 **第三方与开发者选项**，打开 **允许安装未签名开发包**
3. 在 **安装插件包** 区域点击 **安装 .dbxp**，选择 `dist/com.jlian.apitester-*-darwin-arm64.dbxp`（按你的平台选择对应包）
4. 安装完成后，在 **插件中心 → 已安装 → API Tester → 工作台** 打开

### 插件中心安装（从 URL）

1. 在 **插件中心 → 设置 → 第三方与开发者选项** 打开 **允许安装未签名开发包**
2. 在 **安装插件包** 区域的 URL 输入框填入 `.dbxp` 地址（本地或 HTTPS），点击 **从 URL 安装**
3. 也可以直接打开 `dbx://plugins/install?url=<编码后的包地址>` 触发安装
4. 安装完成后从 **插件中心 → 已安装 → API Tester** 打开工作台

打开工作台后，也可以在插件中心的快捷入口设置里把它固定到侧边栏。

> **为什么“新建连接”里没有这个插件？**
> “新建连接”只列出声明了 `connection-provider` 的插件。API Tester 是工作台型插件（manifest 中只有 `workbench`），不提供数据库连接类型，所以不会出现在那里。请从插件中心的工作台入口打开。

### HTTP

1. 在 HTTP 标签填写方法与 URL
2. 用 **参数 / 请求头 / 请求体 / 选项** 配置请求
3. 点击 **发送**，在下方查看状态、耗时、响应头和响应体

### gRPC

1. 填写目标地址，例如 `localhost:50051`；TLS 服务勾选 **TLS**
2. 点击 **发现服务**（服务端需开启 Server Reflection）
3. 选择 service / method，填写 JSON 请求消息
4. 点击 **调用**；服务端流式方法会展示多条消息

## 项目结构

```
manifest.json            # 插件身份、权限、workbench 与 backend 入口
dbx-plugin.toml          # Go sidecar 构建信息与打包 include
assets/plugin.svg
ui/
  index.html             # 沙箱工作台入口
  styles.css             # 基于 DBX 主题 token 的样式
  app.js                 # 单文件前端逻辑（入口脚本会被宿主内联，勿用 import）
backend/
  main.go                # JSON-RPC 分发
  http.go                # HTTP 客户端
  grpc.go                # gRPC 反射、动态编解码、调用
  sdk/                   # 随插件 CLI 0.1.10 分发的 DBX Go SDK（见下）
```

## 开发

需要 Node.js 22+、Go 1.22+，以及 `@dbx-app/plugin-cli`：

```bash
npm install --global @dbx-app/plugin-cli
dbx-plugin dev --path . --port 5190
```

`dbx-plugin dev` 会编译 `backend/` 并在 `.dbx-dev/bin/` 下启动 sidecar，然后打开 `http://127.0.0.1:5190/`。`.dbx-dev/` 可能包含明文开发数据，已在 `.gitignore` 中忽略。

前端是纯静态资源，没有构建步骤；修改 `ui/` 后刷新页面即可。

### 测试

后端测试（HTTP 用 `httptest`，gRPC 用本地 health 服务 + reflection）：

```bash
cd backend
GOWORK=../.dbx-dev/go.work go test ./...
```

`GOWORK` 指向 dev 宿主生成的工作区；首次运行前先启动一次 `dbx-plugin dev`。前端改动至少执行：

```bash
node --check ui/app.js
```

### 打包

```bash
dbx-plugin package .
```

产物是当前平台（本机为 `darwin-arm64`）的未签名候选包 `dist/com.jlian.apitester-0.0.1-darwin-arm64.dbxp` 及其 `.artifact.json`。

### 多平台打包

带原生 sidecar 的插件不能交叉打包：`dbx-plugin package` 只允许当前主机对应的 target。Windows、Linux 和 Intel macOS 需要在对应平台上打包。

仓库的 `.github/workflows/plugin-release.yml` 在发布 GitHub Release 时用矩阵构建：

| target | runner |
| --- | --- |
| `linux-x64` | `ubuntu-24.04` |
| `linux-arm64` | `ubuntu-24.04-arm` |
| `windows-x64` | `windows-2022` |
| `darwin-x64` | `macos-15-intel` |
| `darwin-arm64` | `macos-15` |

每个 target 产出独立的 `.dbxp` 和 `.artifact.json`，作为同一个 Release 的资产。插件 ID、名称和版本在所有包里保持一致，DBX 安装时按用户的 OS/CPU 选择对应包。

也可以直接在对应系统上执行 `dbx-plugin package .`；本机只能产出 `darwin-arm64`。

## 安全边界

- 密码、Token 等敏感值不写入 `config`、Workbench context、事件或日志。
- sidecar 只把协议消息写到 stdout，日志写 stderr。
- sidecar 以当前用户权限运行，不是操作系统沙箱；发布前需要审查原生代码。

## Sidecar 与 SDK

`backend/sdk` 是插件 CLI 0.1.10 携带的官方 Go SDK 副本，`backend/go.mod` 通过相对路径 `replace` 使用它。这样做的原因是当前 Go 工作区在同时引入 gRPC/protobuf 依赖后会忽略工作区里对 SDK 模块的本地 `use`，转而去代理拉取并不存在的 `v0.1.0`。升级插件 CLI 时同步替换 `backend/sdk` 下的三个文件即可。

Go module 路径为 `github.com/jlian/dbx-plugin-api-tester`。

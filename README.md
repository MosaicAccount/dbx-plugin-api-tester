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
- 支持一元、服务端流、客户端流与双向流；流式方法一次发送全部消息，再接收响应
- Metadata 编辑、TLS、跳过 TLS 校验、超时；展示响应 Metadata 与 Trailers

**工作台**
- 多级目录树：侧栏顶部 `+` 或目录行 `+` 可新建请求/子目录，目录支持折叠与删除
- 请求保存/另存为、历史记录、草稿自动恢复；保存时从已有目录中选择，或直接新建目录
- 中英文、浅色/深色主题（跟随 DBX 注入的 `--color-*` / `--radius-*` / `--font-*` token）
- `Cmd/Ctrl + Enter` 发送请求，`Cmd/Ctrl + S` 保存，`Shift + Cmd/Ctrl + S` 另存为

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

> 双向流目前是「一次发送、一次接收」：所有消息要在点「调用」之前填好。想按服务端回复
> 决定下一条发什么的交互式用法尚未实现，设计记录见
> [docs/grpc-interactive-streaming.md](docs/grpc-interactive-streaming.md)。

## 项目结构

```
manifest.json            # 插件身份、权限、workbench 与 backend 入口
dbx-plugin.toml          # Go sidecar 构建信息、打包 include 与 [dev] 构建命令
vite.config.js           # 前端构建配置（相对 base + 构建成功信号）
package.json             # 前端依赖（Svelte 5 + Vite）
index.html               # Vite 入口页面
src/
  main.js                # 挂载 App，引入全局样式
  App.svelte             # 根组件：布局、快捷键、模态框编排
  styles.css             # 基于 DBX 主题 token 的样式（含深色主题）
  lib/
    host.js              # 唯一访问 window.dbxPlugin 的地方
    i18n.js              # 中英文词典与 t()
    model.js             # 领域常量与纯函数
    format.js            # JSON 高亮与响应体呈现
    store.js             # 全部可变状态与持久化
  components/            # SelectMenu / KVEditor / CodeEditor / SavedTree 等
ui/                      # Vite 构建产物（gitignore），宿主从这里加载
backend/
  main.go                # 仅装配：SDK 适配、panic 兜底、错误转换
  internal/apperr/       # 统一错误码与错误分类
  internal/values/       # 跨包共用类型
  internal/httptool/     # HTTP 请求执行
  internal/grpctool/     # gRPC 反射、动态编解码、调用
  internal/rpc/          # JSON-RPC 分发与方法登记
  sdk/                   # 随插件 CLI 0.1.10 分发的 DBX Go SDK（见下）
docs/                    # 设计记录（交互式双向流尚未实现）
assets/plugin.svg
```

技术栈：**Svelte 5（runes）+ Vite 7** 构建前端，**Go 1.22** 写 sidecar。宿主会把
`ui/index.html` 引用的脚本与样式内联进沙箱 iframe，所以 Vite 必须使用相对 `base`，
否则产物里的 `/assets/...` 会 404。

## 开发

需要 Node.js 22+、Go 1.22+，以及 `@dbx-app/plugin-cli`：

```bash
npm install --global @dbx-app/plugin-cli
dbx-plugin dev --path . --port 5190
```

首次运行先安装前端依赖：

```bash
npm install
```

`dbx-plugin dev` 会按 `dbx-plugin.toml` 的 `[dev]` 先跑 `npm run build`（产物写到 `ui/`），
再编译 `backend/` 并在 `.dbx-dev/bin/` 下启动 sidecar，最后打开 `http://127.0.0.1:5190/`。
`.dbx-dev/` 可能包含明文开发数据，已在 `.gitignore` 中忽略。

修改 `src/` 后 Vite 监听到变化会重新构建并输出 `DBX_UI_BUILD_SUCCESS`，dev 宿主据此重载页面；
也可以点页面上的「重载页面」手动刷新。修改 `backend/` 后点「重建后端」。

### 测试

前端构建与静态检查：

```bash
npm run build                                 # 必须出现 DBX_UI_BUILD_SUCCESS
npx svelte-check --tsconfig ./jsconfig.json   # 期望 0 errors 0 warnings
```

后端测试（HTTP 用 `httptest`，gRPC 起本地 health 服务 + reflection）：

```bash
cd backend
GOWORK=off go test ./...
GOWORK=off go vet ./...
```

`go.mod` 已用 `replace ... => ./sdk` 指向仓库内 SDK，所以 `GOWORK=off` 即可，
无需先启动 dev 宿主。`internal/rpc` 的测试会校验「登记的方法都已实现」，
新增 RPC 方法时如果不三处同步，这里会直接失败。

### 打包

`ui/` 是 Vite 产物且被 `.gitignore` 忽略，干净检出里并不存在。`dbx-plugin package` 只
负责**收集** `ui/`、不负责构建，缺少 `ui/index.html` 会直接报
`manifest UI entry 'ui/index.html' does not exist`。所以打包前必须先构建前端：

```bash
npm ci
npm run build          # 输出到 ui/，成功时打印 DBX_UI_BUILD_SUCCESS
dbx-plugin package .
```

本地产物是当前平台（本机为 `darwin-arm64`）的未签名候选包 `dist/com.jlian.apitester-0.1.0-darwin-arm64.dbxp` 及其 `.artifact.json`。

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

复用 workflow 只上传 `.dbxp` 与 `release-candidates.json`，随后由同一次运行的 `sidecars` job 调用 `scripts/backfill-release-sidecars.mjs` 补传每个候选包对应的 `.artifact.json`：先核对已上传包的 `sha256`/`size`，只补缺失项，已有的只校验不覆盖。需要给历史 Release 补时，手动触发该 workflow 并传入 tag：

```bash
gh workflow run plugin-release.yml -f tag=v0.1.0
```

也可以直接在对应系统上执行 `dbx-plugin package .`；本机只能产出 `darwin-arm64`。

## 安全边界

- 密码、Token 等敏感值不写入 `config`、Workbench context、事件或日志。
- sidecar 只把协议消息写到 stdout，日志写 stderr。
- sidecar 以当前用户权限运行，不是操作系统沙箱；发布前需要审查原生代码。
- 前端运行在不透明源 iframe：没有 Node、没有网络、没有 `localStorage` 与
  `navigator.clipboard`，一切跨边界操作只能经 `window.dbxPlugin`；仓库里只有
  `src/lib/host.js` 直接接触它。
- 错误经 `internal/apperr` 统一分类返回，`error.data.kind` 只承载分类标签，
  不回传内部堆栈。

## Sidecar 与 SDK

`backend/sdk` 是插件 CLI 0.1.10 携带的官方 Go SDK 副本，`backend/go.mod` 通过相对路径 `replace` 使用它。这样做的原因是当前 Go 工作区在同时引入 gRPC/protobuf 依赖后会忽略工作区里对 SDK 模块的本地 `use`，转而去代理拉取并不存在的 `v0.1.0`。升级插件 CLI 时同步替换 `backend/sdk` 下的三个文件即可。

Go module 路径为 `github.com/jlian/dbx-plugin-api-tester`。

## License

MIT

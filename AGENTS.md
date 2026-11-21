# AGENTS.md

面向 AI 编码助手的仓库规范。保持简短，优先遵守现有代码风格。

## 工作方式

- 先读 `manifest.json`、`ui/`、`backend/` 相关文件再修改；只做请求范围内的事。
- 不做无关重构，不添加推测性功能；没有必要时不写注释。
- 未经用户明确要求，不要执行 git commit、tag 或 push。
- `backend/sdk/` 是官方 SDK 的固定副本；除非任务就是升级 SDK，否则不要修改。
- 修改插件身份或版本时，同步 `manifest.json` 与 `backend/main.go`；插件 ID 固定为 `com.jerrymouse.apitester`。

## 插件边界

- UI 运行在沙箱 iframe：不要使用 Node.js、直接网络请求、`localStorage`、`navigator.clipboard`；一切通过 `window.dbxPlugin`。
- 宿主会内联入口脚本，`ui/app.js` 必须保持单文件，不要新增 ES module import。
- 用户可见文案必须同时提供 `en` 与 `zh`；颜色、圆角、字体使用 `var(--color-*)`、`var(--radius-*)`、`var(--font-*)`。
- sidecar 的 stdout 只输出 JSON-RPC，日志写 stderr；不得记录凭据、Token、metadata 值或请求体。
- 后端入口做参数校验：非法参数返回 `-32602`，网络/RPC 失败返回 `-32000`。

## 验证

- `node --check ui/app.js`
- `cd backend && GOWORK=../.dbx-dev/go.work go test ./...`
- 涉及界面或链路时，用 `dbx-plugin dev --path . --port 5190` 手动验证 HTTP 与 gRPC。
- 改打包配置后执行 `dbx-plugin package .`。
- 带原生 sidecar 的插件只能在目标平台打包，不要用 `--target` 交叉打包；多平台发布走 `.github/workflows/plugin-release.yml` 的矩阵。

## Git 提交

- 只有用户明确要求时才提交。
- 使用 Conventional Commits：`feat:`、`fix:`、`docs:`、`refactor:`、`test:`、`chore:`；祈使句，标题不超过 72 字符。
- 一个提交只做一件事；不要把格式化或重命名和行为修改混在一起。
- 可选正文说明“为什么”，不要逐行复述 diff；关联 issue 时写 `Refs #123`。
- 不提交 `dist/`、`.dbx-dev/`、`*.dbxp`、日志或任何密钥；它们应在 `.gitignore` 中。
- 不 force-push 共享分支，不重写已发布历史。
- PR 说明写清楚：改了什么、用户可见影响、如何验证。

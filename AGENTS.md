# AGENTS.md

面向 AI 编码助手的仓库规范。保持简短，优先遵守现有代码风格。

## 技术栈

| 层 | 选型 | 说明 |
| --- | --- | --- |
| 前端 | Svelte 5（runes）+ Vite 7 | 官方 `svelte` 模板同款；产物由 Vite 构建到 `ui/` |
| 后端 | Go 1.22 + 官方 DBX Go SDK | 以 sidecar 形式运行，stdio-jsonl JSON-RPC |
| 样式 | 原生 CSS + DBX 主题 token | 不引入 UI 框架或 CSS-in-JS，见 `src/styles.css` |

选框架的硬约束来自宿主：DBX 会把 `ui/index.html` 引用的入口脚本与样式表**内联**进沙箱
iframe，所以构建必须使用相对 `base`（`base: "./"`）。用绝对路径会解析到宿主根目录而 404。

## 目录结构

```
manifest.json          插件身份/权限/入口，改版本时与 backend/main.go 同步
dbx-plugin.toml        打包 include 与 [dev] 构建命令
vite.config.js         前端构建；相对 base + DBX_UI_BUILD_SUCCESS 构建信号
src/
  main.js              入口：挂载 App，引入全局样式
  app.d.ts             window.dbxPlugin 的最小类型声明
  App.svelte           根组件：布局装配、全局快捷键、模态框编排
  styles.css           全部样式（DBX token 驱动，含深色主题）
  lib/
    host.js            唯一允许访问 window.dbxPlugin 的地方
    i18n.js            en/zh 词典与 t()；新增文案两种语言都要补
    model.js           领域常量与纯函数（无 DOM、无宿主，可单测）
    format.js          JSON 高亮与响应体呈现（纯函数）
    store.js           全部可变状态与持久化 action
  components/          按界面区域拆分的展示组件
backend/
  main.go              仅装配：SDK 适配、panic 兜底、错误转换
  internal/apperr/     统一错误码与错误分类（唯一来源）
  internal/values/     跨包共用类型（KeyValue、ClampInt）
  internal/httptool/   HTTP 请求执行
  internal/grpctool/   gRPC 反射、proto 文件编译、动态编解码与调用
  internal/rpc/        JSON-RPC 分发、方法名登记、参数解码
  sdk/                 官方 Go SDK 固定副本，非升级任务不要改
scripts/              发布辅助脚本（不参与打包）
  backfill-release-sidecars.mjs  Release 缺失 .artifact.json 的补传
assets/plugin.svg
```

## 前端规范

- **状态只有一个来源**：所有可变状态在 `src/lib/store.js`，组件只读 `state` store、只调
  action。不要在组件里直接改数组或写 `plugin.storage`。
- **宿主访问必须经过 `lib/host.js`**。其他文件出现 `window.dbxPlugin` 视为越界。
- **纯函数放 `model.js` / `format.js`**：不碰 DOM、不碰宿主，这样能被直接测试。
- **响应式依赖要显式**：`$derived` 里只能直接引用 props/store。不要把依赖藏在内部
  `get(state)` 的函数里——那样变更不会触发重算（曾因此出现"新建目录不显示"的 bug）。
- **store 名不要叫 `state`**：Svelte 5 的 `$state` 是 rune，组件内以 `appState` 引入。
- **组件只发事件不自作主张**：需要用户输入的流程（保存命名、删除确认）由 `App.svelte`
  用模态框收集，把结果回传给 action；store 不弹窗。
- **用户可见文案必须同时提供 `en` 与 `zh`**，写在 `lib/i18n.js`，不要在组件里硬编码中文。
- **颜色/圆角/字体只用 token**：`var(--color-*)`、`var(--radius-*)`、`var(--font-*)`，
  禁止写死色值，否则深色主题会坏。
- **沙箱限制**：没有 Node、直接网络请求、`localStorage`、`navigator.clipboard`。
  持久化用 `host.createStorage()`，剪贴板用 `host.copy()`。
- 新增界面区块时优先拆成 `components/` 下的组件，不要让 `App.svelte` 继续变长。

## 后端规范

- **`main.go` 只做装配**。业务逻辑进 `internal/<领域>` 包，禁止在 main 里堆 switch。
- **错误码只能来自 `internal/apperr`**：参数问题用 `apperr.Invalid`（-32602），
  执行期/上游失败用 `apperr.Upstream(kind, ...)`（-32000），内部异常用 `apperr.Internal`
  （-32603）。不要手写数字字面量。
- **错误必须带 `Kind`**：前端靠 `error.data.kind` 选择排错提示（timeout/tls/dns/
  connection/auth/reflection/proto）。新增分类要同时更新 `apperr` 与前端 `ERROR_HINT_KEYS`。
- **新增 RPC 方法三处同步**：`internal/rpc` 里的方法名常量、`Methods()` 列表、`Dispatch`
  分支；前端 `store.js` 的 `invoke` 第一个参数要一致。`rpc_test.go` 会检查登记与实现是否漂移。
- **参数校验在边界做完**：非法参数返回 `-32602`，不要让它流到网络层才炸。
- **stdout 只输出 JSON-RPC**，日志一律 `log.Printf` 走 stderr；不得记录凭据、Token、
  metadata 值或请求体。
- **跨包类型放 `internal/values`**，避免各包重复定义 `KeyValue` 造成结构漂移。
- 插件 ID 固定 `com.jlian.apitester`；改版本时同步 `manifest.json` 与 `backend/main.go`。

## 验证

前端（改 `src/` 或样式后）：

```bash
npm run build                      # 必须输出 DBX_UI_BUILD_SUCCESS
npx svelte-check --tsconfig ./jsconfig.json   # 期望 0 errors 0 warnings
npm run test:unit                  # tests/ 下纯函数单测（node --test，无浏览器依赖）
```

后端：

```bash
cd backend && GOWORK=off go test ./...
cd backend && GOWORK=off go vet ./...
```

发布脚本（改 `scripts/` 后）：

```bash
npm run test:scripts
node scripts/backfill-release-sidecars.mjs --tag <tag> --dry-run   # 只读，验证补传计划
```

`AGENTS.md` 早期写的 `GOWORK=../.dbx-dev/go.work` 会因 Go 要求绝对路径而直接失败；
`go.mod` 已用 `replace ... => ./sdk` 指向本地 SDK，所以 `GOWORK=off` 是正确用法。

链路（涉及界面或宿主交互时）：

```bash
dbx-plugin dev --path . --port 5190
```

打开 `http://127.0.0.1:5190/` → 打开工作台 → 手动验证 HTTP / gRPC / 目录树 / 语言 / 主题。
需要读脱敏日志时用 `/api/diagnostics`。`.dbx-dev/` 含明文开发数据，禁止提交。

打包（改打包配置后）：

```bash
npm run build                     # 必须先构建，ui/ 不入库且 package 不会自动构建
dbx-plugin package .
```

`ui/` 是构建产物，干净检出里不存在；漏掉 `npm run build` 时 `dbx-plugin package` 会报
`manifest UI entry 'ui/index.html' does not exist`。CI 里由 `plugin-release.yml` 的
`package-command` 一并执行 `npm ci && npm run build`。

带原生 sidecar 的插件只能在目标平台打包，不要用 `--target` 交叉打包；多平台发布走
`.github/workflows/plugin-release.yml` 的矩阵。

## 分支与版本号

- **`main` 只允许正式版本号**：`main` 上的版本必须是可以直接发布、直接打 tag 的 `X.Y.Z`，
  不允许出现 `-SNAPSHOT`、`-dev`、`-rc`、`-beta` 之类的后缀。在 `main` 上看到后缀，就说明
  发版流程漏了收敛版本这一步。
- **开发分支用「下一个正式版本 + 预发布后缀」**：长期开发分支 `dev` 和临时分支
  `feat/*`、`fix/*` 都算开发分支。从 `main` 切出来后第一件事是把版本改成
  `X.Y.(Z+1)-SNAPSHOT`（例：`main` 是 `0.1.0` → 开发分支写 `0.1.1-SNAPSHOT`），并单独提交
  一次 `chore: 开发分支版本号切换到 0.1.1-SNAPSHOT`，不要和功能提交混在一起。有破坏性改动
  或成组新特性时按 SemVer 取 `0.2.0-SNAPSHOT` / `1.0.0-SNAPSHOT`，不要为了省事一律 +1。
- **后缀写法跟生态标准走，但同一时间只用一种**：本仓库统一用 `-SNAPSHOT`（DBX manifest 的
  `version` 正则 `^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$` 和 npm semver 都接受）。
  换到别的语言/生态就换成那边的标准写法（Java/Maven `-SNAPSHOT`、Rust `-alpha.N`、
  Python `.devN`、Go 模块用伪版本或 `-dev.N`），不要自造 `-dev-final-v2` 这类没有标准
  含义的后缀，否则升级比较和商店版本排序都会失去意义。
- **开发分支的版本号不许发布**：`-SNAPSHOT` 产物只能本地 `dbx-plugin dev` 或内部安装验证，
  不进 Release、不写商店候选、不动 `.dbx-store.json`。
- **合回 `main` 后再收敛版本，然后才发版**：开发分支的功能合并进 `main` 后，先按下面的清单
  把所有版本号位置从 `X.Y.Z-SNAPSHOT` 改成 `X.Y.Z`，跑一遍 `npm run build` 和后端测试，
  单独提交 `chore(release): bump version to X.Y.Z`，按 PR 规范合入并推送到 `main`、确认 CI 全绿，
  **最后**才打 annotated tag `vX.Y.Z` → `gh release create`。不要先打 tag 再补版本号，
  也不要在发布后回头改 tag 或覆盖资产。
- **每次发版后把开发分支推到下一版**：`main` 打出 `X.Y.Z` 之后，`dev` 先 merge（或 rebase 到）
  `main`，再把版本提到 `X.Y.(Z+1)-SNAPSHOT`；否则开发分支带着已发布的版本号继续开发，
  下次发版就对不上了。
- **发版前逐项检查版本号**（漏一处就会让商店候选的 `version` 或 sidecar 身份与包内 manifest
  对不上，签名阶段才会暴露）：

  ```bash
  # 1) 清单文件与 Go 常量：值必须都等于即将发布的 X.Y.Z
  grep -n '"version"' manifest.json package.json package-lock.json   # lock 内有两处
  grep -n 'pluginVersion =' backend/main.go

  # 2) 文档与商店元数据里的版本引用
  grep -n 'apitester-' README.md          # 本地产物名示例
  grep -n 'tree/v' .dbx-store.json        # source 必须指向新 tag

  # 3) 兜底：在即将发版的 main 上，除 AGENTS.md 的约定说明外不应再有 SNAPSHOT 残留
  #    （在 dev 分支上这条会命中各版本文件本身，属正常）
  grep -rn --exclude-dir={node_modules,dist,.git,.dbx-dev,sdk} 'SNAPSHOT' .
  ```

  不要跟着改的东西：`backend/go.mod` 里的 SDK `v0.1.0`、README 里历史 tag 的示例命令、
  `scripts/*.test.mjs` 里的 `-0.1.0-`——它们是依赖版本、文档示例和测试夹具，不是插件版本。

## Git 提交

- 只有用户明确要求时才提交。
- 使用 Conventional Commits，**类型前缀保持英文、冒号后的描述用中文**：
  `feat:`、`fix:`、`docs:`、`refactor:`、`test:`、`chore:` 等前缀不翻译；
  描述用祈使句中文，标题整体不超过 72 字符。例如 `fix: 修正客户端流取消后未回收会话`。
- 一个提交只做一件事；不要把格式化或重命名和行为修改混在一起。
- 可选正文说明"为什么"，不要逐行复述 diff；关联 issue 时写 `Refs #123`。
- 不提交 `dist/`、`.dbx-dev/`、`node_modules/`、`ui/`（构建产物）、`*.dbxp`、日志或密钥。
- 不 force-push 共享分支，不重写已发布历史。
- PR 按下面的 `## PR 规范` 提交。

## PR 规范

- **署名只用本人身份**：提交作者和提交者都必须是
  `MosaicAccount <66773914+MosaicAccount@users.noreply.github.com>`（或 `meetJlian@gmail.com`）。
  不要自己拼 `xxx@users.noreply.github.com`——拼错会把无关账号绑定成提交者，PR 侧栏凭空多出
  "2 participants"（曾把 `jlian@users.noreply.github.com` 写进 `dbx-store` 的 PR，结果关联到一个
  真实存在的陌生人）。提交后用 `git log -1 --format='%an <%ae> | %cn <%ce>'` 核对一遍。
- **动公共历史前先问**：对公开仓库的 PR 分支 force-push、改写提交、关闭或重开 PR，都先跟用户确认，
  不要自己决定。
- **标题**：英文 Conventional Commits，不超过 72 字符，和仓库已有同类 PR 保持一致
  （如 `chore(automation): register owner/repo for auto-update`）。
- **正文**：中文，写给维护者看，固定三段「改了什么 / 用户可见影响 / 如何验证」；跨仓库 PR
  （如 `t8y2/dbx-store`）同样适用。
- **只写结论，不写过程**：不要出现"我这边跑了…""接着我又验证了…"这类对话或工作日志口吻，也不要把
  会话里的临时输出整段贴上去。验证命令可以贴，但写在正文里的结论必须是自己复跑过的。
- **数字必须对得上**：正文里的 `sha256`、`size`、target 数量、版本号发之前逐一核对，核不实就写核不实。
- **提完自检**：`gh pr view <n> --json changedFiles` 确认只改了预期文件；页面上 participants 只应有本人；
  CI 状态自己看一遍，红了要能解释原因。

## 发版与商店

- **版本号必须已收敛为正式版**：`manifest.json`、`backend/main.go` 的 `pluginVersion`、
  `package.json`、`package-lock.json`（lock 内两处）、README 里的产物名示例、`.dbx-store.json`
  的 `source` 六处都要等于 `X.Y.Z` 且不带后缀，逐项清单见 `## 分支与版本号`。
  `rpc_test.go` 里的版本是测试夹具，不跟随。
- **顺序**：`X.Y.Z-SNAPSHOT` → `X.Y.Z` 的版本提交经 PR 合入 `main` 并确认 CI 全绿 →
  打 annotated tag `vX.Y.Z` → `gh release create`
  （标题 `API Tester vX.Y.Z`，说明用中文写「变化 / 安装 / 校验」）。`plugin-release.yml` 监听
  `release: published`，五个平台各自构建（`darwin-arm64`、`darwin-x64`、`linux-x64`、
  `linux-arm64`、`windows-x64`）；本机不要 `--target` 交叉打包。
- **Release 资产**：复用 workflow 只上传 `.dbxp` 与 `release-candidates.json`；每包
  `.artifact.json` 由发布 workflow 之后的 `sidecars` job 自动补传
  （`scripts/backfill-release-sidecars.mjs`）。补传前会用 `release-candidates.json` 逐项核对
  已上传包的 `sha256`/`size`，已有的 sidecar 只校验不覆盖。不要手工 `gh release upload` 补，
  也不要绕过脚本改这两个字段——脚本失败就说明 Release 本身有问题。
- **`.dbx-store.json` 每版都要改**：商店同步按 Release 的 **tag** 读取该文件，`releaseNotes`
  不会自动跟版本走，`source` 应指向当期 tag。`releaseNotes` 只能写英文基线——`localizations`
  只接受 `name` / `description`，写别的字段会被 `validate.mjs` 拒绝。
- **商店目录在 `t8y2/dbx-store`**：候选文件是 `candidates/<plugin-id>.json`，首次上架还要
  `publishers/<publisher-id>.json`；登记在 `automation/plugin-sources.json` 里
  `autoUpdate: true` 的仓库由每小时同步自动开候选 PR，首次或改展示字段才手工提。
- **签名边界**：不填 `signingKeyId`、不自建密钥、不写 `verified: true`。未签名包只是审核输入；
  签名由维护者对候选 PR 跑 `/sign`，之后 `candidates/` 被删除，`plugins/`、`catalog/` 由工作流
  生成，禁止手改。
- **已发布的东西不要动**：签名制品与 Release 资产不可覆盖，改内容必须递增版本。tag 一旦被签名
  或被同步消费就不能移动；只有尚未被消费、且只改不进包的文件（如 `.dbx-store.json`）时，经用户
  确认后才可移动 tag，包字节必须保持不变。移动后 `raw.githubusercontent.com` 有约 5 分钟缓存，
  验证元数据要用 `gh api repos/<owner>/<repo>/contents/...?ref=<tag>` 或等缓存过期。
- **PR 文案**：按 `## PR 规范`（英文 Conventional 标题 + 中文三段正文）；跨仓库 PR 如 `dbx-store`
  同样适用。

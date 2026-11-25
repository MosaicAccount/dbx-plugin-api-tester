/**
 * 领域常量与纯函数。
 *
 * 这里不碰 DOM、不碰宿主，只做数据归一化和计算，因此可以被单元测试直接覆盖。
 */

export const HTTP_TIMEOUT_DEFAULT = 30000;
export const GRPC_TIMEOUT_DEFAULT = 20000;
export const TIMEOUT_MAX = 110000;
export const HISTORY_LIMIT = 30;
export const DRAFT_LIMIT = 200000;
export const SAVE_LIMIT = 240000;
export const BRIDGE_BODY_LIMIT = 1800000;
export const NEW_FOLDER = "__new__";

/**
 * 导入 proto 文件的限制。
 *
 * 文件内容会随请求快照一起写进宿主存储（单值上限 256 KiB），所以前端比后端更早
 * 收紧总量：留出消息、metadata 等字段的空间，避免"能导入但保存失败"。
 */
export const PROTO_FILE_LIMIT = 128 * 1024;
export const PROTO_TOTAL_LIMIT = 160 * 1024;
export const PROTO_FILE_COUNT_LIMIT = 64;

export const IS_MAC =
  typeof navigator === "undefined" ? false : /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent || "");
export const SAVE_SHORTCUT = IS_MAC ? "⌘S" : "Ctrl+S";
export const SAVE_AS_SHORTCUT = IS_MAC ? "⇧⌘S" : "Ctrl+Shift+S";

export const BODY_CONTENT_TYPES = {
  json: "application/json",
  text: "text/plain",
  xml: "application/xml",
  form: "application/x-www-form-urlencoded",
};

export const BODY_TYPES = ["none", "json", "text", "xml", "form"];
export const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
export const PROTOCOLS = ["http", "grpc"];

/** @returns {{ name: string, value: string, enabled: boolean }} */
export function blankKV() {
  return { name: "", value: "", enabled: true };
}

export function defaultHttp() {
  return {
    method: "GET",
    url: "",
    params: [blankKV()],
    headers: [blankKV()],
    bodyType: "none",
    bodyText: "",
    timeoutMs: HTTP_TIMEOUT_DEFAULT,
    followRedirects: true,
    skipTlsVerify: false,
  };
}

export function defaultGrpc() {
  return {
    target: "",
    tls: false,
    skipTlsVerify: false,
    // reflection：从服务端反射发现方法；file：用导入的 .proto 文件解析（适合未开反射的服务）。
    schemaMode: "reflection",
    protoFiles: [],
    service: "",
    method: "",
    metadata: [blankKV()],
    // 消息用数组：一元与服务端流只用第一条，客户端流/双向流可以有多条。
    // 统一成数组后，切换方法类型不需要重排请求体。
    // 空白保持空字符串，让编辑器显示"输入 JSON 消息"的占位提示，而不是看起来像真实内容的 {}。
    messages: [""],
    timeoutMs: GRPC_TIMEOUT_DEFAULT,
    services: [],
    methods: [],
  };
}

export function clampNumber(value, fallback, minimum, maximum) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) return fallback;
  return Math.max(minimum, Math.min(maximum, Math.round(parsed)));
}

export function normalizeKV(value) {
  if (!Array.isArray(value)) return [blankKV()];
  const rows = value
    .filter((item) => item && typeof item === "object")
    .map((item) => ({
      name: item.name === undefined || item.name === null ? "" : String(item.name),
      value: item.value === undefined || item.value === null ? "" : String(item.value),
      enabled: item.enabled !== false,
    }));
  return rows.length ? rows : [blankKV()];
}

/**
 * 归一化 gRPC 请求消息列表。
 *
 * 新快照写的是 `messages` 数组；旧快照只有单条 `message` 字符串，这里顺手迁移，
 * 避免升级后打开历史记录发现请求体是空的。空数组视为"没填过"，回退到默认值。
 * 旧版本的默认值 `{\n  \n}` 是纯占位，语义等于空消息，统一清空以免被误认为是真内容。
 */
export function normalizeGrpcMessages(grpc) {
  const rows = Array.isArray(grpc?.messages)
    ? grpc.messages.filter((item) => typeof item === "string")
    : typeof grpc?.message === "string"
      ? [grpc.message]
      : [];
  return trimGrpcMessages(rows.map(blankGrpcTemplate));
}

/**
 * 去掉尾部空消息，至少保留一条。
 *
 * 多消息编辑器里"加了但没填"的空消息没有信息量；不清理的话保存、恢复或切换方法后
 * 会看到一长串空编辑器，很容易被误认为"默认有 N 个消息"。
 */
export function trimGrpcMessages(messages) {
  const rows = (Array.isArray(messages) ? messages : []).filter((item) => typeof item === "string");
  let end = rows.length;
  while (end > 1 && !String(rows[end - 1] || "").trim()) end -= 1;
  return end > 0 ? rows.slice(0, end) : [""];
}

/** 空的大括号消息等价于空消息；旧快照里的 `{}` 占位统一清空。 */
export function blankGrpcTemplate(value) {
  const text = typeof value === "string" ? value : "";
  return /^\{\s*\}$/.test(text) ? "" : text;
}

/**
 * 归一化导入的 proto 文件。
 *
 * 前端只保留 basename 作为文件名：浏览器多选文件时拿不到目录结构，后端也按
 * basename 编译，两边保持一致。同名文件后导入的覆盖先导入的。
 */
export function normalizeGrpcProtoFiles(value) {
  if (!Array.isArray(value)) return [];
  const files = [];
  const seen = new Set();
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const rawName = String(item.name || "").trim().replace(/\\/g, "/");
    const name = rawName.slice(rawName.lastIndexOf("/") + 1);
    const content = typeof item.content === "string" ? item.content : "";
    if (!name || !content.trim()) continue;
    if (seen.has(name)) continue;
    seen.add(name);
    files.push({ name, content });
  }
  return files;
}

/** 目录路径归一化：统一分隔符、去重斜杠、去首尾斜杠。 */
export function normalizeFolder(value) {
  return String(value || "")
    .trim()
    .replace(/\s+/g, " ")
    .replace(/\\/g, "/")
    .replace(/\/{2,}/g, "/")
    .replace(/^\/+|\/+$/g, "");
}

export function folderParent(folder) {
  const value = normalizeFolder(folder);
  const index = value.lastIndexOf("/");
  return index > 0 ? value.slice(0, index) : "";
}

/** 返回 `a`、`a/b`、`a/b/c` 这样的全部祖先路径。 */
export function folderAncestors(folder) {
  const value = normalizeFolder(folder);
  if (!value) return [];
  const parts = value.split("/");
  const paths = [];
  for (let index = 1; index <= parts.length; index += 1) paths.push(parts.slice(0, index).join("/"));
  return paths;
}

/** 合并已保存请求所在目录与显式登记的目录，得到完整目录列表。 */
export function collectFolders(folderIndex, savedIndex) {
  const folders = new Set((folderIndex || []).map(normalizeFolder).filter(Boolean));
  for (const item of savedIndex || []) {
    const folder = normalizeFolder(item.folder);
    if (folder) for (const path of folderAncestors(folder)) folders.add(path);
  }
  return [...folders].sort((a, b) => a.localeCompare(b));
}

export function contentTypeForBody(bodyType) {
  return BODY_CONTENT_TYPES[bodyType] || "";
}

/**
 * 把启用的参数行合并进 URL。
 *
 * 返回 null 表示 URL 非法，调用方需要据此提示用户，而不是静默发送一个坏请求。
 */
export function buildRequestUrl(rawUrl, params) {
  const raw = String(rawUrl || "").trim();
  if (!raw) return null;
  let parsed;
  try {
    parsed = new URL(raw);
  } catch {
    return null;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;
  for (const row of params || []) {
    if (row.enabled === false) continue;
    const name = String(row.name || "").trim();
    if (!name) continue;
    parsed.searchParams.append(name, row.value);
  }
  return parsed.toString();
}

/** 拼出发送用的请求头，并按请求体类型补 Content-Type。 */
export function buildHeaders(headers, bodyType) {
  const result = [];
  for (const row of headers || []) {
    if (row.enabled === false) continue;
    const name = String(row.name || "").trim();
    if (!name) continue;
    result.push({ name, value: row.value });
  }
  const contentType = contentTypeForBody(bodyType);
  if (contentType && !result.some((header) => header.name.toLowerCase() === "content-type")) {
    result.push({ name: "Content-Type", value: contentType });
  }
  return result;
}

/** 收集启用的键值行，用于 gRPC metadata 等场景。 */
export function enabledEntries(rows) {
  const result = [];
  for (const row of rows || []) {
    if (row.enabled === false) continue;
    const name = String(row.name || "").trim();
    if (!name) continue;
    result.push({ name, value: row.value });
  }
  return result;
}

export function byteLength(text) {
  return new TextEncoder().encode(String(text ?? "")).length;
}

export function truncate(text, limit) {
  const value = String(text || "");
  return value.length > limit ? `${value.slice(0, limit - 1)}…` : value;
}

export function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

export function uid() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return `id-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/** 响应状态码对应的语义色彩。 */
export function statusTone(value) {
  if (value >= 500) return "err";
  if (value >= 400) return "warn";
  if (value >= 300) return "info";
  return "ok";
}

/**
 * 把一份快照叠加到当前请求状态上。
 *
 * 草稿、已保存请求和历史记录都走这里，保证反序列化时的容错规则只有一份：
 * 字段缺失或类型不对时回落到默认值，而不是把坏数据塞进表单。
 */
export function applySnapshot(snapshot) {
  const next = {
    protocol: "http",
    http: defaultHttp(),
    grpc: defaultGrpc(),
    responseTab: "body",
  };
  if (!snapshot || typeof snapshot !== "object") return next;

  if (snapshot.protocol === "http" || snapshot.protocol === "grpc") next.protocol = snapshot.protocol;

  const http = snapshot.http && typeof snapshot.http === "object" ? snapshot.http : {};
  next.http = {
    ...defaultHttp(),
    bodyType: typeof http.bodyType === "string" ? http.bodyType : "none",
    method: typeof http.method === "string" && http.method.trim() ? http.method.trim().toUpperCase() : "GET",
    url: typeof http.url === "string" ? http.url : "",
    bodyText: typeof http.bodyText === "string" ? http.bodyText : "",
    params: normalizeKV(http.params),
    headers: normalizeKV(http.headers),
    timeoutMs: clampNumber(http.timeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    followRedirects: http.followRedirects !== false,
    skipTlsVerify: Boolean(http.skipTlsVerify),
  };
  if (next.http.bodyType !== "none" && !BODY_CONTENT_TYPES[next.http.bodyType]) next.http.bodyType = "none";

  const grpc = snapshot.grpc && typeof snapshot.grpc === "object" ? snapshot.grpc : {};
  next.grpc = {
    ...defaultGrpc(),
    target: typeof grpc.target === "string" ? grpc.target : "",
    service: typeof grpc.service === "string" ? grpc.service : "",
    method: typeof grpc.method === "string" ? grpc.method : "",
    schemaMode: grpc.schemaMode === "file" ? "file" : "reflection",
    protoFiles: normalizeGrpcProtoFiles(grpc.protoFiles),
    metadata: normalizeKV(grpc.metadata),
    messages: normalizeGrpcMessages(grpc),
    timeoutMs: clampNumber(grpc.timeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    tls: Boolean(grpc.tls),
    skipTlsVerify: Boolean(grpc.skipTlsVerify),
  };

  next.responseTab = snapshot.responseTab === "headers" ? "headers" : "body";
  return next;
}

/** 生成用于持久化的快照，剔除仅存在于运行期的字段（services / methods）。 */
export function toSnapshot(state) {
  return {
    protocol: state.protocol,
    http: {
      method: state.http.method,
      url: state.http.url,
      params: state.http.params.map((row) => ({ ...row })),
      headers: state.http.headers.map((row) => ({ ...row })),
      bodyType: state.http.bodyType,
      bodyText: state.http.bodyText,
      timeoutMs: state.http.timeoutMs,
      followRedirects: state.http.followRedirects,
      skipTlsVerify: state.http.skipTlsVerify,
    },
    grpc: {
      target: state.grpc.target,
      tls: state.grpc.tls,
      skipTlsVerify: state.grpc.skipTlsVerify,
      schemaMode: state.grpc.schemaMode,
      protoFiles: state.grpc.protoFiles.map((file) => ({ ...file })),
      service: state.grpc.service,
      method: state.grpc.method,
      metadata: state.grpc.metadata.map((row) => ({ ...row })),
      messages: trimGrpcMessages(state.grpc.messages),
      timeoutMs: state.grpc.timeoutMs,
    },
    responseTab: state.responseTab,
  };
}

/** 侧栏显示的一行摘要。 */
export function summarize(payload, untitled) {
  if (payload.protocol === "grpc") {
    const grpc = payload.grpc || {};
    if (grpc.service && grpc.method) return truncate(`${grpc.service}/${grpc.method}`, 54);
    return truncate(grpc.method || grpc.target || untitled, 54);
  }
  const http = payload.http || {};
  let path = http.url || "";
  try {
    const parsed = new URL(path);
    path = `${parsed.pathname}${parsed.search}`;
  } catch {
    // 非法 URL 就直接用原文，摘要不需要报错。
  }
  return truncate(`${(http.method || "GET").toUpperCase()} ${path}`.trim(), 54);
}

/**
 * 请求方式：HTTP 用大写动词，gRPC 用方法名（服务名在标题里已经能看到）。
 *
 * 侧栏列表只用这一列，避免把「GET /api/users?page=2」这种长摘要塞进第二行。
 */
export function requestMethod(payload) {
  if (!payload) return "";
  if (payload.protocol === "grpc") return String(payload.grpc?.method || "").trim();
  return String(payload.http?.method || "GET").trim().toUpperCase();
}

/**
 * 读取索引条目里的请求方式。
 *
 * 旧版本的索引条目只存了 summary，没有单独的 method 字段，这里做一次兜底解析，
 * 免得升级后老请求在侧栏里丢掉方式这一列。
 */
export function entryMethod(item) {
  const explicit = String(item?.method || "").trim();
  if (explicit) return explicit;
  const summary = String(item?.summary || "").trim();
  if (!summary) return "";
  if (item?.protocol === "grpc") {
    const slash = summary.lastIndexOf("/");
    return slash >= 0 ? summary.slice(slash + 1) : summary;
  }
  const token = summary.split(/\s+/)[0].toUpperCase();
  return HTTP_METHODS.includes(token) ? token : "";
}

/**
 * 协议标签色名：HTTP 与 gRPC 各一种，由样式表映射到主题 token。
 */
export function protocolTone(protocol) {
  return protocol === "grpc" ? "grpc" : "http";
}

/**
 * 请求方式标签色名。
 *
 * HTTP 按动词区分（get/post/put/patch/delete/head/options），未知动词退回 other。
 */
export function methodTone(method) {
  const value = String(method || "").trim().toUpperCase();
  return HTTP_METHODS.includes(value) ? value.toLowerCase() : "other";
}

/**
 * 侧栏一行里要不要单独显示请求方式。
 *
 * 只有 HTTP 显示动词标签：gRPC 没有 HTTP 动词，方法名本身就在请求名称里，
 * 再放一个标签只会重复并挤占宽度。
 */
export function shouldShowMethod(item) {
  return item?.protocol !== "grpc" && Boolean(entryMethod(item));
}

/**
 * 历史记录行的标题。
 *
 * 历史条目没有自定义名称，摘要形如「GET /hello」或「service/Method」：
 * - HTTP 去掉开头的动词，方式由彩色标签呈现，避免同一信息出现两次。
 * - gRPC 保留完整「service/Method」，因为不再单独显示方法标签。
 */
export function historyTitle(item) {
  const summary = String(item?.summary || "").trim();
  if (!summary || item?.protocol === "grpc") return summary;
  const method = entryMethod(item);
  if (!method) return summary;
  const prefix = `${method} `;
  return summary.toUpperCase().startsWith(prefix.toUpperCase()) ? summary.slice(prefix.length) : summary;
}

/** 保存对话框里的建议名称。 */
export function suggestName(payload, untitled) {
  if (payload.protocol === "grpc") {
    const grpc = payload.grpc || {};
    if (grpc.service && grpc.method) return `${grpc.service}/${grpc.method}`;
    return grpc.target || untitled;
  }
  const http = payload.http || {};
  try {
    const parsed = new URL(http.url);
    return `${http.method || "GET"} ${parsed.pathname || "/"}`;
  } catch {
    return truncate(http.url || `${http.method || "GET"} request`, 48);
  }
}

/**
 * 把后端返回的错误映射成人话提示。
 *
 * 后端已经在 error.data.kind 里给出了分类，优先用它；没有 data 时退回关键词匹配，
 * 以兼容旧版本 sidecar。
 */
export function describeErrorKind(error) {
  const kind = error?.data?.kind;
  if (typeof kind === "string" && kind) return kind;
  const text = String(error?.message || error || "").toLowerCase();
  if (!text) return "";
  if (text.includes("timed out") || text.includes("timeout") || text.includes("deadline")) return "timeout";
  if (text.includes("certificate") || text.includes("x509") || text.includes("tls") || text.includes("handshake")) return "tls";
  if (text.includes("reflection")) return "reflection";
  if (text.includes("no such host") || text.includes("dns") || text.includes("resolve host")) return "dns";
  if (text.includes("unauthenticated") || text.includes("permission")) return "auth";
  if (text.includes("connection refused") || text.includes("unavailable") || text.includes("cannot connect") || text.includes("connection")) return "connection";
  return "";
}

/** 错误分类到文案键的映射，与 i18n 词典保持一致。 */
export const ERROR_HINT_KEYS = {
  timeout: "hintTimeout",
  tls: "hintTls",
  reflection: "hintReflection",
  dns: "hintDns",
  connection: "hintConnect",
  auth: "hintAuth",
  proto: "hintProto",
};

export function errorMessage(error, fallback) {
  if (!error) return fallback;
  if (typeof error === "string") return error;
  return error.message || String(error);
}

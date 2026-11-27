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

/**
 * multipart 上传的限制。
 *
 * 文件不再塞进单个 JSON-RPC 消息：按 MULTIPART_UPLOAD_CHUNK 分块交给 sidecar
 * 暂存到临时文件，发送时流式拼成正文。因此上限只是"别把用户磁盘写满"的保守值，
 * 不是技术上限，与 BRIDGE_BODY_LIMIT 无关。后端 internal/uploads 与 httptool
 * 里有同样的值；要支持更大的单文件，改这三处常量即可。
 */
export const MULTIPART_FILE_LIMIT = 1 * 1024 * 1024 * 1024;
export const MULTIPART_TOTAL_LIMIT = 5 * 1024 * 1024 * 1024;
export const MULTIPART_FILE_COUNT_LIMIT = 100;
export const MULTIPART_UPLOAD_CHUNK = 512 * 1024;
/** 文件行字段名留空时使用的默认 part 名（绝不拿文件名顶替用户填的字段名）。 */
export const MULTIPART_DEFAULT_PART_NAME = "file";

/**
 * 交互式会话（客户端流 / 双向流）的时限。
 *
 * 默认值与后端 stream_session.go 保持一致；后端还会再校验一次，
 * 前端先收敛是为了让用户在失焦时就看到合法值，而不是点了发送才报错。
 */
export const STREAM_WAIT_DEFAULT = 20000;
export const STREAM_WAIT_MIN = 5000;
export const STREAM_WAIT_MAX = 60000;
export const STREAM_IDLE_DEFAULT = 180000;
export const STREAM_IDLE_MIN = 30000;
export const STREAM_IDLE_MAX = 900000;
export const STREAM_DURATION_DEFAULT = 1800000;
export const STREAM_DURATION_MIN = 60000;
export const STREAM_DURATION_MAX = 7200000;

/**
 * 把响应体流式保存到本地文件时的限额。
 *
 * 大小未知，所以走"边收边写"：每次从 sidecar 读一块、写一块，内存里只留一块。
 * 读块大小以宿主 beginSave 返回的 chunkBytes 为准，缺省用 DOWNLOAD_CHUNK_DEFAULT。
 */
export const DOWNLOAD_CHUNK_DEFAULT = 256 * 1024;
/** 单次 read 的等待上限；到点没有新数据后端会作废会话并返回 timeout。 */
export const DOWNLOAD_READ_TIMEOUT_MS = 30000;
/** 宿主没给出文件名时保存对话框用的兜底名。 */
export const DOWNLOAD_FALLBACK_NAME = "download.bin";

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

export const BODY_TYPES = ["none", "json", "text", "xml", "form", "multipart"];
export const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
export const PROTOCOLS = ["http", "grpc"];

/** @returns {{ name: string, value: string, enabled: boolean }} */
export function blankKV() {
  return { name: "", value: "", enabled: true };
}

export function defaultHttp(settings) {
  return {
    method: "GET",
    url: "",
    params: [blankKV()],
    headers: [blankKV()],
    bodyType: "none",
    bodyText: "",
    // 表单行由 Form 与 Multipart Form 共用：文本行发普通字段，文件行可以挂
    // 多个文件（同一个字段名生成多个 part）。文件内容暂存在 sidecar，快照只带
    // 文件名 / 类型 / 大小，重新打开后需要重新选择。
    formRows: [],
    timeoutMs: clampNumber(settings?.httpTimeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    followRedirects: true,
    skipTlsVerify: false,
  };
}

export function defaultGrpc(settings) {
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
    timeoutMs: clampNumber(settings?.grpcTimeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    // 交互式会话（客户端流 / 双向流）的时限；一元与服务端流不使用。
    stream: defaultStreamOptions(),
    services: [],
    methods: [],
  };
}

/** 交互式会话的时限选项，随请求快照一起保存。 */
export function defaultStreamOptions() {
  return {
    waitMs: STREAM_WAIT_DEFAULT,
    idleTimeoutMs: STREAM_IDLE_DEFAULT,
    maxDurationMs: STREAM_DURATION_DEFAULT,
  };
}

/** 归一化会话时限；缺失或非法值回落到默认，超出范围按边界收敛。 */
export function normalizeStreamOptions(value) {
  const options = value && typeof value === "object" ? value : {};
  return {
    waitMs: clampNumber(options.waitMs, STREAM_WAIT_DEFAULT, STREAM_WAIT_MIN, STREAM_WAIT_MAX),
    idleTimeoutMs: clampNumber(options.idleTimeoutMs, STREAM_IDLE_DEFAULT, STREAM_IDLE_MIN, STREAM_IDLE_MAX),
    maxDurationMs: clampNumber(options.maxDurationMs, STREAM_DURATION_DEFAULT, STREAM_DURATION_MIN, STREAM_DURATION_MAX),
  };
}

/** 默认超时偏好：新建请求使用它，单个请求仍可在「选项」里覆盖。 */
export function defaultSettings() {
  return { httpTimeoutMs: HTTP_TIMEOUT_DEFAULT, grpcTimeoutMs: GRPC_TIMEOUT_DEFAULT };
}

/** 归一化持久化的超时设置；缺失或非法值回落到内置默认。 */
export function normalizeSettings(value) {
  const settings = value && typeof value === "object" ? value : {};
  return {
    httpTimeoutMs: clampNumber(settings.httpTimeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    grpcTimeoutMs: clampNumber(settings.grpcTimeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
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
 * 新建一行表单部件；文本字段与文件部件共用一个列表，靠 kind 区分。
 * @returns {{ kind: "text" | "file", name: string, value: string, files: Array<object>, enabled: boolean }}
 */
export function blankFormRow(kind = "text") {
  return {
    kind: kind === "file" ? "file" : "text",
    name: "",
    value: "",
    files: [],
    enabled: true,
  };
}

/**
 * 归一化表单行。
 *
 * 只保留有内容可发的行（文本行要有字段名或值，文件行至少挂一个文件）；文件内容与
 * 暂存 id 都不进快照，恢复后 files 里没有 uploadId，UI 据此提示重新选择。
 * 旧快照里的单文件字段（fileName / size / contentBase64）会迁移成 files[0]。
 */
export function normalizeFormRows(value) {
  if (!Array.isArray(value)) return [];
  const rows = [];
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const kind = item.kind === "file" ? "file" : "text";
    const name = item.name === undefined || item.name === null ? "" : String(item.name);
    const enabled = item.enabled !== false;
    if (kind === "file") {
      const files = normalizeFormFiles(item);
      if (!files.length) continue;
      rows.push({ kind, name, value: "", files, enabled });
      continue;
    }
    const value = item.value === undefined || item.value === null ? "" : String(item.value);
    if (!name.trim() && !value) continue;
    rows.push({ kind, name, value, files: [], enabled });
  }
  return rows;
}

/** 归一化一行里的文件列表；兼容合并前的单文件行。 */
export function normalizeFormFiles(item) {
  const raw = Array.isArray(item.files)
    ? item.files
    : item.fileName
      ? [{ fileName: item.fileName, contentType: item.contentType, size: item.size }]
      : [];
  const files = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "object") continue;
    const fileName = String(entry.fileName || "").trim();
    if (!fileName) continue;
    const size = Number(entry.size);
    files.push({
      id: typeof entry.id === "string" && entry.id ? entry.id : uid(),
      fileName,
      contentType: typeof entry.contentType === "string" ? entry.contentType : "",
      size: Number.isFinite(size) && size > 0 ? Math.round(size) : 0,
      // 暂存 id 只属于当前运行期；快照里不写，恢复后自然为空。
      uploadId: "",
      status: "missing",
      progress: 0,
    });
  }
  return files;
}

/** 启用的文件行里挂着的文件，按行展开。 */
export function formFileEntries(rows) {
  const entries = [];
  for (const row of Array.isArray(rows) ? rows : []) {
    if (!row || row.enabled === false || row.kind !== "file") continue;
    for (const file of Array.isArray(row.files) ? row.files : []) entries.push({ row, file });
  }
  return entries;
}

export function formFileCount(rows) {
  return formFileEntries(rows).length;
}

/** 文件原始字节合计，用来和 MULTIPART_TOTAL_LIMIT 比较（文本行不占额度）。 */
export function multipartTotalBytes(rows) {
  return formFileEntries(rows).reduce((sum, entry) => sum + (Number(entry.file.size) || 0), 0);
}

/** 选择文件后先做配额预检，避免把 60 MB 传进 sidecar 才被拒绝。 */
export function planFormFiles(rows, items, options = {}) {
  const list = Array.isArray(rows) ? rows : [];
  const replaceRow = Number.isInteger(options.rowIndex) ? list[options.rowIndex] : null;
  const replaced =
    options.fileId && replaceRow?.kind === "file"
      ? (replaceRow.files || []).find((file) => file.id === options.fileId)
      : null;
  let total = multipartTotalBytes(list) - (Number(replaced?.size) || 0);
  let count = formFileCount(list) - (replaced ? 1 : 0);

  const accepted = [];
  const rejected = [];
  for (const item of Array.isArray(items) ? items : []) {
    const size = Number(item.size) || 0;
    if (item.error === "size" || item.error === "read") {
      rejected.push({ fileName: item.fileName, reason: item.error, source: item });
      continue;
    }
    if (size > MULTIPART_FILE_LIMIT) {
      rejected.push({
        fileName: item.fileName,
        reason: "size",
        size,
        limit: MULTIPART_FILE_LIMIT,
        source: item,
      });
      continue;
    }
    if (count >= MULTIPART_FILE_COUNT_LIMIT) {
      rejected.push({
        fileName: item.fileName,
        reason: "count",
        current: count,
        limit: MULTIPART_FILE_COUNT_LIMIT,
        source: item,
      });
      continue;
    }
    if (total + size > MULTIPART_TOTAL_LIMIT) {
      rejected.push({
        fileName: item.fileName,
        reason: "total",
        size,
        total: total + size,
        limit: MULTIPART_TOTAL_LIMIT,
        source: item,
      });
      continue;
    }
    total += size;
    count += 1;
    accepted.push(item);
  }
  return { accepted, rejected };
}

/** 常见扩展名到 Content-Type；识别不出时按二进制流处理。 */
const MULTIPART_CONTENT_TYPES = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  svg: "image/svg+xml",
  pdf: "application/pdf",
  json: "application/json",
  xml: "application/xml",
  txt: "text/plain",
  csv: "text/csv",
  zip: "application/zip",
  gz: "application/gzip",
  tar: "application/x-tar",
  mp4: "video/mp4",
  mp3: "audio/mpeg",
  wav: "audio/wav",
};

export function guessContentType(fileName) {
  const name = String(fileName || "");
  const dot = name.lastIndexOf(".");
  const extension = dot >= 0 ? name.slice(dot + 1).toLowerCase() : "";
  return MULTIPART_CONTENT_TYPES[extension] || "application/octet-stream";
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

/** 取文件名本身，和后端 normalizeProtoName 的 basename 规则一致。 */
function protoBaseName(value) {
  return String(value || "")
    .trim()
    .replace(/\\/g, "/")
    .split("/")
    .pop();
}

/** 与后端 normalizeProtoImports 同一条规则：import 只认 basename，google/protobuf/* 是内置的。 */
const PROTO_IMPORT_PATTERN = /^[ \t]*import[ \t]+(?:public[ \t]+|weak[ \t]+)?"([^"\r\n]+)"/gm;

/** 读出一段 proto 源码 import 的其他文件（basename，已跳过内置的 google/protobuf/*）。 */
function protoImportsOf(content) {
  const imports = [];
  for (const match of String(content || "").matchAll(PROTO_IMPORT_PATTERN)) {
    const path = match[1];
    if (path.startsWith("google/protobuf/")) continue;
    const target = protoBaseName(path);
    if (!target || imports.includes(target)) continue;
    imports.push(target);
  }
  return imports;
}

/** 找出直接 import 了指定文件的那些文件（删除前用来提示"会把谁弄坏"）。 */
export function protoDependents(files, name) {
  const target = protoBaseName(name);
  const dependents = [];
  for (const file of files) {
    const base = protoBaseName(file.name);
    if (!base || base === target) continue;
    if (protoImportsOf(file.content).includes(target)) dependents.push(base);
  }
  return dependents.sort();
}

/**
 * 找出因缺少 import 而编译不过的文件。
 *
 * 只用于界面提示：编译的权威判定仍在 sidecar，漏报就退回后端报错。规则刻意和后端
 * 保持一致（去掉目录前缀、跳过 google/protobuf/*），注释掉的 import 不会被匹配到。
 *
 * @param {{ name: string, content: string }[]} files
 * @returns {{ missing: Map<string, string[]>, broken: Set<string> }}
 *   missing：缺失的文件名 → 直接依赖它的文件；broken：自身或依赖链上有缺失的文件。
 */
export function protoImportGaps(files) {
  const names = files.map((file) => protoBaseName(file.name));
  const present = new Set(names);
  const imports = new Map();
  const directMissing = new Map();

  for (const file of files) {
    const name = protoBaseName(file.name);
    const wanted = protoImportsOf(file.content);
    imports.set(name, wanted);
    const absent = wanted.filter((target) => !present.has(target));
    if (absent.length) directMissing.set(name, absent);
  }

  const missing = new Map();
  for (const [name, absent] of directMissing) {
    for (const target of absent) missing.set(target, [...(missing.get(target) || []), name]);
  }

  // 依赖链上有坏文件也算坏：删掉 common.proto 时 chain_top.proto 同样无法编译。
  const broken = new Set(directMissing.keys());
  for (let changed = true; changed; ) {
    changed = false;
    for (const [name, wanted] of imports) {
      if (broken.has(name)) continue;
      if (!wanted.some((target) => broken.has(target))) continue;
      broken.add(name);
      changed = true;
    }
  }

  return { missing, broken };
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

/**
 * 把表单键值行拼成 application/x-www-form-urlencoded 正文。
 *
 * 用 URLSearchParams 而不是手写 `encodeURIComponent`：空格要编码成 `+`，
 * 且和浏览器 / curl 的 `--data-urlencode` 行为一致。
 */
export function buildFormBody(rows) {
  const params = new URLSearchParams();
  for (const row of rows || []) {
    if (row.enabled === false) continue;
    const name = String(row.name || "").trim();
    if (!name) continue;
    params.append(name, row.value === undefined || row.value === null ? "" : String(row.value));
  }
  return params.toString();
}

/** 把旧快照里的 urlencoded 正文解析成键值行；解析不出内容时返回空数组。 */
export function parseFormBody(text) {
  const raw = typeof text === "string" ? text.trim() : "";
  if (!raw) return [];
  const rows = [];
  for (const pair of raw.split("&")) {
    if (!pair) continue;
    const separator = pair.indexOf("=");
    const rawName = separator < 0 ? pair : pair.slice(0, separator);
    const rawValue = separator < 0 ? "" : pair.slice(separator + 1);
    rows.push({ name: decodeFormComponent(rawName), value: decodeFormComponent(rawValue), enabled: true });
  }
  return rows;
}

/** 解码单个 urlencoded 片段；非法转义保留原文，避免整条正文被丢掉。 */
function decodeFormComponent(value) {
  const text = String(value || "").replace(/\+/g, " ");
  try {
    return decodeURIComponent(text);
  } catch {
    return text;
  }
}

/**
 * 请求头里应该补的 Content-Type。
 *
 * multipart 的 boundary 只有 sidecar 知道，这里返回空串、由后端设置；
 * 其余类型沿用固定映射。
 */
export function httpContentType(http) {
  const bodyType = BODY_TYPES.includes(http?.bodyType) ? http.bodyType : "none";
  if (bodyType === "multipart") return "";
  if (bodyType !== "form") return contentTypeForBody(bodyType);
  return BODY_CONTENT_TYPES.form;
}

/**
 * 把表单行拆成 sidecar 需要的文本字段与文件部件。
 *
 * 文件只传 uploadId（内容已经分块暂存在 sidecar），字段名留空时统一用 "file"，
 * 不会把文件名当成字段名，也不会覆盖用户填过的值。
 */
export function multipartPayload(rows) {
  const fields = [];
  const files = [];
  for (const row of Array.isArray(rows) ? rows : []) {
    if (!row || row.enabled === false) continue;
    const name = String(row.name || "").trim();
    if (row.kind === "file") {
      for (const file of Array.isArray(row.files) ? row.files : []) {
        if (!file?.uploadId) continue;
        files.push({
          name: name || MULTIPART_DEFAULT_PART_NAME,
          fileName: file.fileName || "file",
          contentType: file.contentType || guessContentType(file.fileName),
          uploadId: file.uploadId,
        });
      }
      continue;
    }
    if (name) fields.push({ name, value: row.value === undefined || row.value === null ? "" : String(row.value) });
  }
  return { fields, files };
}

/**
 * 组装 HTTP 正文参数。
 *
 * 纯函数：表单按内容自动选择编码——有文件（或勾了强制 multipart）时只把字段与文件
 * 交给 sidecar，由后端生成 boundary 与 Content-Type；否则在这里拼 urlencoded 正文。
 * 发送前的体积校验留在 store。
 */
export function buildBodyParams(http) {
  const bodyType = BODY_TYPES.includes(http?.bodyType) ? http.bodyType : "none";
  if (bodyType === "multipart") {
    return { bodyEncoding: "multipart", bodyText: "", multipart: multipartPayload(http.formRows || []) };
  }
  if (bodyType === "form") {
    const rows = (Array.isArray(http.formRows) ? http.formRows : []).filter((row) => row.kind !== "file");
    return { bodyEncoding: "text", bodyText: buildFormBody(rows) };
  }
  return {
    bodyEncoding: bodyType === "none" ? "none" : "text",
    bodyText: bodyType === "none" ? "" : http.bodyText,
  };
}

/** 拼出发送用的请求头，并在用户没填 Content-Type 时补上推断出的值。 */
export function buildHeaders(headers, contentType) {
  const result = [];
  for (const row of headers || []) {
    if (row.enabled === false) continue;
    const name = String(row.name || "").trim();
    if (!name) continue;
    result.push({ name, value: row.value });
  }
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
  if (bytes < 1024 * 1024) return `${Number((bytes / 1024).toFixed(1))} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${Number((bytes / (1024 * 1024)).toFixed(2))} MB`;
  // GB 去掉多余的 .00：5 GB 比 5.00 GB 好读。
  return `${Number((bytes / (1024 * 1024 * 1024)).toFixed(2))} GB`;
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

/** 大小写不敏感地取一个响应头的值；取不到时返回空串。 */
export function headerValue(headers, name) {
  if (!Array.isArray(headers)) return "";
  const wanted = String(name || "").toLowerCase();
  for (const entry of headers) {
    if (String(entry?.name || "").toLowerCase() === wanted) return String(entry?.value || "");
  }
  return "";
}

/** Content-Disposition 是否是「附件」：只有 attachment 才表示服务器在给文件。 */
export function isAttachmentDisposition(value) {
  return /^\s*attachment\b/i.test(String(value || ""));
}

/** 文件名只留最后一段，避免把 `../` 或 Windows 路径画到界面上。 */
function displayFileName(value) {
  return String(value || "")
    .trim()
    .split(/[\\/]/)
    .filter(Boolean)
    .pop() || "";
}

/**
 * 从 Content-Disposition 里取文件名，只用于界面展示。
 *
 * 优先 RFC 5987 的 `filename*`（带字符集和百分号编码），退回普通 `filename`；
 * 解析不出来就返回空串，由调用方回落到 URL。
 */
export function dispositionFileName(value) {
  const header = String(value || "");
  if (!header) return "";
  const encoded = /filename\*\s*=\s*([^;]+)/i.exec(header);
  if (encoded) {
    const raw = encoded[1].trim().replace(/^["']|["']$/g, "");
    const parts = raw.split("'");
    const payload = parts.length >= 3 ? parts.slice(2).join("'") : raw;
    try {
      return displayFileName(decodeURIComponent(payload));
    } catch {
      return displayFileName(payload);
    }
  }
  const plain = /filename\s*=\s*(?:"([^"]*)"|([^;]+))/i.exec(header);
  if (!plain) return "";
  return displayFileName(plain[1] ?? plain[2] ?? "");
}

/**
 * 这次响应是不是一份文件。
 *
 * 判断只看响应本身：正文被判定为二进制，或服务器用 Content-Disposition: attachment
 * 明确声明了附件。文本接口（JSON / HTML / 纯文本）不给下载入口——普通调试顺手把响应
 * 存成文件，比「想下载时找不到按钮」更容易让人误会。
 *
 * 所以下载入口是「拿到响应之后」才出现的：请求发出前无从判断对方要返回什么。
 */
export function isDownloadResponse(response) {
  if (!response || response.kind !== "done" || response.protocol !== "http") return false;
  const data = response.data;
  if (!data) return false;
  if (data.bodyKind === "binary") return true;
  return isAttachmentDisposition(headerValue(data.headers, "content-disposition"));
}

/**
 * 文件响应的完整大小（字节）；判断不出来时返回 0。
 *
 * 优先读 `Content-Length`：正文可能在展示上限（3 MB）处被截断，`sizeBytes` 只是"读进
 * 内存的长度"，而 `Content-Length` 是这次响应的真实总字节数——下载保存的就是这么多。
 * 响应头里没有（chunked）时退回 `sizeBytes`，但只在正文没被截断时才可信：截断后它的
 * 值是把上限，报出来会让人以为文件就这么大。
 */
export function responseFileSizeBytes(data) {
  const declared = Number(headerValue(data?.headers, "content-length"));
  if (Number.isFinite(declared) && declared > 0) return declared;
  if (data?.truncated) return 0;
  const received = Number(data?.sizeBytes);
  return Number.isFinite(received) && received > 0 ? received : 0;
}

/**
 * 把一份快照叠加到当前请求状态上。
 *
 * 草稿、已保存请求和历史记录都走这里，保证反序列化时的容错规则只有一份：
 * 字段缺失或类型不对时回落到默认值，而不是把坏数据塞进表单。
 *
 * @param {any} snapshot
 * @param {{ httpTimeoutMs?: number, grpcTimeoutMs?: number }} [settings]
 *   用户设置的默认超时；旧快照没有 timeoutMs 时用它补齐。
 */
/**
 * 恢复表单行，兼容几种旧快照：
 * - `formRows`：当前格式（文本 / 文件混排，文件行可挂多个文件）；
 * - `multipartRows`：multipart 类型合并前的混排行；
 * - `formFields`：更早的 urlencoded 键值行；
 * - `bodyText`：最早只有 urlencoded 文本。
 */
function restoreFormRows(http, bodyType) {
  if (Array.isArray(http.formRows)) return normalizeFormRows(http.formRows);
  if (Array.isArray(http.multipartRows)) return normalizeFormRows(http.multipartRows);
  if (Array.isArray(http.formFields)) {
    return normalizeFormRows(http.formFields.map((row) => ({ ...row, kind: "text" })));
  }
  if (bodyType !== "form" || typeof http.bodyText !== "string") return [];
  return normalizeFormRows(parseFormBody(http.bodyText).map((row) => ({ ...row, kind: "text" })));
}

export function applySnapshot(snapshot, settings) {
  const defaults = normalizeSettings(settings);
  const next = {
    protocol: "http",
    http: defaultHttp(defaults),
    grpc: defaultGrpc(defaults),
    responseTab: "body",
  };
  if (!snapshot || typeof snapshot !== "object") return next;

  if (snapshot.protocol === "http" || snapshot.protocol === "grpc") next.protocol = snapshot.protocol;

  const http = snapshot.http && typeof snapshot.http === "object" ? snapshot.http : {};
  const rawBodyType = typeof http.bodyType === "string" ? http.bodyType : "none";
  const formRows = restoreFormRows(http, rawBodyType);
  // 合并期快照用 forceMultipart / 文件行表示"发 multipart"；现在类型显式，
  // 打开这类旧请求时直接收敛到 multipart，界面不会再出现那个勾选框。
  let bodyType = rawBodyType;
  if (bodyType === "form" && (http.forceMultipart === true || formRows.some((row) => row.kind === "file"))) {
    bodyType = "multipart";
  }
  next.http = {
    ...defaultHttp(defaults),
    bodyType,
    method: typeof http.method === "string" && http.method.trim() ? http.method.trim().toUpperCase() : "GET",
    url: typeof http.url === "string" ? http.url : "",
    bodyText: typeof http.bodyText === "string" ? http.bodyText : "",
    formRows,
    params: normalizeKV(http.params),
    headers: normalizeKV(http.headers),
    timeoutMs: clampNumber(http.timeoutMs, defaults.httpTimeoutMs, 1, TIMEOUT_MAX),
    followRedirects: http.followRedirects !== false,
    skipTlsVerify: Boolean(http.skipTlsVerify),
  };
  if (!BODY_TYPES.includes(next.http.bodyType)) next.http.bodyType = "none";

  const grpc = snapshot.grpc && typeof snapshot.grpc === "object" ? snapshot.grpc : {};
  next.grpc = {
    ...defaultGrpc(defaults),
    target: typeof grpc.target === "string" ? grpc.target : "",
    service: typeof grpc.service === "string" ? grpc.service : "",
    method: typeof grpc.method === "string" ? grpc.method : "",
    schemaMode: grpc.schemaMode === "file" ? "file" : "reflection",
    protoFiles: normalizeGrpcProtoFiles(grpc.protoFiles),
    metadata: normalizeKV(grpc.metadata),
    messages: normalizeGrpcMessages(grpc),
    timeoutMs: clampNumber(grpc.timeoutMs, defaults.grpcTimeoutMs, 1, TIMEOUT_MAX),
    tls: Boolean(grpc.tls),
    skipTlsVerify: Boolean(grpc.skipTlsVerify),
    stream: normalizeStreamOptions(grpc.stream),
  };

  // "request" 是「实际请求」页：快照里保留，回看历史时仍停在原来那一页。
  next.responseTab = ["headers", "request"].includes(snapshot.responseTab) ? snapshot.responseTab : "body";
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
      // 文件内容与暂存 id 故意不落快照：恢复后由 UI 要求重新选择。
      formRows: normalizeFormRows(state.http.formRows).map((row) =>
        row.kind === "file"
          ? {
              kind: "file",
              name: row.name,
              enabled: row.enabled,
              files: row.files.map((file) => ({
                fileName: file.fileName,
                contentType: file.contentType,
                size: file.size,
              })),
            }
          : { kind: "text", name: row.name, value: row.value, enabled: row.enabled },
      ),
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
      stream: normalizeStreamOptions(state.grpc.stream),
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
 * 从 URL 里猜一个下载文件名，只用于 start 返回之前的占位显示。
 *
 * 真正的文件名以 `http/download/start` 返回的 fileName（来自 Content-Disposition）为准；
 * 这里猜不出来就返回空串，由界面回落到"下载中"。
 */
export function fileNameFromUrl(value) {
  try {
    const parsed = new URL(String(value || ""));
    const segment = parsed.pathname.split("/").filter(Boolean).pop() || "";
    if (!segment) return "";
    try {
      return decodeURIComponent(segment);
    } catch {
      return segment;
    }
  } catch {
    return "";
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
  if (text.includes("canceled") || text.includes("cancelled")) return "canceled";
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
  canceled: "hintCanceled",
  timeout: "hintTimeout",
  tls: "hintTls",
  reflection: "hintReflection",
  dns: "hintDns",
  connection: "hintConnect",
  auth: "hintAuth",
  proto: "hintProto",
  // 下载专用的分类：非 2xx 由 download/start 直接 reject，响应体没有保存。
  status: "hintStatus",
};

export function errorMessage(error, fallback) {
  if (!error) return fallback;
  if (typeof error === "string") return error;
  return error.message || String(error);
}

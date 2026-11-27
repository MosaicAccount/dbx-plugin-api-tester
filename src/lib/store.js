import { writable, get } from "svelte/store";
import * as host from "./host.js";
import { translator } from "./i18n.js";
import {
  BRIDGE_BODY_LIMIT,
  DRAFT_LIMIT,
  GRPC_TIMEOUT_DEFAULT,
  HISTORY_LIMIT,
  HTTP_TIMEOUT_DEFAULT,
  MULTIPART_FILE_COUNT_LIMIT,
  MULTIPART_TOTAL_LIMIT,
  MULTIPART_UPLOAD_CHUNK,
  PROTO_FILE_COUNT_LIMIT,
  PROTO_FILE_LIMIT,
  PROTO_TOTAL_LIMIT,
  SAVE_LIMIT,
  STREAM_DURATION_DEFAULT,
  STREAM_DURATION_MAX,
  STREAM_DURATION_MIN,
  STREAM_IDLE_DEFAULT,
  STREAM_IDLE_MAX,
  STREAM_IDLE_MIN,
  STREAM_WAIT_DEFAULT,
  STREAM_WAIT_MAX,
  STREAM_WAIT_MIN,
  TIMEOUT_MAX,
  applySnapshot,
  blankFormRow,
  blankKV,
  buildBodyParams,
  buildHeaders,
  buildRequestUrl,
  byteLength,
  clampNumber,
  collectFolders,
  defaultGrpc,
  defaultHttp,
  defaultSettings,
  describeErrorKind,
  enabledEntries,
  errorMessage,
  folderAncestors,
  folderParent,
  formatBytes,
  formFileEntries,
  guessContentType,
  httpContentType,
  multipartTotalBytes,
  planFormFiles,
  normalizeFolder,
  normalizeSettings,
  requestMethod,
  summarize,
  toSnapshot,
  truncate,
  uid,
} from "./model.js";
import { responseText } from "./format.js";

/**
 * 应用状态仓库。
 *
 * 职责边界：
 * - 这里持有全部可变的业务状态，并负责持久化（草稿 / 已保存 / 历史）。
 * - 组件只读 store、只调这里导出的 action；不直接写数组，不直接访问宿主。
 * - 需要用户输入的流程（保存命名、删除确认）由组件用模态框收集，再把结果回传给这里的
 *   action。状态层不弹窗，保证可以在测试里直接调用。
 *
 * 所有写路径集中在 update() 中，避免状态散落导致后续维护时找不到真正的来源。
 */

function createInitialState() {
  return {
    /** 宿主是否可用。false 时工作台只显示降级提示。 */
    ready: false,
    storageAvailable: true,
    protocol: "http",
    activeSavedId: null,
    activeName: "",
    activeFolder: "",
    http: defaultHttp(),
    grpc: defaultGrpc(),
    /** 新建请求使用的默认超时；单个请求可在「选项」里覆盖。 */
    settings: defaultSettings(),
    savedIndex: [],
    folderIndex: [],
    historyIndex: [],
    response: null,
    responseTab: "body",
    sending: false,
    discovering: false,
    /** 已发出终止指令，等 sidecar 收尾。 */
    canceling: false,
    /** 交互式会话（客户端流 / 双向流）的运行期状态；不持久化，见 createStreamState。 */
    stream: createStreamState(),
  };
}

/**
 * 交互式会话的运行期状态。
 *
 * 它和 grpc 配置分开：配置会存进草稿与历史，会话（消息时间线、已发送序号）
 * 无法重放，也不该跟着请求一起保存。
 */
function createStreamState() {
  return {
    id: "",
    kind: "", // client | bidi
    state: "idle", // idle | connecting | open | halfClosed | ended | error
    outSeq: 0,
    inSeq: 0,
    /** 消息下标 → 该消息实际发出的 outSeq。 */
    sent: {},
    timeline: [],
    dropped: 0,
    busy: null, // send | finish | close
    receiving: false,
    startedAt: 0,
    /** 半关闭后开始等待回复的时刻；用来在界面上显示已经等了多久。 */
    waitingSince: 0,
    error: null,
    status: null,
  };
}

/** 时间线最多保留的条目数；交互式会话可以很长，界面只保留最近一段。 */
const STREAM_TIMELINE_LIMIT = 400;

export const state = writable(createInitialState());

/** 折叠的目录路径集合。仅影响展示，不持久化。 */
export const collapsedFolders = writable(new Set());

/** 轻提示。null 表示不显示。 */
export const toast = writable(null);

/**
 * 界面布局偏好：请求 / 响应分栏比例、历史分组是否折叠、历史面板占多高。
 *
 * 单独存在 "layout" 键里，不写进请求快照：同一台机器上所有请求共用同一个偏好，
 * 也不会因为切换请求被草稿覆盖。
 */
export const layout = writable({ split: 0.5, historyCollapsed: false, historyRatio: null });

let storage = null;
let draftTimer = null;
let toastTimer = null;
let splitTimer = null;
let t = translator();

/**
 * 当前这次请求。
 *
 * sidecar 的取消是按 requestId 定位的，所以「正在跑的是谁」必须和状态分开记。
 * seq 用来丢弃被终止、或被新请求取代之后才回来的结果。
 */
let activeRun = null;
let runSeq = 0;
let cancelTimer = null;
const CANCEL_RPC_TIMEOUT_MS = 5000;
/** 终止指令发出后最多再等这么久；后端没有收尾也要让界面先退出加载态。 */
const CANCEL_SETTLE_MS = 8000;

/** 语言切换后刷新状态层使用的翻译函数。 */
export function refreshTranslator() {
  t = translator();
}

function update(fn) {
  state.update((currentState) => {
    const next = fn(currentState);
    return next === undefined ? currentState : next;
  });
}

/** 同步读取当前状态。 */
export function current() {
  return get(state);
}

/** 分栏比例的合法区间。太窄的一侧连工具栏都放不下，所以留 25% 下界。 */
export function clampSplit(value) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return 0.5;
  return Math.max(0.25, Math.min(0.75, parsed));
}

/** 把当前布局偏好整体写回同一个 "layout" 键。 */
function persistLayout() {
  if (storage) void storage.set("layout", get(layout));
}

/** 更新分栏比例并持久化（拖拽时高频调用，写存储做防抖）。 */
export function setSplit(value) {
  const split = clampSplit(value);
  layout.update((current) => (Math.abs(current.split - split) < 0.001 ? current : { ...current, split }));
  clearTimeout(splitTimer);
  splitTimer = setTimeout(persistLayout, 400);
}

/**
 * 折叠 / 展开历史分组。
 *
 * 历史最多 30 条，展开时会把侧栏剩余高度吃满，已保存的接口目录只剩几行可见；
 * 折叠后这段高度让给目录树。折叠状态和分栏比例存在同一个 layout 键里，下次打开保持一致。
 */
export function toggleHistory() {
  layout.update((current) => ({ ...current, historyCollapsed: !current.historyCollapsed }));
  clearTimeout(splitTimer);
  persistLayout();
}

/** 历史面板占比的合法区间：太小时一条记录都露不全，太大时目录树会被挤没。 */
export const HISTORY_RATIO_MIN = 0.12;
export const HISTORY_RATIO_MAX = 0.8;
/** 没设置过时的占比：与历史列表自动占高的上限一致。 */
const HISTORY_RATIO_DEFAULT = 0.35;

export function clampHistoryRatio(value) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return HISTORY_RATIO_DEFAULT;
  return Math.max(HISTORY_RATIO_MIN, Math.min(HISTORY_RATIO_MAX, parsed));
}

/**
 * 设置历史面板占「已保存 + 历史两块列表可用高度」的比例。
 *
 * 比例而不是像素：侧栏高度变化时两块列表按同一比例缩放。没设置过时历史按内容占高，
 * 第一次拖动分隔条后固定下来。
 */
export function setHistoryRatio(value) {
  const historyRatio = clampHistoryRatio(value);
  layout.update((current) => (current.historyRatio === historyRatio ? current : { ...current, historyRatio }));
  // 拖拽时高频调用，和分栏比例共用同一个防抖计时器，反正写的都是整份 layout。
  clearTimeout(splitTimer);
  splitTimer = setTimeout(persistLayout, 400);
}

/** 更新默认超时设置并持久化。 */
export function updateSettings(patch) {
  const previous = current().settings;
  const settings = normalizeSettings({ ...previous, ...patch });
  update((s) => ({
    ...s,
    settings,
    // 当前请求如果还在用旧默认值（没有单独改过），跟着新默认一起走；
    // 已经手动覆盖过的请求保持原值，避免"改全局设置把单个请求也改了"。
    http: s.http.timeoutMs === previous.httpTimeoutMs ? { ...s.http, timeoutMs: settings.httpTimeoutMs } : s.http,
    grpc: s.grpc.timeoutMs === previous.grpcTimeoutMs ? { ...s.grpc, timeoutMs: settings.grpcTimeoutMs } : s.grpc,
  }));
  if (storage) void storage.set("settings", settings);
  scheduleDraftPersist();
  return settings;
}

export function showToast(message, tone, durationMs = 3200) {
  if (!message) return;
  toast.set({ message, tone: tone || "" });
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.set(null), durationMs);
}

export function dismissToast() {
  clearTimeout(toastTimer);
  toast.set(null);
}

// ---------------------------------------------------------------------------
// 持久化
// ---------------------------------------------------------------------------

const savedKey = (id) => `saved.entry.${id}`;
const historyKey = (id) => `history.entry.${id}`;

async function readIndex(key) {
  const value = await storage.get(key);
  return Array.isArray(value) ? value.filter((item) => item && typeof item.id === "string") : [];
}

async function readFolders(key) {
  const value = await storage.get(key);
  return Array.isArray(value) ? value.map(normalizeFolder).filter(Boolean) : [];
}

let draftTooLargeWarned = false;

export function scheduleDraftPersist() {
  clearTimeout(draftTimer);
  draftTimer = setTimeout(async () => {
    const payload = toSnapshot(current());
    try {
      if (byteLength(JSON.stringify(payload)) > DRAFT_LIMIT) {
        // 草稿是自动保存的，失败不能静默：否则用户以为下次还能恢复。
        if (!draftTooLargeWarned) {
          draftTooLargeWarned = true;
          showToast(t("draftTooLarge"), "warn");
        }
        return;
      }
    } catch {
      return;
    }
    await storage.set("draft", payload);
  }, 450);
}

async function persistSavedIndex(index) {
  await storage.set("saved.index", index);
}

async function persistFolders(folders) {
  await storage.set("folder.index", folders);
}

function ensureFolderPath(folders, folder) {
  const value = normalizeFolder(folder);
  if (!value) return folders;
  const known = new Set(folders.map(normalizeFolder));
  const next = [...folders];
  for (const path of folderAncestors(value)) {
    if (known.has(path)) continue;
    next.push(path);
    known.add(path);
  }
  return next.sort((a, b) => a.localeCompare(b));
}

function expandFolder(folder) {
  const target = normalizeFolder(folder);
  if (!target) return;
  collapsedFolders.update((set) => {
    if (!set.has(target)) return set;
    const next = new Set(set);
    next.delete(target);
    return next;
  });
}

export function toggleFolder(folder) {
  collapsedFolders.update((set) => {
    const next = new Set(set);
    if (next.has(folder)) next.delete(folder);
    else next.add(folder);
    return next;
  });
}

/** 目录列表：已登记目录与请求实际所在目录的并集。 */
export function listFolders() {
  const s = current();
  return collectFolders(s.folderIndex, s.savedIndex);
}

// ---------------------------------------------------------------------------
// 初始化
// ---------------------------------------------------------------------------

export async function initialize() {
  if (!host.available) {
    update((s) => ({ ...s, ready: false }));
    return;
  }

  await host.ready();
  t = translator();

  storage = host.createStorage({
    onDegraded: () => update((s) => ({ ...s, storageAvailable: false })),
    onWriteFailed: () => showToast(t("storageUnavailable"), "warn"),
  });

  const [savedIndex, folderIndex, historyIndex, storedSettings, draft] = await Promise.all([
    readIndex("saved.index"),
    readFolders("folder.index"),
    readIndex("history.index"),
    storage.get("settings"),
    storage.get("draft"),
  ]);

  const settings = normalizeSettings(storedSettings);
  update((s) => ({
    ...s,
    ready: true,
    savedIndex,
    folderIndex,
    historyIndex,
    settings,
    // 没有草稿时，空表单的超时也应该跟着用户设置的默认值走。
    ...(draft && typeof draft === "object"
      ? {}
      : {
          http: { ...s.http, timeoutMs: settings.httpTimeoutMs },
          grpc: { ...s.grpc, timeoutMs: settings.grpcTimeoutMs },
        }),
  }));

  const storedLayout = await storage.get("layout");
  if (storedLayout && typeof storedLayout === "object") {
    const stored = /** @type {{ split?: unknown, historyCollapsed?: unknown, historyRatio?: unknown }} */ (storedLayout);
    const split = Number(stored.split);
    const ratio = stored.historyRatio;
    layout.set({
      split: Number.isFinite(split) ? clampSplit(split) : 0.5,
      historyCollapsed: Boolean(stored.historyCollapsed),
      historyRatio: ratio === null || ratio === undefined ? null : clampHistoryRatio(ratio),
    });
  }

  if (draft && typeof draft === "object") applySnapshotToState(draft);
}

/** 把一份快照写入表单状态。 */
export function applySnapshotToState(snapshot) {
  abortFormUploads(current().http.formRows);
  const normalized = applySnapshot(snapshot, current().settings);
  // 旧会话属于上一个请求；不关就会在 sidecar 里留着一条没人用的流。
  resetStreamSession("", false);
  update((s) => ({
    ...s,
    protocol: normalized.protocol,
    http: normalized.http,
    grpc: normalized.grpc,
    responseTab: normalized.responseTab,
    // 切换请求时清掉上一次的发现结果，避免把 A 服务的 method 用到 B 服务上。
    response: null,
  }));
  // services / methods 属于运行期字段，不持久化；打开已保存请求或恢复草稿后
  // 需要重新拉一次，否则客户端流的多消息编辑器会因为不知道方法类型而消失。
  void hydrateGrpcMethods();
}

/**
 * 请求身份指纹：用来丢弃"请求已经切走之后才回来的"方法列表。
 *
 * 只包含决定方法描述符的字段；messages/metadata 变化不影响，避免用户输入时反复加载。
 */
function grpcIdentity(s) {
  const grpc = s.grpc;
  const files = grpc.protoFiles.map((file) => `${file.name}:${file.content.length}`).join(",");
  return [s.protocol, grpc.schemaMode, grpc.target.trim(), grpc.service, files].join("|");
}

/**
 * 打开请求后补齐 gRPC 方法信息。
 *
 * 静默执行：目标连不上时不要弹错误打断"只是打开一个历史记录"，用户会在真正点调用时
 * 看到具体失败原因。按请求身份记录进行中的加载，切到别的请求不会把它的加载吞掉。
 */
const hydratingGrpcIdentities = new Set();
async function hydrateGrpcMethods() {
  const s = current();
  if (s.protocol !== "grpc" || !s.grpc.service || s.grpc.methods.length || !host.available) return;
  if (s.grpc.schemaMode === "file" ? !s.grpc.protoFiles.length : !s.grpc.target.trim()) return;
  const identity = grpcIdentity(s);
  if (hydratingGrpcIdentities.has(identity)) return;
  hydratingGrpcIdentities.add(identity);
  try {
    await loadGrpcMethods({ silent: true });
  } finally {
    hydratingGrpcIdentities.delete(identity);
  }
}

// ---------------------------------------------------------------------------
// 表单编辑
// ---------------------------------------------------------------------------

export function setProtocol(protocol) {
  if (protocol !== "http" && protocol !== "grpc") return;
  if (current().protocol !== protocol) resetStreamSession("", false);
  update((s) => ({
    ...s,
    protocol,
    // 响应属于刚才那个请求；切到另一种协议后继续显示旧响应会让人以为它是新请求的结果。
    ...(s.protocol === protocol ? {} : { response: null, responseTab: "body" }),
  }));
  scheduleDraftPersist();
  if (protocol === "grpc") void hydrateGrpcMethods();
}

export function patchHttp(patch) {
  update((s) => ({ ...s, http: { ...s.http, ...patch } }));
  scheduleDraftPersist();
}

// --- 表单 / Multipart 编辑 -------------------------------------------------

/** 用最新状态更新表单行，避免事件闭包里的旧数组覆盖用户刚输入的内容。 */
export function updateFormRows(updater) {
  update((s) => ({ ...s, http: { ...s.http, formRows: updater(s.http.formRows || []) } }));
  scheduleDraftPersist();
}

export function addFormRow(kind = "text") {
  updateFormRows((rows) => [...rows, blankFormRow(kind)]);
}

export function patchFormRow(index, patch) {
  updateFormRows((rows) => rows.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch } : row)));
}

/** 文本 ⇄ 文件切换；切回文本时释放已经暂存的文件，避免占着磁盘没人用。 */
export function setFormRowKind(index, kind) {
  const row = (current().http.formRows || [])[index];
  if (!row || row.kind === kind || (kind !== "text" && kind !== "file")) return;
  if (kind === "text") {
    for (const file of row.files || []) void abortStagedUpload(file.uploadId);
    updateFormRows((rows) => rows.map((item, rowIndex) => (rowIndex === index ? { ...item, kind, files: [] } : item)));
    return;
  }
  updateFormRows((rows) => rows.map((item, rowIndex) => (rowIndex === index ? { ...item, kind } : item)));
}

export function removeFormRow(index) {
  const row = (current().http.formRows || [])[index];
  for (const file of row?.files || []) void abortStagedUpload(file.uploadId);
  updateFormRows((rows) => rows.filter((_, rowIndex) => rowIndex !== index));
}

export function removeFormFile(fileId) {
  const target = findFormFile(current().http.formRows, fileId);
  if (target?.file?.uploadId) void abortStagedUpload(target.file.uploadId);
  updateFormRows((rows) =>
    rows.map((row) => (row.kind === "file" ? { ...row, files: (row.files || []).filter((file) => file.id !== fileId) } : row)),
  );
}

/** 在最新的行状态里按 id 找文件（包括被禁用 / 已删除但仍在暂存中的行）。 */
function findFormFile(rows, fileId) {
  for (const row of Array.isArray(rows) ? rows : []) {
    if (row?.kind !== "file") continue;
    const file = (row.files || []).find((item) => item.id === fileId);
    if (file) return { row, file };
  }
  return null;
}

function patchFormFile(fileId, patch) {
  updateFormRows((rows) =>
    rows.map((row) => {
      if (row.kind !== "file") return row;
      let changed = false;
      const files = (row.files || []).map((file) => {
        if (file.id !== fileId) return file;
        changed = true;
        return { ...file, ...patch };
      });
      return changed ? { ...row, files } : row;
    }),
  );
}

/** 删除 sidecar 里的暂存文件。失败不影响界面，过期后由后端自行回收。 */
async function abortStagedUpload(uploadId) {
  if (!uploadId || !host.available) return;
  try {
    await host.invoke("http/upload/abort", { uploadId }, { timeoutMs: 5000 });
  } catch {
    // 暂存文件有 TTL；这里失败不需要打断用户。
  }
}

function abortFormUploads(rows) {
  for (const row of Array.isArray(rows) ? rows : []) {
    for (const file of row?.files || []) void abortStagedUpload(file.uploadId);
  }
}

/**
 * sidecar 重启或暂存区回收后，内存里的 uploadId 会全部失效。
 * 保留文件名与大小，只把 uploadId 清掉，让发送前的检查给出「重新选择」而不是
 * 把 sidecar 的 Upload has expired 直接甩给用户。
 */
function invalidateFormUploads() {
  updateFormRows((rows) =>
    rows.map((row) => ({
      ...row,
      files: (row.files || []).map((file) =>
        file.uploadId ? { ...file, uploadId: "", status: "missing", progress: 0 } : file,
      ),
    })),
  );
}

/**
 * 给某一行添加文件（多选追加），或替换该行里的一个文件。
 *
 * 先按配额预检，再插占位行，然后逐个分块传给 sidecar。文件内容不经过 store 的
 * 快照状态，只保留 uploadId；切换请求或移除文件时释放。
 */
export async function addFormFiles(rowIndex, sources, options = {}) {
  const list = (Array.isArray(sources) ? sources : []).filter(Boolean);
  if (!list.length) return;
  const rows = current().http.formRows || [];
  const row = rows[rowIndex];
  if (!row) {
    for (const source of list) void source.close?.();
    return;
  }

  const { accepted, rejected } = planFormFiles(rows, list, { rowIndex, fileId: options.fileId });
  for (const item of rejected) void item.source?.close?.();
  if (rejected.length) showToast(describeRejectedFiles(rejected), "error", 5200);
  if (!accepted.length) return;

  if (options.fileId) {
    const replaced = findFormFile(rows, options.fileId)?.file;
    if (replaced?.uploadId) void abortStagedUpload(replaced.uploadId);
  }

  const placeholders = accepted.map((source) => ({
    id: uid(),
    fileName: source.fileName,
    contentType: source.contentType || guessContentType(source.fileName),
    size: Number(source.size) || 0,
    uploadId: "",
    status: "staging",
    progress: 0,
  }));

  updateFormRows((currentRows) =>
    currentRows.map((currentRow, index) => {
      if (index !== rowIndex) return currentRow;
      const files = (currentRow.files || []).filter((file) => !options.fileId || file.id !== options.fileId);
      return { ...currentRow, kind: "file", files: [...files, ...placeholders] };
    }),
  );

  // 串行暂存：文件本来就在本地磁盘上，并行只会让进度抖动。
  for (let index = 0; index < accepted.length; index += 1) {
    await stageUploadSource(placeholders[index], accepted[index]);
  }
}

/** 把拒绝原因写成一句能看懂的话：带上实际大小、上限和"未添加"。 */
function describeRejectedFiles(rejected) {
  const first = rejected[0];
  let message;
  if (first.reason === "size") {
    message = t("formFileTooLarge", {
      name: first.fileName,
      actual: formatBytes(first.size),
      limit: formatBytes(first.limit),
    });
  } else if (first.reason === "total") {
    message = t("formTotalTooLarge", {
      name: first.fileName,
      actual: formatBytes(first.size),
      total: formatBytes(first.total),
      limit: formatBytes(first.limit),
    });
  } else if (first.reason === "count") {
    message = t("formFileCountLimit", {
      name: first.fileName,
      current: first.current,
      limit: first.limit,
    });
  } else {
    message = t("formFileUnreadable", { name: first.fileName });
  }
  if (rejected.length === 1) return message;
  return `${message} ${t("formFilesRejectedMore", { count: rejected.length - 1 })}`;
}

/** 把一个上传源分块写进 sidecar 暂存文件，并同步进度。 */
async function stageUploadSource(file, source) {
  let uploadId = "";
  let lastPercent = -1;
  try {
    const begun = await host.invoke(
      "http/upload/begin",
      { fileName: file.fileName, size: Math.max(0, Number(source.size) || 0) },
      { timeoutMs: 15000 },
    );
    uploadId = String(begun?.uploadId || "");
    if (!uploadId) throw new Error("missing upload id");

    let offset = 0;
    for (;;) {
      const chunk = await source.readChunk(offset, MULTIPART_UPLOAD_CHUNK);
      const length = Math.max(0, Number(chunk.length) || 0);
      if (length > 0) {
        await host.invoke("http/upload/chunk", { uploadId, dataBase64: chunk.dataBase64 }, { timeoutMs: 15000 });
        offset += length;
        const percent = file.size > 0 ? Math.min(100, Math.round((offset / file.size) * 100)) : 100;
        if (percent !== lastPercent) {
          lastPercent = percent;
          patchFormFile(file.id, { progress: percent });
        }
      }
      if (chunk.eof) break;
      if (length <= 0) throw new Error("empty chunk");
    }

    // 暂存期间行可能被删除 / 切换请求，此时内容已经没人要了。
    if (!findFormFile(current().http.formRows, file.id)) {
      void abortStagedUpload(uploadId);
      return;
    }
    patchFormFile(file.id, { uploadId, size: offset || file.size, status: "ready", progress: 100 });
  } catch {
    if (uploadId) void abortStagedUpload(uploadId);
    if (findFormFile(current().http.formRows, file.id)) {
      patchFormFile(file.id, { status: "error", progress: 0 });
      showToast(t("formFileStageFailed", { name: file.fileName }), "error");
    }
  } finally {
    void source.close?.();
  }
}

/** 建流时定死的参数；改了就等同于"需要重连"，直接结束当前会话。 */
const SESSION_PARAM_KEYS = ["target", "tls", "skipTlsVerify", "metadata"];

function closeStreamForParamChange(patch) {
  if (!current().stream.id) return;
  if (!SESSION_PARAM_KEYS.some((key) => Object.prototype.hasOwnProperty.call(patch, key))) return;
  resetStreamSession(t("streamParamsChanged"));
}

/**
 * 描述符来源变了（服务 / 方法 / proto 文件）后回收旧会话。
 *
 * 这几种变化都会让当前会话「对不上」界面：它绑的是旧方法的 descriptor，继续留着只会在
 * sidecar 里占一条连接，直到空闲超时被回收。没有会话时什么都不做，避免凭空清掉时间线。
 */
function resetStreamForSchemaChange() {
  const stream = current().stream;
  if (!stream.id && stream.state !== "connecting") return;
  resetStreamSession("", false);
}

export function patchGrpc(patch) {
  closeStreamForParamChange(patch);
  update((s) => ({ ...s, grpc: { ...s.grpc, ...patch } }));
  scheduleDraftPersist();
}

/** 更新某个键值列表中的一行。用不可变更新，让 Svelte 准确感知变化。 */
export function patchKV(scope, listName, index, patch) {
  if (scope === "grpc" && listName === "metadata") closeStreamForParamChange({ metadata: true });
  update((s) => {
    const rows = s[scope][listName].map((row, i) => (i === index ? { ...row, ...patch } : row));
    return { ...s, [scope]: { ...s[scope], [listName]: rows } };
  });
  scheduleDraftPersist();
}

export function addKV(scope, listName) {
  update((s) => ({
    ...s,
    [scope]: { ...s[scope], [listName]: [...s[scope][listName], blankKV()] },
  }));
  scheduleDraftPersist();
}

export function removeKV(scope, listName, index) {
  update((s) => {
    const rows = s[scope][listName].filter((_, i) => i !== index);
    return { ...s, [scope]: { ...s[scope], [listName]: rows.length ? rows : [blankKV()] } };
  });
  scheduleDraftPersist();
}

/** 在最后一行的输入框里按 Enter 时追加一行。返回是否处理了该次按键。 */
export function appendKVOnEnter(scope, listName, index) {
  if (index !== current()[scope][listName].length - 1) return false;
  addKV(scope, listName);
  return true;
}

export function setResponseTab(tab) {
  update((s) => ({ ...s, responseTab: tab === "headers" ? "headers" : "body" }));
  scheduleDraftPersist();
}

export function setGrpcMethods(methods) {
  const previous = current().grpc.method;
  // 旧方法不在新列表里时会被顶成第一个：这是隐式换方法，同样得回收旧会话。
  const next = methods.some((method) => method.name === previous) ? previous : methods[0]?.name || "";
  if (next !== previous) resetStreamForSchemaChange();
  update((s) => ({ ...s, grpc: { ...s.grpc, methods, method: next } }));
}

export function setGrpcService(service) {
  if (service === current().grpc.service) return;
  resetStreamSession("", false);
  // 服务变了，输入消息的 schema 也随之变化：旧响应和旧请求体都不再成立。
  update((s) => ({
    ...s,
    grpc: { ...s.grpc, service, method: "", methods: [], messages: [""] },
    response: null,
  }));
  scheduleDraftPersist();
}

export function setGrpcMethod(method) {
  if (method === current().grpc.method) return;
  resetStreamSession("", false);
  // 方法变了：输入类型可能完全不同，旧请求体不能继续留着，旧响应也不再对应当前方法。
  update((s) => ({ ...s, grpc: { ...s.grpc, method, messages: [""] }, response: null }));
  scheduleDraftPersist();
}

/**
 * 切换 gRPC 方法描述来源。
 *
 * 两种来源的 service / method 不通用，切换时清空发现结果；导入 proto 后自动重新解析
 * 一次，省掉用户再点一次「发现服务」。
 */
export function setGrpcSchemaMode(mode) {
  if (mode !== "file" && mode !== "reflection") return;
  if (current().grpc.schemaMode === mode) return;
  resetStreamSession("", false);
  update((s) => ({
    ...s,
    grpc: { ...s.grpc, schemaMode: mode, services: [], service: "", methods: [], method: "", messages: [""] },
    response: null,
  }));
  scheduleDraftPersist();
  if (mode === "file" && current().grpc.protoFiles.length) void discoverGrpc();
}

/**
 * 导入或替换 proto 文件。
 *
 * 文件内容由调用方（组件）通过浏览器 File API 读好，这里只做归一化、限额与状态写入，
 * 这样 store 仍然不碰 DOM，测试里可以直接传对象。
 *
 * @param {{ name: string, content: string }[]} entries
 * @returns {{ added: number, rejected: { name: string, reason: "size" | "total" | "count" | "empty" | "duplicate" }[] }}
 */
export function addGrpcProtoFiles(entries) {
  const incoming = Array.isArray(entries) ? entries : [];
  /** @type {{ name: string, reason: "size" | "total" | "count" | "empty" | "duplicate" }[]} */
  const rejected = [];
  const accepted = [];
  const existing = new Map(current().grpc.protoFiles.map((file) => [file.name, file]));
  let total = [...existing.values()].reduce((sum, file) => sum + byteLength(file.content), 0);
  const batch = new Map();

  for (const entry of incoming) {
    const rawName = String(entry?.name || "").trim().replace(/\\/g, "/");
    const name = rawName.slice(rawName.lastIndexOf("/") + 1);
    const content = typeof entry?.content === "string" ? entry.content : "";
    if (!name || !content.trim()) {
      rejected.push({ name: rawName || "?", reason: "empty" });
      continue;
    }
    if (batch.has(name)) {
      if (batch.get(name) === content) continue;
      // 同一次拖入/多选里出现两个同名文件：无法在内存里区分，显式拒绝而不是静默覆盖。
      rejected.push({ name, reason: "duplicate" });
      continue;
    }
    batch.set(name, content);
    const size = byteLength(content);
    if (size > PROTO_FILE_LIMIT) {
      rejected.push({ name, reason: "size" });
      continue;
    }
    const previous = existing.get(name);
    const nextTotal = total - (previous ? byteLength(previous.content) : 0) + size;
    if (nextTotal > PROTO_TOTAL_LIMIT) {
      rejected.push({ name, reason: "total" });
      continue;
    }
    if (!previous && existing.size >= PROTO_FILE_COUNT_LIMIT) {
      rejected.push({ name, reason: "count" });
      continue;
    }
    total = nextTotal;
    existing.set(name, { name, content });
    accepted.push(name);
  }

  if (accepted.length) {
    const switching = current().grpc.schemaMode !== "file";
    // 从反射切到文件模式会把 service / method 清掉，旧会话的描述符来源已经不同。
    if (switching) resetStreamForSchemaChange();
    update((s) => ({
      ...s,
      grpc: {
        ...s.grpc,
        schemaMode: "file",
        protoFiles: [...existing.values()],
        // 从反射切过来时旧服务的描述符来源不同，先清掉；文件模式的再次导入则保留选择。
        ...(switching ? { services: [], service: "", methods: [], method: "" } : {}),
      },
    }));
    scheduleDraftPersist();
  }
  return { added: accepted.length, rejected };
}

export function removeGrpcProtoFile(name) {
  const target = String(name || "");
  // 服务 / 方法会被清空，旧会话绑的是被删文件编译出来的 descriptor，必须一起回收。
  resetStreamForSchemaChange();
  update((s) => ({
    ...s,
    grpc: {
      ...s.grpc,
      protoFiles: s.grpc.protoFiles.filter((file) => file.name !== target),
      // 服务是这一批文件编译出来的，少一个文件就可能整批编译不过（比如被删的正被别人
      // import）：先清空再让调用方重新解析，免得下拉框停在已经不成立的列表上。
      services: [],
      service: "",
      methods: [],
      method: "",
    },
  }));
  scheduleDraftPersist();
}

/** 清空由描述来源派生的服务 / 方法选择，并让草稿记住这个选择。 */
function clearGrpcSchema() {
  update((s) => ({ ...s, grpc: { ...s.grpc, services: [], service: "", methods: [], method: "" } }));
  scheduleDraftPersist();
}

export function setGrpcMessage(index, value) {
  update((s) => ({
    ...s,
    grpc: { ...s.grpc, messages: s.grpc.messages.map((item, i) => (i === index ? value : item)) },
  }));
  scheduleDraftPersist();
}

export function addGrpcMessage() {
  update((s) => ({ ...s, grpc: { ...s.grpc, messages: [...s.grpc.messages, ""] } }));
  scheduleDraftPersist();
}

export function removeGrpcMessage(index) {
  update((s) => {
    const rows = s.grpc.messages.filter((_, i) => i !== index);
    return { ...s, grpc: { ...s.grpc, messages: rows.length ? rows : [""] } };
  });
  scheduleDraftPersist();
}

export function formatGrpcMessage(index) {
  const raw = current().grpc.messages[index];
  if (!raw || !raw.trim()) return;
  try {
    setGrpcMessage(index, JSON.stringify(JSON.parse(raw), null, 2));
  } catch {
    showToast(t("invalidJsonMessage", { index: index + 1 }), "error");
  }
}

// ---------------------------------------------------------------------------
// 新建 / 保存 / 打开 / 删除
// ---------------------------------------------------------------------------

export function newRequest(folder) {
  const target = normalizeFolder(folder);
  abortFormUploads(current().http.formRows);
  resetStreamSession("", false);
  update((s) => ({
    ...s,
    activeSavedId: null,
    activeName: "",
    activeFolder: target,
    http: defaultHttp(s.settings),
    grpc: defaultGrpc(s.settings),
    response: null,
    responseTab: "body",
  }));
  expandFolder(target);
  scheduleDraftPersist();
  if (target) showToast(t("newRequestInFolder", { folder: target }), "ok");
}

/**
 * 保存当前请求。
 * @param {{ name: string, folder?: string } | null} identity 保存归属；为 null 表示放弃保存。
 * @param {boolean} asNew 为 true 时总是创建新条目。
 */
export async function saveCurrent(identity, asNew) {
  if (!identity) return null;

  const s = current();
  const payload = toSnapshot(s);
  const name = identity.name;
  const folder = normalizeFolder(identity.folder);
  const id = asNew || !s.activeSavedId ? uid() : s.activeSavedId;

  const record = { ...payload, name, folder };
  if (byteLength(JSON.stringify(record)) > SAVE_LIMIT) {
    showToast(t("requestTooLarge"), "error");
    return null;
  }

  const stored = await storage.set(savedKey(id), record);
  if (!stored) {
    showToast(t("saveFailed"), "error");
    return null;
  }

  let folderIndex = s.folderIndex;
  if (folder) {
    folderIndex = ensureFolderPath(folderIndex, folder);
    await persistFolders(folderIndex);
  }

  const entry = {
    id,
    name,
    folder,
    protocol: payload.protocol,
    method: requestMethod(payload),
    summary: summarize(payload, t("untitled")),
    updatedAt: Date.now(),
  };
  const savedIndex = [...s.savedIndex];
  const existing = savedIndex.findIndex((item) => item.id === id);
  if (existing >= 0) savedIndex[existing] = { ...savedIndex[existing], ...entry };
  else savedIndex.unshift(entry);

  update((st) => ({ ...st, savedIndex, folderIndex, activeSavedId: id, activeName: name, activeFolder: folder }));
  await persistSavedIndex(savedIndex);
  expandFolder(folder);
  showToast(t("savedToast", { name }), "ok");
  scheduleDraftPersist();
  return { id, name };
}

export async function openSaved(id) {
  const payload = await storage.get(savedKey(id));
  if (!payload) {
    showToast(t("loadFailed"), "error");
    return;
  }
  const entry = current().savedIndex.find((item) => item.id === id);
  applySnapshotToState(payload);
  update((s) => ({
    ...s,
    activeSavedId: id,
    activeName: entry?.name || payload.name || t("untitled"),
    activeFolder: normalizeFolder(entry?.folder ?? payload.folder),
  }));
  scheduleDraftPersist();
}

export async function openHistory(id) {
  const payload = await storage.get(historyKey(id));
  if (!payload) {
    showToast(t("loadFailed"), "error");
    return;
  }
  applySnapshotToState(payload);
  update((s) => ({ ...s, activeSavedId: null, activeName: "", activeFolder: "" }));
  // 历史记录不保存 proto 源码，文件模式下重新打开会缺文件；先说明再让用户导入。
  if (payload.grpc?.schemaMode === "file" && !payload.grpc?.protoFiles?.length) {
    showToast(t("historyProtoMissing"), "warn");
  }
  scheduleDraftPersist();
}

export async function deleteSaved(id) {
  await storage.remove(savedKey(id));
  const savedIndex = current().savedIndex.filter((item) => item.id !== id);
  update((s) => ({
    ...s,
    savedIndex,
    ...(s.activeSavedId === id ? { activeSavedId: null, activeName: "", activeFolder: "" } : {}),
  }));
  await persistSavedIndex(savedIndex);
}

export async function moveSaved(id, folder) {
  const target = normalizeFolder(folder);
  const savedIndex = current().savedIndex.map((item) => (item.id === id ? { ...item, folder: target } : item));
  const record = await storage.get(savedKey(id));
  if (record && typeof record === "object") {
    await storage.set(savedKey(id), { ...record, folder: target });
  }
  let folderIndex = current().folderIndex;
  if (target) {
    folderIndex = ensureFolderPath(folderIndex, target);
    await persistFolders(folderIndex);
  }
  update((s) => ({
    ...s,
    savedIndex,
    folderIndex,
    ...(s.activeSavedId === id ? { activeFolder: target } : {}),
  }));
  await persistSavedIndex(savedIndex);
  expandFolder(target);
  showToast(t("movedToast", { folder: target || t("ungrouped") }), "ok");
}

export async function clearHistory() {
  const historyIndex = current().historyIndex;
  if (!historyIndex.length) return;
  for (const entry of historyIndex) await storage.remove(historyKey(entry.id));
  update((s) => ({ ...s, historyIndex: [] }));
  await storage.set("history.index", []);
  showToast(t("historyCleared"), "ok");
}

/**
 * 历史记录里不保存 proto 文件正文。
 *
 * 宿主存储整库只有 1 MiB，而每次调用都会写一条历史；把最多 160 KiB 的 proto 源码
 * 复制 30 份会直接挤爆存储。重新打开历史时提示用户重新导入即可，saved 请求不受影响。
 */
function historySnapshot(payload) {
  if (!payload.grpc?.protoFiles?.length) return payload;
  return { ...payload, grpc: { ...payload.grpc, protoFiles: [] } };
}

/** 记录一条历史。相同内容连续发送时不重复记录。 */
async function addHistory(rawPayload) {
  const payload = historySnapshot(rawPayload);
  const signature = JSON.stringify({ protocol: payload.protocol, http: payload.http, grpc: payload.grpc });
  const index = current().historyIndex;
  if (index[0] && index[0].signature === signature) return;

  const id = uid();
  const stored = await storage.set(historyKey(id), payload);
  if (!stored) return;

  const historyIndex = [
    {
      id,
      protocol: payload.protocol,
      method: requestMethod(payload),
      summary: summarize(payload, t("untitled")),
      signature,
      at: Date.now(),
    },
    ...index,
  ];
  while (historyIndex.length > HISTORY_LIMIT) {
    const removed = historyIndex.pop();
    await storage.remove(historyKey(removed.id));
  }
  update((s) => ({ ...s, historyIndex }));
  await storage.set("history.index", historyIndex);
}

// ---------------------------------------------------------------------------
// 目录
// ---------------------------------------------------------------------------

export async function createFolder(parent, name) {
  const base = normalizeFolder(parent);
  const path = normalizeFolder(base ? `${base}/${name}` : name);
  if (!path) return false;
  const folderIndex = ensureFolderPath(current().folderIndex, path);
  update((s) => ({ ...s, folderIndex }));
  await persistFolders(folderIndex);
  expandFolder(base);
  showToast(t("createdFolderToast", { name: path }), "ok");
  return true;
}

/** 删除目录及其下所有请求。 */
/**
 * 把目录（连同整棵子树）移动到另一个目录下。
 *
 * - 目标为空字符串表示移到根（未分组同级），只改目录层级，不动其中的请求归属语义。
 * - 不能移到自己或自己的子孙目录下，否则会把子树挂成环。
 * - 目录里所有请求的 folder 前缀要一起重写，否则请求会留在旧的路径下变成孤儿。
 */
export async function moveFolder(path, targetParent) {
  const source = normalizeFolder(path);
  if (!source) return false;
  const parent = normalizeFolder(targetParent);

  // 目标不能是自身或自身的子孙。
  if (parent === source || parent.startsWith(`${source}/`)) return false;

  const baseName = source.slice(source.lastIndexOf("/") + 1);
  const nextPath = parent ? `${parent}/${baseName}` : baseName;
  if (nextPath === source) return false;

  const s = current();
  const prefix = `${source}/`;

  const remap = (folder) => {
    const value = normalizeFolder(folder);
    if (value === source) return nextPath;
    if (value.startsWith(prefix)) return `${nextPath}/${value.slice(prefix.length)}`;
    return value;
  };

  // 目录索引：重写被移动子树里的每一项。
  const folderIndex = s.folderIndex.map(remap);
  const normalized = [...new Set(folderIndex.map(normalizeFolder).filter(Boolean))].sort((a, b) => a.localeCompare(b));
  const withParents = ensureFolderPath(normalized, nextPath);

  // 请求索引与各自的记录都跟着改。
  const movedItems = s.savedIndex.filter((item) => {
    const value = normalizeFolder(item.folder);
    return value === source || value.startsWith(prefix);
  });
  for (const item of movedItems) {
    const record = await storage.get(savedKey(item.id));
    if (record && typeof record === "object") {
      await storage.set(savedKey(item.id), { ...record, folder: remap(item.folder) });
    }
  }
  const savedIndex = s.savedIndex.map((item) =>
    movedItems.some((moved) => moved.id === item.id) ? { ...item, folder: remap(item.folder) } : item,
  );

  // 折叠状态跟着新路径走，避免展开状态错位。
  collapsedFolders.update((set) => {
    const next = new Set();
    for (const candidate of set) next.add(remap(candidate));
    return next;
  });

  update((st) => ({
    ...st,
    savedIndex,
    folderIndex: withParents,
    ...(st.activeFolder === source || st.activeFolder.startsWith(prefix)
      ? { activeFolder: remap(st.activeFolder) }
      : {}),
  }));

  await persistSavedIndex(savedIndex);
  await persistFolders(withParents);
  expandFolder(parent);
  showToast(t("movedFolderToast", { name: nextPath }), "ok");
  return true;
}

/** 删除目录及其下所有请求。 */
export async function deleteFolder(path) {
  const target = normalizeFolder(path);
  if (!target) return false;
  const prefix = `${target}/`;
  const s = current();
  const removedItems = s.savedIndex.filter((item) => {
    const itemFolder = normalizeFolder(item.folder);
    return itemFolder === target || itemFolder.startsWith(prefix);
  });

  for (const item of removedItems) await storage.remove(savedKey(item.id));

  const removedIds = new Set(removedItems.map((item) => item.id));
  const savedIndex = s.savedIndex.filter((item) => !removedIds.has(item.id));
  const folderIndex = s.folderIndex.filter((candidate) => {
    const value = normalizeFolder(candidate);
    return value !== target && !value.startsWith(prefix);
  });

  collapsedFolders.update((set) => {
    const next = new Set();
    for (const candidate of set) {
      if (candidate === target || candidate.startsWith(prefix)) continue;
      next.add(candidate);
    }
    return next;
  });

  update((st) => {
    const next = { ...st, savedIndex, folderIndex };
    if (st.activeSavedId && removedIds.has(st.activeSavedId)) {
      next.activeSavedId = null;
      next.activeName = "";
      next.activeFolder = "";
    } else if (st.activeFolder === target || st.activeFolder.startsWith(prefix)) {
      next.activeFolder = folderParent(target);
    }
    return next;
  });

  await persistSavedIndex(savedIndex);
  await persistFolders(folderIndex);
  showToast(t("deletedFolderToast", { name: target }), "ok");
  return true;
}

/** 待删除目录的统计信息，供确认框展示。 */
export function folderDeleteStats(path) {
  const target = normalizeFolder(path);
  const prefix = `${target}/`;
  const s = current();
  const requests = s.savedIndex.filter((item) => {
    const folder = normalizeFolder(item.folder);
    return folder === target || folder.startsWith(prefix);
  }).length;
  const folders = collectFolders(s.folderIndex, s.savedIndex).filter((candidate) => candidate.startsWith(prefix)).length;
  return { requests, folders };
}

// ---------------------------------------------------------------------------
// 发送请求
// ---------------------------------------------------------------------------

/**
 * 开始一次请求。
 *
 * mode 决定加载态走 sending 还是 discovering；两者互斥，保证同一时间只有一个
 * 可终止的请求，终止按钮不会定位到错的 requestId。
 */
function beginRun(protocol, mode) {
  runSeq += 1;
  clearTimeout(cancelTimer);
  cancelTimer = null;
  const run = { id: uid(), protocol, mode, seq: runSeq };
  activeRun = run;
  update((s) => ({
    ...s,
    sending: mode === "send",
    discovering: mode === "discover",
    canceling: false,
    response: { protocol, kind: "loading" },
    responseTab: "body",
  }));
  return run;
}

function isCurrentRun(run) {
  return Boolean(activeRun) && activeRun.seq === run.seq;
}

/** 收尾一次请求。被终止或被新请求取代的迟到结果在这里被丢弃。 */
function finishRun(run, response) {
  if (!isCurrentRun(run)) return;
  clearTimeout(cancelTimer);
  cancelTimer = null;
  activeRun = null;
  update((s) => ({ ...s, sending: false, discovering: false, canceling: false, response }));
}

/**
 * 终止当前请求。
 *
 * 取消走另一条 RPC：sidecar 的 JSON-RPC 分发是并发的，因此它能打断正在阻塞的
 * http/send 或 grpc/invoke。原请求随后会以 canceled 错误返回，由 finishRun 收尾。
 */
export async function cancelCurrentRequest() {
  const run = activeRun;
  const s = current();
  if (!run || s.canceling || (!s.sending && !s.discovering) || !host.available) return false;

  update((st) => ({ ...st, canceling: true }));
  clearTimeout(cancelTimer);
  cancelTimer = setTimeout(() => {
    if (!isCurrentRun(run)) return;
    // 兜底：终止 RPC 或原请求迟迟不返回时也要退出加载态，
    // 否则"点了终止还是卡住"。迟到结果会因为 activeRun 已清空而被丢弃。
    activeRun = null;
    runSeq += 1;
    update((st) => ({
      ...st,
      sending: false,
      discovering: false,
      canceling: false,
      response: { protocol: run.protocol, kind: "error", errorKind: "canceled", error: t("requestCanceled") },
    }));
  }, CANCEL_SETTLE_MS);

  try {
    await host.invoke("request/cancel", { requestId: run.id }, { timeoutMs: CANCEL_RPC_TIMEOUT_MS });
    return true;
  } catch (error) {
    // 请求可能刚好已经结束，此时没必要弹错误；仍然在跑的话让兜底计时器
    // 继续走，避免终止 RPC 自己失败后又永久停在加载态。
    if (isCurrentRun(run)) {
      update((st) => ({ ...st, canceling: false }));
      showToast(errorMessage(error, t("cancelFailed")), "error");
    }
    return false;
  }
}

export async function sendHttp() {
  const s = current();
  if (s.sending || s.discovering || !host.available) return false;

  if (!s.http.url.trim()) {
    showToast(t("needUrl"), "warn");
    return false;
  }
  const url = buildRequestUrl(s.http.url, s.http.params);
  if (!url) {
    showToast(t("invalidUrl"), "error");
    return false;
  }

  // 文件内容暂存在 sidecar 里：恢复自快照的文件只有元数据，必须重新选择后才能发。
  if (s.http.bodyType === "multipart") {
    const entries = formFileEntries(s.http.formRows);
    if (entries.some((entry) => entry.file.status === "staging")) {
      showToast(t("formFileStaging"), "warn");
      return false;
    }
    const missing = entries.find((entry) => !entry.file.uploadId);
    if (missing) {
      showToast(t("formFileMissing", { name: missing.file.fileName }), "warn");
      return false;
    }
    if (entries.length > MULTIPART_FILE_COUNT_LIMIT) {
      showToast(t("formTooManyFiles", { limit: MULTIPART_FILE_COUNT_LIMIT }), "error");
      return false;
    }
    const totalBytes = multipartTotalBytes(s.http.formRows);
    if (totalBytes > MULTIPART_TOTAL_LIMIT) {
      showToast(
        t("formFilesTotalTooLarge", {
          total: formatBytes(totalBytes),
          limit: formatBytes(MULTIPART_TOTAL_LIMIT),
        }),
        "error",
      );
      return false;
    }
  }

  const headers = buildHeaders(s.http.headers, httpContentType(s.http));
  const timeoutMs = clampNumber(s.http.timeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX);
  const bodyParams = buildBodyParams(s.http);
  if (byteLength(bodyParams.bodyText) > BRIDGE_BODY_LIMIT) {
    showToast(t("bodyTooLarge"), "error");
    return false;
  }
  // 文件走分块暂存，但文本字段仍随 http/send 一次性过桥；字段过多/过长时提前拦。
  if (bodyParams.multipart && byteLength(JSON.stringify(bodyParams.multipart.fields)) > BRIDGE_BODY_LIMIT) {
    showToast(t("bodyTooLarge"), "error");
    return false;
  }

  const payload = toSnapshot(s);
  const params = {
    method: s.http.method,
    url,
    headers,
    ...bodyParams,
    timeoutMs,
    followRedirects: s.http.followRedirects !== false,
    skipTlsVerify: Boolean(s.http.skipTlsVerify),
  };

  const run = beginRun("http", "send");
  const started = performance.now();
  try {
    const data = await host.invoke(
      "http/send",
      { ...params, requestId: run.id },
      { timeoutMs: Math.min(118000, timeoutMs + 5000) },
    );
    finishRun(run, { protocol: "http", kind: "done", data, elapsedMs: Math.round(performance.now() - started) });
  } catch (error) {
    const message = errorMessage(error, t("requestFailed"));
    if (message.includes("Upload has expired")) {
      invalidateFormUploads();
      showToast(t("formFileExpired"), "warn");
    }
    finishRun(run, {
      protocol: "http",
      kind: "error",
      error: message,
      errorKind: describeErrorKind(error),
      elapsedMs: Math.round(performance.now() - started),
    });
  } finally {
    await addHistory(payload);
    scheduleDraftPersist();
  }
  return true;
}

function grpcBaseParams(s) {
  const fileMode = s.grpc.schemaMode === "file";
  return {
    target: s.grpc.target.trim(),
    tls: Boolean(s.grpc.tls),
    skipTlsVerify: Boolean(s.grpc.skipTlsVerify),
    timeoutMs: clampNumber(s.grpc.timeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    metadata: enabledEntries(s.grpc.metadata),
    // 只有文件模式才传 proto 源码：反射模式传空数组，避免把上一次导入的内容
    // 悄悄带到另一个服务的调用里。
    useProtoFiles: fileMode,
    protoFiles: fileMode ? s.grpc.protoFiles.map((file) => ({ name: file.name, content: file.content })) : [],
  };
}

/** 文件模式没有 target 也能解析；反射模式必须有 target。 */
function grpcSchemaReady(s, base) {
  return base.useProtoFiles ? Boolean(s.grpc.protoFiles.length) : Boolean(base.target);
}

export async function discoverGrpc() {
  const s = current();
  if (s.sending || s.discovering || !host.available) return false;

  const base = grpcBaseParams(s);
  if (!grpcSchemaReady(s, base)) {
    showToast(base.useProtoFiles ? t("needProtoFiles") : t("needTarget"), "warn");
    return false;
  }

  const run = beginRun("grpc", "discover");
  try {
    const data = await host.invoke(
      "grpc/services",
      { ...base, requestId: run.id },
      { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
    );
    const services = Array.isArray(data?.services) ? data.services : [];
    // 发现结果里没有当前服务时会顺移到一个新服务并清空方法：这是隐式换 schema。
    const previousService = current().grpc.service;
    const nextService = services.includes(previousService) ? previousService : services[0] || "";
    if (nextService !== previousService) resetStreamForSchemaChange();
    update((st) => ({
      ...st,
      grpc: {
        ...st.grpc,
        services,
        service: nextService,
        // 同一个服务刷新时保留已选方法，避免每次重新发现都把用户的选择重置成第一个。
        method: nextService === previousService ? st.grpc.method : "",
        methods: [],
      },
    }));

    // 方法列表是第二步 RPC；复用同一个 requestId，终止按钮在这两步之间也能命中。
    if (services.length) await loadGrpcMethods({ requestId: run.id, propagateCancel: true });

    finishRun(run, null);
    showToast(
      services.length ? t("discovered", { count: services.length }) : t("noServices"),
      services.length ? "ok" : "warn",
    );
  } catch (error) {
    const message = errorMessage(error, t("requestFailed"));
    const kind = describeErrorKind(error);
    // 文件模式下服务只能来自当前这批文件：编译失败说明这份列表已经不成立了，
    // 继续显示只会让用户以为还能调用（点了也只会再报同一个编译错误）。
    // 被终止或被新请求取代的迟到失败不写状态，和 finishRun 的丢弃规则保持一致。
    if (isCurrentRun(run) && base.useProtoFiles) clearGrpcSchema();
    finishRun(run, {
      protocol: "grpc",
      kind: "error",
      // 解析 / 发现失败不是"请求失败"，响应面板按这个字段换措辞。
      action: base.useProtoFiles ? "parse" : "discover",
      error: message,
      errorKind: kind,
    });
    if (kind === "canceled") showToast(t("requestCanceled"), "warn");
    // proto 语法错误、缺少 import 等只出现在这里，用提示条保证用户不会漏看。
    else showToast(truncate(message, 180), "error");
  } finally {
    scheduleDraftPersist();
  }
  return true;
}

/**
 * 加载服务的方法列表。
 *
 * options.requestId：由 discoverGrpc 传入时可被终止；为 undefined 时（打开请求后的
 * 静默补齐）不参与终止，避免抢走正在发送的请求的取消目标。
 * options.propagateCancel：为 true 时把"已终止"抛给调用方收尾，而不是在这里静默吞掉。
 */
export async function loadGrpcMethods(options = {}) {
  const s = current();
  if (!s.grpc.service || !host.available) return;
  const base = grpcBaseParams(s);
  if (!grpcSchemaReady(s, base)) return;
  const identity = grpcIdentity(s);
  try {
    const data = await host.invoke(
      "grpc/methods",
      { ...base, service: s.grpc.service, ...(options.requestId ? { requestId: options.requestId } : {}) },
      { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
    );
    // 加载期间用户可能已经切到另一个请求，旧请求的结果不能写到新请求上。
    if (grpcIdentity(current()) !== identity) return;
    setGrpcMethods(Array.isArray(data?.methods) ? data.methods : []);
  } catch (error) {
    if (grpcIdentity(current()) !== identity) return;
    const kind = describeErrorKind(error);
    if (kind === "canceled" && options.propagateCancel) throw error;
    setGrpcMethods([]);
    if (!options.silent && kind !== "canceled") showToast(errorMessage(error, t("requestFailed")), "error");
  }
}

let grpcInvokeBusy = false;

export async function invokeGrpc() {
  // 方法补齐在 beginRun 之前，必须自己挡重复点击，否则一次快速双击会发两次调用。
  if (grpcInvokeBusy) return false;
  grpcInvokeBusy = true;
  try {
    return await invokeGrpcInternal();
  } finally {
    grpcInvokeBusy = false;
  }
}

async function invokeGrpcInternal() {
  if (!host.available) return false;
  let s = current();
  if (s.sending || s.discovering) return false;

  // 打开历史记录或草稿后方法信息是异步补的；用户可能抢在补完之前点调用，
  // 这里再等一次，避免把客户端流误判成一元、只发出第一条消息。
  if (!s.grpc.methods.length && s.grpc.service) {
    const identity = grpcIdentity(s);
    await loadGrpcMethods({ silent: true });
    if (grpcIdentity(current()) !== identity) return false;
    s = current();
  }

  const base = grpcBaseParams(s);
  if (!base.target) {
    showToast(t("needTarget"), "warn");
    return false;
  }
  if (!s.grpc.service) {
    showToast(t("needService"), "warn");
    return false;
  }
  if (!s.grpc.method) {
    showToast(t("needMethod"), "warn");
    return false;
  }
  if (base.useProtoFiles && !s.grpc.protoFiles.length) {
    showToast(t("needProtoFiles"), "warn");
    return false;
  }

  // 只有客户端流（双向流也带这个标记）会把多条消息全部发出去；
  // 一元与服务端流在协议上只接受一条，这里取第一条，和后端校验保持一致。
  const methodInfo = s.grpc.methods.find((item) => item.name === s.grpc.method);
  const authored = Array.isArray(s.grpc.messages) ? s.grpc.messages : [s.grpc.message || ""];
  const outgoing = methodInfo?.clientStreaming ? authored : authored.slice(0, 1);

  const messages = [];
  for (const [index, text] of outgoing.entries()) {
    const raw = String(text || "").trim();
    if (!raw) {
      messages.push({});
      continue;
    }
    try {
      messages.push(JSON.parse(raw));
    } catch {
      showToast(t("invalidJsonMessage", { index: index + 1 }), "error");
      return false;
    }
  }
  if (byteLength(JSON.stringify(messages)) > BRIDGE_BODY_LIMIT) {
    showToast(t("bodyTooLarge"), "error");
    return false;
  }

  const payload = toSnapshot(s);
  const run = beginRun("grpc", "send");
  const started = performance.now();
  try {
    const data = await host.invoke(
      "grpc/invoke",
      { ...base, service: s.grpc.service, method: s.grpc.method, messages, requestId: run.id },
      { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
    );
    finishRun(run, { protocol: "grpc", kind: "done", data, elapsedMs: Math.round(performance.now() - started) });
  } catch (error) {
    finishRun(run, {
      protocol: "grpc",
      kind: "error",
      error: errorMessage(error, t("requestFailed")),
      errorKind: describeErrorKind(error),
      elapsedMs: Math.round(performance.now() - started),
    });
  } finally {
    await addHistory(payload);
    scheduleDraftPersist();
  }
  return true;
}

// ---------------------------------------------------------------------------
// 交互式会话（客户端流 / 双向流）
// ---------------------------------------------------------------------------
//
// 阶段一采用「拉」：每次点击只发一次短 RPC，真正的流由 sidecar 的会话表持有。
// 界面从不直接读网络，wait 只是从 sidecar 的队列里取；这既不需要事件通道，
// 也不会出现"用户不点就收不到"以外的能力缺口。

/** 接收循环的代数。切换请求或重连后旧循环必须失效，否则会把结果写到新会话上。 */
let streamGen = 0;

function streamOf() {
  return current().stream;
}

function patchStream(patch) {
  update((s) => ({ ...s, stream: { ...s.stream, ...patch } }));
}

function pushTimeline(entries) {
  if (!entries.length) return;
  update((s) => {
    const next = [...s.stream.timeline, ...entries];
    const trimmed = next.length > STREAM_TIMELINE_LIMIT ? next.slice(next.length - STREAM_TIMELINE_LIMIT) : next;
    return { ...s, stream: { ...s.stream, timeline: trimmed } };
  });
}

/** 时间线条目带上相对会话开始的毫秒数，界面不用自己算。 */
function timelineEntry(type, payload = {}) {
  const startedAt = current().stream.startedAt;
  return { type, at: startedAt ? Date.now() - startedAt : 0, ...payload };
}

/** 状态名单独展示；这里只保留服务端给的说明，避免同一句 OK 出现两次。 */
function streamStatusText(status) {
  if (!status) return "";
  return String(status.message || "");
}

/**
 * 只在后端还有会话时尽力关闭它。
 *
 * 失败一律忽略：切换请求、关闭面板时不该因为收尾失败弹错误；真正的泄漏由
 * sidecar 的空闲回收兜底。
 */
async function closeStreamOnBackend(id) {
  if (!id || !host.available) return;
  try {
    await host.invoke("grpc/stream/close", { streamId: id }, { timeoutMs: 5000 });
  } catch {
    // 会话可能已经结束；这里没有需要恢复的用户可见状态。
  }
}

/**
 * 结束本地会话状态，并让在飞的接收循环失效。
 *
 * keepTimeline 为 false 时连时间线一起清空：切换方法 / 服务 / 请求后就该是全新一屏，
 * 留着上一条流的结果只会让人以为它是当前方法的输出。
 */
export function resetStreamSession(note = "", keepTimeline = true) {
  streamGen += 1;
  const previous = streamOf();
  if (previous.id) void closeStreamOnBackend(previous.id);
  update((s) => {
    const timeline = !keepTimeline
      ? []
      : note && previous.timeline.length
        ? [...previous.timeline, { type: "note", at: 0, text: note }]
        : previous.timeline;
    return { ...s, stream: { ...createStreamState(), timeline } };
  });
}

/** 清空时间线，保留会话状态。 */
export function clearStreamTimeline() {
  update((s) => ({ ...s, stream: { ...s.stream, timeline: [] } }));
}

export function patchGrpcStreamOption(patch) {
  update((s) => {
    const stream = { ...s.grpc.stream, ...patch };
    return {
      ...s,
      grpc: {
        ...s.grpc,
        stream: {
          waitMs: clampNumber(stream.waitMs, STREAM_WAIT_DEFAULT, STREAM_WAIT_MIN, STREAM_WAIT_MAX),
          idleTimeoutMs: clampNumber(stream.idleTimeoutMs, STREAM_IDLE_DEFAULT, STREAM_IDLE_MIN, STREAM_IDLE_MAX),
          maxDurationMs: clampNumber(stream.maxDurationMs, STREAM_DURATION_DEFAULT, STREAM_DURATION_MIN, STREAM_DURATION_MAX),
        },
      },
    };
  });
  scheduleDraftPersist();
}

/** 当前方法是否支持逐条发送（客户端流或双向流）。 */
export function isInteractiveMethod(grpc) {
  const info = grpc.methods.find((item) => item.name === grpc.method);
  return Boolean(info?.clientStreaming);
}

/**
 * 建立交互式会话。
 *
 * 和一元调用不同：这里不发送任何消息，只建连接和流。首条消息同样走 send，
 * 这样「消息下标 → outSeq」的映射从第一条开始就是一致的。
 */
export async function connectGrpc() {
  if (!host.available) return false;
  let s = current();
  if (s.stream.state === "connecting" || s.stream.busy) return false;

  if (!s.grpc.methods.length && s.grpc.service) {
    const identity = grpcIdentity(s);
    await loadGrpcMethods({ silent: true });
    if (grpcIdentity(current()) !== identity) return false;
    s = current();
  }

  const base = grpcBaseParams(s);
  if (!base.target) {
    showToast(t("needTarget"), "warn");
    return false;
  }
  if (!s.grpc.service) {
    showToast(t("needService"), "warn");
    return false;
  }
  if (!s.grpc.method) {
    showToast(t("needMethod"), "warn");
    return false;
  }
  if (base.useProtoFiles && !s.grpc.protoFiles.length) {
    showToast(t("needProtoFiles"), "warn");
    return false;
  }

  // 旧会话必须显式关闭，否则 sidecar 里会留着一条没人用的流。
  if (s.stream.id) await closeStreamOnBackend(s.stream.id);
  streamGen += 1;
  const gen = streamGen;

  const options = s.grpc.stream;
  const startedAt = Date.now();
  // 服务端流只有一个请求：它随建流一起发出去，之后只收不发。
  const methodInfo = s.grpc.methods.find((item) => item.name === s.grpc.method);
  let serverMessage;
  if (methodInfo?.serverStreaming && !methodInfo?.clientStreaming) {
    const raw = String(s.grpc.messages[0] ?? "").trim();
    if (raw) {
      try {
        serverMessage = JSON.parse(raw);
      } catch {
        showToast(t("invalidJsonMessage", { index: 1 }), "error");
        return false;
      }
    }
  }
  update((st) => ({
    ...st,
    stream: {
      ...createStreamState(),
      state: "connecting",
      startedAt,
      timeline: st.stream.timeline.length
        ? [...st.stream.timeline, { type: "note", at: 0, text: t("streamNewSession") }]
        : st.stream.timeline,
    },
  }));

  try {
    const data = await host.invoke(
      "grpc/stream/open",
      {
        ...base,
        service: s.grpc.service,
        method: s.grpc.method,
        idleTimeoutMs: options.idleTimeoutMs,
        maxDurationMs: options.maxDurationMs,
        ...(serverMessage !== undefined ? { message: serverMessage } : {}),
      },
      { timeoutMs: Math.min(118000, Math.max(20000, base.timeoutMs + 5000)) },
    );
    if (gen !== streamGen) {
      // 建流本身已经成功，只是这期间用户切走了（换方法 / 目标 / 请求）。
      // 这条流已经没有人认领，必须自己关掉，否则 sidecar 里会一直留到空闲超时。
      void closeStreamOnBackend(String(data?.streamId || ""));
      return false;
    }
    const id = String(data?.streamId || "");
    if (!id) throw new Error(t("streamOpenFailed"));
    const kind = String(data?.kind || "");
    update((st) => ({
      ...st,
      stream: { ...st.stream, id, kind, state: "open", error: null },
    }));
    // 连接成功不弹 toast：响应区头部已经常驻显示状态，再弹一次就是同一句话出现两遍。
    // 双向流与服务端流都要一直收；客户端流要等半关闭之后才有响应。
    if (kind === "bidi" || kind === "server") void receiveStreamLoop(gen, id);
    return true;
  } catch (error) {
    if (gen !== streamGen) return false;
    const message = errorMessage(error, t("streamOpenFailed"));
    update((st) => ({
      ...st,
      stream: { ...st.stream, id: "", state: "error", error: message },
    }));
    pushTimeline([timelineEntry("error", { text: message })]);
    showToast(message, "error");
    return false;
  }
}

/** 发一条消息；只写入，不等待回复。 */
export async function sendGrpcMessage(index) {
  const s = current();
  const stream = s.stream;
  if (!host.available || stream.state !== "open" || stream.busy) return false;

  const text = String(s.grpc.messages[index] ?? "").trim();
  if (!text) {
    showToast(t("streamEmptyMessage", { index: index + 1 }), "warn");
    return false;
  }
  let message;
  try {
    message = JSON.parse(text);
  } catch {
    showToast(t("invalidJsonMessage", { index: index + 1 }), "error");
    return false;
  }
  if (byteLength(text) > BRIDGE_BODY_LIMIT) {
    showToast(t("bodyTooLarge"), "error");
    return false;
  }

  const gen = streamGen;
  const id = stream.id;
  patchStream({ busy: "send" });
  try {
    const data = await host.invoke(
      "grpc/stream/send",
      { streamId: id, message },
      { timeoutMs: 30000 },
    );
    if (gen !== streamGen) return false;
    const outSeq = Number(data?.outSeq) || 0;
    update((st) => ({
      ...st,
      stream: {
        ...st.stream,
        busy: null,
        outSeq,
        sent: { ...st.stream.sent, [index]: outSeq },
      },
    }));
    pushTimeline([timelineEntry("out", { index: index + 1, seq: outSeq, text })]);
    return true;
  } catch (error) {
    if (gen !== streamGen) return false;
    const message = errorMessage(error, t("streamSendFailed"));
    // 发送失败通常意味着流已经不可用；把本地状态标成错误，用户需要重新连接。
    update((st) => ({
      ...st,
      stream: { ...st.stream, busy: null, state: "error", error: message },
    }));
    pushTimeline([timelineEntry("error", { text: message })]);
    showToast(message, "error");
    return false;
  }
}

/**
 * 半关闭：我不再发了，但还在收。
 *
 * 客户端流靠它触发那一条总响应；双向流用它表示"说完了"，之后仍可继续收。
 */
export async function finishGrpcStream() {
  const s = current();
  const stream = s.stream;
  if (!host.available || (stream.state !== "open" && stream.state !== "halfClosed") || stream.busy) return false;
  if (stream.state === "halfClosed") return false;

  const gen = streamGen;
  const id = stream.id;
  // 点了就绪：客户端已经不可能再发消息了，界面先切到"等待回复"，
  // 不要等服务端返回才更新——那段时间正好是最需要反馈的。
  patchStream({ busy: "finish", state: "halfClosed", waitingSince: Date.now() });
  try {
    const data = await host.invoke(
      "grpc/stream/halfClose",
      { streamId: id, waitMs: s.grpc.stream.waitMs },
      { timeoutMs: Math.min(118000, s.grpc.stream.waitMs + 10000) },
    );
    if (gen !== streamGen) return false;
    applyWaitResult(data);
    update((st) => ({
      ...st,
      stream: {
        ...st.stream,
        busy: null,
        state: st.stream.state === "ended" ? "ended" : "halfClosed",
        waitingSince: st.stream.state === "ended" ? 0 : st.stream.waitingSince || Date.now(),
      },
    }));
    // 还没结束说明服务端仍在处理：继续收，直到 status 或用户终止。
    if (current().stream.state === "halfClosed") void receiveStreamLoop(gen, id);
    return true;
  } catch (error) {
    if (gen !== streamGen) return false;
    const message = errorMessage(error, t("streamFinishFailed"));
    update((st) => ({
      ...st,
      stream: { ...st.stream, busy: null, state: "error", error: message, waitingSince: 0 },
    }));
    pushTimeline([timelineEntry("error", { text: message })]);
    showToast(message, "error");
    return false;
  }
}

/** 把一次 wait / halfClose 的返回写进时间线与状态。 */
function applyWaitResult(data) {
  const entries = [];
  // 同一批消息可能既出现在 halfClose 的返回里，又出现在接收循环里，按会话内的
  // inSeq 水位去重；不能用时间线判断——时间线跨会话保留，上一条流的 seq 会误伤新会话。
  const watermark = current().stream.inSeq;
  const messages = Array.isArray(data?.messages) ? data.messages : [];
  for (const message of messages) {
    const seq = Number(message?.seq) || 0;
    if (seq <= watermark) continue;
    entries.push(timelineEntry("in", { seq, text: String(message?.json ?? "") }));
  }
  // 只记一次终态：halfClose 和接收循环都可能带回同一个 status。
  const ending = Boolean(data?.ended) && current().stream.state !== "ended";
  if (ending) {
    const status = data?.status || null;
    entries.push(timelineEntry("status", { text: streamStatusText(status), ok: (status?.code ?? 0) === 0, status }));
  }
  const patch = {
    inSeq: Number(data?.inSeq) || current().stream.inSeq,
    dropped: Number(data?.dropped) || 0,
  };
  if (data?.ended) {
    patch.state = "ended";
    patch.status = data?.status || null;
    patch.waitingSince = 0;
  }
  update((s) => ({ ...s, stream: { ...s.stream, ...patch } }));
  pushTimeline(entries);
}

/**
 * 接收循环：单飞的 wait，超时就再挂起一次。
 *
 * 这不是定时轮询——没有消息时它挂在 sidecar 里等待，不空转也不丢帧；
 * 只是每次最多等 waitMs，避免一个请求挂太久。
 */
async function receiveStreamLoop(gen, id) {
  if (current().stream.receiving) return;
  patchStream({ receiving: true });
  try {
    while (gen === streamGen) {
      const stream = current().stream;
      if (stream.id !== id) return;
      if (stream.state !== "open" && stream.state !== "halfClosed") return;

      let data;
      try {
        data = await host.invoke(
          "grpc/stream/wait",
          { streamId: id, waitMs: current().grpc.stream.waitMs },
          { timeoutMs: Math.min(118000, current().grpc.stream.waitMs + 10000) },
        );
      } catch (error) {
        if (gen !== streamGen) return;
        const kind = describeErrorKind(error);
        const message = errorMessage(error, t("streamWaitFailed"));
        if (kind === "canceled") return;
        update((s) => ({
          ...s,
          stream: { ...s.stream, state: "error", error: message },
        }));
        pushTimeline([timelineEntry("error", { text: message })]);
        return;
      }
      if (gen !== streamGen) return;
      applyWaitResult(data);
    }
  } finally {
    if (gen === streamGen) patchStream({ receiving: false });
  }
}

/** 终止会话：取消流、关连接、回收。失败不阻塞界面。 */
export async function closeGrpcStream() {
  const s = current();
  const stream = s.stream;
  if (!stream.id) return false;

  streamGen += 1;
  const id = stream.id;
  patchStream({ busy: "close" });
  await closeStreamOnBackend(id);
  update((st) => ({
    ...st,
    stream: {
      ...createStreamState(),
      timeline: [...st.stream.timeline, { type: "note", at: st.stream.startedAt ? Date.now() - st.stream.startedAt : 0, text: t("streamClosed") }],
    },
  }));
  return true;
}

/**
 * 页面即将销毁时的最佳努力清理。
 *
 * 关闭标签页 / 刷新时，前端没有可靠的卸载回调：页面一销毁，后续调用就发不出去。
 * 不清理的话，sidecar 里的会话要等空闲 TTL（3 分钟）才回收，期间：
 *  - 服务端仍认为订阅有效；
 *  - 消息继续入队并在满后丢弃；
 *  - 连续开关标签页会撞上“最多 4 条会话”的上限。
 *
 * 这里只做优化，不承担正确性：请求可能根本到不了（桥是 postMessage，页面可能先被销毁），
 * 失败一律忽略，正确性仍由 sidecar 的 TTL 兜底。
 */
function flushStreamOnUnload() {
  const id = current().stream.id;
  if (!id || !host.available) return;
  try {
    // 不 await：页面正在销毁，拿不到结果，也没有需要恢复的状态。
    void host.invoke("grpc/stream/close", { streamId: id }, { timeoutMs: 2000 });
  } catch {
    // 宿主已不可用，交给 TTL。
  }
}

let unloadCleanupInstalled = false;

/**
 * 注册 pagehide 清理，返回注销函数（给 onMount 用）。
 *
 * 用 pagehide 而不是 beforeunload：后者在移动端与 bfcache 场景不可靠。
 * bfcache 把页面“冻结”时也会触发 pagehide（persisted = true），那种情况页面之后可能
 * 原样恢复，不算离开，不能关会话。
 */
export function installStreamUnloadCleanup() {
  if (unloadCleanupInstalled || typeof window === "undefined") return () => {};
  unloadCleanupInstalled = true;

  const onPageHide = (event) => {
    if (event?.persisted) return;
    flushStreamOnUnload();
  };
  window.addEventListener("pagehide", onPageHide);

  return () => {
    window.removeEventListener("pagehide", onPageHide);
    unloadCleanupInstalled = false;
  };
}

/** 打开历史的「选项」时展示的取值；超出范围按边界收敛。 */
export function normalizeStreamSettings(value) {
  return {
    waitMs: clampNumber(value?.waitMs, STREAM_WAIT_DEFAULT, STREAM_WAIT_MIN, STREAM_WAIT_MAX),
    idleTimeoutMs: clampNumber(value?.idleTimeoutMs, STREAM_IDLE_DEFAULT, STREAM_IDLE_MIN, STREAM_IDLE_MAX),
    maxDurationMs: clampNumber(value?.maxDurationMs, STREAM_DURATION_DEFAULT, STREAM_DURATION_MIN, STREAM_DURATION_MAX),
  };
}

export async function copyResponse() {
  const text = responseText(current().response);
  if (!text) {
    showToast(t("nothingToCopy"), "warn");
    return;
  }
  try {
    await host.copy(text);
    showToast(t("copyDone"), "ok");
  } catch {
    showToast(t("copyFailed"), "error");
  }
}

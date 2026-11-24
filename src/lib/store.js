import { writable, get } from "svelte/store";
import * as host from "./host.js";
import { translator } from "./i18n.js";
import {
  BRIDGE_BODY_LIMIT,
  DRAFT_LIMIT,
  GRPC_TIMEOUT_DEFAULT,
  HISTORY_LIMIT,
  HTTP_TIMEOUT_DEFAULT,
  SAVE_LIMIT,
  TIMEOUT_MAX,
  applySnapshot,
  blankKV,
  buildHeaders,
  buildRequestUrl,
  byteLength,
  clampNumber,
  defaultGrpc,
  defaultHttp,
  describeErrorKind,
  enabledEntries,
  errorMessage,
  requestMethod,
  summarize,
  toSnapshot,
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
    http: defaultHttp(),
    grpc: defaultGrpc(),
    savedIndex: [],
    historyIndex: [],
    response: null,
    responseTab: "body",
    sending: false,
    discovering: false,
  };
}

export const state = writable(createInitialState());

/** 轻提示。null 表示不显示。 */
export const toast = writable(null);

let storage = null;
let draftTimer = null;
let toastTimer = null;
let t = translator();

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

export function showToast(message, tone) {
  if (!message) return;
  toast.set({ message, tone: tone || "" });
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.set(null), 3200);
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

export function scheduleDraftPersist() {
  clearTimeout(draftTimer);
  draftTimer = setTimeout(async () => {
    const payload = toSnapshot(current());
    try {
      if (JSON.stringify(payload).length > DRAFT_LIMIT) return;
    } catch {
      return;
    }
    await storage.set("draft", payload);
  }, 450);
}

async function persistSavedIndex(index) {
  await storage.set("saved.index", index);
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

  const [savedIndex, historyIndex] = await Promise.all([readIndex("saved.index"), readIndex("history.index")]);

  update((s) => ({ ...s, ready: true, savedIndex, historyIndex }));

  const draft = await storage.get("draft");
  if (draft && typeof draft === "object") applySnapshotToState(draft);
}

/** 把一份快照写入表单状态。 */
export function applySnapshotToState(snapshot) {
  const normalized = applySnapshot(snapshot);
  update((s) => ({
    ...s,
    protocol: normalized.protocol,
    http: normalized.http,
    grpc: normalized.grpc,
    responseTab: normalized.responseTab,
    // 切换请求时清掉上一次的发现结果，避免把 A 服务的 method 用到 B 服务上。
    response: null,
  }));
}

// ---------------------------------------------------------------------------
// 表单编辑
// ---------------------------------------------------------------------------

export function setProtocol(protocol) {
  if (protocol !== "http" && protocol !== "grpc") return;
  update((s) => ({ ...s, protocol }));
  scheduleDraftPersist();
}

export function patchHttp(patch) {
  update((s) => ({ ...s, http: { ...s.http, ...patch } }));
  scheduleDraftPersist();
}

export function patchGrpc(patch) {
  update((s) => ({ ...s, grpc: { ...s.grpc, ...patch } }));
  scheduleDraftPersist();
}

/** 更新某个键值列表中的一行。用不可变更新，让 Svelte 准确感知变化。 */
export function patchKV(scope, listName, index, patch) {
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
  update((s) => {
    const stillValid = methods.some((method) => method.name === s.grpc.method);
    return {
      ...s,
      grpc: { ...s.grpc, methods, method: stillValid ? s.grpc.method : methods[0]?.name || "" },
    };
  });
}

export function setGrpcService(service) {
  update((s) => ({ ...s, grpc: { ...s.grpc, service, method: "", methods: [] } }));
  scheduleDraftPersist();
}

export function setGrpcMethod(method) {
  update((s) => ({ ...s, grpc: { ...s.grpc, method } }));
  scheduleDraftPersist();
}

// ---------------------------------------------------------------------------
// 新建 / 保存 / 打开 / 删除
// ---------------------------------------------------------------------------

export function newRequest() {
  update((s) => ({
    ...s,
    activeSavedId: null,
    activeName: "",
    http: defaultHttp(),
    grpc: defaultGrpc(),
    response: null,
    responseTab: "body",
  }));
  scheduleDraftPersist();
}

/**
 * 保存当前请求。
 * @param {{ name: string } | null} identity 保存归属；为 null 表示放弃保存。
 * @param {boolean} asNew 为 true 时总是创建新条目。
 */
export async function saveCurrent(identity, asNew) {
  if (!identity) return null;

  const s = current();
  const payload = toSnapshot(s);
  const name = identity.name;
  const id = asNew || !s.activeSavedId ? uid() : s.activeSavedId;

  const record = { ...payload, name };
  if (byteLength(JSON.stringify(record)) > SAVE_LIMIT) {
    showToast(t("requestTooLarge"), "error");
    return null;
  }

  const stored = await storage.set(savedKey(id), record);
  if (!stored) {
    showToast(t("saveFailed"), "error");
    return null;
  }

  const entry = {
    id,
    name,
    protocol: payload.protocol,
    method: requestMethod(payload),
    summary: summarize(payload, t("untitled")),
    updatedAt: Date.now(),
  };
  const savedIndex = [...s.savedIndex];
  const existing = savedIndex.findIndex((item) => item.id === id);
  if (existing >= 0) savedIndex[existing] = { ...savedIndex[existing], ...entry };
  else savedIndex.unshift(entry);

  update((st) => ({ ...st, savedIndex, activeSavedId: id, activeName: name }));
  await persistSavedIndex(savedIndex);
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
  update((s) => ({ ...s, activeSavedId: null, activeName: "" }));
  scheduleDraftPersist();
}

export async function deleteSaved(id) {
  await storage.remove(savedKey(id));
  const savedIndex = current().savedIndex.filter((item) => item.id !== id);
  update((s) => ({
    ...s,
    savedIndex,
    ...(s.activeSavedId === id ? { activeSavedId: null, activeName: "" } : {}),
  }));
  await persistSavedIndex(savedIndex);
}

export async function clearHistory() {
  const historyIndex = current().historyIndex;
  if (!historyIndex.length) return;
  for (const entry of historyIndex) await storage.remove(historyKey(entry.id));
  update((s) => ({ ...s, historyIndex: [] }));
  await storage.set("history.index", []);
  showToast(t("historyCleared"), "ok");
}

/** 记录一条历史。相同内容连续发送时不重复记录。 */
async function addHistory(payload) {
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
// 发送请求
// ---------------------------------------------------------------------------

function beginRequest(protocol) {
  update((s) => ({ ...s, sending: true, response: { protocol, kind: "loading" }, responseTab: "body" }));
}

function endRequest(response) {
  update((s) => ({ ...s, sending: false, response }));
}

export async function sendHttp() {
  const s = current();
  if (s.sending || !host.available) return false;

  if (!s.http.url.trim()) {
    showToast(t("needUrl"), "warn");
    return false;
  }
  const url = buildRequestUrl(s.http.url, s.http.params);
  if (!url) {
    showToast(t("invalidUrl"), "error");
    return false;
  }

  const headers = buildHeaders(s.http.headers, s.http.bodyType);
  const timeoutMs = clampNumber(s.http.timeoutMs, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX);
  const bodyText = s.http.bodyType === "none" ? "" : s.http.bodyText;
  if (byteLength(bodyText) > BRIDGE_BODY_LIMIT) {
    showToast(t("bodyTooLarge"), "error");
    return false;
  }

  const payload = toSnapshot(s);
  const params = {
    method: s.http.method,
    url,
    headers,
    bodyEncoding: s.http.bodyType === "none" ? "none" : "text",
    bodyText,
    timeoutMs,
    followRedirects: s.http.followRedirects !== false,
    skipTlsVerify: Boolean(s.http.skipTlsVerify),
  };

  beginRequest("http");
  const started = performance.now();
  try {
    const data = await host.invoke("http/send", params, { timeoutMs: Math.min(118000, timeoutMs + 5000) });
    endRequest({ protocol: "http", kind: "done", data, elapsedMs: Math.round(performance.now() - started) });
  } catch (error) {
    endRequest({
      protocol: "http",
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

function grpcBaseParams(s) {
  return {
    target: s.grpc.target.trim(),
    tls: Boolean(s.grpc.tls),
    skipTlsVerify: Boolean(s.grpc.skipTlsVerify),
    timeoutMs: clampNumber(s.grpc.timeoutMs, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX),
    metadata: enabledEntries(s.grpc.metadata),
  };
}

export async function discoverGrpc() {
  const s = current();
  if (s.sending || s.discovering || !host.available) return false;

  const base = grpcBaseParams(s);
  if (!base.target) {
    showToast(t("needTarget"), "warn");
    return false;
  }

  update((st) => ({ ...st, discovering: true, response: { protocol: "grpc", kind: "loading" }, responseTab: "body" }));
  try {
    const data = await host.invoke("grpc/services", base, { timeoutMs: Math.min(118000, base.timeoutMs + 5000) });
    const services = Array.isArray(data?.services) ? data.services : [];
    update((st) => ({
      ...st,
      grpc: {
        ...st.grpc,
        services,
        service: services.includes(st.grpc.service) ? st.grpc.service : services[0] || "",
        method: "",
        methods: [],
      },
    }));

    if (services.length) await loadGrpcMethods();

    update((st) => ({ ...st, response: null }));
    showToast(
      services.length ? t("discovered", { count: services.length }) : t("noServices"),
      services.length ? "ok" : "warn",
    );
  } catch (error) {
    update((st) => ({
      ...st,
      response: {
        protocol: "grpc",
        kind: "error",
        error: errorMessage(error, t("requestFailed")),
        errorKind: describeErrorKind(error),
      },
    }));
  } finally {
    update((st) => ({ ...st, discovering: false }));
    scheduleDraftPersist();
  }
  return true;
}

export async function loadGrpcMethods() {
  const s = current();
  if (!s.grpc.service || !host.available) return;
  const base = grpcBaseParams(s);
  if (!base.target) return;
  try {
    const data = await host.invoke(
      "grpc/methods",
      { ...base, service: s.grpc.service },
      { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
    );
    setGrpcMethods(Array.isArray(data?.methods) ? data.methods : []);
  } catch (error) {
    setGrpcMethods([]);
    showToast(errorMessage(error, t("requestFailed")), "error");
  }
}

export async function invokeGrpc() {
  const s = current();
  if (s.sending || s.discovering || !host.available) return false;

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

  let message = {};
  const raw = s.grpc.message.trim();
  if (raw) {
    try {
      message = JSON.parse(raw);
    } catch {
      showToast(t("invalidJson"), "error");
      return false;
    }
  }
  if (byteLength(JSON.stringify(message)) > BRIDGE_BODY_LIMIT) {
    showToast(t("bodyTooLarge"), "error");
    return false;
  }

  const payload = toSnapshot(s);
  beginRequest("grpc");
  const started = performance.now();
  try {
    const data = await host.invoke(
      "grpc/invoke",
      { ...base, service: s.grpc.service, method: s.grpc.method, message },
      { timeoutMs: Math.min(118000, base.timeoutMs + 5000) },
    );
    endRequest({ protocol: "grpc", kind: "done", data, elapsedMs: Math.round(performance.now() - started) });
  } catch (error) {
    endRequest({
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

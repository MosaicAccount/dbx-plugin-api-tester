/**
 * 宿主桥接层。
 *
 * 这是插件里唯一允许直接访问 window.dbxPlugin 的模块，其余代码一律通过这里导出的
 * 函数与宿主交互。这样做的好处是：宿主 API 的探测、降级与错误归一化只有一处实现，
 * 组件和状态层不必各自判断 capabilities。
 *
 * 沙箱约束（见 AGENTS.md）：UI 运行在不透明源 iframe 中，没有 Node、没有网络、
 * 没有 localStorage、没有 navigator.clipboard。所有跨边界操作都必须走宿主。
 */

import { DOWNLOAD_CHUNK_DEFAULT, DOWNLOAD_FALLBACK_NAME } from "./model.js";

/** @typedef {{ get: (key: string) => Promise<unknown>, set: (key: string, value: unknown) => Promise<boolean>, remove: (key: string) => Promise<void> }} PluginStorage */

const plugin = typeof window === "undefined" ? undefined : window.dbxPlugin;

/** 宿主是否已注入桥接对象。 */
export const available = Boolean(plugin && plugin.ready);

/** 宿主区域设置，形如 `zh-CN` / `en`。 */
export function hostLocale() {
  return plugin?.locale || "";
}

/** 宿主能力位，用于能力探测而不是试探调用。 */
export function capabilities() {
  return plugin?.capabilities || {};
}

/** 等待宿主完成初始化握手。 */
export function ready() {
  return plugin?.ready || Promise.resolve();
}

/**
 * 调用 sidecar 的 JSON-RPC 方法。
 * @param {string} method
 * @param {unknown} params
 * @param {{ timeoutMs?: number }} [options]
 */
export function invoke(method, params, options = {}) {
  if (!plugin) throw new Error("DBX plugin host is unavailable");
  return plugin.invoke(method, params, options);
}

/**
 * 写入剪贴板。
 *
 * 优先用宿主提供的能力（沙箱内 navigator.clipboard 通常不可用）；
 * 只有在宿主没有该能力时才退回 navigator.clipboard。
 * @param {string} text
 */
export async function copy(text) {
  if (typeof plugin?.copy === "function") return plugin.copy(text);
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    return navigator.clipboard.writeText(text);
  }
  throw new Error("Clipboard unavailable");
}

/** 宿主是否提供原生文件选择（不提供时组件退回 `<input type="file">`）。 */
export function canPickFiles() {
  return typeof plugin?.fileTransfer?.pick === "function";
}

/**
 * 宿主是否支持把内容边收边写到本地文件。
 *
 * 这里只做能力探测、不做试探调用：`beginSave` 会弹出系统保存对话框，真到那一步
 * 才发现不支持，用户已经等过一次交互了。能力位显式写成 false 的宿主也按不支持处理，
 * 让按钮提前禁用而不是点了没反应。
 */
export function canSaveFiles() {
  const transfer = plugin?.fileTransfer;
  if (typeof transfer?.beginSave !== "function" || typeof transfer?.write !== "function") return false;
  const caps = /** @type {{ fileTransfer?: { beginSave?: unknown } }} */ (capabilities());
  return caps.fileTransfer?.beginSave !== false;
}

const FILE_CHUNK_BYTES = 256 * 1024;

function decodeBase64(data) {
  const binary = atob(String(data || ""));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

function joinBytes(chunks) {
  const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const merged = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    merged.set(chunk, offset);
    offset += chunk.length;
  }
  return merged;
}

function fileError(code) {
  return Object.assign(new Error(code === "size" ? "File is too large" : "Cannot read the file"), { code });
}

/**
 * 分块读出一个句柄的全部内容，按 UTF-8 解码成文本。
 *
 * maxBytes 是硬上限：超过后立刻中止，避免把几百 MB 的文件整份读进内存再让上层拒绝。
 */
async function readHandleText(transfer, handle, maxBytes = 0) {
  if (maxBytes > 0 && Number(handle.size) > maxBytes) throw fileError("size");

  const chunks = [];
  let offset = 0;
  let total = 0;
  try {
    for (;;) {
      const chunk = await transfer.read(handle.handleId, offset, FILE_CHUNK_BYTES);
      if (!chunk || typeof chunk.dataBase64 !== "string") throw fileError("read");
      const bytes = decodeBase64(chunk.dataBase64);
      total += bytes.length;
      if (maxBytes > 0 && total > maxBytes) throw fileError("size");
      chunks.push(bytes);
      if (chunk.eof) break;
      const length = Number(chunk.length) || 0;
      if (length <= 0) throw fileError("read");
      offset += length;
    }
  } finally {
    try {
      await transfer.cancel?.(handle.handleId);
    } catch {
      // 句柄关闭失败不影响已经读到的内容。
    }
  }
  const decoder = new TextDecoder("utf-8");
  return { name: String(handle.name || "imported.proto"), content: decoder.decode(joinBytes(chunks)) };
}

/** 把一个句柄读成结果；失败时返回带 error 的占位项，不中断其余文件。 */
async function readHandleResult(transfer, handle, maxBytes) {
  try {
    return await readHandleText(transfer, handle, maxBytes);
  } catch (error) {
    return {
      name: String(handle.name || "imported.proto"),
      content: "",
      error: error?.code === "size" ? "size" : "unreadable",
    };
  }
}

/**
 * 用宿主文件传输打开本地文件并读成文本。
 *
 * 返回 null 表示宿主没有 fileTransfer（调用方应退回浏览器文件选择器）；
 * 返回空数组表示用户取消了选择。传输按分块读，避免一次性占满内存。
 * @returns {Promise<{ name: string, content: string }[] | null>}
 */
export async function pickProtoFiles(options = {}) {
  const transfer = plugin?.fileTransfer;
  if (!transfer || typeof transfer.pick !== "function") return null;

  const maxBytes = Number(options.maxBytes) || 0;
  const selection = await transfer.pick({ multiple: true });
  const handles = Array.isArray(selection?.files) ? selection.files : [];
  const results = [];
  for (const handle of handles) results.push(await readHandleResult(transfer, handle, maxBytes));
  return results;
}

/** 把二进制分块拼成 base64；分块拼接避免 apply 参数过多导致栈溢出。 */
function bytesToBase64(bytes) {
  let binary = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode.apply(null, bytes.subarray(offset, offset + chunkSize));
  }
  return btoa(binary);
}

/**
 * 上传源：把「宿主文件句柄」和「浏览器 File」抹平成同一套分块读取接口。
 *
 * store 拿到源之后按 MULTIPART_UPLOAD_CHUNK 逐块读出，再经 RPC 写进 sidecar
 * 暂存文件；这样文件内容不会整份留在 JS 内存里，也不再受单条 JSON-RPC 体积上限
 * 约束。调用方用完必须调用 close()，让宿主释放句柄。
 *
 * @typedef {{
 *   fileName: string,
 *   size: number,
 *   contentType: string,
 *   readChunk: (offset: number, length: number) => Promise<{ dataBase64: string, eof: boolean, length: number }>,
 *   close: () => Promise<void>,
 * }} UploadSource
 */

/** 包装宿主 fileTransfer 返回的句柄。 */
function hostUploadSource(transfer, handle) {
  const fileName = String(handle.name || "upload.bin");
  const size = Math.max(0, Number(handle.size) || 0);
  let closed = false;
  return {
    fileName,
    size,
    contentType: typeof handle.contentType === "string" ? handle.contentType : "",
    async readChunk(offset, length) {
      const chunk = await transfer.read(handle.handleId, offset, length);
      if (!chunk || typeof chunk.dataBase64 !== "string") throw fileError("read");
      const bytes = Number(chunk.length) || decodeBase64(chunk.dataBase64).length;
      return { dataBase64: chunk.dataBase64, eof: Boolean(chunk.eof), length: bytes };
    },
    async close() {
      if (closed) return;
      closed = true;
      try {
        await transfer.cancel?.(handle.handleId);
      } catch {
        // 句柄关闭失败不影响已经暂存的内容。
      }
    },
  };
}

/**
 * 用宿主文件传输选择要上传的文件，返回分块上传源。
 *
 * 返回 null 表示宿主没有 fileTransfer（调用方应退回浏览器文件选择器）；
 * 返回空数组表示用户取消了选择。
 * @returns {Promise<UploadSource[] | null>}
 */
export async function pickUploadSources(options = {}) {
  const transfer = plugin?.fileTransfer;
  if (!transfer || typeof transfer.pick !== "function") return null;

  const selection = await transfer.pick({ multiple: options.multiple !== false });
  const handles = Array.isArray(selection?.files) ? selection.files : [];
  return handles.map((handle) => hostUploadSource(transfer, handle));
}

/**
 * 把浏览器 `<input type="file">` 选中的 File 包装成上传源（宿主没有 fileTransfer 时的兜底）。
 * @param {File} file
 * @returns {UploadSource}
 */
export function browserUploadSource(file) {
  const fileName = String(file?.name || "upload.bin");
  const size = Math.max(0, Number(file?.size) || 0);
  return {
    fileName,
    size,
    contentType: String(file?.type || ""),
    async readChunk(offset, length) {
      const start = Math.max(0, offset);
      if (start >= size) return { dataBase64: "", eof: true, length: 0 };
      const end = Math.min(size, start + length);
      const bytes = new Uint8Array(await file.slice(start, end).arrayBuffer());
      return { dataBase64: bytesToBase64(bytes), eof: end >= size, length: bytes.length };
    },
    async close() {},
  };
}

/** 保存时单次写入的合法块大小；超出范围的宿主建议值按边界收敛。 */
const SAVE_CHUNK_MIN = 64 * 1024;
const SAVE_CHUNK_MAX = 1024 * 1024;

/** base64 字符串解码后的字节数；用它判断"这一块是否写完了"，避免每次都真的解码。 */
function base64ByteLength(data) {
  const text = String(data || "");
  const padding = text.endsWith("==") ? 2 : text.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((text.length * 3) / 4) - padding);
}

/**
 * 打开一个「边收边写」的本地文件接收器。
 *
 * 响应体可能几百 MB，所以这里只负责把每一块交给宿主，绝不把内容留在内存里拼成整份
 * 再写。返回 null 表示宿主不支持该能力，或用户在系统保存对话框里取消了选择——两种情况
 * 对调用方都是"这次没有文件可写"，不需要区分。
 *
 * 返回值里的 `chunkBytes` 是给后端 `http/download/read` 用的单次读取上限：按宿主建议的
 * 写入块大小去取数据，既不撑爆桥接消息，也不会为了攒大块而多占内存。
 *
 * @param {{ name?: string, contentType?: string, size?: number }} [descriptor]
 * @returns {Promise<null | {
 *   chunkBytes: number,
 *   write: (base64: string) => Promise<number>,
 *   finish: () => Promise<void>,
 *   cancel: () => Promise<void>,
 * }>}
 */
export async function createFileSink(descriptor = {}) {
  const transfer = plugin?.fileTransfer;
  if (!transfer) return null;
  // 先把可选方法绑到对象上：后续回调里不能再依赖 TS 的属性收窄，也不能丢 this。
  const beginSave = typeof transfer.beginSave === "function" ? transfer.beginSave.bind(transfer) : null;
  const writeChunk = typeof transfer.write === "function" ? transfer.write.bind(transfer) : null;
  const finishSave = typeof transfer.finish === "function" ? transfer.finish.bind(transfer) : null;
  const cancelSave = typeof transfer.cancel === "function" ? transfer.cancel.bind(transfer) : null;
  if (!beginSave || !writeChunk) return null;

  const name = String(descriptor.name || "").trim() || DOWNLOAD_FALLBACK_NAME;
  const contentType = String(descriptor.contentType || "");
  const size = Number(descriptor.size);
  /** @type {{ name: string, contentType?: string, size?: number }} */
  const request = { name };
  // 空 Content-Type 与未知大小都不要传给宿主：前者会被当成 "text/plain" 之类的默认值，
  // 后者（-1）会让保存对话框显示一个错误的大小。
  if (contentType) request.contentType = contentType;
  if (Number.isFinite(size) && size > 0) request.size = Math.round(size);

  const target = await beginSave(request);
  if (!target || typeof target.handleId !== "string" || !target.handleId) return null;

  const handleId = target.handleId;
  const suggestedChunk = Number(target.chunkBytes);
  const chunkBytes =
    Number.isFinite(suggestedChunk) && suggestedChunk > 0
      ? Math.min(SAVE_CHUNK_MAX, Math.max(SAVE_CHUNK_MIN, Math.round(suggestedChunk)))
      : DOWNLOAD_CHUNK_DEFAULT;

  let offset = Number(target.offset) > 0 ? Math.round(Number(target.offset)) : 0;
  let closed = false;

  return {
    chunkBytes,

    /**
     * 写入一块 base64，返回实际写入的字节数。
     *
     * 宿主可能只吃下一部分（比如有自己的单次写入上限），所以按返回的 written / nextOffset
     * 推进 offset，并把剩下的字节重新编码后接着写——宁可多一次 RPC，也不能丢数据。
     * 只有当宿主真的只写了一半时才做解码/重编码，正常情况整块直传。
     */
    async write(base64) {
      if (closed) return 0;
      let data = String(base64 || "");
      if (!data) return 0;
      let writtenTotal = 0;
      for (;;) {
        const total = base64ByteLength(data);
        if (total <= 0) return writtenTotal;
        const result = await writeChunk(handleId, offset, data);
        const reported = Number(result?.written);
        const nextOffset = Number(result?.nextOffset);
        let consumed = Number.isFinite(reported) && reported > 0 ? Math.round(reported) : total;
        if (Number.isFinite(nextOffset) && nextOffset > offset) consumed = Math.round(nextOffset - offset);
        if (consumed <= 0) throw new Error("The host rejected the file write");
        consumed = Math.min(consumed, total);
        offset += consumed;
        writtenTotal += consumed;
        if (consumed >= total) return writtenTotal;
        data = bytesToBase64(decodeBase64(data).subarray(consumed));
      }
    },

    /** 收尾落盘；重复调用是安全的。失败时不标记为已关闭，让调用方还能 cancel 掉句柄。 */
    async finish() {
      if (closed) return;
      if (finishSave) await finishSave(handleId);
      closed = true;
    },

    /** 放弃这次保存；重复调用是安全的，失败也不抛（句柄由宿主自己回收）。 */
    async cancel() {
      if (closed) return;
      closed = true;
      try {
        await cancelSave?.(handleId);
      } catch {
        // 取消失败不影响已经展示给用户的状态。
      }
    },
  };
}

/**
 * 监听桌面端系统拖放。返回取消订阅函数；宿主不支持时返回 null。
 *
 * 桌面宿主会接管落到工作台上的系统拖拽，此时 DOM drop 收不到文件，必须用这条通道。
 */
export function onProtoFileDrop(handler, options = {}) {
  const transfer = plugin?.fileTransfer;
  if (!transfer || typeof transfer.onDrop !== "function") return null;
  const maxBytes = Number(options.maxBytes) || 0;
  return transfer.onDrop(async (handles) => {
    const files = [];
    for (const handle of Array.isArray(handles) ? handles : []) {
      files.push(await readHandleResult(transfer, handle, maxBytes));
    }
    try {
      handler(files);
    } catch {
      // 展示层自己的错误提示已经覆盖；这里只保证宿主回调不产生未处理的 rejection。
    }
  });
}

/**
 * 构造宿主存储。
 *
 * 宿主声明了 host.storage 且能力位未显式关闭时使用真实存储；否则退化为内存存储，
 * 并通过 onDegraded 通知上层展示提示。上层不需要关心用的是哪一种。
 * @param {{ onDegraded?: () => void, onWriteFailed?: () => void }} [hooks]
 * @returns {PluginStorage}
 */
export function createStorage(hooks = {}) {
  const memory = new Map();
  const supported = Boolean(
    plugin?.storage &&
      typeof plugin.storage.get === "function" &&
      typeof plugin.storage.set === "function" &&
      typeof plugin.storage.delete === "function" &&
      capabilities().storage !== false,
  );

  if (!supported && hooks.onDegraded) hooks.onDegraded();
  let warned = false;

  return {
    get(key) {
      if (supported) return plugin.storage.get(key).catch(() => null);
      return Promise.resolve(memory.has(key) ? memory.get(key) : null);
    },
    async set(key, value) {
      if (supported) {
        try {
          await plugin.storage.set(key, value);
          return true;
        } catch {
          if (!warned) {
            warned = true;
            hooks.onWriteFailed?.();
          }
          return false;
        }
      }
      memory.set(key, value);
      return true;
    },
    async remove(key) {
      if (supported) {
        try {
          await plugin.storage.delete(key);
        } catch {
          // 删除失败不影响内存态，上层会以内存里的索引为准。
        }
        return;
      }
      memory.delete(key);
    },
  };
}

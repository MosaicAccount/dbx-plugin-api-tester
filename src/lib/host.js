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

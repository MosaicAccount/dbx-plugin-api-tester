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

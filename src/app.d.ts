/**
 * 全局类型声明。
 *
 * 只描述 UI 侧真正会用到的那部分宿主契约：`window.dbxPlugin` 的方法与能力位。
 * 权威定义在 DBX 的 Host API 文档里；这里保持最小，避免与宿主实现漂移后产生
 * 虚假的类型安全感。
 */

declare module "*.css";

/** 宿主文件传输句柄（用于读取本地 .proto 文件）。 */
interface DbxPluginFileHandle {
  handleId: string;
  name?: string;
  size?: number;
  contentType?: string;
}

/** 宿主注入到沙箱 iframe 的桥接对象。 */
interface DbxPluginBridge {
  ready: Promise<unknown>;
  readonly contributionId?: string;
  readonly context?: Record<string, unknown>;
  readonly locale?: string;
  readonly capabilities?: Record<string, unknown>;
  invoke(method: string, params?: unknown, options?: { timeoutMs?: number }): Promise<any>;
  notify(method: string, params?: unknown): Promise<unknown>;
  copy(text: string): Promise<unknown>;
  storage: {
    get(key: string): Promise<unknown>;
    set(key: string, value: unknown): Promise<unknown>;
    delete(key: string): Promise<unknown>;
  };
  fileTransfer?: {
    pick(options: { multiple?: boolean }): Promise<{ files?: DbxPluginFileHandle[] } | null>;
    read(handleId: string, offset: number, length?: number): Promise<{ dataBase64: string; length: number; eof: boolean }>;
    cancel?(handleId: string): Promise<unknown>;
    onDrop?(fn: (files: DbxPluginFileHandle[], meta?: Record<string, unknown>) => void): () => void;
  };
  onContext?(fn: (context: Record<string, unknown>) => void): () => void;
  onEvent?(fn: (event: unknown) => void): () => void;
}

interface Window {
  dbxPlugin?: DbxPluginBridge;
  dbxPluginContext?: Record<string, unknown>;
}

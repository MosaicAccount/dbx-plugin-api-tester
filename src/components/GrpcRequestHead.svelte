<script>
  /**
   * gRPC 请求头区域（描述来源 / 目标 / 服务 / 方法）。
   *
   * 拆出来单独占满整行：方法、目标、proto 文件这些"接口信息"不应该被右侧响应栏挤窄。
   * 下面的消息 / 元数据编辑器和响应体做左右分栏。
   *
   * proto 文件只在「Proto 文件」模式下出现：没有文件时是一整块可点击 / 可拖入的虚线投放区，
   * 有文件时文件变成胶囊标签，导入入口收成标签行末尾的虚线小按钮，不再单独占一行。
   */
  import * as host from "../lib/host.js";
  import SelectMenu from "./SelectMenu.svelte";
  import {
    PROTO_FILE_COUNT_LIMIT,
    PROTO_FILE_LIMIT,
    PROTO_TOTAL_LIMIT,
    byteLength,
    formatBytes,
    protoDependents,
    protoImportGaps,
  } from "../lib/model.js";
  import {
    addGrpcProtoFiles,
    cancelCurrentRequest,
    closeGrpcStream,
    connectGrpc,
    finishGrpcStream,
    discoverGrpc,
    invokeGrpc,
    loadGrpcMethods,
    patchGrpc,
    removeGrpcProtoFile,
    setGrpcMethod,
    setGrpcSchemaMode,
    setGrpcService,
    showToast,
  } from "../lib/store.js";

  let {
    t,
    grpc,
    stream = null,
    sending = false,
    discovering = false,
    canceling = false,
    onconfirmremove = null,
  } = $props();

  /** 隐藏的文件选择器，宿主没有 fileTransfer 时的降级路径。 */
  let fileInput = $state(null);
  /** 正在把文件拖到面板上。 */
  let dragActive = $state(false);

  const fileMode = $derived(grpc.schemaMode === "file");
  /**
   * 客户端流与双向流可以逐条发送：顶部按钮变成「连接 / 终止」，
   * 一元与服务端流保持原来的「调用」。
   */
  const methodInfo = $derived(grpc.methods.find((item) => item.name === grpc.method) || null);
  /** 需要建会话的方法：服务端流、客户端流、双向流。一元仍然是一次性调用。 */
  const sessionMethod = $derived(Boolean(methodInfo?.serverStreaming || methodInfo?.clientStreaming));
  /** 可以逐条发送的方法：只有客户端流与双向流。 */
  const interactive = $derived(Boolean(methodInfo?.clientStreaming));
  const serverOnly = $derived(Boolean(methodInfo?.serverStreaming && !methodInfo?.clientStreaming));
  const streamActive = $derived(
    Boolean(stream?.id) && ["connecting", "open", "halfClosed"].includes(stream.state),
  );
  const sentCount = $derived(Number(stream?.outSeq) || 0);
  // 服务端流没有"已发送"的概念，显示已收到的条数更有意义。
  const pillCount = $derived(serverOnly ? Number(stream?.inSeq) || 0 : sentCount);
  const canFinish = $derived(stream?.state === "open" && !stream?.busy && !sending);
  const streamLabel = $derived.by(() => {
    switch (stream?.state) {
      case "connecting":
        return t("connecting");
      case "halfClosed":
        // 客户端流半关闭后是"等那一条响应"，双向流是"继续收"，措辞要分开。
        return stream?.kind === "client" ? t("streamWaitingReply") : t("streamHalfClosed");
      case "open":
        return stream?.kind === "server" ? t("streamReceiving") : t("streamOpen");
      default:
        return t("streamOpen");
    }
  });

  /** 服务端流点完「发送」会持续接收；一元不会，所以只有它需要这句说明。 */
  const protoFiles = $derived(Array.isArray(grpc.protoFiles) ? grpc.protoFiles : []);
  const protoBytes = $derived(protoFiles.reduce((total, file) => total + byteLength(file.content), 0));

  /**
   * 少导入一个被 import 的文件，整批就编译不过。后端会报第一个失败的文件，
   * 但光看报错很容易误以为是那个文件坏了，所以这里直接把缺失的文件和依赖它的文件列出来。
   */
  const importGaps = $derived(protoImportGaps(protoFiles));
  const missingHints = $derived.by(() =>
    [...importGaps.missing].map(([name, dependents]) =>
      t("protoMissingImport", { name, files: dependents.join(t("listSeparator")) }),
    ),
  );

  const serviceItems = $derived.by(() => {
    // 文件模式下还没导入文件时不要展示旧的 service：它来自上一次的 proto，已经无法解析。
    if (fileMode && !protoFiles.length) return [];
    const services = [...grpc.services];
    if (grpc.service && !services.includes(grpc.service)) services.unshift(grpc.service);
    // 文件模式下按钮叫「解析文件」，占位文案也跟着换，别让用户去找「发现服务」。
    if (!services.length) return [{ value: "", label: t(fileMode ? "parseFirst" : "chooseService"), disabled: true }];
    return services.map((name) => ({ value: name, label: name }));
  });

  /** 方法类型标签：一元 / 服务端流 / 客户端流 / 双向流。 */
  function kindLabel(info) {
    if (!info) return "";
    if (info.clientStreaming) return t(info.serverStreaming ? "bidiStreaming" : "clientStreaming");
    return t(info.serverStreaming ? "serverStreaming" : "unaryShort");
  }

  /** 标签配色复用侧栏 GET / GRPC 那套 token，四类方法各有颜色。 */
  function kindTone(info) {
    if (!info) return "other";
    if (info.clientStreaming) return info.serverStreaming ? "post" : "put";
    return info.serverStreaming ? "head" : "other";
  }

  const methodItems = $derived.by(() => {
    if (fileMode && !protoFiles.length) return [];
    const byName = new Map(grpc.methods.map((method) => [method.name, method]));
    let names = grpc.methods.map((method) => method.name);
    if (grpc.method && !names.includes(grpc.method)) names = [grpc.method, ...names];
    if (!names.length) return [{ value: "", label: t(fileMode ? "parseFirstMethod" : "discoverFirst"), disabled: true }];
    // 带类型标签：选方法时就能看出它是一元还是流式，不用先选中再读签名行。
    return names.map((name) => ({
      value: name,
      label: name,
      badge: kindLabel(byName.get(name)),
      tone: kindTone(byName.get(name)),
    }));
  });

  const signature = $derived.by(() => {
    if (fileMode && !protoFiles.length) return "";
    if (!grpc.method) return "";
    const info = grpc.methods.find((method) => method.name === grpc.method);
    if (!info) return grpc.service ? `${grpc.service}/${grpc.method}` : grpc.method;
    return `${info.inputType} → ${info.outputType}`;
  });

  function onTargetInput(value) {
    const patch = { target: value };
    // 粘贴 https:// / grpcs:// 地址时自动开启 TLS，省一次手动勾选。
    if (/^(https|grpcs):\/\//i.test(value) && !grpc.tls) patch.tls = true;
    patchGrpc(patch);
  }

  function onTlsChange(checked) {
    patchGrpc({ tls: checked, ...(checked ? {} : { skipTlsVerify: false }) });
  }

  /** 读取 File 对象为文本；Blob.text() 在旧 WebView 里缺失时退回 FileReader。 */
  function readFileText(file) {
    if (typeof file.text === "function") return file.text();
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result ?? ""));
      reader.onerror = () => reject(reader.error || new Error("read failed"));
      reader.readAsText(file);
    });
  }

  function applyProtoFiles(entries) {
    if (sending || discovering) return;
    for (const entry of entries) {
      if (entry.error === "size") {
        showToast(t("protoFileTooLarge", { name: entry.name, size: formatBytes(PROTO_FILE_LIMIT) }), "error");
      } else if (entry.error) {
        showToast(t("protoFileUnreadable", { name: entry.name }), "error");
      }
    }
    const readable = entries.filter((entry) => !entry.error);
    const protoEntries = readable.filter((entry) => /\.proto$/i.test(entry.name || ""));
    if (readable.length && !protoEntries.length) {
      showToast(t("notProtoFile", { name: readable[0]?.name || "?" }), "warn");
      return;
    }
    if (!protoEntries.length) return;

    const result = addGrpcProtoFiles(protoEntries);
    if (result.rejected.length) {
      const rejection = result.rejected[0];
      const message =
        rejection.reason === "size"
          ? t("protoFileTooLarge", { name: rejection.name, size: formatBytes(PROTO_FILE_LIMIT) })
          : rejection.reason === "total"
            ? t("protoTotalTooLarge", { size: formatBytes(PROTO_TOTAL_LIMIT) })
            : rejection.reason === "count"
              ? t("protoFileLimit", { count: PROTO_FILE_COUNT_LIMIT })
              : rejection.reason === "duplicate"
                ? t("protoDuplicateFile", { name: rejection.name })
                : t("protoFileUnreadable", { name: rejection.name });
      showToast(message, "error");
    }
    if (!result.added) return;
    showToast(t("protoImported", { count: result.added }), "ok");
    // 导入后立即解析一遍，服务下拉框马上可用；解析失败会显示在响应面板里。
    void discoverGrpc();
  }

  async function onImportProto() {
    if (sending || discovering) return;
    if (host.canPickFiles()) {
      try {
        const picked = await host.pickProtoFiles({ maxBytes: PROTO_FILE_LIMIT });
        if (picked) {
          applyProtoFiles(picked);
          return;
        }
      } catch {
        // 宿主文件对话框不可用时退回 <input type="file">。
      }
    }
    fileInput?.click();
    // 沙箱 iframe 可能拦截文件选择框；拖放不受影响，给用户一条兜底提示。
    if (!host.canPickFiles()) showToast(t("protoDragHint"), "warn");
  }

  async function onFileInput(event) {
    const input = event.currentTarget;
    const files = [...(input.files || [])];
    input.value = "";
    await loadProtoEntries(files);
  }

  async function loadProtoEntries(files) {
    const entries = [];
    for (const file of files) {
      // 先按浏览器给出的 size 拒绝超大文件，避免白读一遍。
      if (Number(file.size) > PROTO_FILE_LIMIT) {
        entries.push({ name: file.name, content: "", error: "size" });
        continue;
      }
      try {
        entries.push({ name: file.name, content: await readFileText(file) });
      } catch {
        entries.push({ name: file.name, content: "", error: "unreadable" });
      }
    }
    applyProtoFiles(entries);
  }

  function hasFiles(event) {
    const types = event.dataTransfer?.types;
    return types ? [...types].includes("Files") : false;
  }

  function onDragOver(event) {
    if (!hasFiles(event)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
    dragActive = true;
  }

  function onDragLeave(event) {
    // 只在真正离开面板时收起高亮，避免子元素之间移动时闪烁。
    if (event.currentTarget.contains(event.relatedTarget)) return;
    dragActive = false;
  }

  function onFileDrop(event) {
    if (!hasFiles(event)) return;
    event.preventDefault();
    dragActive = false;
    void loadProtoEntries([...(event.dataTransfer?.files || [])]);
  }

  // 桌面宿主会接管系统拖放，DOM drop 收不到文件；两条通道都接上。
  $effect(() => {
    const off = host.onProtoFileDrop((files) => applyProtoFiles(files), { maxBytes: PROTO_FILE_LIMIT });
    return () => off?.();
  });

  /**
   * 删除一个已导入的文件。
   *
   * 被别的文件 import 时先交由父组件确认：删掉这类文件会让整批编译失败，
   * 属于"看不出后果的操作"，不该点一下就生效。
   */
  function onRemoveProtoFile(name) {
    const dependents = protoDependents(protoFiles, name);
    if (dependents.length && onconfirmremove) {
      onconfirmremove(name, dependents);
      return;
    }
    dropProtoFile(name);
  }

  /** 真正执行删除：清掉文件并重新解析剩下的组合。 */
  function dropProtoFile(name) {
    const remaining = protoFiles.filter((file) => file.name !== name).length;
    removeGrpcProtoFile(name);
    // 还剩文件时立刻重新解析，避免下拉框停留在被删文件的服务上。
    if (remaining) void discoverGrpc();
  }
</script>

<div
  class="grpc-head"
  class:is-dragging={dragActive}
  role="group"
  aria-label={t("grpcRequest")}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onFileDrop}
>
  <div class="request-bar">
    <input
      class="input url-input"
      type="text"
      spellcheck="false"
      placeholder={t("targetPlaceholder")}
      aria-label={t("grpcTargetLabel")}
      value={grpc.target}
      oninput={(event) => onTargetInput(event.currentTarget.value)}
      onkeydown={(event) => {
        if (event.key !== "Enter") return;
        event.preventDefault();
        void discoverGrpc();
      }}
    />
    <label class="check check--inline">
      <input type="checkbox" checked={Boolean(grpc.tls)} onchange={(event) => onTlsChange(event.currentTarget.checked)} />
      <span>{t("tls")}</span>
    </label>
    {#if discovering}
      <!-- 反射/解析同样可能长时间没有响应，发现中也允许终止。 -->
      <button
        class="btn btn-danger"
        type="button"
        title={t("stop")}
        disabled={canceling}
        onclick={() => void cancelCurrentRequest()}
      >
        {canceling ? t("stopping") : t("stop")}
      </button>
    {:else}
      <button
        class="btn btn-secondary"
        type="button"
        disabled={sending || (fileMode && !protoFiles.length)}
        onclick={() => void discoverGrpc()}
      >
        {fileMode ? t("parseProto") : t("discover")}
      </button>
    {/if}
    {#if sessionMethod}
      {#if streamActive}
        <!-- 连接状态占用「连接」按钮的位置：不再单开一行状态条。 -->
        <span class="stream-pill" data-tone={stream?.state === "connecting" ? "idle" : "live"}>
          <span class="stream-dot" aria-hidden="true"></span>
          {streamLabel}{#if pillCount > 0}<span class="stream-count">· {t("streamSentShort", { count: pillCount })}</span>{/if}
        </span>
        {#if interactive}
          <button
            class="btn btn-secondary"
            type="button"
            title={t("finishSendingHint")}
            disabled={!canFinish}
            onclick={() => void finishGrpcStream()}
          >
            {stream?.state === "halfClosed" ? t("finishedSending") : t("finishSending")}
          </button>
        {/if}
        <button
          class="btn btn-ghost btn-sm stream-stop"
          type="button"
          title={t("stopStreamHint")}
          disabled={stream?.busy === "close"}
          onclick={() => void closeGrpcStream()}
        >
          {t("stopStream")}
        </button>
      {:else}
        <button
          class="btn btn-primary"
          type="button"
          disabled={sending || discovering || stream?.busy === "close"}
          onclick={() => void connectGrpc()}
        >
          {serverOnly ? t("send") : t("connect")}
        </button>
      {/if}
    {:else if sending}
      <button
        class="btn btn-danger"
        type="button"
        title={t("stop")}
        disabled={canceling}
        onclick={() => void cancelCurrentRequest()}
      >
        {canceling ? t("stopping") : t("stop")}
      </button>
    {:else}
      <button class="btn btn-primary" type="button" disabled={discovering} onclick={() => void invokeGrpc()}>
        {t("send")}
      </button>
    {/if}
  </div>

  <div class="grpc-schema">
    <span class="grpc-schema-label">{t("schemaSource")}</span>
    <div class="segmented segmented--small" role="group" aria-label={t("schemaSource")}>
      <button
        class="seg-btn"
        class:is-active={!fileMode}
        type="button"
        aria-pressed={!fileMode}
        disabled={sending || discovering}
        onclick={() => setGrpcSchemaMode("reflection")}>{t("schemaReflection")}</button
      >
      <button
        class="seg-btn"
        class:is-active={fileMode}
        type="button"
        aria-pressed={fileMode}
        disabled={sending || discovering}
        onclick={() => setGrpcSchemaMode("file")}>{t("schemaFile")}</button
      >
    </div>
  </div>

  {#if fileMode}
    {#if protoFiles.length}
      <div class="proto-files">
        {#each protoFiles as file (file.name)}
          <span class="proto-chip" class:is-broken={importGaps.broken.has(file.name)} title={file.name}>
            <span class="proto-chip-name">{file.name}</span>
            <button
              class="proto-chip-remove"
              type="button"
              aria-label={t("removeProtoFile", { name: file.name })}
              title={t("removeProtoFile", { name: file.name })}
              onclick={() => onRemoveProtoFile(file.name)}
            >
              <svg viewBox="0 0 9 9" width="9" height="9" fill="none" aria-hidden="true"
                ><path d="M2 2l5 5M7 2l-5 5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" /></svg
              >
            </button>
          </span>
        {/each}
        <button
          class="proto-add"
          type="button"
          title={t("protoFilesHint")}
          disabled={sending || discovering}
          onclick={() => void onImportProto()}
        >
          <span class="plus" aria-hidden="true">+</span>{t("importProto")}
        </button>
        <span class="proto-total">{t("protoFileCount", { count: protoFiles.length, size: formatBytes(protoBytes) })}</span>
      </div>
      {#if missingHints.length}
        <div class="proto-missing" role="status">
          {#each missingHints as hint (hint)}<span>{hint}</span>{/each}
        </div>
      {/if}
    {:else}
      <button
        class="proto-drop"
        type="button"
        title={t("protoFilesHint")}
        disabled={sending || discovering}
        onclick={() => void onImportProto()}
      >
        <span class="proto-drop-icon" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="14" height="14" fill="none">
            <path
              d="M8 10.4V3.4m0 0L5.4 6M8 3.4l2.6 2.6"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
            <path
              d="M3 10.2v1.4c0 .8.6 1.4 1.4 1.4h7.2c.8 0 1.4-.6 1.4-1.4v-1.4"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
            />
          </svg>
        </span>
        <span class="proto-drop-copy">
          <span class="proto-drop-title">{t("protoDropTitle")}</span>
          <span class="proto-drop-sub">{t("protoDropHint")}</span>
        </span>
      </button>
    {/if}
  {/if}

  <div class="grpc-selects">
    <div class="field">
      <span>{t("service")}</span>
      <SelectMenu
        items={serviceItems}
        value={grpc.service}
        disabled={!grpc.services.length}
        placeholder={fileMode && !protoFiles.length ? t("protoServicePlaceholder") : ""}
        ariaLabel={t("service")}
        onchange={(value) => {
          setGrpcService(value);
          void loadGrpcMethods();
        }}
      />
    </div>
    <div class="field">
      <span>{t("method")}</span>
      <SelectMenu
        items={methodItems}
        value={grpc.method}
        disabled={!grpc.methods.length && !grpc.method}
        placeholder={fileMode && !protoFiles.length ? t("protoServicePlaceholder") : ""}
        ariaLabel={t("method")}
        onchange={(value) => setGrpcMethod(value)}
      />
    </div>
  </div>

  <div class="grpc-signature">{signature}</div>


  <input
    class="visually-hidden"
    type="file"
    accept=".proto"
    multiple
    bind:this={fileInput}
    tabindex="-1"
    aria-hidden="true"
    onchange={onFileInput}
  />
</div>

<style>
  .stream-stop {
    color: var(--muted);
  }

  .stream-stop:hover:not(:disabled) {
    color: var(--danger, #c0442f);
  }

  .stream-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 32px;
    padding: 0 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm, 6px);
    font-size: 12px;
    color: var(--fg);
    white-space: nowrap;
  }

  .stream-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--muted);
  }

  .stream-pill[data-tone="live"] .stream-dot {
    background: var(--success, #2f9e73);
  }

  .stream-count {
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .grpc-head.is-dragging {
    outline: 2px dashed var(--primary);
    outline-offset: -4px;
    border-radius: 8px;
  }

  .plus {
    font-size: 14px;
    line-height: 1;
  }
</style>

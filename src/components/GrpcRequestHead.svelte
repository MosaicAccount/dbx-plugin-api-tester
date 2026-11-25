<script>
  /**
   * gRPC 请求头区域（描述来源 / 目标 / 服务 / 方法）。
   *
   * 拆出来单独占满整行：方法、目标、proto 文件这些"接口信息"不应该被右侧响应栏挤窄。
   * 下面的消息 / 元数据编辑器和响应体做左右分栏。
   */
  import * as host from "../lib/host.js";
  import SelectMenu from "./SelectMenu.svelte";
  import {
    PROTO_FILE_COUNT_LIMIT,
    PROTO_FILE_LIMIT,
    PROTO_TOTAL_LIMIT,
    byteLength,
    formatBytes,
  } from "../lib/model.js";
  import {
    addGrpcProtoFiles,
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

  let { t, grpc, sending = false, discovering = false } = $props();

  /** 隐藏的文件选择器，宿主没有 fileTransfer 时的降级路径。 */
  let fileInput = $state(null);
  /** 正在把文件拖到面板上。 */
  let dragActive = $state(false);

  const fileMode = $derived(grpc.schemaMode === "file");
  const protoFiles = $derived(Array.isArray(grpc.protoFiles) ? grpc.protoFiles : []);
  const protoBytes = $derived(protoFiles.reduce((total, file) => total + byteLength(file.content), 0));

  const serviceItems = $derived.by(() => {
    // 文件模式下还没导入文件时不要展示旧的 service：它来自上一次的 proto，已经无法解析。
    if (fileMode && !protoFiles.length) return [];
    const services = [...grpc.services];
    if (grpc.service && !services.includes(grpc.service)) services.unshift(grpc.service);
    if (!services.length) return [{ value: "", label: t("chooseService"), disabled: true }];
    return services.map((name) => ({ value: name, label: name }));
  });

  const methodItems = $derived.by(() => {
    if (fileMode && !protoFiles.length) return [];
    let names = grpc.methods.map((method) => method.name);
    if (grpc.method && !names.includes(grpc.method)) names = [grpc.method, ...names];
    if (!names.length) return [{ value: "", label: t("discoverFirst"), disabled: true }];
    return names.map((name) => ({ value: name, label: name }));
  });

  const signature = $derived.by(() => {
    if (fileMode && !protoFiles.length) return "";
    if (!grpc.method) return "";
    const info = grpc.methods.find((method) => method.name === grpc.method);
    if (!info) return grpc.service ? `${grpc.service}/${grpc.method}` : grpc.method;
    const kind = info.clientStreaming
      ? info.serverStreaming
        ? t("bidiStreaming")
        : t("clientStreaming")
      : info.serverStreaming
        ? t("serverStreaming")
        : t("unary");
    return `${info.inputType} → ${info.outputType} · ${kind}`;
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

  function onRemoveProtoFile(name) {
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
    <button class="btn btn-secondary" type="button" disabled={sending || discovering} onclick={() => void discoverGrpc()}>
      {discovering ? t("discovering") : fileMode ? t("parseProto") : t("discover")}
    </button>
    <button class="btn btn-primary" type="button" disabled={sending || discovering} onclick={() => void invokeGrpc()}>
      {t("invoke")}
    </button>
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
    <span class="grow"></span>
    <button
      class="btn btn-secondary btn-sm"
      type="button"
      title={t("protoFilesHint")}
      disabled={sending || discovering}
      onclick={() => void onImportProto()}
    >
      <span class="plus" aria-hidden="true">+</span>{t("importProto")}
    </button>
  </div>

  {#if fileMode && protoFiles.length}
    <div class="proto-files">
      {#each protoFiles as file (file.name)}
        <span class="proto-chip" title={file.name}>
          <span class="proto-chip-name">{file.name}</span>
          <button
            class="proto-chip-remove"
            type="button"
            aria-label={t("removeProtoFile", { name: file.name })}
            title={t("removeProtoFile", { name: file.name })}
            onclick={() => onRemoveProtoFile(file.name)}>×</button
          >
        </span>
      {/each}
      <span class="proto-total">{t("protoFileCount", { count: protoFiles.length, size: formatBytes(protoBytes) })}</span>
    </div>
  {:else if fileMode}
    <p class="hint proto-hint">{t("protoFilesHint")}</p>
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
  .grpc-head.is-dragging {
    outline: 2px dashed var(--primary);
    outline-offset: -4px;
    border-radius: 8px;
  }

  .grow {
    flex: 1;
  }

  .plus {
    font-size: 14px;
    line-height: 1;
  }
</style>

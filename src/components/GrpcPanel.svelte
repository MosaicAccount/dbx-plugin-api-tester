<script>
  /**
   * gRPC 调用面板。
   *
   * 服务与方法来自后端的 reflection 发现结果，所以下拉里要处理"尚未发现"的空状态，
   * 而不是显示一个空列表让人猜。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import KVEditor from "./KVEditor.svelte";
  import CodeEditor from "./CodeEditor.svelte";
  import { GRPC_TIMEOUT_DEFAULT, TIMEOUT_MAX, blankKV, clampNumber } from "../lib/model.js";
  import {
    addGrpcMessage,
    discoverGrpc,
    formatGrpcMessage,
    invokeGrpc,
    loadGrpcMethods,
    patchGrpc,
    removeGrpcMessage,
    setGrpcMessage,
    setGrpcMethod,
    setGrpcService,
  } from "../lib/store.js";

  let { t, grpc, sending = false, discovering = false } = $props();

  let tab = $state("metadata");

  const methodInfo = $derived(grpc.methods.find((method) => method.name === grpc.method) || null);
  /** 客户端流与双向流可以填多条消息；一元与服务端流协议上只接受一条。 */
  const multiMessage = $derived(Boolean(methodInfo?.clientStreaming));
  const visibleMessages = $derived(multiMessage ? grpc.messages : grpc.messages.slice(0, 1));

  const serviceItems = $derived.by(() => {
    const services = [...grpc.services];
    if (grpc.service && !services.includes(grpc.service)) services.unshift(grpc.service);
    if (!services.length) return [{ value: "", label: t("chooseService"), disabled: true }];
    return services.map((name) => ({ value: name, label: name }));
  });

  const methodItems = $derived.by(() => {
    let names = grpc.methods.map((method) => method.name);
    if (grpc.method && !names.includes(grpc.method)) names = [grpc.method, ...names];
    if (!names.length) return [{ value: "", label: t("discoverFirst"), disabled: true }];
    return names.map((name) => ({ value: name, label: name }));
  });

  const signature = $derived.by(() => {
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

</script>

<div class="editor-pane">
  <div class="request-bar">
    <input
      class="input url-input"
      type="text"
      spellcheck="false"
      placeholder={t("targetPlaceholder")}
      aria-label="gRPC target"
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
      {discovering ? t("discovering") : t("discover")}
    </button>
    <button class="btn btn-primary" type="button" disabled={sending || discovering} onclick={() => void invokeGrpc()}>
      {t("invoke")}
    </button>
  </div>

  <div class="grpc-selects">
    <div class="field">
      <span>{t("service")}</span>
      <SelectMenu
        items={serviceItems}
        value={grpc.service}
        disabled={!grpc.services.length}
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
        ariaLabel={t("method")}
        onchange={(value) => setGrpcMethod(value)}
      />
    </div>
  </div>

  <div class="grpc-signature">{signature}</div>

  <div class="tabs">
    {#each ["metadata", "message", "options"] as name (name)}
      <button class="tab" class:is-active={tab === name} type="button" onclick={() => (tab = name)}>
        {t(name)}
      </button>
    {/each}
  </div>

  {#if tab === "metadata"}
    <div class="tab-panel is-active">
      <KVEditor t={t} scope="grpc" listName="metadata" rows={grpc.metadata} namePlaceholder={t("key")} valuePlaceholder={t("value")} />
      <div class="panel-row">
        <button
          class="btn btn-ghost btn-sm"
          type="button"
          onclick={() => patchGrpc({ metadata: [...grpc.metadata, blankKV()] })}>{t("addMetadata")}</button
        >
      </div>
      <p class="hint">{t("metadataHint")}</p>
    </div>
  {:else if tab === "message"}
    <div class="tab-panel is-active">
      <p class="hint">{multiMessage ? t("clientStreamHint") : t("messageHint")}</p>
      {#each visibleMessages as message, index (index)}
        <div class="message-block">
          <div class="panel-row">
            {#if multiMessage}
              <span class="hint">{t("messageIndex", { index: index + 1 })}</span>
            {/if}
            <span class="grow"></span>
            {#if multiMessage && grpc.messages.length > 1}
              <button class="btn btn-ghost btn-sm" type="button" onclick={() => removeGrpcMessage(index)}>
                {t("removeMessage")}
              </button>
            {/if}
            <button class="btn btn-ghost btn-sm" type="button" onclick={() => formatGrpcMessage(index)}>
              {t("format")}
            </button>
          </div>
          <CodeEditor
            value={message}
            placeholder={"{ }"}
            ariaLabel={multiMessage ? t("messageIndex", { index: index + 1 }) : t("message")}
            json
            onchange={(value) => setGrpcMessage(index, value)}
          />
        </div>
      {/each}
      {#if multiMessage}
        <div class="panel-row">
          <button class="btn btn-ghost btn-sm" type="button" onclick={() => addGrpcMessage()}>{t("addMessage")}</button>
        </div>
      {/if}
    </div>
  {:else}
    <div class="tab-panel is-active">
      <div class="form-grid">
        <label class="field">
          <span>{t("timeout")}</span>
          <input
            class="input"
            type="number"
            min="1"
            max={TIMEOUT_MAX}
            step="1000"
            value={grpc.timeoutMs}
            oninput={(event) =>
              patchGrpc({ timeoutMs: clampNumber(event.currentTarget.value, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX) })}
          />
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={Boolean(grpc.skipTlsVerify)}
            disabled={!grpc.tls}
            onchange={(event) => patchGrpc({ skipTlsVerify: event.currentTarget.checked })}
          />
          <span>{t("skipTls")}</span>
        </label>
      </div>
      <p class="hint">{t("grpcHint")}</p>
    </div>
  {/if}
</div>

<style>
  .grow {
    flex: 1;
  }

  .message-block + .message-block {
    margin-top: 12px;
    padding-top: 12px;
    border-top: 1px solid var(--color-border);
  }
</style>

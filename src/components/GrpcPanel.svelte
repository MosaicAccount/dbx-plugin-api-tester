<script>
  /**
   * gRPC 请求编辑面板（tab 内容）。
   *
   * 目标 / 描述来源 / 服务 / 方法在 GrpcRequestHead 里，占满整行；这里只负责
   * 元数据 / 消息 / 选项三个 tab 的内容，和右侧响应栏做左右分栏。
   */
  import { tick } from "svelte";
  import KVEditor from "./KVEditor.svelte";
  import CodeEditor from "./CodeEditor.svelte";
  import { GRPC_TIMEOUT_DEFAULT, TIMEOUT_MAX, blankKV, clampNumber } from "../lib/model.js";
  import {
    addGrpcMessage,
    formatGrpcMessage,
    patchGrpc,
    removeGrpcMessage,
    setGrpcMessage,
  } from "../lib/store.js";

  /**
   * @type {{
   *   t: (key: string, variables?: Record<string, unknown>) => string,
   *   grpc: any,
   *   sending?: boolean,
   *   defaultTimeout?: number,
   * }}
   */
  let { t, grpc, sending = false, defaultTimeout = GRPC_TIMEOUT_DEFAULT } = $props();

  let tab = $state("metadata");
  /** 消息列表容器：新增消息后用它滚到底部。 */
  let messageList = $state(null);

  const methodInfo = $derived(grpc.methods.find((method) => method.name === grpc.method) || null);
  /** 客户端流与双向流可以填多条消息；一元与服务端流协议上只接受一条。 */
  const multiMessage = $derived(Boolean(methodInfo?.clientStreaming));
  const visibleMessages = $derived(multiMessage ? grpc.messages : grpc.messages.slice(0, 1));

  async function onAddMessage() {
    addGrpcMessage();
    await tick();
    if (!messageList) return;
    messageList.scrollTop = messageList.scrollHeight;
    const areas = messageList.querySelectorAll("textarea.code-area");
    const last = areas[areas.length - 1];
    if (!last) return;
    last.focus({ preventScroll: true });
    last.setSelectionRange(last.value.length, last.value.length);
    messageList.scrollTop = messageList.scrollHeight;
  }
</script>

<div class="editor-pane">
  <div class="tabs">
    {#each ["metadata", "message", "options"] as name (name)}
      <button class="tab" class:is-active={tab === name} type="button" onclick={() => (tab = name)}>
        {t(name)}
      </button>
    {/each}
    <span class="grow"></span>
    {#if tab === "message" && multiMessage}
      <button class="btn btn-secondary btn-sm message-add" type="button" disabled={sending} onclick={() => void onAddMessage()}>
        <span class="plus" aria-hidden="true">+</span>{t("addMessage")}
      </button>
    {/if}
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
    <div class="tab-panel is-active tab-panel--messages">
      <p class="hint message-hint">{multiMessage ? t("clientStreamHint") : t("messageHint")}</p>
      <div class="message-list" class:is-single={!multiMessage} bind:this={messageList}>
        {#each visibleMessages as message, index (index)}
          <div class="message-block">
            <div class="message-head">
              <span class="message-label">{multiMessage ? t("messageIndex", { index: index + 1 }) : t("message")}</span>
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
              placeholder={t("messagePlaceholder")}
              ariaLabel={multiMessage ? t("messageIndex", { index: index + 1 }) : t("message")}
              json
              fill={!multiMessage}
              onchange={(value) => setGrpcMessage(index, value)}
            />
          </div>
        {/each}
      </div>
    </div>
  {:else}
    <div class="tab-panel is-active">
      <div class="form-grid">
        <div class="field">
          <span>{t("timeout")}</span>
          <input
            class="input"
            type="number"
            min="1"
            max={TIMEOUT_MAX}
            step="1000"
            aria-label={t("timeout")}
            value={grpc.timeoutMs}
            oninput={(event) =>
              patchGrpc({ timeoutMs: clampNumber(event.currentTarget.value, GRPC_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX) })}
          />
          <button class="link-btn field-action" type="button" onclick={() => patchGrpc({ timeoutMs: defaultTimeout })}>
            {t("useDefaultTimeout", { value: defaultTimeout })}
          </button>
        </div>
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

  .plus {
    font-size: 14px;
    line-height: 1;
  }

  .message-add {
    margin-left: auto;
  }

  .message-hint {
    flex: none;
    margin: 4px 0 6px;
  }

  .message-block + .message-block {
    margin-top: 12px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
  }

  .field-action {
    padding: 0;
    color: var(--muted);
    font-size: 11px;
    text-align: left;
    white-space: nowrap;
  }

  .field-action:hover {
    color: var(--fg);
    text-decoration: underline;
  }
</style>

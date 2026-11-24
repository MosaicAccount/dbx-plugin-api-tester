<script>
  /**
   * HTTP 请求编辑面板。
   *
   * 只负责展示与收集输入，所有状态变更都调 store 的 action。
   * 面板内部的 tab（参数/请求头/请求体/选项）是本组件的局部状态。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import KVEditor from "./KVEditor.svelte";
  import CodeEditor from "./CodeEditor.svelte";
  import {
    HTTP_METHODS,
    HTTP_TIMEOUT_DEFAULT,
    TIMEOUT_MAX,
    BODY_TYPES,
    blankKV,
    clampNumber,
    contentTypeForBody,
    buildRequestUrl,
  } from "../lib/model.js";
  import { patchHttp, sendHttp, showToast } from "../lib/store.js";

  let { t, http, sending = false } = $props();

  let tab = $state("params");

  const methodItems = $derived(
    [...new Set([...HTTP_METHODS, http.method])].map((method) => ({ value: method, label: method })),
  );
  const effectiveUrl = $derived.by(() => {
    const raw = http.url.trim();
    if (!raw) return "";
    return buildRequestUrl(raw, http.params) || raw;
  });
  const bodyTypeOptions = $derived([
    { value: "none", label: t("none") },
    { value: "json", label: "JSON" },
    { value: "text", label: t("text") },
    { value: "xml", label: "XML" },
    { value: "form", label: t("form") },
  ]);
  const bodyType = $derived(BODY_TYPES.includes(http.bodyType) ? http.bodyType : "none");

  function formatBody() {
    if (http.bodyType !== "json") return;
    const raw = http.bodyText;
    if (!raw.trim()) return;
    try {
      patchHttp({ bodyText: JSON.stringify(JSON.parse(raw), null, 2) });
    } catch {
      showToast(t("invalidJson"), "error");
    }
  }
</script>

<div class="editor-pane">
  <div class="request-bar">
    <SelectMenu
      items={methodItems}
      value={http.method}
      ariaLabel="HTTP method"
      onchange={(value) => patchHttp({ method: value })}
    />
    <input
      class="input url-input"
      type="text"
      spellcheck="false"
      placeholder={t("urlPlaceholder")}
      aria-label="URL"
      value={http.url}
      oninput={(event) => patchHttp({ url: event.currentTarget.value })}
      onkeydown={(event) => {
        if (event.key !== "Enter") return;
        event.preventDefault();
        void sendHttp();
      }}
    />
    <button class="btn btn-primary" type="button" disabled={sending} onclick={() => void sendHttp()}>
      {sending ? t("sending") : t("send")}
    </button>
  </div>

  <div class="tabs">
    {#each ["params", "headers", "body", "options"] as name (name)}
      <button
        class="tab"
        class:is-active={tab === name}
        type="button"
        onclick={() => (tab = name)}
      >
        {t(name)}
      </button>
    {/each}
  </div>

  {#if tab === "params"}
    <div class="tab-panel is-active">
      <KVEditor t={t} scope="http" listName="params" rows={http.params} namePlaceholder={t("key")} valuePlaceholder={t("value")} />
      <div class="panel-row">
        <button class="btn btn-ghost btn-sm" type="button" onclick={() => patchHttp({ params: [...http.params, blankKV()] })}>{t("addParam")}</button>
      </div>
      <div class="effective-url">
        <span class="effective-label">{t("effectiveUrl")}</span>
        <code title={effectiveUrl}>{effectiveUrl || "—"}</code>
      </div>
    </div>
  {:else if tab === "headers"}
    <div class="tab-panel is-active">
      <KVEditor t={t} scope="http" listName="headers" rows={http.headers} namePlaceholder={t("key")} valuePlaceholder={t("value")} />
      <div class="panel-row">
        <button class="btn btn-ghost btn-sm" type="button" onclick={() => patchHttp({ headers: [...http.headers, blankKV()] })}>{t("addHeader")}</button>
      </div>
      <p class="hint">{t("headerHint")}</p>
    </div>
  {:else if tab === "body"}
    <div class="tab-panel is-active">
      <div class="panel-row">
        <SelectMenu
          items={bodyTypeOptions}
          value={bodyType}
          ariaLabel={t("body")}
          onchange={(value) => patchHttp({ bodyType: value })}
        />
        <span class="grow"></span>
        <button class="btn btn-ghost btn-sm" type="button" disabled={bodyType === "none"} onclick={formatBody}>
          {t("format")}
        </button>
      </div>
      {#if bodyType === "none"}
        <p class="hint">{t("none")}</p>
      {:else}
        <CodeEditor
          value={http.bodyText}
          placeholder="Request body"
          ariaLabel={t("body")}
          json={bodyType === "json"}
          onchange={(value) => patchHttp({ bodyText: value })}
        />
        <p class="hint">{t("contentTypeHint", { type: contentTypeForBody(bodyType) })}</p>
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
            value={http.timeoutMs}
            oninput={(event) =>
              patchHttp({ timeoutMs: clampNumber(event.currentTarget.value, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX) })}
          />
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={http.followRedirects !== false}
            onchange={(event) => patchHttp({ followRedirects: event.currentTarget.checked })}
          />
          <span>{t("followRedirects")}</span>
        </label>
        <label class="check">
          <input
            type="checkbox"
            checked={Boolean(http.skipTlsVerify)}
            onchange={(event) => patchHttp({ skipTlsVerify: event.currentTarget.checked })}
          />
          <span>{t("skipTls")}</span>
        </label>
      </div>
      <p class="hint warn">{t("tlsHint")}</p>
    </div>
  {/if}
</div>

<style>
  .grow {
    flex: 1;
  }
</style>

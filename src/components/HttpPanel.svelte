<script>
  /**
   * HTTP 请求编辑面板（tab 内容）。
   *
   * 方法 / URL 那一行在 HttpRequestBar 里，占满整行；这里只负责
   * 参数 / 请求头 / 请求体 / 选项四个 tab 的内容。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import KVEditor from "./KVEditor.svelte";
  import CodeEditor from "./CodeEditor.svelte";
  import {
    HTTP_TIMEOUT_DEFAULT,
    TIMEOUT_MAX,
    BODY_TYPES,
    blankKV,
    clampNumber,
    contentTypeForBody,
    buildRequestUrl,
  } from "../lib/model.js";
  import { patchHttp, showToast } from "../lib/store.js";

  let { t, http } = $props();

  let tab = $state("params");

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
    <div class="tab-panel is-active tab-panel--fill">
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
          placeholder={t("bodyPlaceholder")}
          ariaLabel={t("body")}
          json={bodyType === "json"}
          fill
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

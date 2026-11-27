<script>
  /**
   * HTTP 请求编辑面板（tab 内容）。
   *
   * 方法 / URL 那一行在 HttpRequestBar 里，占满整行；这里只负责
   * 参数 / 请求头 / 请求体 / 选项四个 tab 的内容。
   */
  import SelectMenu from "./SelectMenu.svelte";
  import KVEditor from "./KVEditor.svelte";
  import FormEditor from "./FormEditor.svelte";
  import CodeEditor from "./CodeEditor.svelte";
  import {
    HTTP_TIMEOUT_DEFAULT,
    TIMEOUT_MAX,
    BODY_TYPES,
    blankKV,
    clampNumber,
    buildRequestUrl,
  } from "../lib/model.js";
  import { addFormRow, patchHttp, showToast } from "../lib/store.js";

  /**
   * @type {{
   *   t: (key: string, variables?: Record<string, unknown>) => string,
   *   http: any,
   *   defaultTimeout?: number,
   * }}
   */
  let { t, http, defaultTimeout = HTTP_TIMEOUT_DEFAULT } = $props();

  let tab = $state("params");

  const effectiveUrl = $derived.by(() => {
    const raw = http.url.trim();
    if (!raw) return "";
    return buildRequestUrl(raw, http.params) || raw;
  });
  /**
   * 请求体分组标题的图标（12×12 viewBox 的单条 path，由 SelectMenu 统一描边）。
   * 图标只出现在组标题上，选项行保持纯文字，两者一眼能分开。
   */
  const GROUP_ICON_FORM = "M2.5 2.4h7v7.2h-7zM4.5 5h3M4.5 7h2";
  const GROUP_ICON_RAW = "M4.6 3.2 2.2 6l2.4 2.8M7.4 3.2 9.8 6 7.4 8.8";
  const GROUP_ICON_OTHER = "M2.8 2.8h2.2v2.2h-2.2zM7 2.8h2.2v2.2h-2.2zM2.8 7h2.2v2.2h-2.2zM7 7h2.2v2.2h-2.2z";

  // 按表单 / 原始 / 其他分组，和 Bruno 的请求体下拉保持一致。
  const bodyTypeOptions = $derived([
    { value: "form", label: t("form"), group: t("bodyGroupForm"), groupIcon: GROUP_ICON_FORM },
    { value: "multipart", label: t("multipart"), group: t("bodyGroupForm"), groupIcon: GROUP_ICON_FORM },
    { value: "json", label: "JSON", group: t("bodyGroupRaw"), groupIcon: GROUP_ICON_RAW },
    { value: "text", label: t("text"), group: t("bodyGroupRaw"), groupIcon: GROUP_ICON_RAW },
    { value: "xml", label: "XML", group: t("bodyGroupRaw"), groupIcon: GROUP_ICON_RAW },
    { value: "none", label: t("bodyNone"), group: t("bodyGroupOther"), groupIcon: GROUP_ICON_OTHER },
  ]);
  const bodyType = $derived(BODY_TYPES.includes(http.bodyType) ? http.bodyType : "none");
  // 占位提示跟着类型走：同一个编辑器在 JSON / 文本 / XML 下提示不一样。
  const bodyPlaceholder = $derived(
    bodyType === "json" ? t("bodyPlaceholderJson") : bodyType === "xml" ? t("bodyPlaceholderXml") : t("bodyPlaceholder"),
  );

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
    <!-- 请求体编辑器都吃满剩余高度：代码编辑器整块滚动，表单在行列表内滚动。 -->
    <div
      class="tab-panel is-active"
      class:tab-panel--fill={bodyType !== "none"}
    >
      <div class="panel-row">
        <SelectMenu
          items={bodyTypeOptions}
          value={bodyType}
          ariaLabel={t("body")}
          onchange={(value) => patchHttp({ bodyType: value })}
        />
        <span class="grow"></span>
        {#if bodyType === "form" || bodyType === "multipart"}
          <button class="btn btn-ghost btn-sm" type="button" onclick={() => addFormRow("text")}>
            {t("formAddRow")}
          </button>
        {/if}
        {#if bodyType === "json"}
          <button class="btn btn-ghost btn-sm" type="button" onclick={formatBody}>
            {t("format")}
          </button>
        {/if}
      </div>
      {#if bodyType === "none"}
        <p class="hint">{t("noBodyHint")}</p>
      {:else if bodyType === "form" || bodyType === "multipart"}
        <FormEditor {t} {http} />
      {:else}
        <CodeEditor
          value={http.bodyText}
          placeholder={bodyPlaceholder}
          ariaLabel={t("body")}
          json={bodyType === "json"}
          fill
          onchange={(value) => patchHttp({ bodyText: value })}
        />
      {/if}
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
            value={http.timeoutMs}
            oninput={(event) =>
              patchHttp({ timeoutMs: clampNumber(event.currentTarget.value, HTTP_TIMEOUT_DEFAULT, 1, TIMEOUT_MAX) })}
          />
          <button class="link-btn field-action" type="button" onclick={() => patchHttp({ timeoutMs: defaultTimeout })}>
            {t("useDefaultTimeout", { value: defaultTimeout })}
          </button>
        </div>
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

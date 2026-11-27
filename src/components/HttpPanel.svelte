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
    bodyTypeSpec,
    blankKV,
    clampNumber,
    normalizeBodyType,
    buildRequestUrl,
  } from "../lib/model.js";
  import { addFormRow, clearBodyIssue, patchHttp, sendHttp, showToast } from "../lib/store.js";

  /**
   * @type {{
   *   t: (key: string, variables?: Record<string, unknown>) => string,
   *   http: any,
   *   defaultTimeout?: number,
   *   bodyIssue?: { message: string, line: number, column: number } | null,
   * }}
   */
  let { t, http, defaultTimeout = HTTP_TIMEOUT_DEFAULT, bodyIssue = null } = $props();

  let tab = $state("params");

  const effectiveUrl = $derived.by(() => {
    const raw = http.url.trim();
    if (!raw) return "";
    return buildRequestUrl(raw, http.params) || raw;
  });
  /**
   * 下拉选项全部来自注册表：分组标题的图标（12×12 viewBox 的单条 path，由 SelectMenu
   * 统一描边）也挂在类型定义上，组标题与选项一眼能分开。
   */
  const bodyTypeOptions = $derived(
    BODY_TYPES.map((spec) => ({
      value: spec.id,
      label: spec.label || t(spec.labelKey),
      group: t(spec.group),
      groupIcon: spec.icon,
    })),
  );
  const bodyType = $derived(normalizeBodyType(http.bodyType));
  /** 当前类型的编辑器 / Content-Type 等行为都从这一份定义取。 */
  const bodySpec = $derived(bodyTypeSpec(bodyType));
  // 占位提示跟着类型走：同一个编辑器在 JSON / 文本 / XML 下提示不一样。
  const bodyPlaceholder = $derived(
    bodyType === "json" ? t("bodyPlaceholderJson") : bodyType === "xml" ? t("bodyPlaceholderXml") : t("bodyPlaceholder"),
  );

  // 发送被拦下时用户可能正停在别的 tab：切回请求体，别让错误条藏在看不见的地方。
  $effect(() => {
    if (bodyIssue) tab = "body";
  });

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
    </div>
  {:else if tab === "body"}
    <!-- 请求体编辑器都吃满剩余高度：代码编辑器整块滚动，表单在行列表内滚动。 -->
    <div
      class="tab-panel is-active"
      class:tab-panel--fill={bodySpec.editor !== "none"}
    >
      <div class="panel-row">
        <SelectMenu
          items={bodyTypeOptions}
          value={bodyType}
          ariaLabel={t("body")}
          onchange={(value) => patchHttp({ bodyType: value })}
        />
        <span class="grow"></span>
        {#if bodySpec.editor === "kv" || bodySpec.editor === "multipart"}
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
      {#if bodySpec.editor === "none"}
        <p class="hint">{t("noBodyHint")}</p>
      {:else if bodySpec.editor === "kv" || bodySpec.editor === "multipart"}
        <FormEditor {t} {http} />
      {:else}
        {#if bodyIssue}
          <!-- 发送被拦下来了：给出可定位的信息，并留一个「仍然发送」的出口。 -->
          <div class="body-issue" role="alert">
            <span class="body-issue-text" title={bodyIssue.message}>{t("jsonInvalidBody")}</span>
            {#if bodyIssue.line > 0}
              <span class="body-issue-pos">
                {t("jsonIssuePosition", { line: bodyIssue.line, column: bodyIssue.column })}
              </span>
            {/if}
            <span class="grow"></span>
            {#if bodyType === "json"}
              <button class="btn btn-ghost btn-sm" type="button" onclick={() => void sendHttp({ force: true })}>
                {t("sendAnyway")}
              </button>
            {/if}
            <button class="btn btn-ghost btn-sm" type="button" onclick={clearBodyIssue}>{t("dismiss")}</button>
          </div>
        {/if}
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

  /* 发送前校验的错误条：和响应面板的错误提示同一套语气（警示色描边 + 浅底）。 */
  .body-issue {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
    padding: 6px 10px;
    border: 1px solid color-mix(in srgb, var(--warning) 45%, transparent);
    border-radius: 8px;
    background: color-mix(in srgb, var(--warning) 12%, transparent);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .body-issue-text {
    color: var(--fg);
    font-weight: 600;
  }

  .body-issue-pos {
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    white-space: nowrap;
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

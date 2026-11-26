<script>
  /**
   * 响应面板。
   *
   * 四种状态：空 / 加载中 / 出错 / 完成。完成态再按协议分 HTTP 与 gRPC 两套渲染。
   * 徽标（状态码、耗时、大小、截断等）由这里统一生成，避免各分支各写一遍。
   */
  import { highlightJSON, presentBody, prettyJSON } from "../lib/format.js";
  import { ERROR_HINT_KEYS, formatBytes, statusTone } from "../lib/model.js";
  import { cancelCurrentRequest, copyResponse, setResponseTab } from "../lib/store.js";

  let { t, response = null, responseTab = "body", protocol = "http", canceling = false } = $props();

  // 响应面板必须用"响应"语义的标签：请求面板已经占用了「请求体 / 请求头」，
  // 这里沿用同一组文案会让人以为看到的是刚刚填的请求。
  // 还没有响应时用当前请求协议判断，gRPC 下也能显示「响应元数据」。
  const activeProtocol = $derived(response?.protocol || protocol);
  const headersTabLabel = $derived(activeProtocol === "grpc" ? t("responseMetadata") : t("responseHeadersTab"));

  const hint = $derived.by(() => {
    if (response?.kind !== "error") return "";
    const key = ERROR_HINT_KEYS[response.errorKind];
    return key ? t(key) : "";
  });

  const httpBadges = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "http" || !data) return [];
    const badges = [{ text: `${data.status} ${data.statusText || ""}`.trim(), tone: statusTone(data.status) }];
    if (data.truncated) badges.push({ text: t("truncatedBadge"), tone: "warn" });
    if (Array.isArray(data.redirects) && data.redirects.length) {
      badges.push({
        text: t("redirectBadge", { count: data.redirects.length }),
        tone: "info",
        title: data.redirects.map((hop) => `${hop.status} ${hop.url}`).join("\n"),
      });
    }
    return badges;
  });

  const httpMeta = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "http" || !data) return [];
    const items = [];
    if (Number.isFinite(data.elapsedMs)) items.push(`${data.elapsedMs} ms`);
    items.push(formatBytes(data.sizeBytes));
    return items;
  });

  const grpcBadges = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "grpc" || !data) return [];
    const status = data.status || { name: data.ok ? "OK" : "ERROR", code: data.ok ? 0 : -1 };
    // 主动终止时状态码一定是 Canceled，用中性的「已终止」比红色的 Canceled 更准确。
    const badges = [
      data.canceled ? { text: t("canceledBadge"), tone: "warn" } : { text: status.name || "OK", tone: data.ok ? "ok" : "err" },
    ];
    if (data.streaming) {
      // 旧响应没有 streamKind，按服务端流显示，避免回看历史时徽标消失。
      const label =
        data.streamKind === "client"
          ? t("clientStreaming")
          : data.streamKind === "bidi"
            ? t("bidiStreaming")
            : t("serverStreaming");
      badges.push({ text: label, tone: "info" });
    }
    if (data.truncated) badges.push({ text: t("truncatedBadge"), tone: "warn" });
    return badges;
  });

  const grpcMeta = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "grpc" || !data) return [];
    const items = [];
    const status = data.status || {};
    if (!data.canceled && Number.isFinite(status.code) && status.code !== 0) {
      items.push(t("codeLabel", { code: status.code }));
    }
    if (Number.isFinite(data.elapsedMs)) items.push(`${data.elapsedMs} ms`);
    if (Number.isFinite(data.sizeBytes)) items.push(formatBytes(data.sizeBytes));
    return items;
  });

  const body = $derived(response?.kind === "done" && response.protocol === "http" ? presentBody(response.data) : null);

  /** gRPC 调用失败但拿到了 status 时，同样给一条排错提示。 */
  const grpcHint = $derived.by(() => {
    const data = response?.data;
    if (response?.protocol !== "grpc" || data?.ok || data?.canceled) return "";
    const key = ERROR_HINT_KEYS[data?.errorKind || response?.errorKind];
    return key ? t(key) : "";
  });
</script>

<section class="response">
  <div class="response-head">
    <span class="response-title">{t("response")}</span>
    <span class="response-badges">
      {#if response?.kind === "loading"}
        <span class="pill pill--info">{t("loading")}</span>
      {:else if response?.kind === "error"}
        {#if response.errorKind === "canceled"}
          <span class="pill pill--warn">{t("canceledBadge")}</span>
        {:else}
          <span class="pill pill--err">{t("requestFailed")}</span>
        {/if}
        {#if Number.isFinite(response.elapsedMs)}<span class="meta">{response.elapsedMs} ms</span>{/if}
      {:else if response?.protocol === "http"}
        {#each httpBadges as badge (badge.text)}
          <span class="pill pill--{badge.tone}" title={badge.title}>{badge.text}</span>
        {/each}
        {#each httpMeta as item (item)}<span class="meta">{item}</span>{/each}
      {:else if response?.protocol === "grpc"}
        {#each grpcBadges as badge (badge.text)}
          <span class="pill pill--{badge.tone}">{badge.text}</span>
        {/each}
        {#each grpcMeta as item (item)}<span class="meta">{item}</span>{/each}
      {/if}
    </span>
    <span class="grow"></span>
    <button class="btn btn-ghost btn-sm" type="button" onclick={() => void copyResponse()}>{t("copy")}</button>
  </div>

  <div class="tabs tabs--response">
    <button class="tab" class:is-active={responseTab === "body"} type="button" onclick={() => setResponseTab("body")}>
      {t("responseBody")}
    </button>
    <button class="tab" class:is-active={responseTab === "headers"} type="button" onclick={() => setResponseTab("headers")}>
      {headersTabLabel}
    </button>
  </div>

  <div class="response-body">
    {#if !response}
      <div class="response-empty">
        <strong>{t("emptyResponse")}</strong>
        <span>{t("emptyResponseHint")}</span>
      </div>
    {:else if response.kind === "loading"}
      <div class="response-loading">
        <span class="spinner"></span>
        <span>{canceling ? t("stopping") : t("loading")}</span>
        <span class="grow"></span>
        <!-- 响应迟迟不回来时，用户不必回到顶部找终止按钮。 -->
        <button
          class="btn btn-secondary btn-sm"
          type="button"
          disabled={canceling}
          onclick={() => void cancelCurrentRequest()}
        >
          {canceling ? t("stopping") : t("stop")}
        </button>
      </div>
    {:else if response.kind === "error"}
      <div class="response-error">
        <div class="response-error-title">
          {response.errorKind === "canceled" ? t("requestCanceled") : t("requestFailed")}
        </div>
        {#if response.errorKind !== "canceled"}
          <pre class="response-error-code">{response.error}</pre>
        {/if}
        {#if hint}<div class="hint">{hint}</div>{/if}
      </div>
    {:else if response.protocol === "http"}
      {@render httpResult(response.data, body)}
    {:else}
      {@render grpcResult(response.data)}
    {/if}
  </div>
</section>

{#snippet headerTable(entries)}
  {#if !Array.isArray(entries) || entries.length === 0}
    <div class="side-empty">{t("none")}</div>
  {:else}
    <table class="data-table">
      <thead>
        <tr><th>{t("key")}</th><th>{t("value")}</th></tr>
      </thead>
      <tbody>
        {#each entries as entry, index (index)}
          <tr><td>{entry.name || ""}</td><td>{entry.value || ""}</td></tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/snippet}

{#snippet httpResult(data, presented)}
  {#if !data}
    <div class="side-empty">{t("emptyBody")}</div>
  {:else if responseTab === "headers"}
    {#if Array.isArray(data.redirects) && data.redirects.length}
      <div class="section-title">{t("redirects")}</div>
      {@render headerTable(data.redirects.map((hop) => ({ name: String(hop.status), value: hop.url })))}
    {/if}
    <div class="section-title">{t("responseHeaders")}</div>
    {@render headerTable(data.headers)}
  {:else}
    {#if data.truncated}
      <p class="response-note">{t("truncated", { size: formatBytes(data.sizeBytes) })}</p>
    {/if}
    {#if data.bodyKind === "binary"}
      <div class="section-title">{t("binaryBody")}</div>
      <pre class="code-block">{data.bodyBase64 || ""}</pre>
    {:else if !presented || presented.variant === "empty"}
      <div class="side-empty">{t("emptyBody")}</div>
    {:else if presented.json}
      <pre class="code-block">{@html highlightJSON(presented.text)}</pre>
    {:else}
      <pre class="code-block">{presented.text}</pre>
    {/if}
  {/if}
{/snippet}

{#snippet grpcResult(data)}
  {#if !data}
    <div class="side-empty">{t("emptyBody")}</div>
  {:else if responseTab === "headers"}
    <div class="section-title">{t("metadata")}</div>
    {@render headerTable(data.metadata)}
    <div class="section-title">{t("trailers")}</div>
    {@render headerTable(data.trailers)}
  {:else}
    {@const status = data.status || { name: data.ok ? "OK" : "ERROR", code: data.ok ? 0 : -1 }}
    {#if data.canceled}
      <!-- 被终止的调用仍然保留已经收到的消息，下面继续渲染。 -->
      <p class="response-note">{t("hintCanceled")}</p>
    {:else if !data.ok && status.message}
      <div class="response-error">
        <div class="response-error-title">
          {status.name || t("requestFailed")}{Number.isFinite(status.code) ? ` (${status.code})` : ""}
        </div>
        <pre class="response-error-code">{status.message}</pre>
        {#if grpcHint}<div class="hint">{grpcHint}</div>{/if}
      </div>
    {/if}
    {#if data.truncated}
      <p class="response-note">{t("streamTruncated")}</p>
    {/if}
    {#if Array.isArray(data.messages) && data.messages.length}
      <div class="section-title">{t("messages")}</div>
      {#each data.messages as message, index (index)}
        <div class="stream-block">
          <div class="stream-index">#{index + 1}</div>
          <pre class="code-block">{@html highlightJSON(prettyJSON(message))}</pre>
        </div>
      {/each}
    {:else if data.messageJson}
      <pre class="code-block">{@html highlightJSON(prettyJSON(data.messageJson))}</pre>
    {:else if data.ok}
      <div class="side-empty">{t("emptyBody")}</div>
    {/if}
  {/if}
{/snippet}

<style>
  .grow {
    flex: 1;
  }
</style>
